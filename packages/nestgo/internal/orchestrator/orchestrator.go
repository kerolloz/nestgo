package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kerolloz/nestgo/internal/assets"
	"github.com/kerolloz/nestgo/internal/config"
	"github.com/kerolloz/nestgo/internal/logger"
	"github.com/kerolloz/nestgo/internal/plugins"
	"github.com/kerolloz/nestgo/internal/process"
	"github.com/kerolloz/ttsgo/pkg/engine"
	"github.com/kerolloz/ttsgo/pkg/toolchain"
	"github.com/kerolloz/ttsgo/pkg/tsc"
)

type Orchestrator struct {
	Cwd        string
	NestConfig *config.NestConfig
	TsConfig   *tsc.Config
	Assets     *assets.Manager
	Compiler   *toolchain.TSC
	runner     process.ProcessRunner
}

type Options struct {
	Cwd          string
	ConfigPath   string
	TsConfigPath string
	Watch        bool
	WatchAssets  bool
	RunnerOpts   *process.Options
}

func New(opts Options) (*Orchestrator, error) {
	cwd := opts.Cwd
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}

	nestCfg, err := config.Load(cwd, opts.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("config load: %w", err)
	}

	tsConfigPath := opts.TsConfigPath
	if tsConfigPath == "" {
		tsConfigPath = nestCfg.CompilerOptions.TsConfigPath
	}
	if tsConfigPath == "" {
		tsConfigPath = detectTsConfigPath(cwd)
	}
	nestCfg.CompilerOptions.TsConfigPath = tsConfigPath

	compiler, err := toolchain.Locate(cwd)
	if err != nil {
		return nil, err
	}

	// The compiler resolves the tsconfig for us: extends chains, include globs
	// and defaults all come back already applied, so nestgo cannot disagree
	// with the compiler about where output goes.
	tsCfg, err := tsc.LoadConfig(context.Background(), tsc.Options{
		Bin: compiler.Path, Cwd: cwd, Project: tsConfigPath,
	})
	if err != nil {
		return nil, err
	}

	reportConfigProblems(tsCfg)

	var assetList []assets.Asset
	for _, a := range nestCfg.CompilerOptions.Assets {
		assetList = append(assetList, assets.Asset{
			Glob:        a.ResolvedGlob(),
			Exclude:     a.Exclude,
			OutDir:      a.OutDir,
			WatchAssets: a.WatchAssets,
		})
	}
	assetMgr, err := assets.New(cwd, nestCfg.SourceRoot, tsCfg.OutDir, assetList)
	if err != nil {
		return nil, err
	}

	var runner process.ProcessRunner
	if opts.RunnerOpts != nil {
		opts.RunnerOpts.Cwd = cwd
		opts.RunnerOpts.OutDir = tsCfg.OutDir
		// Fill in defaults from nest-cli.json when CLI flags were not provided
		if opts.RunnerOpts.EntryFile == "" {
			opts.RunnerOpts.EntryFile = nestCfg.EntryFile
		}
		if opts.RunnerOpts.SourceRoot == "" {
			opts.RunnerOpts.SourceRoot = nestCfg.SourceRoot
		}
		if opts.RunnerOpts.RootDir == "" {
			opts.RunnerOpts.RootDir = tsCfg.RootDir
		}
		if opts.RunnerOpts.Binary == "" {
			opts.RunnerOpts.Binary = nestCfg.Exec
		}
		runner = process.New(*opts.RunnerOpts)
	}

	return &Orchestrator{
		Cwd:        cwd,
		NestConfig: nestCfg,
		TsConfig:   tsCfg,
		Assets:     assetMgr,
		Compiler:   compiler,
		runner:     runner,
	}, nil
}

// reportConfigProblems warns about settings TypeScript 7 rejects and about
// NestJS requirements the compiler is happy to ignore. The compiler reports the
// former itself, but only after failing; saying it first, with the fix, turns a
// dead end into a two-line edit.
func reportConfigProblems(cfg *tsc.Config) {
	for _, p := range tsc.Preflight(cfg) {
		logger.Warn("compilerOptions.%s: %s", p.Option, p.Detail)
		logger.Warn("  fix: %s", p.Fix)
	}
	for _, w := range tsc.CheckNestJS(cfg) {
		logger.Warn("compilerOptions.%s: %s", w.Option, w.Detail)
	}
}

func (o *Orchestrator) KillRunner() {
	if o.runner != nil {
		o.runner.Kill()
	}
}

func (o *Orchestrator) WaitRunner() int {
	if o.runner != nil {
		return o.runner.Wait()
	}
	return 0
}

func (o *Orchestrator) Build(ctx context.Context, isRebuild bool) error {
	if o.NestConfig.CompilerOptions.DeleteOutDir {
		if err := o.deleteOutDir(); err != nil {
			return err
		}
	}

	if err := o.generatePluginMetadata(ctx); err != nil {
		return err
	}

	logger.Info("Compiling with ttsgo...")
	res, err := engine.CompileWithRewrite(ctx, engine.Options{
		Cwd:          o.Cwd,
		TsConfigPath: o.NestConfig.CompilerOptions.TsConfigPath,
		Bin:          o.compilerPath(),
		Emit:         true,
		Incremental:  true,
	})
	if err != nil {
		return fmt.Errorf("compilation failed: %w", err)
	}
	if len(res.Diagnostics) > 0 {
		for _, d := range res.Diagnostics {
			logger.Error("%s", d)
		}
		return fmt.Errorf("compilation failed with %d diagnostics", len(res.Diagnostics))
	}

	if err := o.Assets.Copy(); err != nil {
		return fmt.Errorf("asset copy failed: %w", err)
	}

	// Kill old process after successful compilation to avoid downtime on build failures
	if isRebuild && o.runner != nil {
		logger.Step("Stopping old process...")
		o.runner.Kill()
	}

	if o.runner != nil {
		logger.Step("Starting process...")
		if err := o.runner.Start(); err != nil {
			logger.Error("Failed to start process: %v", err)
		}
	}

	return nil
}

