package plugins

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateWithoutPluginsDoesNothing(t *testing.T) {
	written, err := Generate(context.Background(), Options{Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if written != "" {
		t.Errorf("expected no file to be written, got %q", written)
	}
}

// A project asking for a plugin it has not installed should be told which one,
// not handed a Node stack trace.
func TestGenerateReportsMissingPlugin(t *testing.T) {
	requireNode(t)

	dir := t.TempDir()
	writeMinimalProject(t, dir)

	_, err := Generate(context.Background(), Options{
		Cwd:          dir,
		TsConfigPath: "tsconfig.json",
		OutputDir:    filepath.Join(dir, "src"),
		Plugins:      []Plugin{{Name: "@nestjs/definitely-not-installed"}},
	})
	if err == nil {
		t.Fatal("expected an error for a plugin that is not installed")
	}
	if !strings.Contains(err.Error(), "@nestjs/definitely-not-installed") {
		t.Errorf("error should name the plugin, got: %v", err)
	}
	if !strings.Contains(err.Error(), "not installed") {
		t.Errorf("error should say it is not installed, got: %v", err)
	}
}

// The visitors need the classic compiler API, which TypeScript 7 does not have.
// Without a TypeScript 6 present the failure has to explain what to install,
// because nothing about "ts.visitNode is not a function" tells a user that.
func TestGenerateExplainsMissingTypeScript6(t *testing.T) {
	requireNode(t)

	dir := t.TempDir()
	writeMinimalProject(t, dir)

	// A plugin package that exists but no TypeScript at all.
	pluginDir := filepath.Join(dir, "node_modules", "fake-plugin")
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(pluginDir, "package.json"), `{"name":"fake-plugin","main":"index.js"}`)
	write(t, filepath.Join(pluginDir, "index.js"), `exports.ReadonlyVisitor = class {};`)

	_, err := Generate(context.Background(), Options{
		Cwd:          dir,
		TsConfigPath: "tsconfig.json",
		OutputDir:    filepath.Join(dir, "src"),
		Plugins:      []Plugin{{Name: "fake-plugin"}},
	})
	if err == nil {
		t.Fatal("expected an error when no TypeScript 6 is available")
	}
	if !strings.Contains(err.Error(), "typescript6") {
		t.Errorf("error should name the package to install, got: %v", err)
	}
}

func TestPluginOptionsReachTheSidecar(t *testing.T) {
	requireNode(t)

	dir := t.TempDir()
	writeMinimalProject(t, dir)

	// A plugin whose visitor records the options it was constructed with, plus
	// a stub TypeScript that satisfies the sidecar's API probe.
	pluginDir := filepath.Join(dir, "node_modules", "recording-plugin")
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(pluginDir, "package.json"), `{"name":"recording-plugin","main":"index.js"}`)
	write(t, filepath.Join(pluginDir, "index.js"), `
const fs = require('fs');
exports.ReadonlyVisitor = class {
  constructor(options) { fs.writeFileSync(process.env.RECORD_TO, JSON.stringify(options)); }
  visit() {}
  collect() { return {}; }
  get typeImports() { return {}; }
};`)

	tsDir := filepath.Join(dir, "node_modules", "@typescript", "typescript6")
	if err := os.MkdirAll(tsDir, 0755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(tsDir, "package.json"), `{"name":"@typescript/typescript6","main":"index.js"}`)
	write(t, filepath.Join(tsDir, "index.js"), stubTypeScript)

	record := filepath.Join(dir, "options.json")
	t.Setenv("RECORD_TO", record)

	if _, err := Generate(context.Background(), Options{
		Cwd:          dir,
		TsConfigPath: "tsconfig.json",
		OutputDir:    filepath.Join(dir, "src"),
		Plugins:      []Plugin{{Name: "recording-plugin", Options: map[string]any{"introspectComments": true}}},
	}); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	recorded, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("plugin was not constructed: %v", err)
	}
	got := string(recorded)
	if !strings.Contains(got, `"introspectComments":true`) {
		t.Errorf("configured options should reach the visitor: %s", got)
	}
	if !strings.Contains(got, `"readonly":true`) {
		t.Errorf("visitors must be constructed in readonly mode: %s", got)
	}
}

// stubTypeScript is the smallest module that satisfies the sidecar: it must
// look like the classic API and produce a printable program.
const stubTypeScript = `
exports.createProgram = () => ({ getSourceFiles: () => [] });
exports.getParsedCommandLineOfConfigFile = () => ({ fileNames: [], options: {} });
exports.sys = {};
exports.createPrinter = () => ({ printNode: () => 'export default async () => ({});' });
exports.createSourceFile = () => ({});
exports.isObjectLiteralExpression = () => false;
exports.EmitHint = { Unspecified: 0 };
exports.ScriptTarget = { Latest: 99 };
exports.ScriptKind = { TS: 3 };
exports.NewLineKind = { LineFeed: 1 };
exports.SyntaxKind = { AsyncKeyword: 134, EqualsGreaterThanToken: 39 };
exports.NodeFlags = { Const: 2, AwaitContext: 32768, ContextFlags: 0, TypeExcludesFlags: 0 };
exports.factory = new Proxy({}, { get: () => () => ({}) });
`

func writeMinimalProject(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "package.json"), `{"name":"fixture","private":true}`)
	write(t, filepath.Join(dir, "tsconfig.json"),
		`{"compilerOptions":{"target":"ES2022","outDir":"./dist","rootDir":"./src"},"include":["src/**/*"]}`)
	write(t, filepath.Join(dir, "src", "main.ts"), "export const x = 1;\n")
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func requireNode(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not on PATH")
	}
}
