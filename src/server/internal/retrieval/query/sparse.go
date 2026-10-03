// Package query encodes a BM25 sparse query against an immutable, verified
// dictionary/statistics generation. Loading validates lineage and term IDs once;
// each request analyzes only its own terms and never mutates the vocabulary.
// The caller must load the manifests from verified, snapshot-pinned artifacts;
// these local structs are projections, not a second wire contract. The output
// matches Rust indexing's frozen BM25 query weights (qtf × IDF, DF=0 for
// descendant-only terms). Benchmark load RSS and warm p50/p95/p99 query latency
// against configs/benchmark-targets.yaml; quality remains REQUIRED_UNMEASURED.
package query

import (
	"errors"
	"math"
	"sort"

	"regulagraph.local/server/internal/domain"
)

// SparseDictionaryView is a local projection of a verified dictionary artifact.
// Lineage digests are evidence only when the artifact chain was verified upstream.
type SparseDictionaryView struct {
	AnalyzerID string
	Revision   string
	Terms      map[string]uint32
	Lineage    map[string][32]byte
}

// FrozenBM25View is a local projection of verified, frozen corpus statistics.
type FrozenBM25View struct {
	AnalyzerID            string
	DictionaryRevision    string
	DictionaryFingerprint [32]byte
	DocumentCount         uint64
	TotalTokens           uint64
	DFByID                map[uint32]uint64
}

// SparseQuery is ready for a sparse backend request. Empty Indices means the
// query had no in-vocabulary term; callers should skip an empty backend search.
type SparseQuery struct {
	Indices []uint32
	Values  []float32
	OOV     uint32
}

// PinnedBM25QueryEncoder is immutable and safe for concurrent query reads.
type PinnedBM25QueryEncoder struct {
	terms map[string]uint32
	df    map[uint32]uint64
	n     uint64
}

var ErrInvalidSparseGeneration = errors.New("invalid or incompatible BM25 sparse generation")

// NewPinnedBM25QueryEncoder verifies the full mapping and freezes the view once.
// The caller must separately prove artifact hashes, snapshot binding, and the
// authoritative PostgreSQL dictionary ancestry before accepting this view.
func NewPinnedBM25QueryEncoder(dictionary SparseDictionaryView, statistics FrozenBM25View) (*PinnedBM25QueryEncoder, error) {
	if !validSparseIdentity(dictionary.AnalyzerID) || !validSparseIdentity(dictionary.Revision) ||
		dictionary.AnalyzerID != LexicalAnalyzerVersion || dictionary.AnalyzerID != statistics.AnalyzerID ||
		!validSparseIdentity(statistics.DictionaryRevision) || statistics.DocumentCount == 0 ||
		statistics.TotalTokens == 0 || len(dictionary.Terms) == 0 || len(dictionary.Terms) > 10_000_000 ||
		len(statistics.DFByID) == 0 || len(statistics.DFByID) > len(dictionary.Terms) ||
		len(dictionary.Lineage) == 0 || len(dictionary.Lineage) > 16_384 {
		return nil, ErrInvalidSparseGeneration
	}
	terms := make(map[string]uint32, len(dictionary.Terms))
	entries := make([]domain.LexicalTerm, 0, len(dictionary.Terms))
	for term, id := range dictionary.Terms {
		terms[term] = id
		entries = append(entries, domain.LexicalTerm{Term: term, ID: id})
	}
	fingerprint, err := domain.FingerprintLexicalDictionary(dictionary.AnalyzerID, dictionary.Revision, entries)
	if err != nil {
		return nil, ErrInvalidSparseGeneration
	}
	ids := make(map[uint32]bool, len(entries))
	for _, entry := range entries {
		ids[entry.ID] = true
	}
	selfDigest, selfExists := dictionary.Lineage[dictionary.Revision]
	ancestorDigest, ancestorExists := dictionary.Lineage[statistics.DictionaryRevision]
	if !selfExists || !ancestorExists || selfDigest != fingerprint ||
		ancestorDigest != statistics.DictionaryFingerprint {
		return nil, ErrInvalidSparseGeneration
	}
	df := make(map[uint32]uint64, len(statistics.DFByID))
	var summedDF uint64
	for id, count := range statistics.DFByID {
		if id == 0 || count == 0 || count > statistics.DocumentCount || !ids[id] {
			return nil, ErrInvalidSparseGeneration
		}
		if count > statistics.TotalTokens-summedDF {
			return nil, ErrInvalidSparseGeneration
		}
		summedDF += count
		df[id] = count
	}
	return &PinnedBM25QueryEncoder{terms: terms, df: df, n: statistics.DocumentCount}, nil
}

// Encode applies the pinned lexical analyzer and returns sorted unique sparse
// term IDs. Repeated terms increase qtf; OOV terms never acquire invented IDs.
func (encoder *PinnedBM25QueryEncoder) Encode(input string) (SparseQuery, error) {
	if encoder == nil || encoder.n == 0 {
		return SparseQuery{}, ErrInvalidSparseGeneration
	}
	tokens, err := AnalyzeLexicalQuery(input)
	if err != nil {
		return SparseQuery{}, err
	}
	frequencies := make(map[uint32]uint32, len(tokens))
	var result SparseQuery
	for _, term := range tokens {
		if id, ok := encoder.terms[term]; ok {
			frequencies[id]++
		} else {
			result.OOV++
		}
	}
	result.Indices = make([]uint32, 0, len(frequencies))
	for id := range frequencies {
		result.Indices = append(result.Indices, id)
	}
	sort.Slice(result.Indices, func(i, j int) bool { return result.Indices[i] < result.Indices[j] })
	result.Values = make([]float32, 0, len(result.Indices))
	for _, id := range result.Indices {
		idf := math.Log(1 + (float64(encoder.n)-float64(encoder.df[id])+0.5)/(float64(encoder.df[id])+0.5))
		weight := float32(float64(frequencies[id]) * idf)
		if math.IsInf(float64(weight), 0) || math.IsNaN(float64(weight)) || weight <= 0 {
			return SparseQuery{}, ErrInvalidSparseGeneration
		}
		result.Values = append(result.Values, weight)
	}
	return result, nil
}

func validSparseIdentity(id string) bool {
	if id == "" || len(id) > 256 {
		return false
	}
	for i := 0; i < len(id); i++ {
		if id[i] < 33 || id[i] > 126 {
			return false
		}
	}
	return true
}
