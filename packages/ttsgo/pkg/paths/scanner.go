package paths

// skipRegions scans text and returns sorted [start, end) intervals that
// represent comments and string literals where import patterns should not
// be matched.
//
// Known limitation: regex literals are not recognized. A regex containing an
// unbalanced quote (e.g. /don't/) opens a phantom string region, so imports
// after it on that file are left unrewritten. Fixing it properly needs a real
// JS tokenizer.
func skipRegions(text string) []region {
	var regions []region
	i := 0
	n := len(text)

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

		default:
			i++
		}
	}

	return regions
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
