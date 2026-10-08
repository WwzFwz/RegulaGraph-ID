// Tests the exact-byte vocabulary handoff from Rust to the operator command.
// Rejects normalization changes, duplicates, unsorted input and hash mismatch
// before database writes; no quality or latency inference comes from fixtures.
package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestReadLexicalVocabulary(t *testing.T) {
	for _, tc := range []struct {
		raw   string
		valid bool
	}{{"izin\npasal\n", true}, {"pasal\nizin\n", false}, {"izin\nizin\n", false}, {"Izin\n", false}, {"two words\n", false}, {"izin\r\n", false}, {"izin", false}, {"", false}, {"\n", false}, {"\xff\n", false}} {
		path := filepath.Join(t.TempDir(), "terms.txt")
		if err := os.WriteFile(path, []byte(tc.raw), 0600); err != nil {
			t.Fatal(err)
		}
		pin := fmt.Sprintf("%x", sha256.Sum256([]byte(tc.raw)))
		terms, err := readLexicalVocabulary(path, pin)
		if (err == nil) != tc.valid {
			t.Fatalf("%q: %v %v", tc.raw, terms, err)
		}
		if _, err = readLexicalVocabulary(path, fmt.Sprintf("%064d", 0)); err == nil {
			t.Fatal("hash mismatch accepted")
		}
	}
}
