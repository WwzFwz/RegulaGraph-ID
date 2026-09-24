// Checks that automatic plans retain all legal scopes and preserve Rust-compatible lookup keys.
// Adversarial cases reject missing policy, overflow and malformed provenance before any registry
// read; these fixtures do not measure candidate recall on human-labelled regulation pairs.
package domain

import (
	"os"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func TestCanonicalEntityTypeCodesCoverPinnedOntologyWithoutCollision(t *testing.T) {
	raw, err := os.ReadFile("../../../../configs/ontology-v1.jsonc")
	if err != nil {
		t.Fatal(err)
	}
	ontology, err := ParseOntologyJSONC(raw)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int16]string{}
	for name := range ontology.entityTypes {
		code := CanonicalEntityTypeCode(name)
		if code <= 0 || seen[code] != "" {
			t.Fatalf("ontology type %q has missing or colliding registry code %d with %q", name, code, seen[code])
		}
		seen[code] = name
	}
	if len(seen) != len(ontology.entityTypes) || CanonicalEntityTypeCode("invented") != 0 ||
		CanonicalEntityTypeCode("regulation") != CanonicalEntityTypeRegulation ||
		CanonicalEntityTypeCode("organization") != CanonicalEntityTypeOrganization ||
		CanonicalEntityTypeCode("provision") != CanonicalEntityTypeProvision {
		t.Fatal("registry codes no longer match the pinned ontology and existing identity IDs")
	}
}

func candidatePlanningFixture() (*pb.ExtractionBatch, CandidatePlanningPolicy) {
	return &pb.ExtractionBatch{Meta: &pb.RecordMeta{CorpusId: "corpus:planning"},
			Context:      &pb.RequestContext{CorpusId: "corpus:planning"},
			Completeness: pb.Completeness_COMPLETENESS_COMPLETE,
			Mentions: []*pb.Mention{
				{Meta: &pb.RecordMeta{CorpusId: "corpus:planning", RecordId: "mention:organization"},
					CandidateType: "organization", SurfaceForm: "  BADAN\tA  ",
					SourceRefs: []*pb.SourceVersionRef{{SourceBlobId: "blob:a", RegulationId: "regulation:a",
						ProvisionVersionId: "version:a"}}},
				{Meta: &pb.RecordMeta{CorpusId: "corpus:planning", RecordId: "mention:provision"},
					CandidateType: "provision", SurfaceForm: "Pasal  1(2)",
					SourceRefs: []*pb.SourceVersionRef{{SourceBlobId: "blob:a", RegulationId: "regulation:a",
						ProvisionVersionId: "version:a"}, {SourceBlobId: "blob:b", RegulationId: "regulation:b",
						ProvisionVersionId: "version:b"}}},
			}}, CandidatePlanningPolicy{ScopesByType: map[string][]string{
			"organization": {"national", "regional"}, "provision": {"national"}},
			IncludeSourceRegulationType: map[string]bool{"provision": true},
			MaximumMentions:             4, MaximumScopesPerMention: 4, MaximumTotalScopes: 8}
}

