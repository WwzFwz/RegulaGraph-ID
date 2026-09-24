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
	_ []domain.RegistryCandidatePlan, _, _, _, _ int) (*pb.RegistryCandidateBatch, error) {
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
