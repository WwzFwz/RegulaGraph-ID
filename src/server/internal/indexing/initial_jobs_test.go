// Tests durable INDEX inventory ownership and recovery against disposable
// PostgreSQL, including all-or-nothing scheduling and single-connection pools.
// Source/checkpoint bytes are synthetic seeds; this is no quality/latency gate.
// Reconstruction tests independently reject omitted plans and mutated sources.
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

func TestInitialIndexInventoryRestoresAdmission(t *testing.T) {
	f := newPlanningFixture(t)
	p, err := PlanInitialIndex(context.Background(), f.authority, f.artifacts, f.config, f.inputs)
	if err != nil {
		t.Fatal(err)
	}
	in, err := p.JobInventory()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := RestoreInitialIndexPlans(context.Background(), f.authority, f.artifacts, in)
	if err != nil || len(restored.Batches()) != len(p.Batches()) {
		t.Fatal(err)
	}
	in.Assignments[0].Plan.Items[0].ChunkId = "changed"
	fresh, _ := p.JobInventory()
	if proto.Equal(in.Assignments[0].Plan, fresh.Assignments[0].Plan) {
		t.Fatal("inventory aliases internal plan")
	}
	if _, err := RestoreInitialIndexPlans(context.Background(), f.authority, f.artifacts, in); err == nil {
		t.Fatal("changed plan admitted")
	}
	fresh.Assignments = fresh.Assignments[:len(fresh.Assignments)-1]
	if _, err := RestoreInitialIndexPlans(context.Background(), f.authority, f.artifacts, fresh); err == nil {
		t.Fatal("missing plan admitted")
	}
	fresh, _ = p.JobInventory()
	f.authority.sources = nil
	if _, err := RestoreInitialIndexPlans(context.Background(), f.authority, f.artifacts, fresh); err == nil {
		t.Fatal("lost source authority admitted")
	}
}

func TestInitialIndexJobInventoryAgainstPostgres(t *testing.T) {
	runInitialIndexPublication(t, false, true, "inventory")
}

