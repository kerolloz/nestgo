package tsc

import (
	"regexp"
	"strconv"
	"strings"
)

// Category is a diagnostic's severity as reported by the compiler.
type Category string

const (
	CategoryError   Category = "error"
	CategoryWarning Category = "warning"
	CategoryMessage Category = "message"
)

// Diagnostic is one compiler message. File, Line and Column are empty/zero for
// diagnostics that are not tied to a source position, such as a missing
// tsconfig (TS5058).
type Diagnostic struct {
	File     string
	Line     int
	Column   int
	Category Category
	Code     int
	Message  string
}

// IsError reports whether the diagnostic should fail a build.
func (d Diagnostic) IsError() bool { return d.Category == CategoryError }

// String renders the diagnostic the way the compiler printed it, so it can be
// shown to users without re-deriving the format.
func (d Diagnostic) String() string {
	var sb strings.Builder
	if d.File != "" {
		sb.WriteString(d.File)
		sb.WriteString("(" + strconv.Itoa(d.Line) + "," + strconv.Itoa(d.Column) + "): ")
	}
	sb.WriteString(string(d.Category))
	sb.WriteString(" TS" + strconv.Itoa(d.Code) + ": ")
	sb.WriteString(d.Message)
	return sb.String()
}

// The compiler emits `file(line,col): error TS1234: message` when a diagnostic
// has a position and `error TS1234: message` when it does not.
var (
	locatedRe   = regexp.MustCompile(`^(.+?)\((\d+),(\d+)\): (error|warning|message) TS(\d+): (.*)$`)
	unlocatedRe = regexp.MustCompile(`^(error|warning|message) TS(\d+): (.*)$`)
)

// emittedFilePrefix marks a line printed by --listEmittedFiles.
const emittedFilePrefix = "TSFILE: "

// ParseOutput extracts diagnostics and emitted-file paths from compiler output.
//
// Elaborated diagnostics span several lines: the continuation lines are
// indented and belong to the diagnostic above them, so they are folded into
// that message rather than dropped or treated as separate diagnostics.
// Anything else (watch-mode status lines, blank lines) is ignored.
func ParseOutput(out string) ([]Diagnostic, []string) {
	var (
		diagnostics []Diagnostic
		emitted     []string
	)

	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSuffix(line, "\r")

		if path, ok := strings.CutPrefix(line, emittedFilePrefix); ok {
			if path = strings.TrimSpace(path); path != "" {
				emitted = append(emitted, path)
			}
			continue
		}

		// Indented text continues the previous diagnostic's message.
		if isContinuation(line) && len(diagnostics) > 0 {
			last := &diagnostics[len(diagnostics)-1]
			last.Message += "\n" + strings.TrimRight(line, " \t")
			continue
		}

		if d, ok := parseDiagnostic(line); ok {
			diagnostics = append(diagnostics, d)
		}
	}

	return diagnostics, emitted
}

func isContinuation(line string) bool {
	return strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")
}

func parseDiagnostic(line string) (Diagnostic, bool) {
	if m := locatedRe.FindStringSubmatch(line); m != nil {
		lineNo, err := strconv.Atoi(m[2])
		if err != nil {
			return Diagnostic{}, false
		}
		col, err := strconv.Atoi(m[3])
		if err != nil {
			return Diagnostic{}, false
		}
		code, err := strconv.Atoi(m[5])
		if err != nil {
			return Diagnostic{}, false
		}
		return Diagnostic{
			File:     m[1],
			Line:     lineNo,
			Column:   col,
			Category: Category(m[4]),
			Code:     code,
			Message:  m[6],
		}, true
	}

	if m := unlocatedRe.FindStringSubmatch(line); m != nil {
		code, err := strconv.Atoi(m[2])
		if err != nil {
			return Diagnostic{}, false
		}
		return Diagnostic{Category: Category(m[1]), Code: code, Message: m[3]}, true
	}

	return Diagnostic{}, false
}

// HasErrors reports whether any diagnostic is an error.
func HasErrors(diagnostics []Diagnostic) bool {
	for _, d := range diagnostics {
		if d.IsError() {
			return true
		}
	}
	return false
}
