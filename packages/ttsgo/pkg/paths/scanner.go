package paths

// skipRegions scans text and returns sorted [start, end) intervals that
// represent comments, string literals and regex literals — places where an
// import-looking pattern is not an import.
//
// Regex literals matter more than they look. A regex containing an apostrophe,
// such as /don't/, would otherwise open a phantom string region that swallows
// the rest of the file, silently leaving every later import unrewritten.
func skipRegions(text string) []region {
	var regions []region
	i := 0
	n := len(text)

	// lastSignificant is the previous byte that was not whitespace or part of a
	// comment. It is what distinguishes division from a regex literal.
	lastSignificant := byte(0)

	for i < n {
		c := text[i]

		switch {
		case c == '/' && i+1 < n && text[i+1] == '/':
			start := i
			i += 2
			for i < n && text[i] != '\n' {
				i++
			}
			regions = append(regions, region{start, i})

		case c == '/' && i+1 < n && text[i+1] == '*':
			start := i
			i += 2
			for i+1 < n && !(text[i] == '*' && text[i+1] == '/') {
				i++
			}
			if i+1 < n {
				i += 2
			} else {
				i = n
			}
			regions = append(regions, region{start, i})

		case c == '/' && startsRegexLiteral(text, i, lastSignificant):
			start := i
			i = scanRegexLiteral(text, i)
			regions = append(regions, region{start, i})
			lastSignificant = '/'

		case c == '"' || c == '\'':
			start := i
			quote := c
			i++
			for i < n && text[i] != quote {
				if text[i] == '\\' {
					i++
				}
				i++
			}
			if i < n {
				i++
			}
			regions = append(regions, region{start, i})
			lastSignificant = quote

		case c == '`':
			start := i
			i++
			for i < n {
				switch text[i] {
				case '\\':
					i += 2
					continue
				case '`':
					i++
					goto templateDone
				case '$':
					if i+1 < n && text[i+1] == '{' {
						i += 2
						braceDepth := 1
						for i < n && braceDepth > 0 {
							switch text[i] {
							case '{':
								braceDepth++
							case '}':
								braceDepth--
							case '\\':
								i++
							}
							i++
						}
						continue
					}
				}
				i++
			}
		templateDone:
			regions = append(regions, region{start, i})
			lastSignificant = '`'

		default:
			if !isSpace(c) {
				lastSignificant = c
			}
			i++
		}
	}

	return regions
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// startsRegexLiteral decides whether the slash at i opens a regex literal or is
// a division operator. JavaScript cannot be tokenised without this distinction,
// and the answer depends on what came before: after a value — an identifier,
// number, or closing bracket — a slash divides; anywhere else it starts a
// regex.
func startsRegexLiteral(text string, i int, lastSignificant byte) bool {
	if i+1 >= len(text) {
		return false
	}
	// Comments are handled before this is reached.
	if next := text[i+1]; next == '/' || next == '*' {
		return false
	}

	switch {
	case lastSignificant == 0: // start of input
		return true
	case isIdentifierByte(lastSignificant) || lastSignificant == ')' ||
		lastSignificant == ']' || lastSignificant == '"' || lastSignificant == '\'' ||
		lastSignificant == '`':
		// A value precedes it, so this divides. Keywords that can precede a
		// regex (return, typeof, case, ...) also end in identifier bytes, but
		// emitted JavaScript rarely puts a regex directly after one, and
		// treating it as division only costs a missed skip region.
		return false
	default:
		return true
	}
}

func isIdentifierByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
		c == '_' || c == '$'
}

// scanRegexLiteral returns the offset just past the regex starting at i,
// including flags. A slash inside a character class does not end the literal.
func scanRegexLiteral(text string, i int) int {
	n := len(text)
	i++ // opening slash
	inClass := false

	for i < n {
		switch text[i] {
		case '\\':
			i++ // skip the escaped byte
		case '[':
			inClass = true
		case ']':
			inClass = false
		case '/':
			if !inClass {
				i++
				// Consume flags.
				for i < n && isIdentifierByte(text[i]) {
					i++
				}
				return i
			}
		case '\n':
			// A regex cannot span lines; treat it as unterminated rather than
			// swallowing the rest of the file.
			return i
		}
		i++
	}
	return n
}

type region struct{ start, end int }

func isInSkipRegion(pos int, regions []region) bool {
	lo, hi := 0, len(regions)
	for lo < hi {
		mid := (lo + hi) / 2
		if regions[mid].end <= pos {
			lo = mid + 1
		} else if regions[mid].start > pos {
			hi = mid
		} else {
			return true
		}
	}
	return false
}
