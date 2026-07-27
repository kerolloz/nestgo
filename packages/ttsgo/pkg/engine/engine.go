// Package engine compiles a TypeScript project and resolves tsconfig path
// aliases in the output.
//
// It drives the project's own TypeScript 7 compiler as a subprocess rather than
// embedding one, so the code that type-checks a user's project is the exact
// compiler they installed. See ARCHITECTURE.md.
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
	"github.com/kerolloz/ttsgo/pkg/toolchain"
	"github.com/kerolloz/ttsgo/pkg/tsc"
)

// Options defines the parameters for a compilation run.
type Options struct {
	Cwd          string
	TsConfigPath string
	OutDir       string
	Emit         bool

	// Bin is the compiler to run. Located from the project when empty.
	Bin string

	// Incremental reuses .tsbuildinfo, which is what makes rebuilds cheap.
	Incremental bool
}

// Result carries the outcome of a compilation.
type Result struct {
	EmittedFiles []string
	Diagnostics  []string

	// Config is the tsconfig as the compiler resolved it.
	Config *tsc.Config

	// Problems are TypeScript 7 migration issues found in the config.
	Problems []tsc.Problem

	// First reports whether this was the initial compilation of a watch run.
	First bool
}

// CompileWithRewrite compiles the project and rewrites path aliases in the
// emitted output.
func CompileWithRewrite(ctx context.Context, opts Options) (*Result, error) {
	bin := opts.Bin
	if bin == "" {
		located, err := toolchain.Locate(opts.Cwd)
		if err != nil {
			return nil, err
		}
		bin = located.Path
	}

	runOpts := tsc.Options{
		Bin:         bin,
		Cwd:         opts.Cwd,
		Project:     opts.TsConfigPath,
		OutDir:      opts.OutDir,
		NoEmit:      !opts.Emit,
		Incremental: opts.Incremental,
	}

	cfg, err := tsc.LoadConfig(ctx, runOpts)
	if err != nil {
		return nil, err
	}

	result := &Result{Config: cfg, Problems: tsc.Preflight(cfg)}

	if opts.Emit && opts.Incremental {
		invalidateStaleBuildInfo(cfg, opts)
	}

	res, err := tsc.Run(ctx, runOpts)
	if err != nil {
		return nil, err
	}
	for _, d := range res.Diagnostics {
		result.Diagnostics = append(result.Diagnostics, d.String())
	}
	if tsc.HasErrors(res.Diagnostics) {
		return result, nil
	}

	result.EmittedFiles = res.EmittedFiles
	if !opts.Emit {
		return result, nil
	}

	if err := rewriteEmitted(cfg, opts, res.EmittedFiles); err != nil {
		return nil, err
	}
	return result, nil
}

// Watch compiles continuously, calling onBuild after each compilation settles.
//
// The compiler owns file watching: it knows the project's real file set and
// recompiles incrementally, so a rebuild costs a fraction of a cold build and
// only the files that actually changed get rewritten.
func Watch(ctx context.Context, opts Options, onBuild func(*Result) error) error {
	bin := opts.Bin
	if bin == "" {
		located, err := toolchain.Locate(opts.Cwd)
		if err != nil {
			return err
		}
		bin = located.Path
	}

	runOpts := tsc.Options{
		Bin:         bin,
		Cwd:         opts.Cwd,
		Project:     opts.TsConfigPath,
		OutDir:      opts.OutDir,
		Incremental: true,
	}

	cfg, err := tsc.LoadConfig(ctx, runOpts)
	if err != nil {
		return err
	}

	return tsc.Watch(ctx, runOpts, nil, func(cycle tsc.Cycle) error {
		result := &Result{
			Config:       cfg,
			EmittedFiles: cycle.EmittedFiles,
			Problems:     tsc.Preflight(cfg),
			First:        cycle.First,
		}
		for _, d := range cycle.Diagnostics {
			result.Diagnostics = append(result.Diagnostics, d.String())
		}

		if !cycle.Failed() && len(cycle.EmittedFiles) > 0 {
			if err := rewriteEmitted(cfg, opts, cycle.EmittedFiles); err != nil {
				return err
			}
		}
		return onBuild(result)
	})
}

