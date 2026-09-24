// Exercises a registry revision changing around candidate preparation before any artifact write.
// This fixture isolates workflow retry classification; PostgreSQL fencing and persisted
// dependency evidence are checked by the semantic integration test with a real database.
package workflows

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type candidateRaceStore struct {
	*semanticHandoffStoreFake
	revisions []uint64
	reads     int
	prepared  *pb.RegistryCandidateBatch
	planned   []domain.RegistryCandidatePlan
}

func (store *candidateRaceStore) ReadFencedResolveRevision(_ context.Context,
	_ domain.SemanticJobFence, _ *pb.ArtifactRef, _ *pb.ExtractionBatch) (uint64, error) {
	index := store.reads
	store.reads++
	if index >= len(store.revisions) {
		return 0, errors.New("unexpected revision read")
	}
	return store.revisions[index], nil
}

func (store *candidateRaceStore) PrepareRegistryCandidateBatch(_ context.Context,
	_ *pb.ExtractionBatch, _ *pb.ArtifactRef, _ *pb.ProducerManifest, _ string,
	plans []domain.RegistryCandidatePlan, _, _, _, _ int) (*pb.RegistryCandidateBatch, error) {
	store.planned = plans
	return store.prepared, nil
}

func (*candidateRaceStore) RegisterArtifact(context.Context, string, *pb.ArtifactRef) error {
	return nil
}

func (*candidateRaceStore) ReplaceArtifactDependencyManifest(context.Context, string,
	string, *pb.DependencyManifest) error {
	return nil
}

type candidateRaceArtifacts struct {
	semanticArtifactReaderFake
	puts int
}

func (store *candidateRaceArtifacts) Put(context.Context, *pb.ArtifactRef, io.Reader) (bool, error) {
	store.puts++
	return false, nil
}

func TestCandidateRevisionRaceRetriesBeforeArtifactWrite(t *testing.T) {
	job := domain.JobRecord{JobID: "job:candidate-race", CorpusID: "corpus:race",
		State: pb.JobState_JOB_STATE_RUNNING, Stage: pb.JobStage_JOB_STAGE_RESOLVE,
		LeaseOwner: "owner:race", LeaseFence: 2, LeaseExpiresAt: time.Now().Add(time.Minute)}
	documentRef := semanticRefForTest("artifact:document-race", []byte("document"))
	documentRef.StorageKey = "objects/document-race"
	source := extractionBatchFixture(job.CorpusID, documentRef)
	source.Mentions = []*pb.Mention{{Meta: &pb.RecordMeta{SchemaVersion: 1,
		CorpusId: job.CorpusID, RecordId: "mention:race"}, CandidateType: "organization",
		SurfaceForm: "Instansi A",
		TextSpan:    &pb.TextSpan{TextArtifactId: "text:race", StartByte: 0, EndByte: 10},
		SourceRefs: []*pb.SourceVersionRef{{SourceBlobId: "blob:race",
			ProvisionVersionId: "provision:race", RegulationId: "regulation:race"}},
		ExtractionManifest: extractionManifest()}}
	raw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	sourceRef := semanticRefForTest("artifact:extraction-race", raw)
	sourceRef.MediaType = extractionBatchMediaType
	sourceRef.StorageKey = "objects/extraction-race"
	checkpoint := &pb.Checkpoint{Meta: &pb.RecordMeta{SchemaVersion: 1,
		CorpusId: job.CorpusID, RecordId: "checkpoint:extraction-race"},
		JobId: job.JobID, Stage: pb.JobStage_JOB_STAGE_EXTRACT, Fence: 1,
		TerminalStatus:     pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED,
		CompletedBatchKeys: []string{sourceRef.ArtifactId},
		ArtifactHashes:     []*pb.ContentHash{proto.Clone(sourceRef.ContentHash).(*pb.ContentHash)}}
	for _, testcase := range []struct {
		name      string
		revisions []uint64
		batchRev  uint64
	}{
		{"changed during lookup", []uint64{7}, 8},
		{"changed after lookup", []uint64{7, 8}, 7},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			store := &candidateRaceStore{semanticHandoffStoreFake: &semanticHandoffStoreFake{
				checkpoint: checkpoint, refs: map[string]*pb.ArtifactRef{sourceRef.ArtifactId: sourceRef}},
				revisions: testcase.revisions,
				prepared: &pb.RegistryCandidateBatch{Meta: &pb.RecordMeta{SchemaVersion: 1,
					CorpusId: job.CorpusID, RecordId: "candidate:race"}, RegistryRevision: testcase.batchRev}}
			artifacts := &candidateRaceArtifacts{semanticArtifactReaderFake: semanticArtifactReaderFake{
				contents: map[string][]byte{sourceRef.ArtifactId: raw}}}
			handoff, buildErr := NewSemanticResolutionHandoff(store, artifacts,
				uint64(len(raw)+1024), 16, 4)
			if buildErr != nil {
				t.Fatal(buildErr)
			}
			_, _, runErr := handoff.PrepareAndStoreCandidates(context.Background(), job,
				extractionManifest(), "candidate:race",
				[]domain.RegistryCandidatePlan{{MentionID: "mention:race", Scopes: []domain.RegistryLookupScope{{
					EntityType: "organization", CanonicalScope: "ID:national", NormalizedLookup: "instansi a"}}}}, 4, 4)
			if !errors.Is(runErr, domain.ErrCandidateViewChanged) || artifacts.puts != 0 {
				t.Fatalf("revision race wrote stale candidate: err=%v puts=%d", runErr, artifacts.puts)
			}
		})
	}
}

