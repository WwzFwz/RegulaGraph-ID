// Exercises durable claim -> restored plan -> worker boundary -> registration ->
// atomic STAGED commit with real PostgreSQL and authenticated synthetic bytes.
// The worker vectors remain fixtures; this proves composition, not model quality.
package indexing

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/workflows"
)

func TestInitialIndexProcessorAgainstPostgres(t *testing.T) {
	runInitialIndexPublication(t, false, true, "processor")
}

type lostIndexCheckpointReply struct {
	*postgres.Repository
	lost bool
}

func (s *lostIndexCheckpointReply) SaveIndexCheckpoint(ctx context.Context, cp *pb.Checkpoint, owner string, ref *pb.ArtifactRef, batch *pb.IndexBatch) error {
	if err := s.Repository.SaveIndexCheckpoint(ctx, cp, owner, ref, batch); err != nil {
		return err
	}
	s.lost = true
	return errors.New("injected lost checkpoint commit acknowledgement")
}

func checkIndexProcessor(t *testing.T, ctx context.Context, repo *postgres.Repository, plans *InitialIndexPlans, artifacts indexMemoryArtifacts, template *pb.IndexBatch) {
	t.Helper()
	inventory, err := plans.JobInventory()
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.ScheduleIndexJobs(ctx, inventory); err != nil {
		t.Fatal(err)
	}
	calls := 0
	worker := indexWorkerFunc(func(_ context.Context, req *pb.ProcessBatchRequest) (*pb.ProcessBatchResponse, error) {
		calls++
		batch := proto.Clone(template).(*pb.IndexBatch)
		batch.Context = proto.Clone(req.Context).(*pb.RequestContext)
		raw, err := proto.Marshal(batch)
		if err != nil {
			return nil, err
		}
		hash := fmt.Sprintf("%x", sha256.Sum256(raw))
		ref := &pb.ArtifactRef{ArtifactId: "artifact:index-batch:" + hash, SchemaVersion: 1, ContentHash: &pb.ContentHash{Sha256: hash}, ByteSize: uint64(len(raw)), MediaType: domain.IndexBatchMediaType, StorageKey: "sha256/" + hash[:2] + "/" + hash[2:4] + "/" + hash + ".bin"}
		artifacts[ref.ArtifactId] = raw
		cp := &pb.Checkpoint{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: req.Context.CorpusId, RecordId: "checkpoint:" + req.Context.RequestId}, JobId: req.JobId, Fence: req.Lease.Fence, Stage: pb.JobStage_JOB_STAGE_INDEX, Manifest: req.Manifest, CompletedBatchKeys: []string{ref.ArtifactId}, ArtifactHashes: []*pb.ContentHash{ref.ContentHash}, TerminalStatus: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED}
		return &pb.ProcessBatchResponse{RequestId: req.Context.RequestId, JobId: req.JobId, Attempt: req.Attempt, Fence: req.Lease.Fence, IndexBatch: ref, Checkpoint: cp, Status: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED}, nil
	})
	store := &lostIndexCheckpointReply{Repository: repo}
	processor, err := NewInitialIndexJobProcessor(store, artifacts, worker, inventory.AuthScope)
	if err != nil {
		t.Fatal(err)
	}
	executor, err := workflows.NewIndexExecutor(repo, processor, workflows.IndexExecutorConfig{OwnerID: "index-processor", Lease: time.Minute, CallTimeout: 30 * time.Second, CancellationPoll: time.Millisecond * 10, RetryBase: time.Second, RetryMax: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	job, response, err := executor.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || response.Status != pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED {
		t.Fatal("worker accounting")
	}
	if !store.lost {
		t.Fatal("lost acknowledgement not exercised")
	}
	changed := proto.Clone(response.Checkpoint).(*pb.Checkpoint)
	changed.Fence++
	if committed, e := repo.IndexCheckpointCommitted(ctx, changed); e != nil || committed {
		t.Fatal("different fence confirmed", e)
	}
	cp, err := repo.LoadLatestCheckpoint(ctx, job.JobID)
	if err != nil || !proto.Equal(cp, response.Checkpoint) {
		t.Fatal("durable output", err)
	}
	if _, _, err = executor.RunOnce(ctx); !errors.Is(err, domain.ErrLeaseUnavailable) || calls != 1 {
		t.Fatal("completed child reran inference", err)
	}
	if _, err = processor.ProcessIndexJob(ctx, job); !errors.Is(err, domain.ErrIndexReplan) || calls != 1 {
		t.Fatal("stale/released claim dispatched", err)
	}
}
