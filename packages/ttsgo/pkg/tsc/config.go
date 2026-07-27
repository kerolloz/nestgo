package tsc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config is a tsconfig as the compiler resolved it: `extends` chains flattened,
// `include` globs expanded, defaults filled in.
//
// Asking the compiler beats parsing tsconfig ourselves. It is JSONC with
// trailing commas, `extends` can chain through node_modules, and defaults shift
// between releases — every difference between our reading and the compiler's is
// a bug where we operate on a directory the compiler never wrote to.
type Config struct {
	// Path is the absolute path of the tsconfig that was read.
	Path string

	// Dir is the directory holding Path. Relative options in the compiler's
	// output are relative to this, not to the working directory.
	Dir string

	// OutDir is absolute, defaulting to Dir when the config does not set it.
	OutDir string

	// RootDir is absolute, or empty when unset — in which case the compiler
	// infers it from the common ancestor of the input files.
	RootDir string

	// Paths is compilerOptions.paths exactly as written. Targets resolve
	// against Dir: TypeScript 7 removed baseUrl, so there is nothing else they
	// could be relative to.
	Paths map[string][]string

	// TsBuildInfoFile is absolute. When the config does not set it, this is
	// where incremental builds put it by default: alongside the tsconfig,
	// named after it. Deleting the output directory without also removing this
	// leaves the compiler believing everything is up to date, and the next
	// build emits nothing at all.
	TsBuildInfoFile string

	EmitDecoratorMetadata  bool
	ExperimentalDecorators bool

	// Files are the absolute paths of the project's input files.
	Files []string

	// Options is every compilerOptions entry the compiler reported, for checks
	// that care about settings this struct does not model.
	Options map[string]any
}

// showConfigOutput mirrors the JSON shape of `tsc --showConfig`.
type showConfigOutput struct {
	CompilerOptions map[string]any `json:"compilerOptions"`
	Files           []string       `json:"files"`
}

// LoadConfig resolves the project's tsconfig by asking the compiler.
//
// It does not validate: `--showConfig` reports options the compiler would
// later reject, which is what lets Preflight explain them before the build
// fails. See Preflight.
func LoadConfig(ctx context.Context, opts Options) (*Config, error) {
	configPath, err := resolveConfigPath(opts.Cwd, opts.Project)
	if err != nil {
		return nil, err
	}

	cmd := Options{Bin: opts.Bin, Cwd: opts.Cwd, Project: opts.Project, Args: []string{"--showConfig"}}
	res, err := Run(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", configPath, err)
	}
	if res.Failed() {
		return nil, fmt.Errorf("reading %s: %s", configPath, firstDiagnostic(res))
	}

	var out showConfigOutput
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		return nil, fmt.Errorf("could not parse the resolved config for %s: %w", configPath, err)
	}

	return buildConfig(configPath, out), nil
}

func buildConfig(configPath string, out showConfigOutput) *Config {
	dir := filepath.Dir(configPath)

	// Relative options are relative to the tsconfig's own directory.
	resolve := func(p string) string {
		if p == "" {
			return ""
		}
		if filepath.IsAbs(p) {
			return filepath.Clean(p)
		}
		return filepath.Join(dir, p)
	}

	cfg := &Config{
		Path:                   configPath,
		Dir:                    dir,
		Options:                out.CompilerOptions,
		Paths:                  stringSliceMap(out.CompilerOptions["paths"]),
		RootDir:                resolve(stringOption(out.CompilerOptions, "rootDir")),
		TsBuildInfoFile:        resolve(buildInfoPath(configPath, out.CompilerOptions)),
		EmitDecoratorMetadata:  boolOption(out.CompilerOptions, "emitDecoratorMetadata"),
		ExperimentalDecorators: boolOption(out.CompilerOptions, "experimentalDecorators"),
	}

	if outDir := stringOption(out.CompilerOptions, "outDir"); outDir != "" {
		cfg.OutDir = resolve(outDir)
	} else {
		// Without outDir the compiler writes next to the sources.
		cfg.OutDir = dir
	}

	for _, f := range out.Files {
		cfg.Files = append(cfg.Files, resolve(f))
	}
	return cfg
}

// resolveConfigPath mirrors how the compiler picks a tsconfig: an explicit -p
// may name a file or a directory, and an absent one searches upward from the
// working directory.
func resolveConfigPath(cwd, project string) (string, error) {
	if project != "" {
		path := project
		if !filepath.IsAbs(path) {
			path = filepath.Join(cwd, path)
		}
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			path = filepath.Join(path, "tsconfig.json")
		}
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("no tsconfig at %s", path)
		}
		return path, nil
	}

	dir := cwd
	for {
		candidate := filepath.Join(dir, "tsconfig.json")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no tsconfig.json found in %s or any parent directory", cwd)
		}
		dir = parent
	}
}

// buildInfoPath returns the configured tsBuildInfoFile, or the path the
// compiler uses by default: the tsconfig's own name with a .tsbuildinfo
// extension, beside the tsconfig.
func buildInfoPath(configPath string, options map[string]any) string {
	if configured := stringOption(options, "tsBuildInfoFile"); configured != "" {
		return configured
	}
	base := filepath.Base(configPath)
	return strings.TrimSuffix(base, filepath.Ext(base)) + ".tsbuildinfo"
}

func firstDiagnostic(res *Result) string {
	if len(res.Diagnostics) > 0 {
		return res.Diagnostics[0].String()
	}
	return fmt.Sprintf("compiler exited with status %d", res.ExitCode)
}

func stringOption(options map[string]any, key string) string {
	s, _ := options[key].(string)
	return s
}

func boolOption(options map[string]any, key string) bool {
	b, _ := options[key].(bool)
	return b
}

func stringSliceMap(v any) map[string][]string {
	raw, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string][]string, len(raw))
	for key, val := range raw {
		items, ok := val.([]any)
		if !ok {
			continue
		}
		var targets []string
		for _, item := range items {
			if s, ok := item.(string); ok {
				targets = append(targets, s)
			}
		}
		out[key] = targets
	}
	return out
}
