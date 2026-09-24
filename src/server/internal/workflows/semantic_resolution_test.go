// Verifies checkpoint-bound semantic input reads and fail-closed handoff before registry CAS.
// Fakes isolate the workflow contract; PostgreSQL fencing/replay are tested by the adapter's
// integration suite. These checks do not measure resolver quality or production latency.
package workflows

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type semanticHandoffStoreFake struct {
	checkpoint *pb.Checkpoint
	refs       map[string]*pb.ArtifactRef
	commits    int
	proof      domain.SemanticJobFence
	input      domain.SemanticRegistryInputs
}

func (fake *semanticHandoffStoreFake) LoadLatestCheckpoint(_ context.Context, _ string) (*pb.Checkpoint, error) {
	return fake.checkpoint, nil
}

func (fake *semanticHandoffStoreFake) LoadArtifact(_ context.Context, _, id string) (*pb.ArtifactRef, error) {
	return fake.refs[id], nil
}

func (fake *semanticHandoffStoreFake) CommitSemanticResolutions(_ context.Context,
	proof domain.SemanticJobFence, input domain.SemanticRegistryInputs,
	_ *pb.RegistryResolveRequest, _ []domain.ReviewedLink, _, _ int) (*pb.RegistryResolveResponse, error) {
	fake.commits++
	fake.proof, fake.input = proof, input
	return &pb.RegistryResolveResponse{RegistryRevision: 8}, nil
}

type semanticArtifactReaderFake struct {
	contents map[string][]byte
	reads    int
}

func (fake *semanticArtifactReaderFake) ReadVerified(_ context.Context,
	ref *pb.ArtifactRef, maximum uint64) ([]byte, error) {
	fake.reads++
	raw := fake.contents[ref.ArtifactId]
	if uint64(len(raw)) > maximum {
		return nil, errors.New("fixture exceeds bound")
	}
	return raw, nil
}

func semanticRefForTest(id string, raw []byte) *pb.ArtifactRef {
	digest := sha256.Sum256(raw)
	return &pb.ArtifactRef{ArtifactId: id, ContentHash: &pb.ContentHash{Sha256: hex.EncodeToString(digest[:])},
		StorageKey: "objects/" + id, MediaType: "application/x-protobuf",
		ByteSize: uint64(len(raw)), SchemaVersion: 1}
}

func semanticHandoffFixture() (domain.JobRecord, *pb.RegistryResolveRequest,
	*pb.ArtifactRef, *semanticHandoffStoreFake, *semanticArtifactReaderFake) {
	job := domain.JobRecord{JobID: "job:resolve", CorpusID: "corpus:one",
		State: pb.JobState_JOB_STATE_RUNNING, Stage: pb.JobStage_JOB_STAGE_RESOLVE,
		LeaseOwner: "owner:one", LeaseFence: 3, LeaseExpiresAt: time.Now().Add(time.Minute)}
	source := semanticRefForTest("artifact:extract", []byte("source"))
	candidate := semanticRefForTest("artifact:candidates", []byte("candidates"))
	checkpoint := &pb.Checkpoint{Meta: &pb.RecordMeta{SchemaVersion: 1,
		CorpusId: job.CorpusID, RecordId: "checkpoint:extract"},
		JobId: job.JobID, Stage: pb.JobStage_JOB_STAGE_EXTRACT, Fence: 2,
		TerminalStatus:     pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED,
		CompletedBatchKeys: []string{source.ArtifactId},
		ArtifactHashes:     []*pb.ContentHash{proto.Clone(source.ContentHash).(*pb.ContentHash)}}
	store := &semanticHandoffStoreFake{checkpoint: checkpoint,
		refs: map[string]*pb.ArtifactRef{source.ArtifactId: source, candidate.ArtifactId: candidate}}
	reader := &semanticArtifactReaderFake{contents: map[string][]byte{
		source.ArtifactId: []byte("source"), candidate.ArtifactId: []byte("candidates")}}
	request := &pb.RegistryResolveRequest{Context: &pb.RequestContext{CorpusId: job.CorpusID}}
	return job, request, candidate, store, reader
}

func TestSemanticHandoffUsesVerifiedCheckpointedBytes(t *testing.T) {
	job, request, candidate, store, reader := semanticHandoffFixture()
	handoff, err := NewSemanticResolutionHandoff(store, reader, 64, 12, 3)
	if err != nil {
		t.Fatal(err)
	}
	response, err := handoff.Commit(context.Background(), job, candidate, request, nil)
	if err != nil || response.RegistryRevision != 8 || store.commits != 1 || reader.reads != 2 ||
		store.proof.JobID != job.JobID || store.proof.OwnerID != job.LeaseOwner ||
		store.proof.Fence != job.LeaseFence ||
		store.proof.SourceCheckpointID != store.checkpoint.Meta.RecordId ||
		string(store.input.SourceBytes) != "source" || string(store.input.CandidateBytes) != "candidates" {
		t.Fatalf("semantic handoff lost verified input or fence: response=%v err=%v proof=%+v", response, err, store.proof)
	}
}

func TestSemanticHandoffRejectsUntrustedOrStaleInputBeforeCommit(t *testing.T) {
	tests := map[string]func(*domain.JobRecord, *pb.ArtifactRef,
		*semanticHandoffStoreFake, *semanticArtifactReaderFake){
		"expired lease": func(job *domain.JobRecord, _ *pb.ArtifactRef, _ *semanticHandoffStoreFake, _ *semanticArtifactReaderFake) {
			job.LeaseExpiresAt = time.Now().Add(-time.Second)
		},
		"wrong checkpoint stage": func(_ *domain.JobRecord, _ *pb.ArtifactRef, store *semanticHandoffStoreFake, _ *semanticArtifactReaderFake) {
			store.checkpoint.Stage = pb.JobStage_JOB_STAGE_CHUNK
		},
		"checkpoint from current fence": func(job *domain.JobRecord, _ *pb.ArtifactRef, store *semanticHandoffStoreFake, _ *semanticArtifactReaderFake) {
			store.checkpoint.Fence = job.LeaseFence
		},
		"wrong source hash": func(_ *domain.JobRecord, _ *pb.ArtifactRef, store *semanticHandoffStoreFake, _ *semanticArtifactReaderFake) {
			store.checkpoint.ArtifactHashes[0].Sha256 = "wrong"
		},
		"candidate metadata forged": func(_ *domain.JobRecord, candidate *pb.ArtifactRef, store *semanticHandoffStoreFake, _ *semanticArtifactReaderFake) {
			store.refs[candidate.ArtifactId] = proto.Clone(candidate).(*pb.ArtifactRef)
			candidate.StorageKey = "objects/forged"
		},
		"total byte budget exceeded": func(_ *domain.JobRecord, _ *pb.ArtifactRef, store *semanticHandoffStoreFake, _ *semanticArtifactReaderFake) {
			store.refs["artifact:extract"].ByteSize = 63
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			job, request, candidate, store, reader := semanticHandoffFixture()
			mutate(&job, candidate, store, reader)
			handoff, err := NewSemanticResolutionHandoff(store, reader, 20, 12, 3)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = handoff.Commit(context.Background(), job, candidate, request, nil); err == nil || store.commits != 0 {
				t.Fatalf("invalid handoff reached registry: err=%v commits=%d", err, store.commits)
			}
		})
	}
}
