package toolchain

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocateUsesEnvOverride(t *testing.T) {
	fake := filepath.Join(t.TempDir(), "my-tsc")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(BinaryEnvVar, fake)

	// cwd has no typescript installed: the override must win before any probe.
	tsc, err := Locate(t.TempDir())
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	if tsc.Path != fake {
		t.Errorf("Path = %q, want %q", tsc.Path, fake)
	}
	if tsc.Origin != BinaryEnvVar {
		t.Errorf("Origin = %q, want %q", tsc.Origin, BinaryEnvVar)
	}
}

func TestLocateRejectsMissingEnvOverride(t *testing.T) {
	t.Setenv(BinaryEnvVar, filepath.Join(t.TempDir(), "does-not-exist"))

	_, err := Locate(t.TempDir())
	if err == nil {
		t.Fatal("expected an error for a non-existent override")
	}
	if !strings.Contains(err.Error(), BinaryEnvVar) {
		t.Errorf("error should name the env var, got: %v", err)
	}
}

// The failure users hit most is having no typescript at all, so the message
// has to say what to install rather than surfacing a Node stack trace.
func TestLocateWithoutTypeScriptExplainsHow(t *testing.T) {
	requireNode(t)
	t.Setenv(BinaryEnvVar, "")

	_, err := Locate(t.TempDir())
	if err == nil {
		t.Fatal("expected an error when typescript is not installed")
	}
	if !strings.Contains(err.Error(), "npm install") {
		t.Errorf("error should suggest how to install TypeScript, got: %v", err)
	}
}

func TestLocateFindsInstalledTypeScript(t *testing.T) {
	requireNode(t)
	t.Setenv(BinaryEnvVar, "")

	project := installTypeScript(t)
	tsc, err := Locate(project)
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}

	if info, err := os.Stat(tsc.Path); err != nil {
		t.Fatalf("located binary is not usable: %v", err)
	} else if info.Mode()&0111 == 0 {
		t.Errorf("located binary %s is not executable", tsc.Path)
	}
	if tsc.MajorVersion() < 7 {
		t.Errorf("MajorVersion() = %d (version %q), want >= 7", tsc.MajorVersion(), tsc.Version)
	}

	// It must be the native binary, not the JS launcher: spawning through Node
	// costs about 100ms per build.
	if strings.HasSuffix(tsc.Path, ".js") {
		t.Errorf("expected the native binary, got a JS entry point: %s", tsc.Path)
	}

	// And it must actually run.
	out, err := exec.Command(tsc.Path, "--version").Output()
	if err != nil {
		t.Fatalf("running the located compiler: %v", err)
	}
	if !strings.Contains(string(out), tsc.Version) {
		t.Errorf("compiler reported %q, but Locate said %q", strings.TrimSpace(string(out)), tsc.Version)
	}
}

func TestMajorVersion(t *testing.T) {
	for _, tc := range []struct {
		version string
		want    int
	}{
		{"7.0.2", 7},
		{"7.1.0-dev.20260727.1", 7},
		{"6.0.3", 6},
		{"", 0},
		{"nonsense", 0},
	} {
		if got := (&TSC{Version: tc.version}).MajorVersion(); got != tc.want {
			t.Errorf("MajorVersion(%q) = %d, want %d", tc.version, got, tc.want)
		}
	}
}

func requireNode(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not on PATH")
	}
}

// installTypeScript creates a throwaway project with typescript installed.
// Skips rather than fails when npm or the network is unavailable, so the suite
// still runs offline.
func installTypeScript(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping npm install in short mode")
	}
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm is not on PATH")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"name":"probe-fixture","version":"1.0.0","private":true}`), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("npm", "install", "--silent", "--no-audit", "--no-fund", "typescript@^7")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("could not install typescript (offline?): %v\n%s", err, out)
	}
	return dir
}
