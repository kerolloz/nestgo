package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func setupTestRewriter(t *testing.T) (*Rewriter, string) {
	t.Helper()
	tmp := t.TempDir()

	// Create source structure
	srcDir := filepath.Join(tmp, "src")
	os.MkdirAll(filepath.Join(srcDir, "utils"), 0755)
	os.WriteFile(filepath.Join(srcDir, "utils", "helper.ts"), []byte(""), 0644)
	os.MkdirAll(filepath.Join(srcDir, "models"), 0755)
	os.WriteFile(filepath.Join(srcDir, "models", "index.ts"), []byte(""), 0644)

	paths := map[string][]string{
		"@app/*":   {"src/*"},
		"@utils/*": {"src/utils/*"},
		"@models":  {"src/models"},
	}

	outDir := filepath.Join(tmp, "dist")
	r := New(Options{Cwd: tmp, Paths: paths, OutDir: outDir, RootDir: "src"})
	return r, tmp
}

// PathsBase is baseUrl when tsconfig sets it: targets resolve under src/,
// not the project root.
func TestRewriteSource_PathsBaseFromBaseURL(t *testing.T) {
	tmp := t.TempDir()
	srcDir := filepath.Join(tmp, "src")
	os.MkdirAll(filepath.Join(srcDir, "utils"), 0755)
	os.WriteFile(filepath.Join(srcDir, "utils", "helper.ts"), []byte(""), 0644)

	r := New(Options{
		Cwd:       tmp,
		Paths:     map[string][]string{"@utils/*": {"utils/*"}},
		OutDir:    filepath.Join(tmp, "dist"),
		RootDir:   "src",
		PathsBase: "src",
	})

	result := r.RewriteSource(filepath.Join(tmp, "dist", "app.js"),
		`const h = require("@utils/helper");`)

	if !contains(result, "./utils/helper.js") {
		t.Errorf("expected ./utils/helper.js, got: %s", result)
	}
}

// Without baseUrl, TypeScript resolves paths against the directory of the
// tsconfig that declared them — which is not the cwd when compiling
// -p configs/tsconfig.json. Targets below are relative to configs/.
func TestRewriteSource_PathsBaseFromTsconfigDir(t *testing.T) {
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, "configs", "shared", "utils"), 0755)
	os.WriteFile(filepath.Join(tmp, "configs", "shared", "utils", "helper.ts"), []byte(""), 0644)

	r := New(Options{
		Cwd:       tmp,
		Paths:     map[string][]string{"@utils/*": {"./shared/utils/*"}},
		OutDir:    filepath.Join(tmp, "dist"),
		RootDir:   filepath.Join(tmp, "configs"),
		PathsBase: filepath.Join(tmp, "configs"),
	})

	result := r.RewriteSource(filepath.Join(tmp, "dist", "app.js"),
		`const h = require("@utils/helper");`)

	if !contains(result, "./shared/utils/helper.js") {
		t.Errorf("expected ./shared/utils/helper.js, got: %s", result)
	}
}

func TestRewriteSource_RequireAlias(t *testing.T) {
	r, tmp := setupTestRewriter(t)
	fileName := filepath.Join(tmp, "dist", "app.js")

	input := `const h = require("@utils/helper");`
	result := r.RewriteSource(fileName, input)

	if result == input {
		t.Fatal("expected rewrite, got unchanged text")
	}
	if !contains(result, "./utils/helper.js") {
		t.Errorf("expected relative path with .js, got: %s", result)
	}
}

func TestRewriteSource_ImportFrom(t *testing.T) {
	r, tmp := setupTestRewriter(t)
	fileName := filepath.Join(tmp, "dist", "app.js")

	input := `import { helper } from "@utils/helper";`
	result := r.RewriteSource(fileName, input)

	if result == input {
		t.Fatal("expected rewrite, got unchanged text")
	}
	if !contains(result, "./utils/helper.js") {
		t.Errorf("expected relative path with .js, got: %s", result)
	}
}

func TestRewriteSource_ExportStarFrom(t *testing.T) {
	r, tmp := setupTestRewriter(t)
	fileName := filepath.Join(tmp, "dist", "index.js")

	input := `export * from "@utils/helper";`
	result := r.RewriteSource(fileName, input)

	if result == input {
		t.Fatal("expected rewrite, got unchanged text")
	}
	if !contains(result, "./utils/helper.js") {
		t.Errorf("expected relative path with .js, got: %s", result)
	}
}

func TestRewriteSource_SkipsComments(t *testing.T) {
	r, tmp := setupTestRewriter(t)
	fileName := filepath.Join(tmp, "dist", "app.js")

	input := `// import { x } from "@utils/helper";
const y = 1;`
	result := r.RewriteSource(fileName, input)

	if result != input {
		t.Errorf("expected no rewrite inside comment, got: %s", result)
	}
}

func TestRewriteSource_SkipsBlockComments(t *testing.T) {
	r, tmp := setupTestRewriter(t)
	fileName := filepath.Join(tmp, "dist", "app.js")

	input := `/* import { x } from "@utils/helper"; */
const y = 1;`
	result := r.RewriteSource(fileName, input)

	if result != input {
		t.Errorf("expected no rewrite inside block comment, got: %s", result)
	}
}

func TestRewriteSource_SkipsStringLiterals(t *testing.T) {
	r, tmp := setupTestRewriter(t)
	fileName := filepath.Join(tmp, "dist", "app.js")

	input := `const s = 'import { x } from "@utils/helper"';`
	result := r.RewriteSource(fileName, input)

	if result != input {
		t.Errorf("expected no rewrite inside string literal, got: %s", result)
	}
}

func TestRewriteSource_MixedRealAndComment(t *testing.T) {
	r, tmp := setupTestRewriter(t)
	fileName := filepath.Join(tmp, "dist", "app.js")

	input := `// import { x } from "@utils/helper";
import { y } from "@utils/helper";`
	result := r.RewriteSource(fileName, input)

	if !contains(result, "// import { x } from \"@utils/helper\"") {
		t.Error("comment should be preserved unchanged")
	}
	if !contains(result, "./utils/helper.js") {
		t.Error("real import should be rewritten")
	}
}

func TestRewriteSource_NoAliasMatch(t *testing.T) {
	r, tmp := setupTestRewriter(t)
	fileName := filepath.Join(tmp, "dist", "app.js")

	input := `import express from "express";`
	result := r.RewriteSource(fileName, input)

	if result != input {
		t.Errorf("expected no rewrite for non-alias, got: %s", result)
	}
}

func TestRewriteSource_RelativeImportUnchanged(t *testing.T) {
	r, tmp := setupTestRewriter(t)
	fileName := filepath.Join(tmp, "dist", "app.js")

	input := `import { x } from "./local.js";`
	result := r.RewriteSource(fileName, input)

	if result != input {
		t.Errorf("expected no rewrite for relative import, got: %s", result)
	}
}

func TestRewriteSource_DirectoryResolvesToIndex(t *testing.T) {
	r, tmp := setupTestRewriter(t)
	fileName := filepath.Join(tmp, "dist", "app.js")

	input := `import { Model } from "@app/models";`
	result := r.RewriteSource(fileName, input)

	if !contains(result, "/index.js") {
		t.Errorf("expected /index.js for directory import, got: %s", result)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
