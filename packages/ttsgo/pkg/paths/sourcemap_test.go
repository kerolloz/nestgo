package paths

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestVLQRoundTrip(t *testing.T) {
	for _, v := range []int{0, 1, -1, 15, 16, -16, 31, 32, -32, 1023, -1023, 65535, -65535} {
		encoded := encodeVLQ(v)
		decoded, err := decodeSegment(encoded)
		if err != nil {
			t.Fatalf("decoding %d (%q): %v", v, encoded, err)
		}
		if len(decoded) != 1 || decoded[0] != v {
			t.Errorf("round trip of %d gave %v via %q", v, decoded, encoded)
		}
	}
}

func TestDecodeSegmentKnownValues(t *testing.T) {
	// "AAAA" is four zeros; "AAgBC" is the well-known [0,0,16,1] sequence.
	got, err := decodeSegment("AAAA")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || got[0] != 0 || got[3] != 0 {
		t.Errorf("AAAA decoded to %v, want four zeros", got)
	}

	got, err = decodeSegment("AAgBC")
	if err != nil {
		t.Fatal(err)
	}
	want := []int{0, 0, 16, 1}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("AAgBC decoded to %v, want %v", got, want)
		}
	}
}

func TestDecodeSegmentRejectsGarbage(t *testing.T) {
	if _, err := decodeSegment("!!"); err == nil {
		t.Error("expected an error for invalid characters")
	}
	if _, err := decodeSegment("g"); err == nil {
		t.Error("expected an error for a truncated sequence (continuation bit set, nothing follows)")
	}
}

// A mapping before the edit stays; one after it shifts by the delta.
func TestAdjustMappingsShiftsOnlyAfterTheEdit(t *testing.T) {
	// Three segments on line 0 at generated columns 0, 10 and 20.
	mappings := encodeLine(0, 10, 20)

	got, err := adjustMappings(mappings, []Edit{{Line: 0, Column: 10, Delta: 3}})
	if err != nil {
		t.Fatal(err)
	}

	cols := decodeLineColumns(t, got)
	want := []int{0, 10, 23} // col 0 before, col 10 is the specifier itself, col 20 moves
	for i := range want {
		if cols[i] != want[i] {
			t.Fatalf("columns = %v, want %v", cols, want)
		}
	}
}

func TestAdjustMappingsHandlesNegativeDelta(t *testing.T) {
	mappings := encodeLine(0, 10, 20)

	got, err := adjustMappings(mappings, []Edit{{Line: 0, Column: 5, Delta: -4}})
	if err != nil {
		t.Fatal(err)
	}

	cols := decodeLineColumns(t, got)
	want := []int{0, 6, 16}
	for i := range want {
		if cols[i] != want[i] {
			t.Fatalf("columns = %v, want %v", cols, want)
		}
	}
}

// Edits on one line must not disturb any other line.
func TestAdjustMappingsLeavesOtherLinesAlone(t *testing.T) {
	mappings := encodeLine(0, 10) + ";" + encodeLine(0, 10) + ";" + encodeLine(0, 10)

	got, err := adjustMappings(mappings, []Edit{{Line: 1, Column: 0, Delta: 5}})
	if err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(got, ";")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %q", len(lines), got)
	}
	if cols := decodeLineColumns(t, lines[0]); cols[1] != 10 {
		t.Errorf("line 0 should be untouched, got %v", cols)
	}
	if cols := decodeLineColumns(t, lines[1]); cols[1] != 15 {
		t.Errorf("line 1 should shift, got %v", cols)
	}
	if cols := decodeLineColumns(t, lines[2]); cols[1] != 10 {
		t.Errorf("line 2 should be untouched, got %v", cols)
	}
}

// Empty lines carry no segments but must keep their place, or every mapping
// after them lands on the wrong line.
func TestAdjustMappingsPreservesEmptyLines(t *testing.T) {
	mappings := encodeLine(0) + ";;;" + encodeLine(0)

	got, err := adjustMappings(mappings, []Edit{{Line: 0, Column: 0, Delta: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(got, ";") != strings.Count(mappings, ";") {
		t.Errorf("line count changed: %q -> %q", mappings, got)
	}
}

func TestAdjustSourceMapPreservesOtherFields(t *testing.T) {
	original := []byte(`{"version":3,"file":"main.js","sourceRoot":"","sources":["../src/main.ts"],"names":[],"mappings":"` + encodeLine(0, 10) + `"}`)

	got, err := AdjustSourceMap(original, []Edit{{Line: 0, Column: 0, Delta: 4}})
	if err != nil {
		t.Fatal(err)
	}

	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if m["version"].(float64) != 3 {
		t.Errorf("version lost: %v", m["version"])
	}
	if m["file"] != "main.js" {
		t.Errorf("file lost: %v", m["file"])
	}
	if sources := m["sources"].([]any); len(sources) != 1 || sources[0] != "../src/main.ts" {
		t.Errorf("sources lost: %v", m["sources"])
	}
	if cols := decodeLineColumns(t, m["mappings"].(string)); cols[1] != 14 {
		t.Errorf("mapping not shifted: %v", cols)
	}
}

func TestAdjustSourceMapNoEdits(t *testing.T) {
	original := []byte(`{"version":3,"mappings":"AAAA"}`)
	got, err := AdjustSourceMap(original, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Errorf("map should be untouched with no edits, got %s", got)
	}
}

func TestAdjustSourceMapRejectsInvalidJSON(t *testing.T) {
	if _, err := AdjustSourceMap([]byte("not json"), []Edit{{Delta: 1}}); err == nil {
		t.Error("expected an error for invalid JSON")
	}
}

// encodeLine builds one mappings line with segments at the given generated
// columns, each pointing at source 0 line 0 column 0.
func encodeLine(columns ...int) string {
	var sb strings.Builder
	prev := 0
	for i, col := range columns {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(encodeVLQ(col - prev))
		sb.WriteString(encodeVLQ(0)) // source index delta
		sb.WriteString(encodeVLQ(0)) // source line delta
		sb.WriteString(encodeVLQ(0)) // source column delta
		prev = col
	}
	return sb.String()
}

func decodeLineColumns(t *testing.T, line string) []int {
	t.Helper()
	if i := strings.IndexByte(line, ';'); i >= 0 {
		line = line[:i]
	}

	var cols []int
	prev := 0
	for _, segment := range strings.Split(line, ",") {
		if segment == "" {
			continue
		}
		values, err := decodeSegment(segment)
		if err != nil {
			t.Fatalf("decoding %q: %v", segment, err)
		}
		prev += values[0]
		cols = append(cols, prev)
	}
	return cols
}
