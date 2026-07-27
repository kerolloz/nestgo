package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/kerolloz/ttsgo/pkg/paths"
	shimcompiler "github.com/microsoft/typescript-go/shim/compiler"
	shimcore "github.com/microsoft/typescript-go/shim/core"
	shimtsoptions "github.com/microsoft/typescript-go/shim/tsoptions"
)

// Options defines the parameters for a compilation run.
type Options struct {
	Cwd          string
	TsConfigPath string
	OutDir       string
	Emit         bool
}

// Result carries the outcome of a compilation.
type Result struct {
	EmittedFiles []string
	Diagnostics  []string
}

// LoadProgram parses the tsconfig and initializes a TSGo program.
func LoadProgram(ctx context.Context, cwd, tsconfigPath string) (*Program, error) {
	fs := DefaultFS()
	host := DefaultHost(cwd, fs)

	resolvedTsConfig := tsconfigPath
	if !filepath.IsAbs(resolvedTsConfig) {
		resolvedTsConfig = filepath.Join(cwd, resolvedTsConfig)
	}

	parsed, diags := shimtsoptions.GetParsedCommandLineOfConfigFile(resolvedTsConfig, &shimcore.CompilerOptions{}, nil, host, nil)
	if len(diags) > 0 {
		var msgs []string
		for _, d := range diags {
			msgs = append(msgs, d.String())
		}
		return nil, fmt.Errorf("failed to parse tsconfig: %s", strings.Join(msgs, "; "))
	}

	tsProgram := shimcompiler.NewProgram(shimcompiler.ProgramOptions{
		Config: parsed,
		Host:   host,
	})

	return &Program{
		TSProgram:    tsProgram,
		ParsedConfig: parsed,
		Host:         host,
	}, nil
}

// CompileWithRewrite is the high-level API for ttsgo.
func CompileWithRewrite(ctx context.Context, opts Options) (*Result, error) {
	prog, err := LoadProgram(ctx, opts.Cwd, opts.TsConfigPath)
	if err != nil {
		return nil, err
	}
	defer prog.Close()

	diags := prog.Diagnostics(ctx)
	if len(diags) > 0 {
		var diagMsgs []string
		for _, d := range diags {
			diagMsgs = append(diagMsgs, d.String())
		}
		return &Result{Diagnostics: diagMsgs}, nil
	}

	if !opts.Emit {
		return &Result{}, nil
	}

	// Setup rewriter
	outDir := opts.OutDir
	if outDir == "" {
		if prog.ParsedConfig.CompilerOptions().OutDir != "" {
			outDir = prog.ParsedConfig.CompilerOptions().OutDir
		} else {
			outDir = opts.Cwd
		}
	}
	if !filepath.IsAbs(outDir) {
		outDir = filepath.Join(opts.Cwd, outDir)
	}

	// Path mapping extraction
	pathMap := make(map[string][]string)
	if prog.ParsedConfig.CompilerOptions().Paths != nil {
		p := prog.ParsedConfig.CompilerOptions().Paths
		for key := range p.Keys() {
			if val, ok := p.Get(key); ok {
				pathMap[key] = val
			}
		}
	}

	// Root dir extraction
	rootDir := ""
	if prog.ParsedConfig.CompilerOptions().RootDir != "" {
		rootDir = prog.ParsedConfig.CompilerOptions().RootDir
	}

	rewriter := paths.New(opts.Cwd, pathMap, outDir, rootDir,
		prog.ParsedConfig.CompilerOptions().GetPathsBasePath(opts.Cwd))

	// --- Concurrent I/O pipeline ---
	// The compiler calls WriteFile synchronously per source file, but I/O
	// is the bottleneck (MkdirAll + WriteFile syscalls for every emitted
	// file). We decouple the callback from disk writes using a bounded
	// worker pool.

	type writeJob struct {
		fileName string
		data     []byte
	}

	var (
		mu         sync.Mutex
		emitted    []string
		dirOnceMap sync.Map // map[string]*sync.Once
		wg         sync.WaitGroup
		writeErr   error
	)

	// Bounded channel — adaptive worker count based on available CPUs
	numWorkers := runtime.NumCPU() * 4
	if numWorkers < 8 {
		numWorkers = 8
	}
	if numWorkers > 128 {
		numWorkers = 128
	}
	if v := os.Getenv("TTSGO_WORKERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			numWorkers = n
		}
	}
	bufSize := numWorkers * 4
	jobs := make(chan writeJob, bufSize)

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				dir := filepath.Dir(j.fileName)

				// Get or create a Once for this directory
				actual, _ := dirOnceMap.LoadOrStore(dir, &sync.Once{})
				once := actual.(*sync.Once)

				var mkdirErr error
				once.Do(func() {
					mkdirErr = os.MkdirAll(dir, 0755)
				})

				if mkdirErr != nil {
					mu.Lock()
					if writeErr == nil {
						writeErr = mkdirErr
					}
					mu.Unlock()
					continue
				}

				if err := os.WriteFile(j.fileName, j.data, 0644); err != nil {
					mu.Lock()
					if writeErr == nil {
						writeErr = err
					}
					mu.Unlock()
				}
			}
		}()
	}

	writeFile := shimcompiler.WriteFile(func(fileName, text string, data *shimcompiler.WriteFileData) error {
		absFileName := fileName
		if !filepath.IsAbs(absFileName) {
			absFileName = filepath.Join(opts.Cwd, absFileName)
		}

		if strings.HasSuffix(fileName, ".js") || strings.HasSuffix(fileName, ".d.ts") {
			text = rewriter.RewriteSource(absFileName, text)
		}

		// Convert to []byte once and send to worker pool.
		// Using unsafe conversion would save this copy but is risky
		// since the compiler may reuse the string's backing array.
		buf := []byte(text)

		mu.Lock()
		emitted = append(emitted, absFileName)
		mu.Unlock()

		jobs <- writeJob{fileName: absFileName, data: buf}
		return nil
	})

	res := prog.Emit(ctx, writeFile)

	// Close channel and wait for all writers to finish
	close(jobs)
	wg.Wait()

	if writeErr != nil {
		return nil, fmt.Errorf("write failed: %w", writeErr)
	}
	if res == nil {
		return nil, fmt.Errorf("emit failed")
	}

	return &Result{
		EmittedFiles: emitted,
	}, nil
}
