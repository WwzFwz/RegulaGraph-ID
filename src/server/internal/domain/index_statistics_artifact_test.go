// Checks frozen population invariants and exact dictionary ownership using the
// shared Rust/Go wire fixtures. These are integrity cases, not retrieval gold.
package domain

import (
	"math"
	"os"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func statisticsFixture(t *testing.T) (*pb.LexicalStatisticsArtifact, *CheckedLexicalDictionary) {
	t.Helper()
	d := new(pb.LexicalDictionaryArtifact)
	s := new(pb.LexicalStatisticsArtifact)
	for name, msg := range map[string]proto.Message{"lexical-dictionary-v1.pb": d, "lexical-statistics-v1.pb": s} {
		raw, err := os.ReadFile("../../../../tests/fixtures/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if err = DecodeWire(raw, msg, DefaultWireLimits); err != nil {
			t.Fatal(err)
		}
	}
	base, err := CheckLexicalDictionaryArtifact(d, "corpus:one", nil, DefaultWireLimits)
	if err != nil {
		t.Fatal(err)
	}
	return s, base
}

func TestStatisticsArtifactRejectsImpossibleOrForeignPopulation(t *testing.T) {
	s, base := statisticsFixture(t)
	if err := CheckLexicalStatisticsArtifact(s, base, DefaultWireLimits); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*pb.LexicalStatisticsArtifact){
		"corpus":   func(s *pb.LexicalStatisticsArtifact) { s.Meta.CorpusId = "other" },
		"snapshot": func(s *pb.LexicalStatisticsArtifact) { s.PopulationSnapshot.CorpusId = "other" },
		"formula":  func(s *pb.LexicalStatisticsArtifact) { s.FormulaId = "other" },
		"revision": func(s *pb.LexicalStatisticsArtifact) { s.DictionaryRegistryRevision++ },
		"mapping": func(s *pb.LexicalStatisticsArtifact) {
			s.DictionaryMappingFingerprint.Sha256 = s.PopulationFingerprint.Sha256
		},
		"empty population":         func(s *pb.LexicalStatisticsArtifact) { s.ZeroTokenDocuments = s.DocumentCount },
		"too many nonempty":        func(s *pb.LexicalStatisticsArtifact) { s.DocumentCount = 10 },
		"frequency includes empty": func(s *pb.LexicalStatisticsArtifact) { s.DocumentFrequencies[0].DocumentCount = 3 },
		"sum exceeds tokens":       func(s *pb.LexicalStatisticsArtifact) { s.TotalTokens = 2 },
		"insufficient coverage":    func(s *pb.LexicalStatisticsArtifact) { s.DocumentCount = 5; s.ZeroTokenDocuments = 1 },
		"foreign term":             func(s *pb.LexicalStatisticsArtifact) { s.DocumentFrequencies[1].TermId = 8 },
		"duplicate":                func(s *pb.LexicalStatisticsArtifact) { s.DocumentFrequencies[1].TermId = 1 },
		"negative k1":              func(s *pb.LexicalStatisticsArtifact) { s.K1 = -1 },
		"nan":                      func(s *pb.LexicalStatisticsArtifact) { s.K1 = math.NaN() },
		"b outside interval":       func(s *pb.LexicalStatisticsArtifact) { s.B = 1.1 },
	} {
		t.Run(name, func(t *testing.T) {
			copy := proto.Clone(s).(*pb.LexicalStatisticsArtifact)
			mutate(copy)
			if CheckLexicalStatisticsArtifact(copy, base, DefaultWireLimits) == nil {
				t.Fatal("invalid statistics accepted")
			}
		})
	}
}

func TestAnalyzerArtifactRejectsPolicyDrift(t *testing.T) {
	raw, err := os.ReadFile("../../../../tests/fixtures/lexical-analyzer-artifact-v1.pb")
	if err != nil {
		t.Fatal(err)
	}
	a := new(pb.LexicalAnalyzerArtifact)
	if err = DecodeWire(raw, a, DefaultWireLimits); err != nil {
		t.Fatal(err)
	}
	if err = ValidateLexicalAnalyzerArtifact(a, "corpus:one", DefaultWireLimits); err != nil {
		t.Fatal(err)
	}
	a.MaximumQueryTerms++
	if ValidateLexicalAnalyzerArtifact(a, "corpus:one", DefaultWireLimits) == nil {
		t.Fatal("unknown policy accepted")
	}
}