func TestPlanRegistryCandidatesKeepsAmbiguousAndSourceScopes(t *testing.T) {
	source, policy := candidatePlanningFixture()
	plans, err := PlanRegistryCandidates(source, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 2 || plans[0].MentionID != "mention:organization" ||
		len(plans[0].Scopes) != 2 || plans[0].Scopes[0].CanonicalScope != "national" ||
		plans[0].Scopes[1].CanonicalScope != "regional" ||
		plans[0].Scopes[0].NormalizedLookup != "badan a" ||
		plans[1].MentionID != "mention:provision" || len(plans[1].Scopes) != 3 ||
		plans[1].Scopes[0].CanonicalScope != "national" ||
		plans[1].Scopes[1].CanonicalScope != "regulation:a" ||
		plans[1].Scopes[2].CanonicalScope != "regulation:b" ||
		plans[1].Scopes[0].NormalizedLookup != "pasal 1(2)" {
		t.Fatalf("planner narrowed or changed exact legal lookups: %+v", plans)
	}
	copy := proto.Clone(source).(*pb.ExtractionBatch)
	copy.Mentions[0], copy.Mentions[1] = copy.Mentions[1], copy.Mentions[0]
	reordered, err := PlanRegistryCandidates(copy, policy)
	if err != nil || len(reordered) != len(plans) || reordered[0].MentionID != plans[0].MentionID {
		t.Fatalf("mention order changed plan: %+v, %v", reordered, err)
	}
	firstHash, err := policy.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	permuted := policy
	permuted.ScopesByType = map[string][]string{"provision": {"national"},
		"organization": {"national", "regional"}}
	secondHash, err := permuted.Fingerprint()
	if err != nil || !proto.Equal(firstHash, secondHash) {
		t.Fatalf("map iteration changed policy fingerprint: %v", err)
	}
	permuted.ScopesByType["organization"] = []string{"national"}
	changed, err := permuted.Fingerprint()
	if err != nil || proto.Equal(firstHash, changed) {
		t.Fatal("removing a legal scope did not invalidate the policy hash")
	}
}

func TestNormalizeCandidateSurfaceKeepsIdentitySignals(t *testing.T) {
	for raw, expected := range map[string]string{
		"  PASAL\t1 (2)  ":   "pasal 1 (2)",
		"İzin Baru":          "i̇zin baru",
		"ΟΣ":                 "ος", // Rust lowercases the complete string with final sigma.
		"Tidak-Berlaku 2024": "tidak-berlaku 2024",
	} {
		got, err := NormalizeCandidateSurface(raw)
		if err != nil || got != expected {
			t.Fatalf("normalize %q = %q, %v; want %q", raw, got, err, expected)
		}
	}
	// Raw spacing can exceed the lookup-key limit while its normalized alias remains valid.
	if got, err := NormalizeCandidateSurface("Pasal" + strings.Repeat(" ", 700) + "1");
		err != nil || got != "pasal 1" {
		t.Fatalf("collapsible whitespace excluded valid alias: %q, %v", got, err)
	}
}

func TestPlanRegistryCandidatesRejectsMissingCoverageAndOverflow(t *testing.T) {
	for _, testcase := range []struct {
		name string
		edit func(*pb.ExtractionBatch, *CandidatePlanningPolicy)
	}{
		{"missing type policy", func(_ *pb.ExtractionBatch, p *CandidatePlanningPolicy) { delete(p.ScopesByType, "provision") }},
		{"unknown entity type", func(s *pb.ExtractionBatch, _ *CandidatePlanningPolicy) { s.Mentions[0].CandidateType = "invented" }},
		{"repeated mention", func(s *pb.ExtractionBatch, _ *CandidatePlanningPolicy) {
			s.Mentions[1].Meta.RecordId = s.Mentions[0].Meta.RecordId
		}},
		{"scope budget", func(_ *pb.ExtractionBatch, p *CandidatePlanningPolicy) { p.MaximumTotalScopes = 4 }},
		{"repeated configured scope", func(_ *pb.ExtractionBatch, p *CandidatePlanningPolicy) {
			p.ScopesByType["organization"] = []string{"national", "national"}
		}},
		{"missing provenance", func(s *pb.ExtractionBatch, _ *CandidatePlanningPolicy) { s.Mentions[0].SourceRefs = nil }},
		{"invalid Unicode", func(s *pb.ExtractionBatch, _ *CandidatePlanningPolicy) {
			s.Mentions[0].SurfaceForm = string([]byte{0xff})
		}},
		{"oversized normalized", func(s *pb.ExtractionBatch, _ *CandidatePlanningPolicy) {
			s.Mentions[0].SurfaceForm = strings.Repeat("İ", 300)
		}},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			source, policy := candidatePlanningFixture()
			testcase.edit(source, &policy)
			if plans, err := PlanRegistryCandidates(source, policy); err == nil {
				t.Fatalf("invalid or truncated candidate plan accepted: %+v", plans)
			}
		})
	}
}
