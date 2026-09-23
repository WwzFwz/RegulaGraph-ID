// Checks revision-pinned candidate handoff and negative lookup dependencies with adversarial
// fixtures. These tests verify structural integrity; gold candidate recall remains unmeasured.
package domain

import (
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func candidateBatchFixture() (*pb.RegistryCandidateBatch, *pb.ExtractionBatch, *pb.ArtifactRef) {
	_, source, sourceRef := resolutionClosureFixture()
	source.Completeness = pb.Completeness_COMPLETENESS_COMPLETE
	revision := &pb.LookupScopeRevision{ScopeId: "lookup:organization:national:instansi-a", Revision: 7}
	batch := &pb.RegistryCandidateBatch{
		Meta:                  &pb.RecordMeta{SchemaVersion: 1, CorpusId: source.Meta.CorpusId, RecordId: "candidates:one"},
		Context:               proto.Clone(source.Context).(*pb.RequestContext),
		SourceExtractionBatch: proto.Clone(sourceRef).(*pb.ArtifactRef),
		RegistryRevision:      7, Completeness: pb.Completeness_COMPLETENESS_COMPLETE,
		Candidates: []*pb.CanonicalEntity{{
			Meta:       &pb.RecordMeta{SchemaVersion: 1, CorpusId: source.Meta.CorpusId, RecordId: "canonical:one"},
			EntityType: "organization", PreferredLabel: "Instansi A", Scope: "national", RegistryRevision: 7, ReviewState: pb.ReviewState_REVIEW_STATE_UNREVIEWED,
		}},
		Lookups: []*pb.CandidateLookup{{
			MentionId: "mention:one", Scopes: []*pb.CandidateLookupScope{{
				Revision: revision, EntityType: "organization", CanonicalScope: "national",
				NormalizedLookup: "instansi a", CandidateIds: []string{"canonical:one"},
			}},
		}},
		Dependencies: &pb.DependencyManifest{
			Dependencies:         []*pb.Dependency{{DependencyId: sourceRef.ArtifactId, Fingerprint: sourceRef.ContentHash}},
			ProducerManifest:     &pb.ProducerManifest{Software: "registry-reader"},
			LookupScopeRevisions: []*pb.LookupScopeRevision{proto.Clone(revision).(*pb.LookupScopeRevision)},
		},
	}
	return batch, source, sourceRef
}

func TestValidateRegistryCandidateBatchRejectsDrift(t *testing.T) {
	base, source, sourceRef := candidateBatchFixture()
	if err := ValidateRegistryCandidateBatch(base, source, sourceRef, 8, 3); err != nil {
		t.Fatal("valid candidate batch rejected:", err)
	}
	cases := map[string]func(*pb.RegistryCandidateBatch){
		"missing mention lookup": func(batch *pb.RegistryCandidateBatch) { batch.Lookups = nil },
		"wrong candidate type":   func(batch *pb.RegistryCandidateBatch) { batch.Candidates[0].EntityType = "legal_concept" },
		"rejected candidate": func(batch *pb.RegistryCandidateBatch) {
			batch.Candidates[0].ReviewState = pb.ReviewState_REVIEW_STATE_REJECTED
		},
		"unknown candidate": func(batch *pb.RegistryCandidateBatch) {
			batch.Lookups[0].Scopes[0].CandidateIds[0] = "canonical:forged"
		},
		"stale context":             func(batch *pb.RegistryCandidateBatch) { batch.Context.AuthScopeRef = "scope:other" },
		"schema version drift":      func(batch *pb.RegistryCandidateBatch) { batch.Meta.SchemaVersion = 2 },
		"different extraction hash": func(batch *pb.RegistryCandidateBatch) { batch.SourceExtractionBatch.ContentHash.Sha256 = "forged" },
		"lookup revision omitted":   func(batch *pb.RegistryCandidateBatch) { batch.Dependencies.LookupScopeRevisions = nil },
		"lookup revision drift":     func(batch *pb.RegistryCandidateBatch) { batch.Dependencies.LookupScopeRevisions[0].Revision = 6 },
		"silent partial":            func(batch *pb.RegistryCandidateBatch) { batch.Completeness = pb.Completeness_COMPLETENESS_PARTIAL },
		"no positive lookup scope":  func(batch *pb.RegistryCandidateBatch) { batch.Lookups[0].Scopes[0].Revision.EmptyResult = true },
		"wrong canonical scope":     func(batch *pb.RegistryCandidateBatch) { batch.Lookups[0].Scopes[0].CanonicalScope = "regional" },
		"candidate overflow": func(batch *pb.RegistryCandidateBatch) {
			batch.Candidates = append(batch.Candidates, proto.Clone(batch.Candidates[0]).(*pb.CanonicalEntity))
			batch.Candidates[1].Meta.RecordId = "canonical:two"
			batch.Lookups[0].Scopes[0].CandidateIds = append(batch.Lookups[0].Scopes[0].CandidateIds, "canonical:two")
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			candidate := proto.Clone(base).(*pb.RegistryCandidateBatch)
			mutate(candidate)
			limit := 3
			if name == "candidate overflow" {
				limit = 1
			}
			if err := ValidateRegistryCandidateBatch(candidate, source, sourceRef, 8, limit); err == nil {
				t.Fatal("invalid candidate batch accepted")
			}
		})
	}
}

