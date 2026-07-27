package tsc

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseOutputLocatedDiagnostic(t *testing.T) {
	// Verbatim from tsc 7.0.2 with --pretty false.
	out := `src/bad.ts(1,7): error TS2322: Type 'string' is not assignable to type 'number'.`

	diags, emitted := ParseOutput(out)
	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %+v", len(diags), diags)
	}
	want := Diagnostic{
		File: "src/bad.ts", Line: 1, Column: 7,
		Category: CategoryError, Code: 2322,
		Message: "Type 'string' is not assignable to type 'number'.",
	}
	if diags[0] != want {
		t.Errorf("got %+v, want %+v", diags[0], want)
	}
	if len(emitted) != 0 {
		t.Errorf("expected no emitted files, got %v", emitted)
	}
}

func TestParseOutputUnlocatedDiagnostic(t *testing.T) {
	// A missing project has no source position.
	out := `error TS5058: The specified path does not exist: '/tmp/nope.json'.`

	diags, _ := ParseOutput(out)
	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics, want 1", len(diags))
	}
	d := diags[0]
	if d.File != "" || d.Line != 0 || d.Column != 0 {
		t.Errorf("expected no position, got %s(%d,%d)", d.File, d.Line, d.Column)
	}
	if d.Code != 5058 || d.Category != CategoryError {
		t.Errorf("got code %d category %q", d.Code, d.Category)
	}
}

// Elaborated diagnostics span several indented lines. They must fold into the
// diagnostic above rather than being dropped or counted separately — an
// "Argument of type X is not assignable" message is close to useless without
// the elaboration explaining which property mismatched.
func TestParseOutputFoldsElaboration(t *testing.T) {
	out := strings.Join([]string{
		`src/bad.ts(5,6): error TS2345: Argument of type 'B' is not assignable to parameter of type 'A'.`,
		`  The types of 'x.y.z' are incompatible between these types.`,
		`    Type 'number' is not assignable to type 'string'.`,
	}, "\n")

	diags, _ := ParseOutput(out)
	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics, want 1 folded: %+v", len(diags), diags)
	}
	for _, fragment := range []string{
		"Argument of type 'B'",
		"The types of 'x.y.z' are incompatible",
		"Type 'number' is not assignable to type 'string'.",
	} {
		if !strings.Contains(diags[0].Message, fragment) {
			t.Errorf("folded message missing %q, got:\n%s", fragment, diags[0].Message)
		}
	}
}

func TestParseOutputEmittedFiles(t *testing.T) {
	out := strings.Join([]string{
		"TSFILE: /project/dist/main.js",
		"TSFILE: /project/dist/main.d.ts",
		"",
	}, "\n")

	diags, emitted := ParseOutput(out)
	want := []string{"/project/dist/main.js", "/project/dist/main.d.ts"}
	if !reflect.DeepEqual(emitted, want) {
		t.Errorf("got %v, want %v", emitted, want)
	}
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics, got %+v", diags)
	}
}

func TestParseOutputMixedAndNoise(t *testing.T) {
	out := strings.Join([]string{
		"[3:04:05 PM] Starting compilation in watch mode...",
		"",
		"src/a.ts(2,1): error TS1005: ';' expected.",
		"  some elaboration",
		"TSFILE: /project/dist/a.js",
		"src/b.ts(9,4): warning TS6133: 'x' is declared but never used.",
		"[3:04:07 PM] Found 1 error. Watching for file changes.",
	}, "\n")

	diags, emitted := ParseOutput(out)
	if len(diags) != 2 {
		t.Fatalf("got %d diagnostics, want 2: %+v", len(diags), diags)
	}
	if !strings.Contains(diags[0].Message, "some elaboration") {
		t.Errorf("elaboration should fold into the first diagnostic: %q", diags[0].Message)
	}
	if diags[1].Category != CategoryWarning {
		t.Errorf("second diagnostic category = %q, want warning", diags[1].Category)
	}
	if !reflect.DeepEqual(emitted, []string{"/project/dist/a.js"}) {
		t.Errorf("emitted = %v", emitted)
	}
	if !HasErrors(diags) {
		t.Error("HasErrors should be true when an error is present")
	}
}

// A path containing parentheses must not be mistaken for the (line,col) group.
func TestParseOutputFileWithParens(t *testing.T) {
	out := `/build (copy)/src/a.ts(3,5): error TS2304: Cannot find name 'x'.`

	diags, _ := ParseOutput(out)
	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics, want 1", len(diags))
	}
	if diags[0].File != "/build (copy)/src/a.ts" {
		t.Errorf("File = %q", diags[0].File)
	}
	if diags[0].Line != 3 || diags[0].Column != 5 {
		t.Errorf("position = (%d,%d), want (3,5)", diags[0].Line, diags[0].Column)
	}
}

func TestHasErrorsIgnoresWarnings(t *testing.T) {
	diags := []Diagnostic{
		{Category: CategoryWarning, Code: 6133},
		{Category: CategoryMessage, Code: 6194},
	}
	if HasErrors(diags) {
		t.Error("warnings and messages alone should not count as errors")
	}
}

func TestDiagnosticString(t *testing.T) {
	located := Diagnostic{
		File: "src/a.ts", Line: 1, Column: 2,
		Category: CategoryError, Code: 2322, Message: "nope",
	}
	if got, want := located.String(), "src/a.ts(1,2): error TS2322: nope"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	unlocated := Diagnostic{Category: CategoryError, Code: 5058, Message: "missing"}
	if got, want := unlocated.String(), "error TS5058: missing"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParseOutputEmpty(t *testing.T) {
	diags, emitted := ParseOutput("")
	if len(diags) != 0 || len(emitted) != 0 {
		t.Errorf("empty output should parse to nothing, got %+v / %v", diags, emitted)
	}
}