func checkDurableIndexInventory(t *testing.T, ctx context.Context, dsn string, conn *pgx.Conn, plans *InitialIndexPlans) {
	t.Helper()
	// One connection proves no pool acquisition is nested inside a transaction.
	repo, err := postgres.Open(ctx, postgres.Config{DSN: dsn, MaxConnections: 1, HealthTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	inventory, err := plans.JobInventory()
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Assignments) < 2 {
		t.Fatal("rollback test requires multiple plans")
	}
	bad, _ := plans.JobInventory()
	bad.Assignments[1].Reference.StorageKey += ".wrong"
	if err = repo.ScheduleIndexJobs(ctx, bad); err == nil {
		t.Fatal("changed registered plan accepted")
	}
	var count int
	if err = conn.QueryRow(ctx, `SELECT count(*) FROM index_job_assignments`).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial inventory", count, err)
	}
	if err = conn.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE stage=$1`, int16(pb.JobStage_JOB_STAGE_INDEX)).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial jobs", count, err)
	}
	for i := 0; i < 2; i++ {
		if err = repo.ScheduleIndexJobs(ctx, inventory); err != nil {
			t.Fatal("schedule/replay", err)
		}
	}
	loaded, err := repo.LoadIndexJobInventory(ctx, inventory.Binding.PublicationID)
	if err != nil || len(loaded.Assignments) != len(inventory.Assignments) {
		t.Fatal("load inventory", err)
	}
	for i, a := range loaded.Assignments {
		if a.JobID != inventory.Assignments[i].JobID || !proto.Equal(a.Plan, inventory.Assignments[i].Plan) {
			t.Fatal("inventory drift")
		}
	}
	drift, _ := plans.JobInventory()
	drift.AuthScope += ":changed"
	if err = repo.ScheduleIndexJobs(ctx, drift); !errors.Is(err, postgres.ErrConflict) {
		t.Fatal("replay drift", err)
	}
	if _, err = repo.ClaimJob(ctx, "generic", time.Minute); !errors.Is(err, postgres.ErrLeaseUnavailable) {
		t.Fatal("generic stole INDEX", err)
	}
	sourceJob := inventory.Assignments[0].SourceJobID
	if _, err = conn.Exec(ctx, `UPDATE jobs SET cancellation_requested=true WHERE job_id=$1`, sourceJob); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.ClaimIndexJob(ctx, "cancelled-source", time.Minute); !errors.Is(err, postgres.ErrLeaseUnavailable) {
		t.Fatal("cancelled source claimed", err)
	}
	if _, err = conn.Exec(ctx, `UPDATE jobs SET cancellation_requested=false WHERE job_id=$1`, sourceJob); err != nil {
		t.Fatal(err)
	}
	first, err := repo.ClaimIndexJob(ctx, "index-a", time.Minute)
	if err != nil {
		t.Fatal("claim first", err)
	}
	second, err := repo.ClaimIndexJob(ctx, "index-b", time.Minute)
	if err != nil || second.JobID == first.JobID {
		t.Fatal("claim second", err)
	}
	if _, err = repo.ClaimIndexJob(ctx, "index-c", time.Minute); !errors.Is(err, postgres.ErrLeaseUnavailable) {
		t.Fatal("live lease reclaimed", err)
	}
	if _, err = conn.Exec(ctx, `UPDATE jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE job_id=$1`, first.JobID); err != nil {
		t.Fatal(err)
	}
	recovered, err := repo.ClaimIndexJob(ctx, "index-recover", time.Minute)
	if err != nil || recovered.JobID != first.JobID || recovered.LeaseFence <= first.LeaseFence || recovered.Attempt != first.Attempt+1 {
		t.Fatal("recover lease", err)
	}
	if _, err = repo.RenewLease(ctx, first.JobID, first.LeaseOwner, first.LeaseFence, time.Minute); !errors.Is(err, postgres.ErrStaleFence) {
		t.Fatal("stale owner accepted", err)
	}
	// An expired attempt with no terminal checkpoint must stop at its budget.
	if _, err = conn.Exec(ctx, `UPDATE jobs SET stage_attempt=max_attempts,lease_expires_at=clock_timestamp()-interval '1 second' WHERE job_id=$1`, second.JobID); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.ClaimIndexJob(ctx, "exhausted", time.Minute); !errors.Is(err, postgres.ErrLeaseUnavailable) {
		t.Fatal("exhausted attempt reclaimed", err)
	}
	var state int16
	if err = conn.QueryRow(ctx, `SELECT state FROM jobs WHERE job_id=$1`, second.JobID).Scan(&state); err != nil || state != int16(pb.JobState_JOB_STATE_FAILED) {
		t.Fatal("budget did not terminate", state, err)
	}
	// A terminal checkpoint permits recovery, without consuming model attempts.
	checkpoint := &pb.Checkpoint{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: inventory.Snapshot.CorpusId, RecordId: "checkpoint:index-recovery"}, JobId: recovered.JobID, Stage: pb.JobStage_JOB_STAGE_INDEX, Fence: recovered.LeaseFence, Manifest: inventory.Assignments[0].Plan.Producer, TerminalStatus: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED}
	if err = repo.SaveCheckpoint(ctx, checkpoint, recovered.LeaseOwner); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `UPDATE jobs SET max_attempts=stage_attempt WHERE job_id=$1`, recovered.JobID); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.CompleteWorkerAttempt(ctx, recovered.JobID, recovered.LeaseOwner, recovered.LeaseFence, pb.JobState_JOB_STATE_RETRY_WAIT, time.Microsecond); err != nil {
		t.Fatal(err)
	}
	before := recovered
	recovered, err = repo.ClaimIndexJob(ctx, "terminal-recovery", time.Minute)
	if err != nil || recovered.JobID != before.JobID || recovered.StageAttempt != before.StageAttempt || recovered.Attempt != before.Attempt+1 {
		t.Fatal("checkpoint recovery budget", err)
	}
	if _, err = repo.CompleteWorkerAttempt(ctx, recovered.JobID, recovered.LeaseOwner, recovered.LeaseFence, pb.JobState_JOB_STATE_RETRY_WAIT, time.Microsecond); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `UPDATE corpus_state SET publisher_fence=publisher_fence+1 WHERE corpus_id=$1`, inventory.Snapshot.CorpusId); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.ClaimIndexJob(ctx, "index-stale-publication", time.Minute); !errors.Is(err, postgres.ErrLeaseUnavailable) {
		t.Fatal("stale publication claimed", err)
	}
	if err = repo.ScheduleIndexJobs(ctx, inventory); !errors.Is(err, postgres.ErrConflict) {
		t.Fatal("stale publication replay", err)
	}
	if err = domain.ValidateIndexJobInventory(loaded); err != nil {
		t.Fatal(err)
	}
}
