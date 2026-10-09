// Verifies completed graph inventory collection on the existing real PostgreSQL
// source fixture, including a one-connection pool. Output metadata is synthetic;
// byte/source projection is verified separately with Rust output. Mutations are
// scoped to disposable jobs and restored; rejection must never return partial data.
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

func checkCompletedGraphResults(t *testing.T, ctx context.Context, db *pgx.Conn, admitted *postgres.GraphJobAdmission,
	pin domain.SnapshotPin, a domain.GraphJobAssignment, ref *pb.ArtifactRef) {
	t.Helper()
	got, err := admitted.ReadCompletedGraph(ctx, pin)
	if err != nil || len(got.Outputs) != 1 || !proto.Equal(got.Outputs[0], ref) || got.Inventory.Assignments[0].JobID != a.JobID {
		t.Fatal("complete graph inventory rejected", err)
	}
	got.Inventory.Assignments[0].Plan.OutputArtifactId = "delta:mutated"
	got.Outputs[0].ArtifactId = "artifact:mutated"
	again, err := admitted.ReadCompletedGraph(ctx, pin)
	if err != nil || !proto.Equal(again.Outputs[0], ref) || !proto.Equal(again.Inventory.Assignments[0].Plan, a.Plan) {
		t.Fatal("caller changed graph admission", err)
	}
	for _, c := range []struct {
		name, change, restore, job string
		pending                    bool
	}{
		{"cancelled child", `UPDATE jobs SET cancellation_requested=true WHERE job_id=$1`, `UPDATE jobs SET cancellation_requested=false WHERE job_id=$1`, a.JobID, true},
		{"unfinished child", `UPDATE jobs SET state=1 WHERE job_id=$1`, `UPDATE jobs SET state=4 WHERE job_id=$1`, a.JobID, true},
		{"cancelled source", `UPDATE jobs SET cancellation_requested=true WHERE job_id=$1`, `UPDATE jobs SET cancellation_requested=false WHERE job_id=$1`, a.SourceJobID, false},
		{"checkpoint fence mismatch", `UPDATE jobs SET lease_fence=lease_fence+1 WHERE job_id=$1`, `UPDATE jobs SET lease_fence=lease_fence-1 WHERE job_id=$1`, a.JobID, false},
		{"source fence mismatch", `UPDATE jobs SET lease_fence=lease_fence+1 WHERE job_id=$1`, `UPDATE jobs SET lease_fence=lease_fence-1 WHERE job_id=$1`, a.SourceJobID, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, e := db.Exec(ctx, c.change, c.job); e != nil {
				t.Fatal(e)
			}
			defer func() {
				if _, e := db.Exec(ctx, c.restore, c.job); e != nil {
					t.Error(e)
				}
			}()
			out, e := admitted.ReadCompletedGraph(ctx, pin)
			if e == nil || len(out.Outputs) != 0 || len(out.Inventory.Assignments) != 0 || c.pending && !errors.Is(e, postgres.ErrGraphOutputsPending) {
				t.Fatal("invalid child/source exposed partial graph results", e)
			}
		})
	}
	if _, err = db.Exec(ctx, `UPDATE corpus_state SET registry_revision=registry_revision+1 WHERE corpus_id=$1`, pin.CorpusID); err != nil {
		t.Fatal(err)
	}
	if out, e := admitted.ReadCompletedGraph(ctx, pin); !errors.Is(e, postgres.ErrConflict) || len(out.Outputs) != 0 {
		t.Fatal("stale registry admission returned results", e)
	}
	if _, err = db.Exec(ctx, `UPDATE corpus_state SET registry_revision=registry_revision-1 WHERE corpus_id=$1`, pin.CorpusID); err != nil {
		t.Fatal(err)
	}
	wrong := pin
	wrong.OwnerID += ":other"
	if out, e := admitted.ReadCompletedGraph(ctx, wrong); e == nil || len(out.Outputs) != 0 {
		t.Fatal("foreign pin returned results", e)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if out, e := admitted.ReadCompletedGraph(cancelled, pin); !errors.Is(e, context.Canceled) || len(out.Outputs) != 0 {
		t.Fatal("cancelled collection returned results", e)
	}
	if _, err = admitted.ReadCompletedGraph(ctx, pin); err != nil {
		t.Fatal("collection failed after rejected cases", err)
	}
	// Make collection wait before it can inspect jobs, then commit cancellation.
	// The result must reflect post-wait authority, not an optimistic earlier read.
	race, done := context.WithTimeout(ctx, 5*time.Second)
	defer done()
	lock, err := db.Begin(race)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback(ctx)
	if _, err = lock.Exec(race, `SELECT corpus_id FROM corpus_state WHERE corpus_id=$1 FOR UPDATE`, pin.CorpusID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, e := admitted.ReadCompletedGraph(race, pin); result <- e }()
	waiting := false
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		if err = lock.QueryRow(race, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE NOT granted AND pg_backend_pid()=ANY(pg_blocking_pids(pid)))`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !waiting {
		t.Fatal("collector did not wait for authority lock")
	}
	if _, err = lock.Exec(race, `UPDATE jobs SET cancellation_requested=true WHERE job_id=$1`, a.JobID); err != nil {
		t.Fatal(err)
	}
	if err = lock.Commit(race); err != nil {
		t.Fatal(err)
	}
	if e := <-result; !errors.Is(e, postgres.ErrGraphOutputsPending) {
		t.Fatal("collection ignored cancellation during lock wait", e)
	}
	if _, err = db.Exec(ctx, `UPDATE jobs SET cancellation_requested=false WHERE job_id=$1`, a.JobID); err != nil {
		t.Fatal(err)
	}
}
