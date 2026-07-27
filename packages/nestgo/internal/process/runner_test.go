package process

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestQuoteShellArg(t *testing.T) {
	cases := []struct{ in, want string }{
		{"simple", "'simple'"},
		{"has space", "'has space'"},
		{"has'quote", "'has'\\''quote'"},
		{"has$dollar", "'has$dollar'"},
		{"has`backtick`", "'has`backtick`'"},
	}
	for _, tc := range cases {
		if got := quoteShellArg(tc.in); got != tc.want {
			t.Errorf("quoteShellArg(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

func TestResolveOutputFileWithRootDir(t *testing.T) {
	dir := t.TempDir()
	// Create dist/src/main.js
	os.MkdirAll(filepath.Join(dir, "dist", "src"), 0755)
	os.WriteFile(filepath.Join(dir, "dist", "src", "main.js"), []byte(""), 0644)

	r := &Runner{opts: Options{Cwd: dir, OutDir: "dist", SourceRoot: "src", EntryFile: "main"}}
	got := r.resolveOutputFile()
	want := filepath.Join(dir, "dist", "src", "main.js")
	if got != want {
		t.Errorf("resolveOutputFile() = %q, want %q", got, want)
	}
}

func TestResolveOutputFileRootDirCandidate(t *testing.T) {
	dir := t.TempDir()
	// Only dist/app/main.js exists (rootDir differs from sourceRoot)
	os.MkdirAll(filepath.Join(dir, "dist", "app"), 0755)
	os.WriteFile(filepath.Join(dir, "dist", "app", "main.js"), []byte(""), 0644)

	r := &Runner{opts: Options{Cwd: dir, OutDir: "dist", SourceRoot: "src", RootDir: "app", EntryFile: "main"}}
	got := r.resolveOutputFile()
	want := filepath.Join(dir, "dist", "app", "main.js")
	if got != want {
		t.Errorf("resolveOutputFile() = %q, want %q", got, want)
	}
}

func TestResolveOutputFileFlatCandidate(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "dist"), 0755)
	os.WriteFile(filepath.Join(dir, "dist", "main.js"), []byte(""), 0644)

	r := &Runner{opts: Options{Cwd: dir, OutDir: "dist", SourceRoot: "src", EntryFile: "main"}}
	got := r.resolveOutputFile()
	want := filepath.Join(dir, "dist", "main.js")
	if got != want {
		t.Errorf("resolveOutputFile() = %q, want %q", got, want)
	}
}

func TestStartKillWaitNoRace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses unix sleep")
	}
	dir := t.TempDir()
	// Write a fake entry file so the runner can find it
	os.MkdirAll(filepath.Join(dir, "dist", "src"), 0755)
	os.WriteFile(filepath.Join(dir, "dist", "src", "main.js"), []byte(""), 0644)

	r := New(Options{
		Cwd:        dir,
		OutDir:     "dist",
		SourceRoot: "src",
		EntryFile:  "main",
		Binary:     "sleep",
		ExtraArgs:  []string{"10"},
		Shell:      false,
	})

	if err := r.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Call Kill and Wait concurrently — must not panic or deadlock.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); r.Kill() }()
	go func() { defer wg.Done(); r.Wait() }()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Kill/Wait deadlocked")
	}
}

func TestWaitAfterKill(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses unix sleep")
	}
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "dist", "src"), 0755)
	os.WriteFile(filepath.Join(dir, "dist", "src", "main.js"), []byte(""), 0644)

	r := New(Options{
		Cwd:        dir,
		OutDir:     "dist",
		SourceRoot: "src",
		EntryFile:  "main",
		Binary:     "sleep",
		ExtraArgs:  []string{"10"},
		Shell:      false,
	})

	if err := r.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	r.Kill()

	done := make(chan int, 1)
	go func() { done <- r.Wait() }()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Wait hung after Kill")
	}
}

func TestResolveOutputFileMultipleCandidates(t *testing.T) {
	dir := t.TempDir()
	// Create both dist/src/main.js and dist/main.js
	os.MkdirAll(filepath.Join(dir, "dist", "src"), 0755)
	os.WriteFile(filepath.Join(dir, "dist", "src", "main.js"), []byte(""), 0644)
	os.WriteFile(filepath.Join(dir, "dist", "main.js"), []byte(""), 0644)

	r := &Runner{opts: Options{Cwd: dir, OutDir: "dist", SourceRoot: "src", EntryFile: "main"}}
	got := r.resolveOutputFile()
	// Should return the first candidate (sourceRoot-based)
	want := filepath.Join(dir, "dist", "src", "main.js")
	if got != want {
		t.Errorf("resolveOutputFile() = %q, want %q", got, want)
	}
}
