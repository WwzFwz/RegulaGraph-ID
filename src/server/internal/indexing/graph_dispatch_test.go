// Extends the published-source integration fixture to live ASSEMBLE dispatch
// authorization and worker request construction. Database fences, cancellation,
// registry stamps and snapshot pins are real; no worker output is synthesized as
// successful execution. Dispatch/commit and Neo4j need their own integration run.
package indexing

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/domain"
)

func checkGraphDispatchAuthority(t *testing.T, ctx context.Context, repo *postgres.Repository, db *pgx.Conn,
	pin domain.SnapshotPin, inventory domain.GraphJobInventory, inputs map[string]domain.GraphJobSourceInputs,
	job, oldClaim domain.JobRecord, stale *postgres.GraphJobAdmission) {
	t.Helper()
	if _, err := stale.AuthorizeGraphDispatch(ctx, pin, job); err == nil {
		t.Fatal("stale registry preflight authorized dispatch")
	}
	fresh, err := repo.PrepareGraphJobAdmission(ctx, inventory, inputs, 4096, 32)
	if err != nil {
		t.Fatal(err)
	}
	a, err := fresh.AuthorizeGraphDispatch(ctx, pin, job)
	if err != nil {
		t.Fatal("authorize live graph job", err)
	}
	call := proto.Clone(a.Plan.Context).(*pb.RequestContext)
	call.Deadline = timestamppb.New(time.Now().Add(time.Minute))
	request, err := domain.BuildGraphAssemblyRequest(a, job, call)
	if err != nil || len(request.Sources) != 4 || !proto.Equal(request.GraphAssemblyPlan, a.Reference) {
		t.Fatal("construct admitted graph request", err)
	}
	a.Plan.OutputArtifactId = "delta:caller-mutated"
	again, err := fresh.AuthorizeGraphDispatch(ctx, pin, job)
	if err != nil || !proto.Equal(again.Plan, inventory.Assignments[0].Plan) {
		t.Fatal("dispatch exposes admitted plan mutation", err)
	}
	for name, mutate := range map[string]func(*domain.JobRecord){
		"previous claim":     func(j *domain.JobRecord) { *j = oldClaim },
		"owner":              func(j *domain.JobRecord) { j.LeaseOwner = "owner:wrong" },
		"attempt":            func(j *domain.JobRecord) { j.Attempt++ },
		"corpus":             func(j *domain.JobRecord) { j.CorpusID = "corpus:wrong" },
		"invented extension": func(j *domain.JobRecord) { j.LeaseExpiresAt = j.LeaseExpiresAt.Add(time.Hour) },
	} {
		j := job
		mutate(&j)
		if _, err := fresh.AuthorizeGraphDispatch(ctx, pin, j); err == nil {
			t.Fatal("invalid graph claim authorized", name)
		}
	}
	for _, id := range []string{job.JobID, inventory.Assignments[0].SourceJobID} {
		if _, err = db.Exec(ctx, `UPDATE jobs SET cancellation_requested=true WHERE job_id=$1`, id); err != nil {
			t.Fatal(err)
		}
		if _, err = fresh.AuthorizeGraphDispatch(ctx, pin, job); err == nil {
			t.Fatal("cancelled child/source dispatched", id)
		}
		if _, err = db.Exec(ctx, `UPDATE jobs SET cancellation_requested=false WHERE job_id=$1`, id); err != nil {
			t.Fatal(err)
		}
	}
	wrongPin := pin
	wrongPin.OwnerID = "owner:foreign"
	if _, err = fresh.AuthorizeGraphDispatch(ctx, wrongPin, job); err == nil {
		t.Fatal("foreign reader pin authorized")
	}
	if _, err = db.Exec(ctx, `UPDATE corpus_state SET publisher_fence=publisher_fence+1 WHERE corpus_id=$1`, job.CorpusID); err != nil {
		t.Fatal(err)
	}
	if _, err = fresh.AuthorizeGraphDispatch(ctx, pin, job); err == nil {
		t.Fatal("obsolete publisher authorized dispatch")
	}
	if _, err = db.Exec(ctx, `UPDATE corpus_state SET publisher_fence=publisher_fence-1 WHERE corpus_id=$1`, job.CorpusID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `UPDATE snapshot_read_leases SET expires_at=clock_timestamp()-interval '1 second' WHERE lease_id=$1`, pin.LeaseID); err != nil {
		t.Fatal(err)
	}
	if _, err = fresh.AuthorizeGraphDispatch(ctx, pin, job); err == nil {
		t.Fatal("expired stored reader lease authorized dispatch")
	}
	if _, err = db.Exec(ctx, `UPDATE snapshot_read_leases SET expires_at=$2 WHERE lease_id=$1`, pin.LeaseID, pin.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `UPDATE jobs SET latest_checkpoint_id=NULL WHERE job_id=$1`, again.SourceJobID); err != nil {
		t.Fatal(err)
	}
	if _, err = fresh.AuthorizeGraphDispatch(ctx, pin, job); err == nil {
		t.Fatal("missing latest source checkpoint authorized dispatch")
	}
	if _, err = db.Exec(ctx, `UPDATE jobs SET latest_checkpoint_id=$2 WHERE job_id=$1`, again.SourceJobID, again.Plan.SourceCheckpointId); err != nil {
		t.Fatal(err)
	}
	// A real heartbeat may extend the stored expiry without invalidating the
	// old claim's shorter dispatch deadline. An invented longer expiry is rejected.
	if _, err = db.Exec(ctx, `UPDATE jobs SET lease_expires_at=lease_expires_at+interval '30 seconds' WHERE job_id=$1`, job.JobID); err != nil {
		t.Fatal(err)
	}
	if _, err = fresh.AuthorizeGraphDispatch(ctx, pin, job); err != nil {
		t.Fatal("heartbeat extension invalidated bounded dispatch", err)
	}
}
