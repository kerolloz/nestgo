package tsc

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
)

// Cycle is one completed compilation in watch mode.
type Cycle struct {
	Diagnostics []Diagnostic

	// EmittedFiles are the files this cycle wrote. Empty is normal: the
	// compiler completes a cycle whenever it re-checks, including when nothing
	// needed re-emitting.
	EmittedFiles []string

	// First reports whether this was the initial compilation rather than a
	// rebuild triggered by an edit.
	First bool
}

// Failed reports whether the cycle produced errors.
func (c Cycle) Failed() bool { return HasErrors(c.Diagnostics) }

// The compiler brackets each cycle with status lines. These have been stable
// since TypeScript 1.x and are what every editor integration keys off; the
// native compiler emits them unchanged.
var (
	cycleStartRe = regexp.MustCompile(`(?:Starting compilation in watch mode|File change detected)`)
	cycleEndRe   = regexp.MustCompile(`Found (\d+) errors?\. Watching for file changes\.`)
)

// Watch runs the compiler in watch mode and calls onCycle after each
// compilation settles.
//
// The compiler owns file watching. It knows the project's real file set,
// including files reached through imports that no directory watcher would
// think to include, and it recompiles incrementally. Watching the source tree
// ourselves and shelling out per change would be slower and less accurate.
//
// Watch blocks until ctx is cancelled or the compiler exits. Returning an error
// from onCycle stops the watch and surfaces that error.
func Watch(ctx context.Context, opts Options, passthrough io.Writer, onCycle func(Cycle) error) error {
	if opts.Bin == "" {
		return errors.New("no compiler binary given")
	}

	args := append(buildArgs(opts), "--watch", "--preserveWatchOutput")
	cmd := exec.CommandContext(ctx, opts.Bin, args...)
	cmd.Dir = opts.Cwd
	// Env is intentionally left nil — see the package comment.

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = passthrough

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting %s --watch: %w", opts.Bin, err)
	}

	callbackErr := consumeWatchOutput(stdout, passthrough, onCycle)

	waitErr := cmd.Wait()
	if callbackErr != nil {
		return callbackErr
	}
	if ctx.Err() != nil {
		return nil // cancelled by the caller, not a failure
	}
	if waitErr != nil {
		return fmt.Errorf("%s --watch exited: %w", opts.Bin, waitErr)
	}
	return nil
}

// consumeWatchOutput parses the compiler's stream, grouping everything between
// the status lines into a cycle.
func consumeWatchOutput(r io.Reader, passthrough io.Writer, onCycle func(Cycle) error) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // elaborated diagnostics can be long

	var (
		cycle Cycle
		first = true
	)
	cycle.First = true

	for scanner.Scan() {
		line := scanner.Text()
		if passthrough != nil {
			fmt.Fprintln(passthrough, line)
		}

		switch {
		case cycleEndRe.MatchString(line):
			cycle.First = first
			first = false
			if err := onCycle(cycle); err != nil {
				return err
			}
			cycle = Cycle{}

		case cycleStartRe.MatchString(line):
			// A new cycle begins; drop anything left over from the last one.
			cycle = Cycle{}

		default:
			if path, ok := strings.CutPrefix(line, emittedFilePrefix); ok {
				if path = strings.TrimSpace(path); path != "" {
					cycle.EmittedFiles = append(cycle.EmittedFiles, path)
				}
				continue
			}
			if isContinuation(line) && len(cycle.Diagnostics) > 0 {
				last := &cycle.Diagnostics[len(cycle.Diagnostics)-1]
				last.Message += "\n" + strings.TrimRight(line, " \t")
				continue
			}
			if d, ok := parseDiagnostic(line); ok {
				cycle.Diagnostics = append(cycle.Diagnostics, d)
			}
		}
	}

	return nil
}
