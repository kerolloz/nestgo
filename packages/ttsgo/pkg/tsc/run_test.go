package tsc

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kerolloz/ttsgo/pkg/toolchain"
)

func TestRunRequiresBinary(t *testing.T) {
	if _, err := Run(context.Background(), Options{Cwd: t.TempDir()}); err == nil {
		t.Fatal("expected an error when no binary is given")
	}
}

func TestRunCompilesAndListsEmittedFiles(t *testing.T) {
	bin, dir := project(t, map[string]string{
		"src/main.ts": "export const greeting: string = 'hi';\n",
	})

	res, err := Run(context.Background(), Options{Bin: bin, Cwd: dir, Project: "tsconfig.json"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.Failed() {
		t.Fatalf("clean project should not fail: exit=%d diags=%+v", res.ExitCode, res.Diagnostics)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("expected no diagnostics, got %+v", res.Diagnostics)
	}

	var foundJS bool
	for _, f := range res.EmittedFiles {
		if !filepath.IsAbs(f) {
			t.Errorf("emitted path should be absolute: %s", f)
		}
		if strings.HasSuffix(f, "main.js") {
			foundJS = true
			if _, err := os.Stat(f); err != nil {
				t.Errorf("reported emitted file does not exist: %v", err)
			}
		}
	}
	if !foundJS {
		t.Errorf("main.js not reported as emitted: %v", res.EmittedFiles)
	}
}

// Type errors are a normal outcome, not a failure to run: Run must report them
// through Result rather than returning an error.
func TestRunReportsTypeErrors(t *testing.T) {
	bin, dir := project(t, map[string]string{
		"src/main.ts": "export const n: number = 'not a number';\n",
	})

	res, err := Run(context.Background(), Options{Bin: bin, Cwd: dir, Project: "tsconfig.json"})
	if err != nil {
		t.Fatalf("type errors should not be a Run error: %v", err)
	}

	if !res.Failed() {
		t.Error("Failed() should be true when the project has type errors")
	}
	if !HasErrors(res.Diagnostics) {
		t.Fatalf("expected an error diagnostic, got %+v", res.Diagnostics)
	}

	d := res.Diagnostics[0]
	if d.Code != 2322 {
		t.Errorf("expected TS2322, got TS%d: %s", d.Code, d.Message)
	}
	if !strings.HasSuffix(d.File, "main.ts") {
		t.Errorf("diagnostic file = %q", d.File)
	}
	if d.Line == 0 || d.Column == 0 {
		t.Errorf("diagnostic should carry a position, got (%d,%d)", d.Line, d.Column)
	}
}

func TestRunNoEmit(t *testing.T) {
	bin, dir := project(t, map[string]string{
		"src/main.ts": "export const greeting: string = 'hi';\n",
	})

	res, err := Run(context.Background(), Options{
		Bin: bin, Cwd: dir, Project: "tsconfig.json", NoEmit: true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.EmittedFiles) != 0 {
		t.Errorf("--noEmit should emit nothing, got %v", res.EmittedFiles)
	}
	if _, err := os.Stat(filepath.Join(dir, "dist")); !os.IsNotExist(err) {
		t.Errorf("--noEmit should not create dist (err=%v)", err)
	}
}

// The compiler is a Go program and trusts $PWD when it stats to the same inode
// as the real working directory. If we let a stale or symlinked PWD through,
// every path it reports is silently rooted somewhere the caller did not ask
// for. This is a live hazard on macOS, where /tmp is a symlink.
func TestRunReportsPathsUnderTheRequestedDirectory(t *testing.T) {
	bin, dir := project(t, map[string]string{
		"src/main.ts": "export const greeting: string = 'hi';\n",
	})

	// Point PWD somewhere else entirely, the way an inherited environment would.
	t.Setenv("PWD", t.TempDir())

	// And reach the project through a symlink, so the real path differs from
	// the one we ask for.
	link := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(dir, link); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	res, err := Run(context.Background(), Options{Bin: bin, Cwd: link, Project: "tsconfig.json"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.EmittedFiles) == 0 {
		t.Fatal("expected emitted files")
	}

	for _, f := range res.EmittedFiles {
		if !strings.HasPrefix(f, link+string(filepath.Separator)) {
			t.Errorf("emitted path %q is not under the requested directory %q", f, link)
		}
	}
}

func TestRunSurfacesUnexpectedFailures(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "not-a-compiler")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho boom >&2\nexit 42\n"), 0755); err != nil {
		t.Fatal(err)
	}

	_, err := Run(context.Background(), Options{Bin: fake, Cwd: dir})
	if err == nil {
		t.Fatal("expected an error for an unrecognised exit status")
	}
	if !strings.Contains(err.Error(), "42") || !strings.Contains(err.Error(), "boom") {
		t.Errorf("error should carry the status and the output, got: %v", err)
	}
}

func TestBuildArgs(t *testing.T) {
	args := buildArgs(Options{Project: "tsconfig.build.json", OutDir: "out", Incremental: true})
	joined := strings.Join(args, " ")

	for _, want := range []string{
		"--pretty false",         // stable, parseable diagnostics
		"-p tsconfig.build.json", //
		"--outDir out",           //
		"--incremental",          // cheap rebuilds
		"--listEmittedFiles",     // drives incremental alias rewriting
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("args missing %q: %v", want, args)
		}
	}

	noEmit := strings.Join(buildArgs(Options{NoEmit: true}), " ")
	if !strings.Contains(noEmit, "--noEmit") {
		t.Errorf("expected --noEmit: %s", noEmit)
	}
	if strings.Contains(noEmit, "--listEmittedFiles") {
		t.Errorf("--listEmittedFiles is pointless with --noEmit: %s", noEmit)
	}

	// Caller args come last so they can override the defaults.
	override := buildArgs(Options{Args: []string{"--pretty", "true"}})
	if last := strings.Join(override[len(override)-2:], " "); last != "--pretty true" {
		t.Errorf("caller args should come last, got %v", override)
	}
}

// project writes a minimal TypeScript project and returns the located
// compiler plus the project directory.
func project(t *testing.T, files map[string]string) (bin, dir string) {
	t.Helper()

	dir = installTypeScript(t)
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Only supply a default config when the caller did not bring its own.
	if _, ok := files["tsconfig.json"]; !ok {
		tsconfig := `{
  "compilerOptions": {
    "target": "ES2022",
    "module": "CommonJS",
    "outDir": "./dist",
    "rootDir": "./src",
    "strict": true
  },
  "include": ["src/**/*"]
}`
		if err := os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte(tsconfig), 0644); err != nil {
			t.Fatal(err)
		}
	}

	tsc, err := toolchain.Locate(dir)
	if err != nil {
		t.Fatalf("locating the compiler: %v", err)
	}
	return tsc.Path, dir
}

// installTypeScript creates a throwaway project with typescript installed,
// skipping when npm or the network is unavailable so the suite still runs
// offline.
func installTypeScript(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping npm install in short mode")
	}
	for _, tool := range []string{"npm", "node"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not on PATH", tool)
		}
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"name":"tsc-fixture","version":"1.0.0","private":true}`), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("npm", "install", "--silent", "--no-audit", "--no-fund", "typescript@^7")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("could not install typescript (offline?): %v\n%s", err, out)
	}
	return dir
}
