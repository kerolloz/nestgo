package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kerolloz/nestgo/internal/assets"
	"github.com/kerolloz/nestgo/internal/config"
)

// Build deletes outDir when deleteOutDir is set. That is the only destructive
// operation in the codebase, so every outDir shape that must NOT be deleted is
// pinned here: an outDir resolving to the project root once wiped entire
// projects, src/ included.
func TestBuildRefusesToDeleteUnsafeOutDir(t *testing.T) {
	for _, tc := range []struct {
		name   string
		outDir string
	}{
		{"project root as dot", "."},
		{"project root as empty", ""},
		{"project root via traversal", "dist/.."},
		{"parent of project", ".."},
		{"escapes via traversal", "../elsewhere"},
		{"absolute outside project", filepath.Join(os.TempDir(), "nestgo-outside")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cwd := t.TempDir()
			canary := filepath.Join(cwd, "src", "main.ts")
			if err := os.MkdirAll(filepath.Dir(canary), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(canary, []byte("export {};"), 0644); err != nil {
				t.Fatal(err)
			}

			o := newTestOrchestrator(t, cwd, tc.outDir, true)
			err := o.Build(context.Background(), false)

			if err == nil {
				t.Fatal("expected Build to refuse the delete, got nil error")
			}
			if !strings.Contains(err.Error(), "refusing to delete") {
				t.Fatalf("expected a refusal error, got: %v", err)
			}
			if _, statErr := os.Stat(canary); statErr != nil {
				t.Fatalf("source file was deleted: %v", statErr)
			}
		})
	}
}

// A normal outDir inside the project is deleted. Compilation fails right after
// (there is no real tsconfig here), so this asserts the delete happened rather
// than that the build succeeded.
func TestBuildDeletesOutDirInsideProject(t *testing.T) {
	cwd := t.TempDir()
	stale := filepath.Join(cwd, "dist", "stale.js")
	if err := os.MkdirAll(filepath.Dir(stale), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("// stale"), 0644); err != nil {
		t.Fatal(err)
	}

	o := newTestOrchestrator(t, cwd, "dist", true)
	_ = o.Build(context.Background(), false)

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("expected outDir to be deleted, stale file still present (err=%v)", err)
	}
}

// deleteOutDir off means outDir is left alone even when it is safe to delete.
func TestBuildKeepsOutDirWhenDeleteDisabled(t *testing.T) {
	cwd := t.TempDir()
	keep := filepath.Join(cwd, "dist", "keep.js")
	if err := os.MkdirAll(filepath.Dir(keep), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keep, []byte("// keep"), 0644); err != nil {
		t.Fatal(err)
	}

	o := newTestOrchestrator(t, cwd, "dist", false)
	_ = o.Build(context.Background(), false)

	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("expected outDir to be untouched: %v", err)
	}
}

func newTestOrchestrator(t *testing.T, cwd, outDir string, deleteOutDir bool) *Orchestrator {
	t.Helper()

	assetMgr, err := assets.New(cwd, "src", outDir, nil)
	if err != nil {
		t.Fatal(err)
	}

	return &Orchestrator{
		Cwd: cwd,
		NestConfig: &config.NestConfig{
			SourceRoot: "src",
			EntryFile:  "main",
			CompilerOptions: config.CompilerOptions{
				DeleteOutDir: deleteOutDir,
				TsConfigPath: "tsconfig.json",
			},
		},
		TsConfig: &config.TsConfig{OutDir: outDir},
		Assets:   assetMgr,
	}
}
