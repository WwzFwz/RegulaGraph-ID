// Canonicalizes one allocated BM25 dictionary snapshot for the Rust indexing
// reader. The fingerprint covers analyzer, revision and sorted term-ID pairs;
// callers must separately bind corpus, trusted PostgreSQL revision and artifact
// bytes before reuse. Sorting costs O(n log n), with O(n) extra memory;
// profile RSS and hashing latency on the full corpus (targets unmeasured).
package postgres

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"unicode"
	"unicode/utf8"
)

// LexicalRevisionName maps PostgreSQL's monotonically increasing revision to
// the stable string carried by the Rust dictionary reader.
func LexicalRevisionName(revision uint64) (string, error) {
	if revision == 0 || revision > 9_223_372_036_854_775_807 {
		return "", errors.New("invalid lexical dictionary revision")
	}
	return fmt.Sprintf("lexrev:%d", revision), nil
}

// FingerprintLexicalDictionary matches Rust indexing/dictionary.rs fingerprint
// v1 exactly. It rejects duplicate terms/IDs and never relies on caller order.
func FingerprintLexicalDictionary(analyzerID, revision string, entries []LexicalTerm) ([32]byte, error) {
	var zero [32]byte
	if !storageIDPattern.MatchString(analyzerID) || !storageIDPattern.MatchString(revision) ||
		len(entries) > 10_000_000 {
		return zero, errors.New("invalid lexical dictionary identity or size")
	}
	ordered := append([]LexicalTerm(nil), entries...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Term < ordered[j].Term })
	seenIDs := make(map[uint32]struct{}, len(ordered))
	for i, entry := range ordered {
		if entry.ID == 0 || entry.Term == "" || len(entry.Term) > 256 ||
			!utf8.ValidString(entry.Term) || i > 0 && entry.Term == ordered[i-1].Term {
			return zero, errors.New("invalid or duplicate lexical dictionary entry")
		}
		for _, ch := range entry.Term {
			if unicode.IsControl(ch) {
				return zero, errors.New("lexical dictionary term contains control character")
			}
		}
		if _, duplicate := seenIDs[entry.ID]; duplicate {
			return zero, errors.New("duplicate lexical dictionary term ID")
		}
		seenIDs[entry.ID] = struct{}{}
	}
	h := sha256.New()
	h.Write([]byte("regulagraph-lexical-dictionary-v1\x00"))
	var length [8]byte
	for _, field := range []string{analyzerID, revision} {
		binary.BigEndian.PutUint64(length[:], uint64(len(field)))
		h.Write(length[:])
		h.Write([]byte(field))
	}
	binary.BigEndian.PutUint64(length[:], uint64(len(ordered)))
	h.Write(length[:])
	var id [4]byte
	for _, entry := range ordered {
		binary.BigEndian.PutUint64(length[:], uint64(len(entry.Term)))
		h.Write(length[:])
		h.Write([]byte(entry.Term))
		binary.BigEndian.PutUint32(id[:], entry.ID)
		h.Write(id[:])
	}
	var result [32]byte
	copy(result[:], h.Sum(nil))
	return result, nil
}
