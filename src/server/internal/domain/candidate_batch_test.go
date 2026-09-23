// Exercises deterministic candidate artifact assembly, negative lookup revisions, and
// fail-closed malformed inputs. Fixtures prove a local wire boundary, not registry authenticity,
// legal identity accuracy, or throughput on the required workload.
package domain

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func candidateAssemblyFixture() (*pb.ExtractionBatch, *pb.ArtifactRef, *pb.ProducerManifest,
	[]*pb.CandidateLookup, []*pb.CanonicalEntity) {
	batch, source, sourceRef := candidateBatchFixture()
	source.Context.RequestId = "request:one"
	source.Context.TraceId = "trace:one"
	source.Context.Deadline = timestamppb.New(time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC))
	sourceRef.StorageKey = "artifacts/extraction.pb"
	sourceRef.MediaType = "application/x-protobuf"
	sourceRef.ByteSize = 100
	sourceRef.SchemaVersion = 1
	producer := &pb.ProducerManifest{
		Software: "registry-reader", Build: "test", SchemaVersion: 1,
		ConfigHash: &pb.ContentHash{Sha256: strings.Repeat("b", 64)},
	}
	return source, sourceRef, producer, batch.Lookups, batch.Candidates
}

func TestAssembleRegistryCandidateBatchSortsAndBindsEveryLookup(t *testing.T) {
	source, sourceRef, producer, lookups, entities := candidateAssemblyFixture()
	secondMention := proto.Clone(source.Mentions[0]).(*pb.Mention)
	secondMention.Meta.RecordId = "mention:two"
	source.Mentions = append(source.Mentions, secondMention)
	secondLookup := proto.Clone(lookups[0]).(*pb.CandidateLookup)
	secondLookup.MentionId = "mention:two"
	secondEntity := proto.Clone(entities[0]).(*pb.CanonicalEntity)
	secondEntity.Meta.RecordId = "canonical:two"
	entities = append(entities, secondEntity)
	firstAlias := &pb.Alias{
		Meta:        &pb.RecordMeta{SchemaVersion: 1, CorpusId: source.Meta.CorpusId, RecordId: "alias:one"},
		CanonicalId: "canonical:one", Surface: "Instansi A", NormalizedLookup: "instansi a",
		Scope: "national", Language: "id", SupportRefs: []string{"mention:source"},
	}
	secondAlias := proto.Clone(firstAlias).(*pb.Alias)
	secondAlias.Meta.RecordId = "alias:two"
	secondAlias.CanonicalId = "canonical:two"
	aliases := []*pb.Alias{firstAlias, secondAlias}
	lookups[0].Scopes[0].CandidateIds = []string{"canonical:one", "canonical:two"}
	secondLookup.Scopes[0].CandidateIds = []string{"canonical:one", "canonical:two"}
	forward := []*pb.CandidateLookup{lookups[0], secondLookup}
	reverse := []*pb.CandidateLookup{
		proto.Clone(secondLookup).(*pb.CandidateLookup),
		proto.Clone(lookups[0]).(*pb.CandidateLookup),
	}
	for _, lookup := range reverse {
		lookup.Scopes[0].CandidateIds = []string{"canonical:two", "canonical:one"}
	}
	first, err := AssembleRegistryCandidateBatch(source, sourceRef, producer, "candidates:sorted", 7,
		forward, entities, aliases, 24, 3)
	if err != nil {
		t.Fatal(err)
	}
	second, err := AssembleRegistryCandidateBatch(source, sourceRef, producer, "candidates:sorted", 7,
		reverse, []*pb.CanonicalEntity{entities[1], entities[0]}, []*pb.Alias{secondAlias, firstAlias}, 24, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(first, second) || len(first.Dependencies.LookupScopeRevisions) != 1 ||
		first.Dependencies.LookupScopeRevisions[0].ScopeId != lookups[0].Scopes[0].Revision.ScopeId ||
		first.Lookups[0].MentionId != "mention:one" || reverse[0].MentionId != "mention:two" ||
		reverse[0].Scopes[0].CandidateIds[0] != "canonical:two" {
		t.Fatal("assembly changed with input order or mutated caller-owned lookups")
	}
	if err := ValidateWire(first, DefaultWireLimits); err != nil {
		t.Fatal("assembled candidate batch is not wire-valid:", err)
	}
}

func TestAssembleRegistryCandidateBatchRetainsNegativeResultAndRejectsDrift(t *testing.T) {
	source, sourceRef, producer, lookups, _ := candidateAssemblyFixture()
	lookups[0].Scopes[0].CandidateIds = nil
	lookups[0].Scopes[0].Revision.EmptyResult = true
	batch, err := AssembleRegistryCandidateBatch(source, sourceRef, producer, "candidates:empty", 7,
		lookups, nil, nil, 8, 3)
	if err != nil {
		t.Fatal("negative lookup was rejected:", err)
	}
	if !batch.Dependencies.LookupScopeRevisions[0].EmptyResult || len(batch.Candidates) != 0 {
		t.Fatal("negative lookup dependency was lost")
	}
	secondMention := proto.Clone(source.Mentions[0]).(*pb.Mention)
	secondMention.Meta.RecordId = "mention:two"
	source.Mentions = append(source.Mentions, secondMention)
	secondLookup := proto.Clone(lookups[0]).(*pb.CandidateLookup)
	secondLookup.MentionId = "mention:two"
	secondLookup.Scopes[0].Revision.Revision = 6
	if _, err := AssembleRegistryCandidateBatch(source, sourceRef, producer, "candidates:drift", 7,
		[]*pb.CandidateLookup{lookups[0], secondLookup}, nil, nil, 12, 3); err == nil {
		t.Fatal("same lookup scope with conflicting revisions was accepted")
	}
	sourceRef.ContentHash = nil
	if _, err := AssembleRegistryCandidateBatch(source, sourceRef, producer, "candidates:missing", 7,
		lookups, nil, nil, 8, 3); err == nil {
		t.Fatal("missing source hash was accepted")
	}
}

func TestAssembleRegistryCandidateBatchCapsBytesBeforeCopy(t *testing.T) {
	source, sourceRef, producer, lookups, entities := candidateAssemblyFixture()
	entities[0].PreferredLabel = strings.Repeat("x", DefaultWireLimits.MaxBytes)
	if _, err := AssembleRegistryCandidateBatch(source, sourceRef, producer, "candidates:oversized", 7,
		lookups, entities, nil, 8, 3); err == nil {
		t.Fatal("oversized registry record was copied into candidate artifact")
	}
}
