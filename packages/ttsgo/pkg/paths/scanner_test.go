package paths

import (
	"strings"
	"testing"
)

func TestSkipRegions_LineComment(t *testing.T) {
	text := `var x = 1; // this is a comment
var y = 2;`
	regions := skipRegions(text)
	if len(regions) != 1 {
		t.Fatalf("expected 1 region, got %d", len(regions))
	}
	if text[regions[0].start:regions[0].end] != "// this is a comment" {
		t.Errorf("unexpected region: %q", text[regions[0].start:regions[0].end])
	}
}

func TestSkipRegions_BlockComment(t *testing.T) {
	text := `var x = /* block */ 1;`
	regions := skipRegions(text)
	if len(regions) != 1 {
		t.Fatalf("expected 1 region, got %d", len(regions))
	}
	if text[regions[0].start:regions[0].end] != "/* block */" {
		t.Errorf("unexpected region: %q", text[regions[0].start:regions[0].end])
	}
}

func TestSkipRegions_DoubleQuoteString(t *testing.T) {
	text := `var s = "hello world";`
	regions := skipRegions(text)
	if len(regions) != 1 {
		t.Fatalf("expected 1 region, got %d", len(regions))
	}
	if text[regions[0].start:regions[0].end] != `"hello world"` {
		t.Errorf("unexpected region: %q", text[regions[0].start:regions[0].end])
	}
}

func TestSkipRegions_SingleQuoteString(t *testing.T) {
	text := `var s = 'hello';`
	regions := skipRegions(text)
	if len(regions) != 1 {
		t.Fatalf("expected 1 region, got %d", len(regions))
	}
	if text[regions[0].start:regions[0].end] != `'hello'` {
		t.Errorf("unexpected region: %q", text[regions[0].start:regions[0].end])
	}
}

func TestSkipRegions_EscapedQuotes(t *testing.T) {
	text := `var s = "he said \"hi\"";`
	regions := skipRegions(text)
	if len(regions) != 1 {
		t.Fatalf("expected 1 region, got %d", len(regions))
	}
	if text[regions[0].start:regions[0].end] != `"he said \"hi\""` {
		t.Errorf("unexpected region: %q", text[regions[0].start:regions[0].end])
	}
}

func TestSkipRegions_TemplateLiteral(t *testing.T) {
	text := "var s = `hello ${name}`;"
	regions := skipRegions(text)
	if len(regions) != 1 {
		t.Fatalf("expected 1 region, got %d", len(regions))
	}
	if text[regions[0].start:regions[0].end] != "`hello ${name}`" {
		t.Errorf("unexpected region: %q", text[regions[0].start:regions[0].end])
	}
}

func TestSkipRegions_Multiple(t *testing.T) {
	text := `// comment
var s = "string";
/* block */`
	regions := skipRegions(text)
	if len(regions) != 3 {
		t.Fatalf("expected 3 regions, got %d", len(regions))
	}
}

func TestIsInSkipRegion(t *testing.T) {
	regions := []region{{5, 10}, {20, 30}, {50, 60}}

	tests := []struct {
		pos  int
		want bool
	}{
		{0, false},
		{5, true},
		{7, true},
		{9, true},
		{10, false},
		{15, false},
		{20, true},
		{25, true},
		{30, false},
		{50, true},
		{60, false},
	}

	for _, tt := range tests {
		got := isInSkipRegion(tt.pos, regions)
		if got != tt.want {
			t.Errorf("isInSkipRegion(%d) = %v, want %v", tt.pos, got, tt.want)
		}
	}
}

func TestIsInSkipRegion_Empty(t *testing.T) {
	if isInSkipRegion(5, nil) {
		t.Error("expected false for empty regions")
	}
}

// Inside a regex, import-looking text must be skipped.
func TestRegexLiteralIsSkipped(t *testing.T) {
	text := `const re = /require("@a\/b")/;`

	regions := skipRegions(text)
	inside := strings.Index(text, "@a")
	if !isInSkipRegion(inside, regions) {
		t.Errorf("text inside a regex should be skipped; regions=%v", regions)
	}
}

func TestStartsRegexLiteralAfterValues(t *testing.T) {
	for _, tc := range []struct {
		name string
		prev byte
		want bool
	}{
		{"start of input", 0, true},
		{"after (", '(', true},
		{"after =", '=', true},
		{"after ,", ',', true},
		{"after return keyword's last byte", 'n', false},
		{"after identifier", 'x', false},
		{"after digit", '9', false},
		{"after )", ')', false},
		{"after ]", ']', false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := startsRegexLiteral("/a/", 0, tc.prev); got != tc.want {
				t.Errorf("startsRegexLiteral(prev=%q) = %v, want %v", tc.prev, got, tc.want)
			}
		})
	}
}
