// Package toolchain locates the TypeScript 7 native compiler binary that a
// project has installed.
//
// typescript@7 is a thin JS launcher over a per-platform native binary shipped
// as @typescript/typescript-<os>-<arch>. Those packages export only their
// package.json, and their location differs per package manager: npm and bun
// hoist them into node_modules/@typescript/, pnpm hides them under
// node_modules/.pnpm/, and Yarn PnP has no node_modules at all and encodes a
// content hash in the unplugged path. Building the path by hand therefore
// works on npm and silently fails everywhere else.
//
// The reliable answer is to ask the launcher itself: typescript ships
// lib/getExePath.js, which is the exact function `tsc` uses to find its own
// binary. We resolve typescript/package.json through Node's resolver and then
// import that file by absolute path (its exports map does not list it).
package toolchain

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// BinaryEnvVar overrides discovery entirely. Set it to an absolute path to run
// a specific compiler, e.g. a locally built tsgo.
const BinaryEnvVar = "NESTGO_TSC_BIN"

// TSC is a located TypeScript compiler.
type TSC struct {
	// Path is the executable to spawn. Unless discovery was overridden this is
	// the native binary, not the Node launcher — going through Node costs
	// roughly 100ms per invocation, which is the same order as a small
	// incremental build.
	Path string

	// Version is the compiler's version ("7.0.2"), or "" when discovery was
	// overridden and the version could not be read.
	Version string

	// Origin describes how Path was found, for diagnostics.
	Origin string
}

// MajorVersion returns the leading component of Version, or 0 if unknown.
func (t *TSC) MajorVersion() int {
	major, _, _ := strings.Cut(t.Version, ".")
	n, err := strconv.Atoi(major)
	if err != nil {
		return 0
	}
	return n
}

// probeScript asks the typescript package where its native binary lives, and
// reports the package version alongside it.
const probeScript = `
import { createRequire } from "node:module";
import { pathToFileURL } from "node:url";
import path from "node:path";
const require = createRequire(path.join(process.cwd(), "__nestgo_probe__.js"));
const pkgPath = require.resolve("typescript/package.json");
const getExePath = (await import(
  pathToFileURL(path.join(path.dirname(pkgPath), "lib", "getExePath.js")).href
)).default;
process.stdout.write(JSON.stringify({
  exe: getExePath(),
  version: require(pkgPath).version,
}));
`

type probeResult struct {
	Exe     string `json:"exe"`
	Version string `json:"version"`
}

// Locate finds the compiler for the project rooted at cwd.
func Locate(cwd string) (*TSC, error) {
	if override := os.Getenv(BinaryEnvVar); override != "" {
		if _, err := os.Stat(override); err != nil {
			return nil, fmt.Errorf("%s is set to %q, which cannot be used: %w", BinaryEnvVar, override, err)
		}
		return &TSC{Path: override, Origin: BinaryEnvVar}, nil
	}

	res, err := probe(cwd)
	if err != nil {
		return nil, err
	}
	return &TSC{Path: res.Exe, Version: res.Version, Origin: "typescript package"}, nil
}

func probe(cwd string) (*probeResult, error) {
	name, args, err := nodeCommand(cwd)
	if err != nil {
		return nil, err
	}
	args = append(args, "--input-type=module", "-e", probeScript)

	cmd := exec.Command(name, args...)
	cmd.Dir = cwd
	var stderr strings.Builder
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		return nil, locateFailure(cwd, stderr.String(), err)
	}

	var res probeResult
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, fmt.Errorf("could not understand the TypeScript locator output %q: %w", string(out), err)
	}
	if res.Exe == "" {
		return nil, fmt.Errorf("the TypeScript locator returned no binary path")
	}
	if _, err := os.Stat(res.Exe); err != nil {
		return nil, fmt.Errorf("TypeScript reported its compiler at %s, but it is not usable: %w", res.Exe, err)
	}
	return &res, nil
}

// nodeCommand returns the command that runs a Node script for this project.
// Under Yarn PnP there is no node_modules, so bare `node` cannot resolve
// anything; `yarn node` injects the PnP resolver and loader.
func nodeCommand(cwd string) (string, []string, error) {
	if _, err := os.Stat(filepath.Join(cwd, ".pnp.cjs")); err == nil {
		if path, err := exec.LookPath("yarn"); err == nil {
			return path, []string{"node"}, nil
		}
		return "", nil, fmt.Errorf("this project uses Yarn Plug'n'Play (.pnp.cjs) but yarn is not on PATH")
	}

	path, err := exec.LookPath("node")
	if err != nil {
		return "", nil, fmt.Errorf("node is required to locate the TypeScript compiler, but it is not on PATH")
	}
	return path, nil, nil
}

func locateFailure(cwd, stderr string, err error) error {
	hint := "install it with: npm install --save-dev typescript@^7"
	if strings.Contains(stderr, "Cannot find module") && !strings.Contains(stderr, "typescript/package.json") {
		// typescript resolved but something inside it did not: almost always a
		// TypeScript 6 or older layout, which has no native binary to find.
		hint = "nestgo needs TypeScript 7 or newer; upgrade with: npm install --save-dev typescript@^7"
	}
	return fmt.Errorf("could not find the TypeScript compiler for %s — %s\n%s",
		cwd, hint, strings.TrimSpace(stderr))
}
