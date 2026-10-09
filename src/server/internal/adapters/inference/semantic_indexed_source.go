// Enumerates immutable source lexical units for optional EXTRACT v3. L/M/N runs
// form units; other non-space runes are individual units, including punctuation.
// Rendering inserts 1-based labels and retains every source byte and whitespace.
// The same enumeration owns provider counting, replay and exact byte projection.
// No source is truncated. Bound input, units and rendering before allocation;
// measure prompt amplification/latency and mention boundary coverage against
// configs/benchmark-targets.yaml. Source selection is not semantic verification.
package inference

import (
	"errors"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const indexedExtractionSchemaID = "https://regulagraph.local/schema/extraction-output-v3.json"
const indexedExtractionSchemaName = "regulagraph_extraction_v3"
const maximumIndexedSourceBytes = 2 << 20
const maximumIndexedUnits = 65536
const maximumIndexedRenderBytes = 4 << 20
const maximumIndexedProjectionBytes = 4 << 20

// Unicode tables are part of producer identity: toolchain upgrades must not
// silently reuse a completion if character classification changes.
const indexedSourceVersion = "regulagraph-indexed-source-v1:L-M-N-runs;other-nonspace-runes;unicode=" + unicode.Version

type sourceUnit struct{ start, end int }

func sourceLexicalUnits(text string) ([]sourceUnit, error) {
	if len(text) > maximumIndexedSourceBytes || !utf8.ValidString(text) {
		return nil, errors.New("indexed source exceeds byte or UTF-8 bounds")
	}
	units := make([]sourceUnit, 0)
	word := false
	for offset, r := range text {
		if unicode.IsSpace(r) {
			word = false
			continue
		}
		isWord := unicode.IsLetter(r) || unicode.IsMark(r) || unicode.IsNumber(r)
		end := offset + utf8.RuneLen(r)
		if isWord && word {
			units[len(units)-1].end = end
		} else {
			if len(units) >= maximumIndexedUnits {
				return nil, errors.New("indexed source exceeds lexical unit budget")
			}
			units = append(units, sourceUnit{offset, end})
		}
		word = isWord
	}
	return units, nil
}

func renderIndexedSource(text string) (string, error) {
	units, err := sourceLexicalUnits(text)
	if err != nil {
		return "", err
	}
	// Labels contain at most five digits for the fixed unit limit.
	if len(text)+len(units)*7 > maximumIndexedRenderBytes {
		return "", errors.New("indexed source rendering exceeds byte budget")
	}
	var out strings.Builder
	out.Grow(len(text) + len(units)*7)
	previous := 0
	for i, unit := range units {
		out.WriteString(text[previous:unit.start])
		out.WriteByte('[')
		out.WriteString(strconv.Itoa(i + 1))
		out.WriteByte(']')
		out.WriteString(text[unit.start:unit.end])
		previous = unit.end
	}
	out.WriteString(text[previous:])
	return out.String(), nil
}
