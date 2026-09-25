// Sparse query tests check frozen BM25 parity with Rust's reference fixture,
// descendant vocabulary behavior, and rejection of incompatible generations.
// These deterministic tests do not measure Recall@k or backend latency.
package query

import (
	"errors"
	"math"
	"reflect"
	"testing"
)

func pinnedFixture() (SparseDictionaryView, FrozenBM25View) {
	terms := map[string]uint32{"pasal": 8, "12/2020": 3, "tidak": 20, "wajib": 21}
	fingerprint := sparseDictionaryFingerprint(LexicalAnalyzerVersion, "r7", terms)
	return SparseDictionaryView{
			AnalyzerID: LexicalAnalyzerVersion,
			Revision:   "r7",
			Terms:      terms,
			Lineage:    map[string][32]byte{"r7": fingerprint},
		}, FrozenBM25View{
			AnalyzerID:            LexicalAnalyzerVersion,
			DictionaryRevision:    "r7",
			DictionaryFingerprint: fingerprint,
			DocumentCount:         2,
			TotalTokens:           6,
			DFByID:                map[uint32]uint64{3: 1, 8: 2, 20: 1, 21: 1},
		}
}

func TestPinnedBM25MatchesRustFrozenQueryWeights(t *testing.T) {
	dictionary, stats := pinnedFixture()
	encoder, err := NewPinnedBM25QueryEncoder(dictionary, stats)
	if err != nil {
		t.Fatal(err)
	}
	// The encoder owns copies. Mutating the artifact loader's maps cannot affect queries.
	dictionary.Terms["pasal"] = 100
	stats.DFByID[8] = 1
	query, err := encoder.Encode("Pasal pasal 12/2020 unknown")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(query.Indices, []uint32{3, 8}) || query.OOV != 1 {
		t.Fatalf("unexpected sparse query: %+v", query)
	}
	for i, expected := range []float64{math.Log(2), 2 * math.Log(1.2)} {
		if math.Abs(float64(query.Values[i])-expected) > 1e-6 {
			t.Fatalf("weight[%d]=%f want %f", i, query.Values[i], expected)
		}
	}
	empty, err := encoder.Encode("unknown")
	if err != nil || len(empty.Indices) != 0 || empty.OOV != 1 {
		t.Fatalf("OOV query=%+v err=%v", empty, err)
	}
}

func TestPinnedBM25DescendantUsesFrozenDFZero(t *testing.T) {
	base, stats := pinnedFixture()
	childTerms := make(map[string]uint32, len(base.Terms)+1)
	for term, id := range base.Terms {
		childTerms[term] = id
	}
	childTerms["izin"] = 31
	child := SparseDictionaryView{
		AnalyzerID: LexicalAnalyzerVersion,
		Revision:   "r8",
		Terms:      childTerms,
		Lineage: map[string][32]byte{
			"r7": stats.DictionaryFingerprint,
			"r8": sparseDictionaryFingerprint(LexicalAnalyzerVersion, "r8", childTerms),
		},
	}
	encoder, err := NewPinnedBM25QueryEncoder(child, stats)
	if err != nil {
		t.Fatal(err)
	}
	query, err := encoder.Encode("izin pasal")
	if err != nil || !reflect.DeepEqual(query.Indices, []uint32{8, 31}) {
		t.Fatalf("descendant query=%+v err=%v", query, err)
	}
	if math.Abs(float64(query.Values[1])-math.Log(6)) > 1e-6 {
		t.Fatalf("appended term weight=%f want log(6)", query.Values[1])
	}
	child.Lineage["r7"] = [32]byte{}
	if _, err := NewPinnedBM25QueryEncoder(child, stats); !errors.Is(err, ErrInvalidSparseGeneration) {
		t.Fatalf("unproved ancestry accepted: %v", err)
	}
}

func TestPinnedBM25RejectsIncompatibleAndCorruptViews(t *testing.T) {
	var zero PinnedBM25QueryEncoder
	if _, err := zero.Encode("pasal"); !errors.Is(err, ErrInvalidSparseGeneration) {
		t.Fatalf("uninitialized encoder accepted: %v", err)
	}
	dictionary, stats := pinnedFixture()
	stats.DFByID = nil
	if _, err := NewPinnedBM25QueryEncoder(dictionary, stats); !errors.Is(err, ErrInvalidSparseGeneration) {
		t.Fatalf("positive-token corpus without DF accepted: %v", err)
	}
	dictionary, stats = pinnedFixture()
	stats.AnalyzerID = "other"
	if _, err := NewPinnedBM25QueryEncoder(dictionary, stats); !errors.Is(err, ErrInvalidSparseGeneration) {
		t.Fatalf("analyzer mismatch accepted: %v", err)
	}
	_, stats = pinnedFixture()
	stats.DictionaryRevision = "future"
	stats.DictionaryFingerprint = [32]byte{}
	if _, err := NewPinnedBM25QueryEncoder(dictionary, stats); !errors.Is(err, ErrInvalidSparseGeneration) {
		t.Fatalf("missing lineage accepted: %v", err)
	}
	_, stats = pinnedFixture()
	dictionary.Terms["wajib"] = dictionary.Terms["pasal"]
	if _, err := NewPinnedBM25QueryEncoder(dictionary, stats); !errors.Is(err, ErrInvalidSparseGeneration) {
		t.Fatalf("duplicate term ID accepted: %v", err)
	}
	dictionary, stats = pinnedFixture()
	stats.DFByID[8] = 3
	if _, err := NewPinnedBM25QueryEncoder(dictionary, stats); !errors.Is(err, ErrInvalidSparseGeneration) {
		t.Fatalf("DF greater than corpus accepted: %v", err)
	}
}