func TestValidateRegistryCandidateBatchKeepsNegativeLookup(t *testing.T) {
	batch, source, sourceRef := candidateBatchFixture()
	batch.Candidates = nil
	batch.Lookups[0].Scopes[0].CandidateIds = nil
	batch.Lookups[0].Scopes[0].Revision.EmptyResult = true
	batch.Dependencies.LookupScopeRevisions[0].EmptyResult = true
	if err := ValidateRegistryCandidateBatch(batch, source, sourceRef, 8, 3); err != nil {
		t.Fatal("explicit negative lookup was rejected:", err)
	}
}

func TestValidateRegistryCandidateBatchRejectsUnrelatedAlias(t *testing.T) {
	batch, source, sourceRef := candidateBatchFixture()
	batch.Aliases = []*pb.Alias{{
		Meta:        &pb.RecordMeta{SchemaVersion: 1, CorpusId: batch.Meta.CorpusId, RecordId: "alias:one"},
		CanonicalId: "canonical:one", Surface: "Instansi A", NormalizedLookup: "instansi a",
		Scope: "national", Language: "id", SupportRefs: []string{"mention:one"},
	}}
	if err := ValidateRegistryCandidateBatch(batch, source, sourceRef, 8, 3); err != nil {
		t.Fatal("alias matching its returned lookup was rejected:", err)
	}
	batch.Aliases[0].NormalizedLookup = "instansi lain"
	if err := ValidateRegistryCandidateBatch(batch, source, sourceRef, 8, 3); err == nil {
		t.Fatal("alias outside the returned lookup was accepted")
	}
	batch.Aliases[0].NormalizedLookup = "instansi a"
	batch.Aliases[0].CanonicalId = "canonical:other"
	if err := ValidateRegistryCandidateBatch(batch, source, sourceRef, 8, 3); err == nil {
		t.Fatal("alias with a foreign canonical owner was accepted")
	}
}

func TestValidateRegistryCandidateBatchRejectsCrossScopeContamination(t *testing.T) {
	batch, source, sourceRef := candidateBatchFixture()
	second := &pb.LookupScopeRevision{ScopeId: "lookup:organization:regional:instansi-a", Revision: 7, EmptyResult: true}
	batch.Lookups[0].Scopes = append(batch.Lookups[0].Scopes, &pb.CandidateLookupScope{
		Revision: second, EntityType: "organization", CanonicalScope: "regional", NormalizedLookup: "instansi a",
	})
	batch.Dependencies.LookupScopeRevisions = append(batch.Dependencies.LookupScopeRevisions, proto.Clone(second).(*pb.LookupScopeRevision))
	if err := ValidateRegistryCandidateBatch(batch, source, sourceRef, 10, 3); err != nil {
		t.Fatal("valid two-scope lookup rejected:", err)
	}
	batch.Lookups[0].Scopes[1].Revision.EmptyResult = false
	batch.Dependencies.LookupScopeRevisions[1].EmptyResult = false
	if err := ValidateRegistryCandidateBatch(batch, source, sourceRef, 10, 3); err == nil {
		t.Fatal("scope B falsely claimed a positive result from scope A")
	}
}

