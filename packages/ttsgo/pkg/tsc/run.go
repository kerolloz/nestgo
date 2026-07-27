// Package tsc runs the TypeScript 7 native compiler as a subprocess and reads
// its output.
//
// One trap is worth knowing about. The compiler is a Go program, and Go's
// os.Getwd trusts $PWD whenever it stats to the same inode as the real working
// directory. On macOS (/tmp is a symlink to /private/tmp) and in containers
// with symlinked mounts, that decides whether the compiler reports paths under
// the directory we asked for or under its resolved target — silently changing
// every path in --listEmittedFiles and every diagnostic.
//
// os/exec already handles this: when Cmd.Env is nil it sets PWD to Cmd.Dir.
// So Run deliberately leaves Env alone. Anything that sets Cmd.Env here must
// carry PWD forward, or the child will resolve paths through the symlink.
// TestRunReportsPathsUnderTheRequestedDirectory guards this.
package tsc

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Exit codes the compiler uses. Anything else is treated as a failure to run.
const (
	exitSuccess        = 0
	exitErrorsNoEmit   = 1 // errors, and outputs were skipped
	exitErrorsWithEmit = 2 // errors, but outputs were still written
)

// Options describes one compiler invocation.
type Options struct {
	// Bin is the compiler executable, from the toolchain package.
	Bin string

	// Cwd is the directory the compiler runs in. Relative paths in its output
	// are relative to this.
	Cwd string

	// Project is the tsconfig path passed to -p. Empty lets the compiler find
	// tsconfig.json itself.
	Project string

	// OutDir overrides the tsconfig outDir when set.
	OutDir string

	// NoEmit type-checks without writing outputs.
	NoEmit bool

	// Incremental enables .tsbuildinfo reuse, which is what makes rebuilds
	// cheap. Harmless on a cold build.
	Incremental bool

	// Args are appended last, so they can override anything above.
	Args []string
}

// Result is the outcome of an invocation.
type Result struct {
	Diagnostics []Diagnostic

	// EmittedFiles are absolute paths reported by --listEmittedFiles. Empty
	// when NoEmit is set or nothing changed.
	EmittedFiles []string

	// ExitCode is the compiler's exit status.
	ExitCode int

	// Output is the compiler's combined stdout and stderr, kept for
	// diagnosing unexpected exit codes.
	Output string
}

// Failed reports whether the invocation should fail a build.
func (r *Result) Failed() bool {
	return r.ExitCode != exitSuccess || HasErrors(r.Diagnostics)
}

// EmitSkipped reports whether the compiler declined to write outputs because
// of errors.
func (r *Result) EmitSkipped() bool { return r.ExitCode == exitErrorsNoEmit }

// Run invokes the compiler once and parses what it printed.
//
// A non-zero exit status is not an error: type errors are a normal outcome and
// are reported through Result. An error is returned only when the compiler
// could not be run or exited in a way that has no diagnostic explanation.
func Run(ctx context.Context, opts Options) (*Result, error) {
	if opts.Bin == "" {
		return nil, errors.New("no compiler binary given")
	}

	cmd := exec.CommandContext(ctx, opts.Bin, buildArgs(opts)...)
	cmd.Dir = opts.Cwd
	// Env is intentionally left nil — see the package comment.

	// The compiler writes diagnostics to stdout; stderr carries crashes. Read
	// them together so an unexpected failure is not silently discarded.
	out, err := cmd.CombinedOutput()
	output := string(out)

	result := &Result{Output: output}
	result.Diagnostics, result.EmittedFiles = ParseOutput(output)

	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return nil, fmt.Errorf("running %s: %w", opts.Bin, err)
		}
		result.ExitCode = exitErr.ExitCode()
	}

	switch result.ExitCode {
	case exitSuccess, exitErrorsNoEmit, exitErrorsWithEmit:
	default:
		return nil, fmt.Errorf("%s exited with status %d\n%s",
			filepath.Base(opts.Bin), result.ExitCode, strings.TrimSpace(output))
	}

	// A failing status with nothing parseable means the compiler died in a way
	// we cannot report usefully; surface its output rather than an empty error.
	if result.ExitCode != exitSuccess && len(result.Diagnostics) == 0 {
		return nil, fmt.Errorf("%s failed with status %d but reported no diagnostics\n%s",
			filepath.Base(opts.Bin), result.ExitCode, strings.TrimSpace(output))
	}

	return result, nil
}

func buildArgs(opts Options) []string {
	// --pretty false keeps diagnostics in the stable, parseable format instead
	// of the ANSI-decorated one. --listEmittedFiles is what lets callers
	// rewrite only the files that actually changed.
	args := []string{"--pretty", "false"}

	if opts.Project != "" {
		args = append(args, "-p", opts.Project)
	}
	if opts.OutDir != "" {
		args = append(args, "--outDir", opts.OutDir)
	}
	if opts.Incremental {
		args = append(args, "--incremental")
	}
	if opts.NoEmit {
		args = append(args, "--noEmit")
	} else {
		args = append(args, "--listEmittedFiles")
	}

	return append(args, opts.Args...)
}
