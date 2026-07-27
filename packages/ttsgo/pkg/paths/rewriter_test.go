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
	r := New(tmp, paths, outDir, "src", "")
	return r, tmp
}

func TestRewriteSource_BaseURLRelativeTargets(t *testing.T) {
	tmp := t.TempDir()
	srcDir := filepath.Join(tmp, "src")
	os.MkdirAll(filepath.Join(srcDir, "utils"), 0755)
	os.WriteFile(filepath.Join(srcDir, "utils", "helper.ts"), []byte(""), 0644)

	// baseUrl "./src": targets are relative to src, not the project root.
	paths := map[string][]string{"@utils/*": {"utils/*"}}
	r := New(tmp, paths, filepath.Join(tmp, "dist"), "src", "src")

	fileName := filepath.Join(tmp, "dist", "app.js")
	result := r.RewriteSource(fileName, `const h = require("@utils/helper");`)

	if !contains(result, "./utils/helper.js") {
		t.Errorf("expected baseUrl-relative rewrite to ./utils/helper.js, got: %s", result)
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
