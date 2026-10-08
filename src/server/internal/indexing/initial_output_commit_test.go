// Verifies output checkpoint/STAGED atomicity against disposable PostgreSQL.
// Synthetic INDEX vectors exercise storage authority, not inference accuracy.
// Failed plan/source/lease/publication checks must leave no checkpoint or state
// advance. Production byte admission is covered by ExecuteBatch tests separately.
package indexing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/domain"
)

func TestInitialIndexOutputCommitAgainstPostgres(t *testing.T) {
	runInitialIndexPublication(t, false, true, "output")
}

func TestInitialIndexWideOutputCommitAgainstPostgres(t *testing.T) {
	runInitialIndexPublication(t, false, true, "wide-output")
}

func checkIndexOutputCommit(t *testing.T, ctx context.Context, repo *postgres.Repository, conn *pgx.Conn, plans *InitialIndexPlans, ref *pb.ArtifactRef, batch *pb.IndexBatch) {
	t.Helper()
	if len(batch.Records) == 128 {
		if err := domain.ValidateWire(batch, domain.DefaultWireLimits); err == nil {
			t.Fatal("wide fixture does not exercise metadata item overflow")
		}
	}
	inventory, err := plans.JobInventory()
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.ScheduleIndexJobs(ctx, inventory); err != nil {
		t.Fatal(err)
	}
	job, err := repo.ClaimIndexJob(ctx, "index-output", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	cp := &pb.Checkpoint{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: job.CorpusID, RecordId: "checkpoint:index-output"}, JobId: job.JobID, Stage: pb.JobStage_JOB_STAGE_INDEX, Fence: job.LeaseFence, Manifest: inventory.Assignments[0].Plan.Producer, CompletedBatchKeys: []string{ref.ArtifactId}, ArtifactHashes: []*pb.ContentHash{ref.ContentHash}, TerminalStatus: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED}
	checkRollback := func() {
		t.Helper()
		if _, e := repo.LoadLatestCheckpoint(ctx, job.JobID); !errors.Is(e, postgres.ErrNotFound) {
			t.Fatal("failed output left checkpoint", e)
		}
		var state int16
		if e := conn.QueryRow(ctx, `SELECT state FROM jobs WHERE job_id=$1`, job.JobID).Scan(&state); e != nil || state != int16(pb.JobState_JOB_STATE_RUNNING) {
			t.Fatal("failed output advanced state", state, e)
		}
	}
	for _, kind := range []string{"plan", "snapshot", "record", "owner", "source-cancel", "job-cancel", "publisher", "unregistered"} {
		t.Run(kind, func(t *testing.T) {
			changed := proto.Clone(batch).(*pb.IndexBatch)
			changedCP := proto.Clone(cp).(*pb.Checkpoint)
			changedRef := proto.Clone(ref).(*pb.ArtifactRef)
			owner := job.LeaseOwner
			switch kind {
			case "plan":
				changed.BuildPlan.ArtifactId = "plan:wrong"
			case "snapshot":
				changed.Context.SnapshotRef.SnapshotId = "snapshot:wrong"
			case "record":
				changed.Records[0].Meta.RecordId = "record:wrong"
			case "owner":
				owner = "owner:wrong"
			case "source-cancel":
				if _, e := conn.Exec(ctx, `UPDATE jobs SET cancellation_requested=true WHERE job_id=$1`, inventory.Assignments[0].SourceJobID); e != nil {
					t.Fatal(e)
				}
				defer func() {
					if _, e := conn.Exec(ctx, `UPDATE jobs SET cancellation_requested=false WHERE job_id=$1`, inventory.Assignments[0].SourceJobID); e != nil {
						t.Error(e)
					}
				}()
			case "job-cancel":
				if _, e := conn.Exec(ctx, `UPDATE jobs SET cancellation_requested=true WHERE job_id=$1`, job.JobID); e != nil {
					t.Fatal(e)
				}
				defer func() {
					if _, e := conn.Exec(ctx, `UPDATE jobs SET cancellation_requested=false WHERE job_id=$1`, job.JobID); e != nil {
						t.Error(e)
					}
				}()
			case "publisher":
				if _, e := conn.Exec(ctx, `UPDATE corpus_state SET publisher_fence=publisher_fence+1 WHERE corpus_id=$1`, job.CorpusID); e != nil {
					t.Fatal(e)
				}
				defer func() {
					if _, e := conn.Exec(ctx, `UPDATE corpus_state SET publisher_fence=publisher_fence-1 WHERE corpus_id=$1`, job.CorpusID); e != nil {
						t.Error(e)
					}
				}()
			case "unregistered":
				changedRef.ArtifactId = "artifact:unregistered"
				changedCP.CompletedBatchKeys[0] = changedRef.ArtifactId
			}
			if e := repo.SaveIndexCheckpoint(ctx, changedCP, owner, changedRef, changed); e == nil {
				t.Fatal("invalid output committed")
			}
			checkRollback()
		})
	}
	t.Run("lease expires while waiting for publication lock", func(t *testing.T) {
		if _, err := conn.Exec(ctx, `UPDATE jobs SET lease_expires_at=clock_timestamp()+interval '1 second' WHERE job_id=$1`, job.JobID); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if _, err := conn.Exec(ctx, `UPDATE jobs SET lease_expires_at=clock_timestamp()+interval '1 minute' WHERE job_id=$1`, job.JobID); err != nil {
				t.Error(err)
			}
		}()
		lock, err := conn.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer lock.Rollback(ctx)
		if _, err = lock.Exec(ctx, `SELECT publication_id FROM snapshots WHERE publication_id=$1 FOR UPDATE`, inventory.Binding.PublicationID); err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() { result <- repo.SaveIndexCheckpoint(ctx, cp, job.LeaseOwner, ref, batch) }()
		// Observe the actual blocked transaction before waiting for lease expiry.
		deadline := time.Now().Add(3 * time.Second)
		blocked := false
		for time.Now().Before(deadline) {
			if err = lock.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE NOT granted AND pg_backend_pid()=ANY(pg_blocking_pids(pid)))`).Scan(&blocked); err != nil {
				t.Fatal(err)
			}
			if blocked {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !blocked {
			lock.Rollback(ctx)
			<-result
			t.Fatal("checkpoint never waited on publisher lock")
		}
		time.Sleep(1100 * time.Millisecond)
		if err = lock.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		if err = <-result; !errors.Is(err, postgres.ErrStaleFence) {
			t.Fatal("late expired checkpoint accepted", err)
		}
		checkRollback()
		if _, err = conn.Exec(ctx, `UPDATE jobs SET lease_expires_at=clock_timestamp()+interval '1 minute' WHERE job_id=$1`, job.JobID); err != nil {
			t.Fatal(err)
		}
	})
	if err = repo.SaveIndexCheckpoint(ctx, cp, job.LeaseOwner, ref, batch); err != nil {
		t.Fatal("commit output", err)
	}
	stored, err := repo.LoadLatestCheckpoint(ctx, job.JobID)
	if err != nil || !proto.Equal(cp, stored) {
		t.Fatal("checkpoint drift", err)
	}
	var state int16
	var released bool
	if err = conn.QueryRow(ctx, `SELECT state,lease_owner IS NULL AND lease_expires_at IS NULL FROM jobs WHERE job_id=$1`, job.JobID).Scan(&state, &released); err != nil || state != int16(pb.JobState_JOB_STATE_STAGED) || !released {
		t.Fatal("checkpoint/state not atomic", state, released, err)
	}
	if _, err = repo.ClaimIndexJob(ctx, "index-after-commit", time.Minute); !errors.Is(err, postgres.ErrLeaseUnavailable) {
		t.Fatal("staged child rerun", err)
	}
	if _, err = repo.ClaimJob(ctx, "generic-after-commit", time.Minute); !errors.Is(err, postgres.ErrLeaseUnavailable) {
		t.Fatal("generic stole staged child", err)
	}
}
