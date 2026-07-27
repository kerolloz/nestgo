package tsc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Relative options in --showConfig output are relative to the tsconfig's own
// directory, not the working directory. Resolving them against cwd works only
// while the two coincide, and silently points at the wrong directory as soon
// as the config lives in a subdirectory — which for a delete-outDir path means
// operating on a directory the compiler never wrote to.
func TestLoadConfigResolvesRelativeToConfigDir(t *testing.T) {
	bin, dir := project(t, map[string]string{
		"src/main.ts": "export const g: string = 'hi';\n",
		"cfg/tsconfig.json": `{
  "compilerOptions": {
    "target": "ES2022",
    "module": "CommonJS",
    "outDir": "../build",
    "rootDir": "../src",
    "tsBuildInfoFile": "../.cache/ts.tsbuildinfo",
    "paths": { "@app/*": ["../src/*"] }
  },
  "include": ["../src/**/*"]
}`,
	})

	cfg, err := LoadConfig(context.Background(), Options{Bin: bin, Cwd: dir, Project: "cfg/tsconfig.json"})
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if want := filepath.Join(dir, "cfg"); cfg.Dir != want {
		t.Errorf("Dir = %q, want %q", cfg.Dir, want)
	}
	// "../build" from cfg/ is <project>/build, not <project>/../build.
	if want := filepath.Join(dir, "build"); cfg.OutDir != want {
		t.Errorf("OutDir = %q, want %q", cfg.OutDir, want)
	}
	if want := filepath.Join(dir, "src"); cfg.RootDir != want {
		t.Errorf("RootDir = %q, want %q", cfg.RootDir, want)
	}
	if want := filepath.Join(dir, ".cache", "ts.tsbuildinfo"); cfg.TsBuildInfoFile != want {
		t.Errorf("TsBuildInfoFile = %q, want %q", cfg.TsBuildInfoFile, want)
	}
	if got := cfg.Paths["@app/*"]; len(got) != 1 || got[0] != "../src/*" {
		t.Errorf("Paths[@app/*] = %v, want [../src/*]", got)
	}
	if len(cfg.Files) == 0 {
		t.Error("expected input files to be listed")
	}
	for _, f := range cfg.Files {
		if !filepath.IsAbs(f) {
			t.Errorf("input file should be absolute: %s", f)
		}
	}
}

// The compiler flattens `extends`, so we never have to walk the chain
// ourselves — that hand-rolled resolution is what this replaces.
func TestLoadConfigFlattensExtends(t *testing.T) {
	bin, dir := project(t, map[string]string{
		"src/main.ts": "export const g: string = 'hi';\n",
		"base.json": `{
  "compilerOptions": {
    "target": "ES2022",
    "strict": true,
    "experimentalDecorators": true,
    "emitDecoratorMetadata": true
  }
}`,
		"tsconfig.json": `{
  "extends": "./base.json",
  "compilerOptions": { "module": "CommonJS", "outDir": "./dist", "rootDir": "./src" },
  "include": ["src/**/*"]
}`,
	})

	cfg, err := LoadConfig(context.Background(), Options{Bin: bin, Cwd: dir, Project: "tsconfig.json"})
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if !cfg.ExperimentalDecorators || !cfg.EmitDecoratorMetadata {
		t.Errorf("decorator options should come through the extends chain: %+v", cfg.Options)
	}
	if want := filepath.Join(dir, "dist"); cfg.OutDir != want {
		t.Errorf("OutDir = %q, want %q", cfg.OutDir, want)
	}
}

// Without outDir the compiler writes alongside the sources.
func TestLoadConfigDefaultsOutDirToConfigDir(t *testing.T) {
	bin, dir := project(t, map[string]string{
		"src/main.ts":   "export const g: string = 'hi';\n",
		"tsconfig.json": `{"compilerOptions":{"target":"ES2022","module":"CommonJS"},"include":["src/**/*"]}`,
	})

	cfg, err := LoadConfig(context.Background(), Options{Bin: bin, Cwd: dir, Project: "tsconfig.json"})
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.OutDir != dir {
		t.Errorf("OutDir = %q, want the config directory %q", cfg.OutDir, dir)
	}
}

func TestResolveConfigPath(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(root, "tsconfig.json")
	if err := os.WriteFile(config, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	t.Run("searches upward when unspecified", func(t *testing.T) {
		got, err := resolveConfigPath(nested, "")
		if err != nil {
			t.Fatal(err)
		}
		if got != config {
			t.Errorf("got %q, want %q", got, config)
		}
	})

	t.Run("accepts a directory", func(t *testing.T) {
		got, err := resolveConfigPath(nested, root)
		if err != nil {
			t.Fatal(err)
		}
		if got != config {
			t.Errorf("got %q, want %q", got, config)
		}
	})

	t.Run("accepts a relative file", func(t *testing.T) {
		got, err := resolveConfigPath(root, "tsconfig.json")
		if err != nil {
			t.Fatal(err)
		}
		if got != config {
			t.Errorf("got %q, want %q", got, config)
		}
	})

	t.Run("reports a missing config", func(t *testing.T) {
		if _, err := resolveConfigPath(t.TempDir(), "nope.json"); err == nil {
			t.Error("expected an error for a missing config")
		}
	})

	t.Run("reports when nothing is found upward", func(t *testing.T) {
		empty := filepath.Join(t.TempDir(), "deep", "deeper")
		if err := os.MkdirAll(empty, 0755); err != nil {
			t.Fatal(err)
		}
		if _, err := resolveConfigPath(empty, ""); err == nil {
			t.Skip("a tsconfig.json exists somewhere above the temp dir")
		} else if !strings.Contains(err.Error(), "tsconfig.json") {
			t.Errorf("error should mention tsconfig.json, got: %v", err)
		}
	})
}

// Without an explicit tsBuildInfoFile the compiler still writes one, beside
// the tsconfig and named after it. deleteOutDir has to remove that file too:
// wiping the output directory while leaving the buildinfo makes the compiler
// believe everything is current, and the next build emits nothing at all.
func TestLoadConfigDefaultsBuildInfoPath(t *testing.T) {
	bin, dir := project(t, map[string]string{
		"src/main.ts": "export const g: string = 'hi';\n",
		"tsconfig.build.json": `{
  "compilerOptions": {"target":"ES2022","module":"CommonJS","outDir":"./dist","rootDir":"./src"},
  "include": ["src/**/*"]
}`,
	})

	cfg, err := LoadConfig(context.Background(), Options{Bin: bin, Cwd: dir, Project: "tsconfig.build.json"})
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	want := filepath.Join(dir, "tsconfig.build.tsbuildinfo")
	if cfg.TsBuildInfoFile != want {
		t.Errorf("TsBuildInfoFile = %q, want %q", cfg.TsBuildInfoFile, want)
	}
}