func TestValidateRegistryCandidateBatchCapsNestedReferences(t *testing.T) {
	batch, source, sourceRef := candidateBatchFixture()
	batch.Dependencies.Dependencies = append(batch.Dependencies.Dependencies,
		&pb.Dependency{DependencyId: "unrelated", Fingerprint: sourceRef.ContentHash})
	if err := ValidateRegistryCandidateBatch(batch, source, sourceRef, 6, 3); err == nil {
		t.Fatal("dependency list exceeded total reference budget")
	}
	batch, source, sourceRef = candidateBatchFixture()
	batch.Aliases = []*pb.Alias{{
		Meta:        &pb.RecordMeta{CorpusId: batch.Meta.CorpusId, RecordId: "alias:one"},
		CanonicalId: "canonical:one", Surface: "Instansi A", NormalizedLookup: "instansi a",
		Scope: "national", SupportRefs: []string{"one", "two", "three", "four"},
	}}
	if err := ValidateRegistryCandidateBatch(batch, source, sourceRef, 8, 3); err == nil {
		t.Fatal("alias support list exceeded total reference budget")
	}
}

func TestValidateRegistryCandidateBatchRequiresStableResultForSameScope(t *testing.T) {
	batch, source, sourceRef := candidateBatchFixture()
	secondMention := proto.Clone(source.Mentions[0]).(*pb.Mention)
	secondMention.Meta.RecordId = "mention:two"
	source.Mentions = append(source.Mentions, secondMention)
	secondCandidate := proto.Clone(batch.Candidates[0]).(*pb.CanonicalEntity)
	secondCandidate.Meta.RecordId = "canonical:two"
	batch.Candidates = append(batch.Candidates, secondCandidate)
	secondLookup := proto.Clone(batch.Lookups[0]).(*pb.CandidateLookup)
	secondLookup.MentionId = "mention:two"
	secondLookup.Scopes[0].CandidateIds = []string{"canonical:two"}
	batch.Lookups = append(batch.Lookups, secondLookup)
	if err := ValidateRegistryCandidateBatch(batch, source, sourceRef, 12, 3); err == nil {
		t.Fatal("identical registry lookup returned different candidate sets")
	}
	batch.Lookups[0].Scopes[0].CandidateIds = append(batch.Lookups[0].Scopes[0].CandidateIds, "canonical:two")
	batch.Lookups[1].Scopes[0].CandidateIds = append(batch.Lookups[1].Scopes[0].CandidateIds, "canonical:one")
	if err := ValidateRegistryCandidateBatch(batch, source, sourceRef, 14, 3); err != nil {
		t.Fatal("same candidate set in different order was rejected:", err)
	}
}

func TestValidateRegistryCandidateBatchScopeKeyCannotCollideOnDelimiter(t *testing.T) {
	batch, source, sourceRef := candidateBatchFixture()
	secondMention := proto.Clone(source.Mentions[0]).(*pb.Mention)
	secondMention.Meta.RecordId = "mention:two"
	source.Mentions = append(source.Mentions, secondMention)
	batch.Candidates = nil
	first := batch.Lookups[0].Scopes[0]
	first.CandidateIds = nil
	first.Revision.EmptyResult = true
	first.CanonicalScope = "a\x00b"
	first.NormalizedLookup = "c"
	batch.Dependencies.LookupScopeRevisions[0].EmptyResult = true
	second := proto.Clone(batch.Lookups[0]).(*pb.CandidateLookup)
	second.MentionId = "mention:two"
	second.Scopes[0].CanonicalScope = "a"
	second.Scopes[0].NormalizedLookup = "b\x00c"
	batch.Lookups = append(batch.Lookups, second)
	if err := ValidateRegistryCandidateBatch(batch, source, sourceRef, 10, 3); err == nil {
		t.Fatal("two different scope keys shared one scope ID")
	}
}

func TestValidateRegistryCandidateBatchRequiresCompleteExtraction(t *testing.T) {
	batch, source, sourceRef := candidateBatchFixture()
	source.Completeness = pb.Completeness_COMPLETENESS_PARTIAL
	if err := ValidateRegistryCandidateBatch(batch, source, sourceRef, 8, 3); err == nil {
		t.Fatal("partial extraction entered resolution candidate handoff")
	}
}
