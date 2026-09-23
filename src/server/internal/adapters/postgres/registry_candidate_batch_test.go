// Verifies read-result to C01 candidate artifact mapping without claiming a real database read.
// PostgreSQL snapshot isolation and alias provenance require separate integration tests;
// these fixtures check deterministic binding, negative scopes, and response drift.
package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func candidateReadFixture() (*pb.ExtractionBatch, *pb.ArtifactRef, *pb.ProducerManifest,
	[]RegistryCandidatePlan, []RegistryLookupScope, []RegistryLookupResult) {
	corpus := "corpus:test"
	scope := RegistryLookupScope{EntityType: "organization", CanonicalScope: "national", NormalizedLookup: "instansi a"}
	source := &pb.ExtractionBatch{
		Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpus, RecordId: "extraction:one"},
		Context: &pb.RequestContext{
			SchemaVersion: 1, RequestId: "request:one", TraceId: "trace:one", CorpusId: corpus,
			Deadline:          timestamppb.New(time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)),
			ConfigFingerprint: &pb.ContentHash{Sha256: strings.Repeat("c", 64)}, AuthScopeRef: "scope:one",
		},
		Completeness: pb.Completeness_COMPLETENESS_COMPLETE,
		Mentions: []*pb.Mention{{
			Meta:          &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpus, RecordId: "mention:one"},
			CandidateType: "organization", SurfaceForm: "Instansi A",
		}},
	}
	sourceRef := &pb.ArtifactRef{
		ArtifactId: "artifact:extract", ContentHash: &pb.ContentHash{Sha256: strings.Repeat("a", 64)},
		StorageKey: "artifacts/extraction.pb", MediaType: "application/x-protobuf", ByteSize: 100, SchemaVersion: 1,
	}
	producer := &pb.ProducerManifest{Software: "registry-reader", Build: "test", SchemaVersion: 1,
		ConfigHash: &pb.ContentHash{Sha256: strings.Repeat("b", 64)}}
	entity := &pb.CanonicalEntity{
		Meta:       &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpus, RecordId: "canonical:one"},
		EntityType: "organization", PreferredLabel: "Instansi A", Scope: "national",
		RegistryRevision: 7, ReviewState: pb.ReviewState_REVIEW_STATE_UNREVIEWED,
	}
	alias := &pb.Alias{
		Meta:        &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpus, RecordId: "alias:one"},
		CanonicalId: "canonical:one", Surface: "Instansi A", NormalizedLookup: "instansi a",
		Language: "id", Scope: "national", SupportRefs: []string{"mention:original"},
	}
	results := []RegistryLookupResult{{
		Scope: scope, Revision: &pb.LookupScopeRevision{
			ScopeId: registryLookupScopeID(scope.EntityType, scope.CanonicalScope, scope.NormalizedLookup), Revision: 7,
		},
		Candidates: []*pb.CanonicalEntity{entity}, Aliases: []*pb.Alias{alias},
	}}
	return source, sourceRef, producer,
		[]RegistryCandidatePlan{{MentionID: "mention:one", Scopes: []RegistryLookupScope{scope}}},
		[]RegistryLookupScope{scope}, results
}

func TestAssembleRegistryCandidateResultsRetainsProvenance(t *testing.T) {
	source, sourceRef, producer, plans, scopes, results := candidateReadFixture()
	batch, err := assembleRegistryCandidateResults(source, sourceRef, producer, "candidates:one",
		plans, scopes, results, 7, 20, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Lookups) != 1 || len(batch.Candidates) != 1 || len(batch.Aliases) != 1 ||
		batch.Lookups[0].Scopes[0].CandidateIds[0] != "canonical:one" ||
		batch.Dependencies.LookupScopeRevisions[0].ScopeId != results[0].Revision.ScopeId ||
		batch.SourceExtractionBatch.ArtifactId != sourceRef.ArtifactId {
		t.Fatal("registry read lost candidate or source provenance")
	}
	batch.Aliases[0].Surface = "changed"
	if results[0].Aliases[0].Surface != "Instansi A" {
		t.Fatal("assembled artifact aliases the registry-owned input")
	}
}

