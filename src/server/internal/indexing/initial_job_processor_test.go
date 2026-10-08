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

	"github.com/jackc/pgx/v5"
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

func checkIndexProcessor(t *testing.T, ctx context.Context, repo *postgres.Repository, conn *pgx.Conn, plans *InitialIndexPlans, artifacts indexMemoryArtifacts, template *pb.IndexBatch, testCorruption bool) {
	t.Helper()
	inventory, err := plans.JobInventory()
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.ScheduleIndexJobs(ctx, inventory); err != nil {
		t.Fatal(err)
	}
	if outputs, e := repo.LoadIndexJobOutputs(ctx, inventory.Binding.PublicationID); !errors.Is(e, postgres.ErrIndexOutputsPending) || len(outputs) != 0 {
		t.Fatal("queued inventory yielded outputs", e)
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
	outputs, err := repo.LoadIndexJobOutputs(ctx, inventory.Binding.PublicationID)
	if err != nil || len(outputs) != 1 || !proto.Equal(outputs[0], response.IndexBatch) {
		t.Fatal("committed output collection", err)
	}
	prepared, err := PrepareCompletedInitialIndex(ctx, repo, artifacts, inventory.Binding.PublicationID)
	if err != nil || prepared.ExpectedBackend().ExpectedCounts.Expected != uint64(len(template.Records)) {
		t.Fatal("completed output admission", err)
	}
	// A committed locator must not authorize subsequently corrupted blob bytes.
	saved := artifacts[response.IndexBatch.ArtifactId]
	artifacts[response.IndexBatch.ArtifactId] = []byte("corrupt")
	if _, e := PrepareCompletedInitialIndex(ctx, repo, artifacts, inventory.Binding.PublicationID); e == nil {
		t.Fatal("corrupt completed bytes admitted")
	}
	artifacts[response.IndexBatch.ArtifactId] = saved
	// Reproduce the old split transaction state: checkpoint is durable but job
	// transition was lost. Recovery at exhausted model budget must not call worker.
	if _, err = conn.Exec(ctx, `UPDATE jobs SET state=$2,lease_owner='legacy',lease_expires_at=clock_timestamp()-interval '1 second',stage_attempt=max_attempts WHERE job_id=$1`, job.JobID, int16(pb.JobState_JOB_STATE_RUNNING)); err != nil {
		t.Fatal(err)
	}
	recoveredJob, recovered, err := executor.RunOnce(ctx)
	if err != nil || recovered == nil {
		t.Fatal("legacy checkpoint recovery", err)
	}
	if calls != 1 || !proto.Equal(recovered.IndexBatch, response.IndexBatch) || recovered.Checkpoint.Meta.RecordId == response.Checkpoint.Meta.RecordId || recovered.Checkpoint.Fence <= response.Checkpoint.Fence || recoveredJob.StageAttempt == 0 {
		t.Fatal("legacy recovery repeated inference or changed output")
	}
	if _, err = PrepareCompletedInitialIndex(ctx, repo, artifacts, inventory.Binding.PublicationID); err != nil {
		t.Fatal("recovered output admission", err)
	}
	// Child cancellation after completion must prevent output collection.
	if _, err = conn.Exec(ctx, `UPDATE jobs SET cancellation_requested=true WHERE job_id=$1`, job.JobID); err != nil {
		t.Fatal(err)
	}
	if outputs, e := repo.LoadIndexJobOutputs(ctx, inventory.Binding.PublicationID); !errors.Is(e, postgres.ErrIndexOutputsPending) || len(outputs) != 0 {
		t.Fatal("cancelled completed child admitted", e)
	}
	if _, err = conn.Exec(ctx, `UPDATE jobs SET cancellation_requested=false WHERE job_id=$1`, job.JobID); err != nil {
		t.Fatal(err)
	}
	if testCorruption {
		artifacts[response.IndexBatch.ArtifactId] = []byte("corrupt")
		if _, err = conn.Exec(ctx, `UPDATE jobs SET state=$2,lease_owner='legacy',lease_expires_at=clock_timestamp()-interval '1 second',stage_attempt=max_attempts WHERE job_id=$1`, job.JobID, int16(pb.JobState_JOB_STATE_RUNNING)); err != nil {
			t.Fatal(err)
		}
		if _, _, err = executor.RunOnce(ctx); !errors.Is(err, domain.ErrPersistentIntegrity) {
			t.Fatal("corrupt legacy output not classified terminal", err)
		}
		var state int16
		if err = conn.QueryRow(ctx, `SELECT state FROM jobs WHERE job_id=$1`, job.JobID).Scan(&state); err != nil || state != int16(pb.JobState_JOB_STATE_FAILED) {
			t.Fatal("corrupt legacy recovery did not fail", err)
		}
		if _, _, err = executor.RunOnce(ctx); !errors.Is(err, domain.ErrLeaseUnavailable) || calls != 1 {
			t.Fatal("corrupt legacy recovery loop", err)
		}
	}
}
