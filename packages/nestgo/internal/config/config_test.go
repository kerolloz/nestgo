package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadNestConfig(t *testing.T) {
	dir := t.TempDir()
	content := `{
		"sourceRoot": "app",
		"entryFile": "index",
		"exec": "node",
		"compilerOptions": {
			"deleteOutDir": true,
			"assets": ["**/*.graphql", {"include": "**/*.proto", "exclude": "**/ignored"}]
		}
	}`
	os.WriteFile(filepath.Join(dir, "nest-cli.json"), []byte(content), 0644)

	cfg, err := Load(dir, "")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.SourceRoot != "app" {
		t.Errorf("SourceRoot = %q, want app", cfg.SourceRoot)
	}
	if !cfg.CompilerOptions.DeleteOutDir {
		t.Error("expected DeleteOutDir true")
	}
	if len(cfg.CompilerOptions.Assets) != 2 {
		t.Errorf("expected 2 assets, got %d", len(cfg.CompilerOptions.Assets))
	}
	if cfg.CompilerOptions.Assets[0].ResolvedGlob() != "**/*.graphql" {
		t.Errorf("unexpected first asset glob: %q", cfg.CompilerOptions.Assets[0].ResolvedGlob())
	}
}

func TestLoadNestConfigDefaults(t *testing.T) {
	cfg, err := Load(t.TempDir(), "")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.SourceRoot != "src" || cfg.EntryFile != "main" || cfg.Exec != "node" {
		t.Errorf("unexpected defaults: %+v", cfg)
	}
}

func TestLoadNestConfigUnsupportedBuilder(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "nest-cli.json"), []byte(`{"compilerOptions":{"builder":"swc"}}`), 0644)
	_, err := Load(dir, "")
	if err == nil {
		t.Error("expected error for unsupported builder")
	}
}
