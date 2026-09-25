// Package query analyzes lexical queries using the same pinned BM25 term policy as
// Rust indexing. NFC precedes ASCII case folding; Unicode letters and ASCII digits
// form terms, with a hyphen between letters and slash between digits retained.
// This is a query representation, not a canonical entity normalizer or BGE tokenizer.
//
// Input is bounded before normalization and output by term size/count. The Rust
// analyzer uses a generated x/text NFC property table to match Go's stream-safe
// behavior for combining marks and Hangul Jamo. Callers must
// bind the analyzer version to a published IndexGeneration. Benchmark term throughput,
// Recall@k, OOV and p50/p95/p99 under configs/benchmark-targets.yaml; status remains
// REQUIRED_UNMEASURED until the pinned workload is evaluated.
package query

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const LexicalAnalyzerVersion = "regulagraph-lexical-nfc-ascii-v1"
const maxLexicalInputBytes = 2_000_000
const maxLexicalTermBytes = 256
const maxLexicalTerms = 1_024

var ErrLexicalInputTooLarge = errors.New("lexical input too large")
var ErrLexicalTermTooLong = errors.New("lexical term too long")
var ErrLexicalTooManyTerms = errors.New("too many lexical terms")

// AnalyzeLexicalQuery returns analyzed terms without mutating the dictionary.
// All output is discarded on an error, so invalid queries cannot be partially searched.
func AnalyzeLexicalQuery(input string) ([]string, error) {
	if len(input) > maxLexicalInputBytes {
		return nil, ErrLexicalInputTooLarge
	}
	if !utf8.ValidString(input) {
		return nil, errors.New("lexical input is not valid UTF-8")
	}
	runes := []rune(norm.NFC.String(input))
	terms := make([]string, 0, min(len(runes)/5, 32))
	var current strings.Builder
	flush := func() error {
		if current.Len() == 0 {
			return nil
		}
		if len(terms) == maxLexicalTerms {
			return ErrLexicalTooManyTerms
		}
		terms = append(terms, current.String())
		current.Reset()
		return nil
	}
	for i, ch := range runes {
		var left, right rune
		if i > 0 {
			left = runes[i-1]
		}
		if i+1 < len(runes) {
			right = runes[i+1]
		}
		keepSeparator := ch == '-' && unicode.IsLetter(left) && unicode.IsLetter(right) ||
			ch == '/' && isASCIIDigit(left) && isASCIIDigit(right)
		if unicode.IsLetter(ch) || isASCIIDigit(ch) || keepSeparator {
			if ch >= 'A' && ch <= 'Z' {
				ch += 'a' - 'A'
			}
			current.WriteRune(ch)
			if current.Len() > maxLexicalTermBytes {
				return nil, ErrLexicalTermTooLong
			}
		} else if err := flush(); err != nil {
			return nil, err
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return terms, nil
}

func isASCIIDigit(ch rune) bool { return ch >= '0' && ch <= '9' }