func TestAssembleRegistryCandidateResultsKeepsNegativeAndRejectsDrift(t *testing.T) {
	source, sourceRef, producer, plans, scopes, results := candidateReadFixture()
	results[0].Candidates = nil
	results[0].Aliases = nil
	results[0].Revision.EmptyResult = true
	batch, err := assembleRegistryCandidateResults(source, sourceRef, producer, "candidates:empty",
		plans, scopes, results, 7, 20, 3)
	if err != nil || !batch.Dependencies.LookupScopeRevisions[0].EmptyResult {
		t.Fatalf("negative result lost or rejected: %v", err)
	}
	results[0].Revision.Revision = 8
	if _, err := assembleRegistryCandidateResults(source, sourceRef, producer, "candidates:future",
		plans, scopes, results, 7, 20, 3); err == nil {
		t.Fatal("future lookup revision was accepted")
	}
	results[0].Revision.Revision = 7
	results[0].Scope.CanonicalScope = "regional"
	if _, err := assembleRegistryCandidateResults(source, sourceRef, producer, "candidates:other",
		plans, scopes, results, 7, 20, 3); err == nil {
		t.Fatal("lookup result for a different key was accepted")
	}
	// The storage adapter must never rewrite caller-owned observations while testing failures.
	if plans[0].Scopes[0] != scopes[0] || !proto.Equal(sourceRef.ContentHash, batch.SourceExtractionBatch.ContentHash) {
		t.Fatal("candidate read inputs were mutated")
	}
}

func TestPrepareRegistryCandidateBatchRejectsInvalidPlanBeforeDatabase(t *testing.T) {
	source, sourceRef, producer, plans, _, _ := candidateReadFixture()
	plans[0].Scopes = append(plans[0].Scopes, plans[0].Scopes[0])
	if _, err := (&Repository{}).PrepareRegistryCandidateBatch(context.Background(), source,
		sourceRef, producer, "candidates:duplicate", plans, 4, 4, 20, 3); err == nil {
		t.Fatal("duplicate lookup scope reached the database")
	}
	plans[0].Scopes = plans[0].Scopes[:1]
	plans[0].Scopes[0].EntityType = "provision"
	if _, err := (&Repository{}).PrepareRegistryCandidateBatch(context.Background(), source,
		sourceRef, producer, "candidates:unsupported", plans, 4, 4, 20, 3); err == nil {
		t.Fatal("unsupported entity type reached the database")
	}
	source.Mentions = nil
	if _, err := (&Repository{}).PrepareRegistryCandidateBatch(context.Background(), source,
		sourceRef, producer, "candidates:empty", nil, 4, 4, 20, 3); !errors.Is(err, ErrNoCandidateMentions) {
		t.Fatalf("zero-mention extraction needs an explicit skip outcome, got %v", err)
	}
}

func TestAssembleRegistryCandidateResultsRejectsImpossiblePositiveRows(t *testing.T) {
	source, sourceRef, producer, plans, scopes, results := candidateReadFixture()
	try := func(name string, mutate func(*RegistryLookupResult)) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			copy := results[0]
			copy.Revision = proto.Clone(results[0].Revision).(*pb.LookupScopeRevision)
			copy.Candidates = append([]*pb.CanonicalEntity(nil), results[0].Candidates...)
			copy.Aliases = append([]*pb.Alias(nil), results[0].Aliases...)
			mutate(&copy)
			if _, err := assembleRegistryCandidateResults(source, sourceRef, producer, "candidates:bad",
				plans, scopes, []RegistryLookupResult{copy}, 7, 20, 3); err == nil {
				t.Fatal("impossible positive lookup result was accepted")
			}
		})
	}
	try("missing alias", func(result *RegistryLookupResult) { result.Aliases = nil })
	try("zero positive revision", func(result *RegistryLookupResult) { result.Revision.Revision = 0 })
	try("wrong alias key", func(result *RegistryLookupResult) {
		alias := proto.Clone(result.Aliases[0]).(*pb.Alias)
		alias.NormalizedLookup = "other"
		result.Aliases[0] = alias
	})
	try("candidate without alias", func(result *RegistryLookupResult) {
		entity := proto.Clone(result.Candidates[0]).(*pb.CanonicalEntity)
		entity.Meta.RecordId = "canonical:two"
		result.Candidates = append(result.Candidates, entity)
	})
}
