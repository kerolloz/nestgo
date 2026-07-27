package paths

import (
	"encoding/json"
	"errors"
	"strings"
)

// Rewriting a module specifier changes the length of a line, which moves every
// mapping that points past the edit. Source maps are not repaired by tsc-alias
// at all, so debuggers and stack traces land on the wrong column for any line
// carrying an import. This file repairs them.

// Edit records a single specifier replacement in emitted text.
type Edit struct {
	// Line is the 0-based generated line the edit occurred on.
	Line int

	// Column is the 0-based generated column where the replacement started.
	Column int

	// Delta is how many characters the line grew (positive) or shrank.
	Delta int
}

// base64VLQ is the alphabet source maps encode segment values with.
const base64VLQ = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

var base64Index = func() [128]int8 {
	var table [128]int8
	for i := range table {
		table[i] = -1
	}
	for i, c := range base64VLQ {
		table[c] = int8(i)
	}
	return table
}()

// AdjustSourceMap shifts the generated columns in a source map to account for
// edits made to the corresponding JavaScript file. It returns the updated JSON,
// preserving every other field.
func AdjustSourceMap(mapJSON []byte, edits []Edit) ([]byte, error) {
	if len(edits) == 0 {
		return mapJSON, nil
	}

	// Decode into an ordered map so unrelated fields survive untouched.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(mapJSON, &raw); err != nil {
		return nil, err
	}

	encoded, ok := raw["mappings"]
	if !ok {
		return mapJSON, nil
	}
	var mappings string
	if err := json.Unmarshal(encoded, &mappings); err != nil {
		return nil, err
	}

	adjusted, err := adjustMappings(mappings, edits)
	if err != nil {
		return nil, err
	}
	if adjusted == mappings {
		return mapJSON, nil
	}

	updated, err := json.Marshal(adjusted)
	if err != nil {
		return nil, err
	}
	raw["mappings"] = updated

	return json.Marshal(raw)
}

// adjustMappings rewrites the VLQ mappings string.
//
// Only the generated column is line-relative; the source index, source line,
// source column and name index all accumulate across the whole map. Decoding
// every segment and re-encoding the lot keeps those running totals correct,
// which selectively patching one line would not.
func adjustMappings(mappings string, edits []Edit) (string, error) {
	shifts := make(map[int][]Edit, len(edits))
	for _, e := range edits {
		if e.Delta != 0 {
			shifts[e.Line] = append(shifts[e.Line], e)
		}
	}
	if len(shifts) == 0 {
		return mappings, nil
	}

	var out strings.Builder
	out.Grow(len(mappings))

	// Running totals for the fields that carry across lines.
	var prevSource, prevSourceLine, prevSourceCol, prevName int

	for lineNo, line := range strings.Split(mappings, ";") {
		if lineNo > 0 {
			out.WriteByte(';')
		}
		if line == "" {
			continue
		}

		var prevGenCol int    // resets every line, per the spec
		var newPrevGenCol int // the same total after shifting

		for segNo, segment := range strings.Split(line, ",") {
			if segNo > 0 {
				out.WriteByte(',')
			}
			if segment == "" {
				continue
			}

			values, err := decodeSegment(segment)
			if err != nil {
				return "", err
			}

			genCol := prevGenCol + values[0]
			prevGenCol = genCol

			shifted := genCol + shiftFor(shifts[lineNo], genCol)
			values[0] = shifted - newPrevGenCol
			newPrevGenCol = shifted

			// Re-encode the remaining fields with their deltas unchanged, but
			// keep the running totals in step so nothing drifts.
			if len(values) >= 4 {
				prevSource += values[1]
				prevSourceLine += values[2]
				prevSourceCol += values[3]
			}
			if len(values) >= 5 {
				prevName += values[4]
			}

			for _, v := range values {
				out.WriteString(encodeVLQ(v))
			}
		}
	}

	return out.String(), nil
}

// shiftFor returns how far a mapping at genCol moves. A mapping exactly at the
// edit start refers to the specifier itself and stays put; anything after it
// moves by the accumulated delta.
func shiftFor(edits []Edit, genCol int) int {
	var shift int
	for _, e := range edits {
		if genCol > e.Column {
			shift += e.Delta
		}
	}
	return shift
}

func decodeSegment(segment string) ([]int, error) {
	var (
		values []int
		value  int
		shift  uint
	)

	for i := 0; i < len(segment); i++ {
		c := segment[i]
		if c >= 128 || base64Index[c] < 0 {
			return nil, errors.New("invalid base64 VLQ character in source map")
		}
		digit := int(base64Index[c])

		value += (digit & 31) << shift
		if digit&32 != 0 {
			shift += 5
			continue
		}

		negative := value&1 == 1
		value >>= 1
		if negative {
			value = -value
		}
		values = append(values, value)
		value, shift = 0, 0
	}

	if shift != 0 {
		return nil, errors.New("truncated base64 VLQ sequence in source map")
	}
	if len(values) == 0 {
		return nil, errors.New("empty source map segment")
	}
	return values, nil
}

func encodeVLQ(value int) string {
	var vlq int
	if value < 0 {
		vlq = (-value << 1) | 1
	} else {
		vlq = value << 1
	}

	var sb strings.Builder
	for {
		digit := vlq & 31
		vlq >>= 5
		if vlq > 0 {
			digit |= 32
		}
		sb.WriteByte(base64VLQ[digit])
		if vlq == 0 {
			break
		}
	}
	return sb.String()
}