// invalidateStaleBuildInfo drops the incremental state when the output it
// describes is gone.
//
// Incremental compilation trusts .tsbuildinfo over the filesystem, so after a
// manual `rm -rf dist` the compiler concludes every file is current and emits
// nothing — exiting 0 having produced no build at all. That is tsc's own
// behaviour, and nest build inherits it, but a build command that silently
// does nothing is the worst possible failure, so we clear the state instead.
func invalidateStaleBuildInfo(cfg *tsc.Config, opts Options) {
	if cfg.TsBuildInfoFile == "" {
		return
	}
	if _, err := os.Stat(cfg.TsBuildInfoFile); err != nil {
		return // no incremental state to invalidate
	}

	outDir := opts.OutDir
	if outDir == "" {
		outDir = cfg.OutDir
	} else if !filepath.IsAbs(outDir) {
		outDir = filepath.Join(opts.Cwd, outDir)
	}

	// Sources emit next to themselves when there is no outDir; there is no
	// single directory whose absence proves the output is missing.
	if outDir == cfg.Dir {
		return
	}

	if _, err := os.Stat(outDir); os.IsNotExist(err) {
		_ = os.Remove(cfg.TsBuildInfoFile)
	}
}

// rewriteEmitted resolves path aliases in the files the compiler just wrote.
//
// TypeScript does not rewrite `paths` aliases on emit — the mapping is
// descriptive, telling the checker where to look, not prescriptive. Emitted
// JavaScript therefore still contains `require("@app/thing")`, which Node
// cannot resolve. Only the files the compiler reported are touched, so a
// watch rebuild costs one pass over the handful that changed rather than the
// whole output tree.
func rewriteEmitted(cfg *tsc.Config, opts Options, emitted []string) error {
	outDir := opts.OutDir
	if outDir == "" {
		outDir = cfg.OutDir
	}

	rewriter := paths.New(paths.Options{
		Cwd:     opts.Cwd,
		Paths:   cfg.Paths,
		OutDir:  outDir,
		RootDir: cfg.RootDir,
		// TypeScript 7 removed baseUrl, so `paths` targets can only be
		// relative to the tsconfig that declared them.
		PathsBase: cfg.Dir,
	})

	targets := make([]string, 0, len(emitted))
	for _, file := range emitted {
		if strings.HasSuffix(file, ".js") || strings.HasSuffix(file, ".d.ts") ||
			strings.HasSuffix(file, ".mjs") || strings.HasSuffix(file, ".cjs") {
			targets = append(targets, file)
		}
	}
	if len(targets) == 0 {
		return nil
	}

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
	)

	jobs := make(chan string, len(targets))
	for _, file := range targets {
		jobs <- file
	}
	close(jobs)

	for range workerCount(len(targets)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for file := range jobs {
				if err := rewriteFile(rewriter, file); err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()

	return firstErr
}

func rewriteFile(rewriter *paths.Rewriter, file string) error {
	source, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("reading %s: %w", file, err)
	}

	rewritten, edits := rewriter.Rewrite(file, string(source))
	if len(edits) == 0 && rewritten == string(source) {
		return nil
	}

	info, err := os.Stat(file)
	mode := os.FileMode(0644)
	if err == nil {
		mode = info.Mode()
	}
	if err := os.WriteFile(file, []byte(rewritten), mode); err != nil {
		return fmt.Errorf("writing %s: %w", file, err)
	}

	// Rewriting moves columns, so the companion map has to move with them or
	// every breakpoint and stack frame on an import line lands off by the
	// difference.
	return adjustCompanionMap(file, edits)
}

func adjustCompanionMap(file string, edits []Edit) error {
	if len(edits) == 0 {
		return nil
	}

	mapFile := file + ".map"
	original, err := os.ReadFile(mapFile)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading %s: %w", mapFile, err)
	}

	updated, err := paths.AdjustSourceMap(original, edits)
	if err != nil {
		// A map we cannot parse is not worth failing a build over; the code is
		// already correct, only debugging fidelity suffers.
		return nil
	}
	if string(updated) == string(original) {
		return nil
	}

	return os.WriteFile(mapFile, updated, 0644)
}

// Edit aliases the rewriter's edit record so callers need only this package.
type Edit = paths.Edit

func workerCount(jobs int) int {
	workers := runtime.NumCPU()
	if v := os.Getenv("TTSGO_WORKERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			workers = n
		}
	}
	return min(max(workers, 1), jobs)
}

// ResolveOutDir reports where the compiler will write, given a config and an
// optional override.
func ResolveOutDir(cfg *tsc.Config, override, cwd string) string {
	if override == "" {
		return cfg.OutDir
	}
	if filepath.IsAbs(override) {
		return override
	}
	return filepath.Join(cwd, override)
}