func TestPlannedCandidatesBindPolicyAndStoredExtract(t *testing.T) {
	handoff, job, oldRef, _, _, _, _, store, artifacts := modelWorkflowFixture(t)
	policy := domain.CandidatePlanningPolicy{ScopesByType: map[string][]string{"permit": {"national"}},
		MaximumMentions: 10, MaximumScopesPerMention: 2, MaximumTotalScopes: 10}
	fingerprint, err := policy.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	prepared := new(pb.RegistryCandidateBatch)
	if err = domain.DecodeWire(artifacts.contents[oldRef.ArtifactId], prepared, domain.DefaultWireLimits); err != nil {
		t.Fatal(err)
	}
	producer := proto.Clone(prepared.Dependencies.ProducerManifest).(*pb.ProducerManifest)
	producer.InputHashes = append(producer.InputHashes, fingerprint)
	prepared.Dependencies.ProducerManifest = proto.Clone(producer).(*pb.ProducerManifest)
	store.request = &pb.IngestionRequest{CorpusId: job.CorpusID,
		ConfigManifest: proto.Clone(parseManifest()).(*pb.ProducerManifest)}
	store.request.ConfigManifest.InputHashes = append(store.request.ConfigManifest.InputHashes, fingerprint)
	store.prepared = prepared
	batch, ref, err := handoff.PrepareAndStorePlannedCandidates(context.Background(), job,
		producer, "candidates:planned", policy, 4, 4)
	if err != nil {
		t.Fatal(err)
	}
	if ref == nil || ref.ArtifactId == oldRef.ArtifactId || store.planned == nil ||
		len(store.planned) != 1 || store.planned[0].MentionID != "mention:fixture" ||
		len(store.planned[0].Scopes) != 1 || store.planned[0].Scopes[0].NormalizedLookup != "izin" ||
		batch.GetDependencies().GetProducerManifest() == nil ||
		!proto.Equal(batch.Dependencies.ProducerManifest, producer) ||
		len(artifacts.contents[ref.ArtifactId]) == 0 || store.dependencies[ref.ArtifactId] == nil {
		t.Fatal("automatic candidate plan was not stored with pinned policy and EXTRACT dependency")
	}
	// A policy drift must fail before reading a registry revision or writing an artifact.
	changed := policy
	changed.ScopesByType = map[string][]string{"permit": {"regional"}}
	store.planned = nil
	if _, _, err = handoff.PrepareAndStorePlannedCandidates(context.Background(), job,
		producer, "candidates:drift", changed, 4, 4); err == nil || store.planned != nil {
		t.Fatal("un-pinned candidate policy was read or persisted")
	}
	// A newly constructed producer cannot authorize a policy absent from the submitted job.
	changedFingerprint, err := changed.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	forgedProducer := proto.Clone(producer).(*pb.ProducerManifest)
	forgedProducer.InputHashes = append(forgedProducer.InputHashes, changedFingerprint)
	if _, _, err = handoff.PrepareAndStorePlannedCandidates(context.Background(), job,
		forgedProducer, "candidates:forged-policy", changed, 4, 4); !errors.Is(err, domain.ErrPersistentIntegrity) ||
		store.planned != nil {
		t.Fatalf("caller re-pinned unsanctioned policy: %v", err)
	}
	// The source must come from the same submitted config even if both manifests pin the policy.
	store.request.ConfigManifest.ConfigHash = parseHash("d")
	if _, _, err = handoff.PrepareAndStorePlannedCandidates(context.Background(), job,
		producer, "candidates:foreign-source", policy, 4, 4); !errors.Is(err, domain.ErrPersistentIntegrity) ||
		store.planned != nil {
		t.Fatalf("candidate planner accepted source from another config: %v", err)
	}
}
