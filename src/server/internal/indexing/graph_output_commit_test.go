// Exercises atomic ASSEMBLE metadata commit on a real admitted PostgreSQL source
// inventory after Qdrant publication. Output metadata is synthetic and explicitly
// does not prove worker projection or model quality; Rust-byte workflow tests own
// that boundary. Fault injection covers rollback, stale authority and exact replay.
package indexing

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/domain"
)

func checkGraphOutputCommit(t *testing.T, ctx context.Context, repo *postgres.Repository, db *pgx.Conn,
	pin domain.SnapshotPin, inventory domain.GraphJobInventory, inputs map[string]domain.GraphJobSourceInputs, job domain.JobRecord) {
	t.Helper()
	admitted, err := repo.PrepareGraphJobAdmission(ctx, inventory, inputs, 4096, 32)
	if err != nil {
		t.Fatal(err)
	}
	a := inventory.Assignments[0]
	call := proto.Clone(a.Plan.Context).(*pb.RequestContext)
	call.Deadline = timestamppb.New(time.Now().Add(time.Minute))
	request, err := domain.BuildGraphAssemblyRequest(a, job, call)
	if err != nil {
		t.Fatal(err)
	}
	ref := &pb.ArtifactRef{ArtifactId: "artifact:graph-commit-fixture", ContentHash: &pb.ContentHash{Sha256: strings.Repeat("a", 64)}, StorageKey: "graph/fixture.pb", MediaType: domain.GraphDeltaMediaType, ByteSize: 10, SchemaVersion: 1}
	cp := &pb.Checkpoint{Meta: &pb.RecordMeta{RecordId: "checkpoint:graph-commit-fixture", CorpusId: job.CorpusID, SchemaVersion: 1}, JobId: job.JobID, Stage: pb.JobStage_JOB_STAGE_ASSEMBLE, CompletedBatchKeys: []string{ref.ArtifactId}, ArtifactHashes: []*pb.ContentHash{ref.ContentHash}, Manifest: a.Plan.ProducerManifest, Fence: job.LeaseFence, TerminalStatus: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED}
	response := &pb.ProcessBatchResponse{RequestId: request.Context.RequestId, JobId: job.JobID, Attempt: job.Attempt, Fence: job.LeaseFence, Checkpoint: cp, GraphDelta: ref, Status: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED}
	manifest := &pb.DependencyManifest{ArtifactId: a.Plan.OutputArtifactId, ProducerManifest: a.Plan.ProducerManifest, Dependencies: []*pb.Dependency{{DependencyId: a.Plan.DocumentBatch.ArtifactId, Fingerprint: a.Plan.DocumentBatch.ContentHash}}}
	commit := func() error { return admitted.CommitGraphOutput(ctx, pin, job, request, response, manifest) }
	assertAbsent := func() {
		t.Helper()
		var count int
		if err := db.QueryRow(ctx, `SELECT (SELECT count(*) FROM artifacts WHERE artifact_id=$1)+(SELECT count(*) FROM job_checkpoints WHERE checkpoint_id=$2)`, ref.ArtifactId, cp.Meta.RecordId).Scan(&count); err != nil || count != 0 {
			t.Fatal("partial graph output survived", count, err)
		}
		var state int16
		var checkpoint *string
		if err := db.QueryRow(ctx, `SELECT state,latest_checkpoint_id FROM jobs WHERE job_id=$1`, job.JobID).Scan(&state, &checkpoint); err != nil || state != int16(pb.JobState_JOB_STATE_RUNNING) || checkpoint != nil {
			t.Fatal("failed commit changed job", state, checkpoint, err)
		}
	}
	// A late failure occurs after artifact/dependency/checkpoint writes; all must roll back.
	if _, err = db.Exec(ctx, `CREATE FUNCTION reject_graph_stage_fixture() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.state=4 THEN RAISE EXCEPTION 'injected graph stage failure'; END IF; RETURN NEW; END $$;
CREATE TRIGGER graph_stage_fixture BEFORE UPDATE ON jobs FOR EACH ROW EXECUTE FUNCTION reject_graph_stage_fixture()`); err != nil {
		t.Fatal(err)
	}
	if err = commit(); err == nil {
		t.Fatal("injected stage failure ignored")
	}
	assertAbsent()
	if _, err = db.Exec(ctx, `DROP TRIGGER graph_stage_fixture ON jobs; DROP FUNCTION reject_graph_stage_fixture()`); err != nil {
		t.Fatal(err)
	}
	// Source cancellation can arrive after the optimistic read while the commit
	// waits for the source row. It must still win before any durable output.
	cancelCtx, cancelWait := context.WithTimeout(ctx, 5*time.Second)
	defer cancelWait()
	sourceLock, err := db.Begin(cancelCtx)
	if err != nil {
		t.Fatal(err)
	}
	defer sourceLock.Rollback(ctx)
	if _, err = sourceLock.Exec(cancelCtx, `SELECT job_id FROM jobs WHERE job_id=$1 FOR UPDATE`, a.SourceJobID); err != nil {
		t.Fatal(err)
	}
	cancelResult := make(chan error, 1)
	go func() { cancelResult <- admitted.CommitGraphOutput(cancelCtx, pin, job, request, response, manifest) }()
	waited := false
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		if err = sourceLock.QueryRow(cancelCtx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE NOT granted AND pg_backend_pid()=ANY(pg_blocking_pids(pid)))`).Scan(&waited); err != nil {
			t.Fatal(err)
		}
		if waited {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !waited {
		t.Fatal("commit did not wait for source lock")
	}
	if _, err = sourceLock.Exec(cancelCtx, `UPDATE jobs SET cancellation_requested=true WHERE job_id=$1`, a.SourceJobID); err != nil {
		t.Fatal(err)
	}
	if err = sourceLock.Commit(cancelCtx); err != nil {
		t.Fatal(err)
	}
	if err = <-cancelResult; err == nil {
		t.Fatal("late source cancellation ignored")
	}
	assertAbsent()
	if _, err = db.Exec(ctx, `UPDATE jobs SET cancellation_requested=false WHERE job_id=$1`, a.SourceJobID); err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{job.JobID, a.SourceJobID} {
		if _, err = db.Exec(ctx, `UPDATE jobs SET cancellation_requested=true WHERE job_id=$1`, id); err != nil {
			t.Fatal(err)
		}
		if err = commit(); err == nil {
			t.Fatal("cancelled child/source output committed", id)
		}
		assertAbsent()
		if _, err = db.Exec(ctx, `UPDATE jobs SET cancellation_requested=false WHERE job_id=$1`, id); err != nil {
			t.Fatal(err)
		}
	}
	bad := proto.Clone(response).(*pb.ProcessBatchResponse)
	bad.Fence++
	if err = admitted.CommitGraphOutput(ctx, pin, job, request, bad, manifest); err == nil {
		t.Fatal("wrong response fence committed")
	}
	assertAbsent()
	// Wait behind the corpus lock, then invalidate the preflight stamp. A check
	// performed only before the transaction would incorrectly allow this commit.
	race, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	locked, err := db.Begin(race)
	if err != nil {
		t.Fatal(err)
	}
	defer locked.Rollback(ctx)
	if _, err = locked.Exec(race, `SELECT corpus_id FROM corpus_state WHERE corpus_id=$1 FOR UPDATE`, job.CorpusID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- admitted.CommitGraphOutput(race, pin, job, request, response, manifest) }()
	blocked := false
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		if err = locked.QueryRow(race, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE NOT granted AND pg_backend_pid()=ANY(pg_blocking_pids(pid)))`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("output commit did not wait for corpus lock")
	}
	if _, err = locked.Exec(race, `UPDATE corpus_state SET registry_revision=registry_revision+1 WHERE corpus_id=$1`, job.CorpusID); err != nil {
		t.Fatal(err)
	}
	if err = locked.Commit(race); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err == nil {
		t.Fatal("stale registry stamp committed after lock wait")
	}
	assertAbsent()
	admitted, err = repo.PrepareGraphJobAdmission(ctx, inventory, inputs, 4096, 32)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := admitted.GraphCheckpointCommitted(ctx, cp); err != nil || ok {
		t.Fatal("uncommitted checkpoint reconciled", ok, err)
	}
	if err = commit(); err != nil {
		t.Fatal("commit graph output", err)
	}
	if ok, err := admitted.GraphCheckpointCommitted(ctx, cp); err != nil || !ok {
		t.Fatal("lost acknowledgement not reconciled", ok, err)
	}
	wrong := proto.Clone(cp).(*pb.Checkpoint)
	wrong.Meta.RecordId += "-other"
	if ok, err := admitted.GraphCheckpointCommitted(ctx, wrong); err != nil || ok {
		t.Fatal("different checkpoint reconciled", ok, err)
	}
	if err = commit(); err == nil {
		t.Fatal("STAGED job admitted as live execution")
	}
	stored, err := repo.LoadArtifact(ctx, job.CorpusID, ref.ArtifactId)
	if err != nil || !proto.Equal(stored, ref) {
		t.Fatal("output registration mismatch", err)
	}
	var deps int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM artifact_dependencies WHERE artifact_id=$1 AND corpus_id=$2`, ref.ArtifactId, job.CorpusID).Scan(&deps); err != nil || deps != 1 {
		t.Fatal("dependency owner mapping failed", deps, err)
	}
	if manifest.ArtifactId != a.Plan.OutputArtifactId {
		t.Fatal("logical dependency owner mutated")
	}
}