// generatePluginMetadata runs the CLI plugins' readonly visitors and writes
// metadata.ts into the source root, before compilation, because the generated
// file is itself an input to the build.
func (o *Orchestrator) generatePluginMetadata(ctx context.Context) error {
	configured := o.NestConfig.CompilerOptions.Plugins
	if len(configured) == 0 {
		return nil
	}

	list := make([]plugins.Plugin, 0, len(configured))
	for _, p := range configured {
		list = append(list, plugins.Plugin{Name: p.Name, Options: p.Options})
	}

	logger.Step("Generating plugin metadata...")
	written, err := plugins.Generate(ctx, plugins.Options{
		Cwd:          o.Cwd,
		TsConfigPath: o.NestConfig.CompilerOptions.TsConfigPath,
		OutputDir:    filepath.Join(o.Cwd, o.NestConfig.SourceRoot),
		Plugins:      list,
	})
	if err != nil {
		return err
	}
	if written != "" {
		if rel, relErr := filepath.Rel(o.Cwd, written); relErr == nil {
			written = rel
		}
		logger.Info("Wrote %s", written)
	}
	return nil
}

func (o *Orchestrator) compilerPath() string {
	if o.Compiler == nil {
		return ""
	}
	return o.Compiler.Path
}

// deleteOutDir removes the output directory, refusing anything that resolves to
// or outside the project root — an outDir of "." would otherwise take the
// sources with it.
func (o *Orchestrator) deleteOutDir() error {
	absOut := o.TsConfig.OutDir
	if !filepath.IsAbs(absOut) {
		absOut = filepath.Join(o.Cwd, absOut)
	}
	absOut = filepath.Clean(absOut)

	if absOut == o.Cwd || !strings.HasPrefix(absOut+string(filepath.Separator), o.Cwd+string(filepath.Separator)) {
		return fmt.Errorf("outDir %q resolves to or outside the project root, refusing to delete", o.TsConfig.OutDir)
	}
	if err := os.RemoveAll(absOut); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete outDir: %w", err)
	}
	if o.TsConfig.TsBuildInfoFile != "" {
		_ = os.Remove(o.TsConfig.TsBuildInfoFile)
	}
	return nil
}

// Watch compiles continuously and restarts the application after every clean
// build.
//
// The compiler does the file watching. It tracks the project's real file set
// and recompiles incrementally, which is both more accurate and far faster
// than watching the source directory ourselves and re-running a full build.
func (o *Orchestrator) Watch(ctx context.Context, watchAssets bool) error {
	logger.Step("Watching %s for changes...", o.NestConfig.SourceRoot)

	if watchAssets || o.NestConfig.CompilerOptions.WatchAssets {
		if err := o.Assets.Watch(ctx); err != nil {
			return fmt.Errorf("asset watcher: %w", err)
		}
	}

	if o.NestConfig.CompilerOptions.DeleteOutDir {
		if err := o.deleteOutDir(); err != nil {
			return err
		}
	}

	if err := o.generatePluginMetadata(ctx); err != nil {
		return err
	}

	defer o.KillRunner()

	err := engine.Watch(ctx, engine.Options{
		Cwd:          o.Cwd,
		TsConfigPath: o.NestConfig.CompilerOptions.TsConfigPath,
		Bin:          o.compilerPath(),
		Emit:         true,
	}, func(res *engine.Result) error {
		if len(res.Diagnostics) > 0 {
			for _, d := range res.Diagnostics {
				logger.Error("%s", d)
			}
			// Leave the running process alone: it is still serving the last
			// good build, which beats taking the app down over a typo.
			return nil
		}

		// The compiler completes a cycle whenever it re-checks, including when
		// nothing needed re-emitting. Restarting then would bounce the app for
		// no reason.
		if !res.First && len(res.EmittedFiles) == 0 {
			return nil
		}

		if err := o.Assets.Copy(); err != nil {
			logger.Error("Asset copy failed: %v", err)
		}

		o.restart()
		logger.Success("Build complete")
		return nil
	})

	if ctx.Err() != nil {
		return nil
	}
	return err
}

// restart replaces the running application. The old process is killed only
// after a successful compile, so a broken edit never leaves nothing running.
func (o *Orchestrator) restart() {
	if o.runner == nil {
		return
	}
	logger.Step("Restarting...")
	o.runner.Kill()
	if err := o.runner.Start(); err != nil {
		logger.Error("Failed to start process: %v", err)
	}
}

func detectTsConfigPath(cwd string) string {
	if _, err := os.Stat(filepath.Join(cwd, "tsconfig.build.json")); err == nil {
		return "tsconfig.build.json"
	}
	return "tsconfig.json"
}
