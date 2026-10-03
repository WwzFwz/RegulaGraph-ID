// Validates typed analyzer and frozen BM25 statistics shared with Rust indexing.
// Corpus, exact base dictionary, population snapshot and formula remain distinct
// pins. Content validation is not proof of population membership or registry
// authority. Load once per generation, not per token/request. Measure load RSS,
// DF validation time and score parity under configs/benchmark-targets.yaml;
// production accuracy and performance remain REQUIRED_UNMEASURED.
package domain

import (
	"encoding/hex"
	"errors"
	"math"

	pb "regulagraph.local/server/gen/regulagraph/v1"
)

const LexicalAnalyzerV1 = "regulagraph-lexical-nfc-ascii-v1"
const LexicalUnicodeV1 = "15.0.0"
const FrozenBM25FormulaV1 = "regulagraph-frozen-bm25-v1"

// ValidateLexicalAnalyzerArtifact admits only the policy actually implemented in
// both runtimes. Alternate limits/Unicode rules require an explicit new version.
func ValidateLexicalAnalyzerArtifact(a *pb.LexicalAnalyzerArtifact, corpus string, limits WireLimits) error {
	if a == nil || !validDocumentID(corpus) {
		return errors.New("analyzer artifact and corpus required")
	}
	if err := ValidateWire(a, limits); err != nil {
		return err
	}
	if a.Meta.CorpusId != corpus || a.Meta.Visibility != nil || a.AnalyzerId != LexicalAnalyzerV1 ||
		a.UnicodeVersion != LexicalUnicodeV1 || a.MaximumInputBytes != 2_000_000 || a.MaximumTermBytes != 256 ||
		a.MaximumDocumentTerms != 100_000 || a.MaximumQueryTerms != 1_024 {
		return errors.New("unsupported lexical analyzer policy or corpus")
	}
	return nil
}

// CheckLexicalStatisticsArtifact requires the exact checked statistics-base
// dictionary. A larger query dictionary is checked for ancestry separately;
// a DF record cannot introduce a term that only exists in a later dictionary.
func CheckLexicalStatisticsArtifact(a *pb.LexicalStatisticsArtifact, base *CheckedLexicalDictionary, limits WireLimits) error {
	if a == nil || base == nil {
		return errors.New("statistics and checked base dictionary required")
	}
	if err := ValidateWire(a, limits); err != nil {
		return err
	}
	if a.Meta.CorpusId != base.corpus || a.Meta.Visibility != nil || a.AnalyzerId != base.analyzer ||
		a.AnalyzerId != LexicalAnalyzerV1 || a.FormulaId != FrozenBM25FormulaV1 ||
		a.DictionaryRegistryRevision != base.revision || a.DictionaryMappingFingerprint.Sha256 != hex.EncodeToString(base.fingerprint[:]) ||
		a.PopulationSnapshot.CorpusId != base.corpus || a.ZeroTokenDocuments >= a.DocumentCount ||
		math.IsNaN(a.K1) || math.IsInf(a.K1, 0) || a.K1 <= 0 || math.IsNaN(a.B) || math.IsInf(a.B, 0) || a.B < 0 || a.B > 1 {
		return errors.New("statistics corpus, base dictionary, population or formula mismatch")
	}
	nonempty := a.DocumentCount - a.ZeroTokenDocuments
	if nonempty > a.TotalTokens || len(a.DocumentFrequencies) > len(base.terms) {
		return errors.New("statistics population counts are impossible")
	}
	ids := make(map[uint32]bool, len(base.terms))
	for _, id := range base.terms {
		ids[id] = true
	}
	var previous uint32
	var sum uint64
	for _, df := range a.DocumentFrequencies {
		if df.TermId <= previous || !ids[df.TermId] || df.DocumentCount > nonempty || df.DocumentCount > a.TotalTokens-sum {
			return errors.New("statistics DF is unsorted, foreign or exceeds population")
		}
		previous = df.TermId
		sum += df.DocumentCount
	}
	if sum < nonempty {
		return errors.New("DF coverage omits nonempty documents")
	}
	return nil
}
