package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kerolloz/nestgo/internal/assets"
	"github.com/kerolloz/nestgo/internal/config"
	"github.com/kerolloz/nestgo/internal/logger"
	"github.com/kerolloz/nestgo/internal/process"
	"github.com/kerolloz/nestgo/internal/watcher"
	"github.com/kerolloz/ttsgo/pkg/engine"
)

type Orchestrator struct {
	Cwd        string
	NestConfig *config.NestConfig
	TsConfig   *config.TsConfig
	Assets     *assets.Manager
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

	tsCfg, err := config.LoadTsConfig(cwd, tsConfigPath)
	if err != nil {
		return nil, fmt.Errorf("tsconfig load: %w", err)
	}

	// Validate decorator metadata flags
	if !tsCfg.EmitDecoratorMetadata {
		logger.Warn("emitDecoratorMetadata is not enabled in tsconfig.json.")
		logger.Warn("NestJS dependency injection requires this flag.")
	}
	if !tsCfg.ExperimentalDecorators {
		logger.Warn("experimentalDecorators is not enabled in tsconfig.json.")
		logger.Warn("NestJS decorators require this flag.")
	}

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
		runner:     runner,
	}, nil
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
		// An absolute outDir must be honoured, not joined onto Cwd — the engine
		// and the asset manager both resolve it that way, and joining would
		// delete a path the compiler never wrote to.
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
			_ = os.Remove(filepath.Join(o.Cwd, o.TsConfig.TsBuildInfoFile))
		}
	}

	logger.Info("Compiling with ttsgo...")
	res, err := engine.CompileWithRewrite(ctx, engine.Options{
		Cwd:          o.Cwd,
		TsConfigPath: o.NestConfig.CompilerOptions.TsConfigPath,
		OutDir:       o.TsConfig.OutDir,
		Emit:         true,
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

func (o *Orchestrator) Watch(ctx context.Context, watchAssets bool) error {
	logger.Step("Watching %s for changes...", o.NestConfig.SourceRoot)

	if watchAssets || o.NestConfig.CompilerOptions.WatchAssets {
		if err := o.Assets.Watch(ctx); err != nil {
			return fmt.Errorf("asset watcher: %w", err)
		}
	}

	rebuildCh := make(chan struct{}, 1)
	absSourceRoot := filepath.Join(o.Cwd, o.NestConfig.SourceRoot)

	wt, err := watcher.New(ctx, absSourceRoot, 500*time.Millisecond, func() {
		select {
		case rebuildCh <- struct{}{}:
		default:
		}
	})
	if err != nil {
		return err
	}
	defer wt.Close()

	// Initial build
	if err := o.Build(ctx, false); err != nil {
		logger.Error("Initial build failed: %v", err)
	} else {
		logger.Success("Build complete")
	}

	for {
		select {
		case <-ctx.Done():
			if o.runner != nil {
				o.runner.Kill()
			}
			return nil
		case <-rebuildCh:
			logger.Step("Change detected — rebuilding...")
			if err := o.Build(ctx, true); err != nil {
				logger.Error("Build failed: %v", err)
			} else {
				logger.Success("Rebuild complete")
			}
		}
	}
}

func detectTsConfigPath(cwd string) string {
	if _, err := os.Stat(filepath.Join(cwd, "tsconfig.build.json")); err == nil {
		return "tsconfig.build.json"
	}
	return "tsconfig.json"
}
