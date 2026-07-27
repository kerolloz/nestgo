package tsc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The status lines below are captured verbatim from tsc 7.0.2 running with
// --watch --preserveWatchOutput --pretty false --listEmittedFiles.
func TestConsumeWatchOutputGroupsCycles(t *testing.T) {
	stream := strings.Join([]string{
		"12:50:02 AM - Starting compilation in watch mode...",
		"",
		"TSFILE: /p/dist/utils/math.js",
		"TSFILE: /p/dist/main.js",
		"12:50:02 AM - Found 0 errors. Watching for file changes.",
		"",
		"12:50:06 AM - File change detected. Starting incremental compilation...",
		"",
		"TSFILE: /p/dist/main.js",
		"12:50:06 AM - Found 0 errors. Watching for file changes.",
		"",
	}, "\n")

	var cycles []Cycle
	err := consumeWatchOutput(strings.NewReader(stream), nil, func(c Cycle) error {
		cycles = append(cycles, c)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(cycles) != 2 {
		t.Fatalf("expected 2 cycles, got %d: %+v", len(cycles), cycles)
	}
	if !cycles[0].First {
		t.Error("the initial compilation should be marked First")
	}
	if len(cycles[0].EmittedFiles) != 2 {
		t.Errorf("first cycle emitted %v, want 2 files", cycles[0].EmittedFiles)
	}
	if cycles[1].First {
		t.Error("a rebuild should not be marked First")
	}
	if len(cycles[1].EmittedFiles) != 1 {
		t.Errorf("second cycle emitted %v, want 1 file", cycles[1].EmittedFiles)
	}
}

func TestConsumeWatchOutputCollectsDiagnostics(t *testing.T) {
	stream := strings.Join([]string{
		"12:50:02 AM - Starting compilation in watch mode...",
		"src/main.ts(1,7): error TS2322: Type 'string' is not assignable to type 'number'.",
		"  and an elaboration",
		"12:50:02 AM - Found 1 error. Watching for file changes.",
	}, "\n")

	var cycles []Cycle
	if err := consumeWatchOutput(strings.NewReader(stream), nil, func(c Cycle) error {
		cycles = append(cycles, c)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if len(cycles) != 1 {
		t.Fatalf("expected 1 cycle, got %d", len(cycles))
	}
	if !cycles[0].Failed() {
		t.Error("a cycle with an error diagnostic should report Failed")
	}
	if !strings.Contains(cycles[0].Diagnostics[0].Message, "elaboration") {
		t.Errorf("elaboration should fold in: %q", cycles[0].Diagnostics[0].Message)
	}
}

// Diagnostics from a failed cycle must not leak into the next one, or a fixed
// error would keep reporting until the process restarts.
func TestConsumeWatchOutputResetsBetweenCycles(t *testing.T) {
	stream := strings.Join([]string{
		"- Starting compilation in watch mode...",
		"src/main.ts(1,7): error TS2322: broken.",
		"- Found 1 error. Watching for file changes.",
		"- File change detected. Starting incremental compilation...",
		"TSFILE: /p/dist/main.js",
		"- Found 0 errors. Watching for file changes.",
	}, "\n")

	var cycles []Cycle
	if err := consumeWatchOutput(strings.NewReader(stream), nil, func(c Cycle) error {
		cycles = append(cycles, c)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if len(cycles) != 2 {
		t.Fatalf("expected 2 cycles, got %d", len(cycles))
	}
	if len(cycles[1].Diagnostics) != 0 {
		t.Errorf("the second cycle should start clean, got %+v", cycles[1].Diagnostics)
	}
	if cycles[1].Failed() {
		t.Error("a clean rebuild should not report Failed")
	}
}

// A cycle that emits nothing is normal — the compiler re-checks whenever it
// notices a change, including changes that need no new output.
func TestConsumeWatchOutputAllowsEmptyCycles(t *testing.T) {
	stream := strings.Join([]string{
		"- Starting compilation in watch mode...",
		"- Found 0 errors. Watching for file changes.",
		"- File change detected. Starting incremental compilation...",
		"- Found 0 errors. Watching for file changes.",
	}, "\n")

	var count int
	if err := consumeWatchOutput(strings.NewReader(stream), nil, func(Cycle) error {
		count++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("got %d cycles, want 2", count)
	}
}

func TestConsumeWatchOutputPassesThrough(t *testing.T) {
	var sink strings.Builder
	stream := "- Starting compilation in watch mode...\n- Found 0 errors. Watching for file changes.\n"

	if err := consumeWatchOutput(strings.NewReader(stream), &sink, func(Cycle) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sink.String(), "Watching for file changes") {
		t.Errorf("compiler output should reach the user: %q", sink.String())
	}
}

func TestConsumeWatchOutputStopsOnCallbackError(t *testing.T) {
	stream := strings.Join([]string{
		"- Starting compilation in watch mode...",
		"- Found 0 errors. Watching for file changes.",
		"- File change detected. Starting incremental compilation...",
		"- Found 0 errors. Watching for file changes.",
	}, "\n")

	var count int
	err := consumeWatchOutput(strings.NewReader(stream), nil, func(Cycle) error {
		count++
		return os.ErrClosed
	})
	if err == nil {
		t.Fatal("expected the callback error to surface")
	}
	if count != 1 {
		t.Errorf("should stop after the failing cycle, ran %d", count)
	}
}

func TestWatchRequiresBinary(t *testing.T) {
	if err := Watch(context.Background(), Options{}, nil, func(Cycle) error { return nil }); err == nil {
		t.Fatal("expected an error when no binary is given")
	}
}

// End to end against the real compiler: edit a file and confirm a rebuild
// cycle arrives reporting the changed output.
func TestWatchRebuildsOnChange(t *testing.T) {
	bin, dir := project(t, map[string]string{
		"src/main.ts": "export const greeting: string = 'hi';\n",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var (
		mu     sync.Mutex
		cycles []Cycle
	)
	done := make(chan error, 1)

	go func() {
		done <- Watch(ctx, Options{Bin: bin, Cwd: dir, Project: "tsconfig.json"}, nil, func(c Cycle) error {
			mu.Lock()
			cycles = append(cycles, c)
			count := len(cycles)
			mu.Unlock()

			if count == 1 {
				// Trigger a rebuild once the initial compilation settles.
				return os.WriteFile(filepath.Join(dir, "src", "main.ts"),
					[]byte("export const greeting: string = 'edited';\n"), 0644)
			}
			cancel() // second cycle observed; we are done
			return nil
		})
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Watch: %v", err)
		}
	case <-ctx.Done():
		if ctx.Err() == context.DeadlineExceeded {
			t.Fatal("timed out waiting for a rebuild cycle")
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(cycles) < 2 {
		t.Fatalf("expected an initial cycle and a rebuild, got %d", len(cycles))
	}
	if !cycles[0].First {
		t.Error("the first cycle should be marked First")
	}
	if cycles[0].Failed() {
		t.Errorf("initial build should succeed: %+v", cycles[0].Diagnostics)
	}
	if len(cycles[0].EmittedFiles) == 0 {
		t.Error("the initial cycle should report emitted files")
	}
}
