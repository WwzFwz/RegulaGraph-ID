// The shared fixture checks the query analyzer against the Rust document analyzer;
// malformed input and resource limits are checked without backend dependencies.
package query

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

func TestSharedLexicalAnalyzerCases(t *testing.T) {
	t.Parallel()
	if norm.Version != "15.0.0" {
		t.Fatalf("lexical analyzer NFC version changed: %s", norm.Version)
	}
	if unicode.Version != "15.0.0" {
		t.Fatalf("lexical Letter category version changed: %s", unicode.Version)
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "tests", "fixtures", "lexical-analyzer-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Version string `json:"version"`
		Cases   []struct {
			Name  string   `json:"name"`
			Input string   `json:"input"`
			Terms []string `json:"terms"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Version != LexicalAnalyzerVersion {
		t.Fatalf("fixture analyzer=%q implementation=%q", fixture.Version, LexicalAnalyzerVersion)
	}
	for _, testCase := range fixture.Cases {
		t.Run(testCase.Name, func(t *testing.T) {
			actual, err := AnalyzeLexicalQuery(testCase.Input)
			if err != nil || !reflect.DeepEqual(actual, testCase.Terms) {
				t.Fatalf("analyzed=%q error=%v want=%q", actual, err, testCase.Terms)
			}
		})
	}
}

func TestLexicalAnalyzerLimits(t *testing.T) {
	if _, err := AnalyzeLexicalQuery(strings.Repeat("a", maxLexicalInputBytes+1)); !errors.Is(err, ErrLexicalInputTooLarge) {
		t.Fatalf("oversize input: %v", err)
	}
	if _, err := AnalyzeLexicalQuery(strings.Repeat("a", maxLexicalTermBytes+1)); !errors.Is(err, ErrLexicalTermTooLong) {
		t.Fatalf("oversize term: %v", err)
	}
	if _, err := AnalyzeLexicalQuery(strings.Repeat("a ", maxLexicalTerms+1)); !errors.Is(err, ErrLexicalTooManyTerms) {
		t.Fatalf("too many terms: %v", err)
	}
	if _, err := AnalyzeLexicalQuery(string([]byte{0xff})); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
}

func TestLongHangulModifierSequenceKeepsStreamSafeBoundary(t *testing.T) {
	modifier := "\uFF9E"
	input := "\uAC00" + strings.Repeat(modifier, 30)
	actual, err := AnalyzeLexicalQuery(input)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"\uAC00" + strings.Repeat(modifier, 29), modifier}
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("terms=%q want=%q", actual, want)
	}
}

func TestDecomposedHangulCrossesStreamSafeBoundary(t *testing.T) {
	vowel := "\u1161"
	input := "\u1100" + strings.Repeat(vowel, 31)
	actual, err := AnalyzeLexicalQuery(input)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"\uAC00" + strings.Repeat(vowel, 29), vowel}
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("terms=%q want=%q", actual, want)
	}
}
