// Extends real PostgreSQL/Qdrant source publication through graph inventory
// admission and atomic child creation. Source EXTRACT/RESOLVE remain synthetic,
// while registered bytes, decision-free source coverage and registry fences use
// production code. These tests do not dispatch Rust or publish Neo4j output.
package indexing

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/workflows"
)

func checkGraphJobInventory(t *testing.T, ctx context.Context, repo *postgres.Repository, db *pgx.Conn, dsn string, pin domain.SnapshotPin,
	prepared *workflows.PreparedGraphAssembly, binding domain.GraphSourceBinding, input domain.GraphSourceBindingInputs, viewBytes []byte) {
	t.Helper()
	inventory := domain.GraphJobInventory{Assignments: []domain.GraphJobAssignment{{JobID: "job:graph-child", SourceJobID: binding.Source.SourceJobID, Plan: proto.Clone(prepared.Plan).(*pb.GraphAssemblyPlan), Reference: proto.Clone(prepared.Reference).(*pb.ArtifactRef)}}}
	inputs := map[string]domain.GraphJobSourceInputs{binding.Source.SourceJobID: {Source: input, RegistryView: viewBytes}}
	// One pool connection proves preflight reads are not nested inside the
	// scheduling transaction. The helper receives this fixture's isolated DSN.
	single, err := postgres.Open(ctx, postgres.Config{DSN: dsn, MaxConnections: 1, HealthTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer single.Close()
	admitted, err := single.PrepareGraphJobAdmission(ctx, inventory, inputs, 4096, 32)
	if err != nil {
		t.Fatal("prepare graph inventory", err)
	}
	// Inject failure after inventory INSERT but before child creation. No partial
	// inventory may survive the transaction rollback; then retry the same proof.
	if _, err = db.Exec(ctx, `CREATE FUNCTION reject_graph_child_fixture() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected graph child failure'; END $$;
CREATE TRIGGER graph_child_fixture BEFORE INSERT ON jobs FOR EACH ROW EXECUTE FUNCTION reject_graph_child_fixture()`); err != nil {
		t.Fatal(err)
	}
	if err = admitted.Schedule(ctx, pin); err == nil {
		t.Fatal("child failure did not abort inventory")
	}
	var partial int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM graph_job_inventories WHERE publication_id=$1`, binding.PublicationID).Scan(&partial); err != nil || partial != 0 {
		t.Fatal("partial graph inventory survived child failure", partial, err)
	}
	if _, err = db.Exec(ctx, `DROP TRIGGER graph_child_fixture ON jobs; DROP FUNCTION reject_graph_child_fixture()`); err != nil {
		t.Fatal(err)
	}
	// The admission must own its plans, not caller pointers.
	inventory.Assignments[0].Plan.OutputArtifactId = "delta:caller-mutation"
	if err = admitted.Schedule(ctx, pin); err != nil {
		t.Fatal("schedule graph inventory", err)
	}
	if err = admitted.Schedule(ctx, pin); err != nil {
		t.Fatal("replay graph inventory", err)
	}
	if _, err = db.Exec(ctx, `UPDATE jobs SET cancellation_requested=true WHERE job_id=$1`, binding.Source.SourceJobID); err != nil {
		t.Fatal(err)
	}
	if err = admitted.Schedule(ctx, pin); err == nil {
		t.Fatal("cancelled source admitted on exact replay")
	}
	if _, err = db.Exec(ctx, `UPDATE jobs SET cancellation_requested=false WHERE job_id=$1`, binding.Source.SourceJobID); err != nil {
		t.Fatal(err)
	}
	stored, err := repo.LoadGraphJobInventory(ctx, pin.CorpusID, binding.PublicationID)
	if err != nil || len(stored.Assignments) != 1 || !proto.Equal(stored.Assignments[0].Plan, prepared.Plan) {
		t.Fatalf("graph inventory changed: %v", err)
	}
	var count int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM graph_job_assignments WHERE publication_id=$1`, binding.PublicationID).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate or missing graph child", count, err)
	}
	if _, err = repo.ClaimJob(ctx, "owner:generic-graph-test", time.Minute); !errors.Is(err, domain.ErrLeaseUnavailable) {
		t.Fatalf("generic claimant stole graph inventory child: %v", err)
	}
	corrupt := inputs[binding.Source.SourceJobID]
	corrupt.RegistryView = append([]byte(nil), viewBytes...)
	corrupt.RegistryView[0] ^= 1
	bad := map[string]domain.GraphJobSourceInputs{binding.Source.SourceJobID: corrupt}
	if _, err = repo.PrepareGraphJobAdmission(ctx, stored, bad, 4096, 32); err == nil {
		t.Fatal("corrupt canonical view admitted")
	}
	var revision uint64
	if err = db.QueryRow(ctx, `SELECT registry_revision FROM corpus_state WHERE corpus_id=$1`, pin.CorpusID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	_, _, err = repo.ResolveCanonicalIdentities(ctx, pin.CorpusID, "identity:graph-job-race", revision, []domain.CanonicalIdentityClaim{{ProposalKey: "issuer:graph-job-unrelated", EntityType: domain.CanonicalEntityTypeOrganization, IdentityScope: domain.IssuerIdentityKeyNamespace, IdentityKey: strings.Repeat("d", 64), PayloadHash: strings.Repeat("e", 64)}})
	if err != nil {
		t.Fatal(err)
	}
	if err = admitted.Schedule(ctx, pin); !errors.Is(err, postgres.ErrConflict) {
		t.Fatalf("stale preflight stamp admitted: %v", err)
	}
	fresh, err := single.PrepareGraphJobAdmission(ctx, stored, inputs, 4096, 32)
	if err != nil {
		t.Fatal("fresh preflight after unrelated registry change", err)
	}
	if err = fresh.Schedule(ctx, pin); err != nil {
		t.Fatal("exact replay with freshly checked context", err)
	}
	// Prove the optimistic stamp is rechecked after acquiring a contended lock,
	// not merely before waiting. This direct revision bump is a synthetic race;
	// the production allocator mutation above covers the normal writer path.
	raceCtx, raceCancel := context.WithTimeout(ctx, 5*time.Second)
	defer raceCancel()
	locked, err := db.Begin(raceCtx)
	if err != nil {
		t.Fatal(err)
	}
	defer locked.Rollback(ctx)
	if _, err = locked.Exec(raceCtx, `SELECT corpus_id FROM corpus_state WHERE corpus_id=$1 FOR UPDATE`, pin.CorpusID); err != nil {
		t.Fatal(err)
	}
	scheduled := make(chan error, 1)
	go func() { scheduled <- fresh.Schedule(raceCtx, pin) }()
	blocked := false
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		if err = locked.QueryRow(raceCtx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE NOT granted AND pg_backend_pid()=ANY(pg_blocking_pids(pid)))`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("graph scheduling did not wait for corpus lock")
	}
	if _, err = locked.Exec(raceCtx, `UPDATE corpus_state SET registry_revision=registry_revision+1 WHERE corpus_id=$1`, pin.CorpusID); err != nil {
		t.Fatal(err)
	}
	if err = locked.Commit(raceCtx); err != nil {
		t.Fatal(err)
	}
	if err = <-scheduled; !errors.Is(err, postgres.ErrConflict) {
		t.Fatalf("registry movement while waiting for lock admitted: %v", err)
	}
	claimed, err := repo.ClaimGraphJob(ctx, "owner:graph-worker", time.Minute)
	if err != nil || claimed.JobID != "job:graph-child" || claimed.Stage != pb.JobStage_JOB_STAGE_ASSEMBLE || claimed.LeaseFence != 1 {
		t.Fatalf("graph child claim failed: %+v %v", claimed, err)
	}
	if _, err = repo.ClaimGraphJob(ctx, "owner:graph-second", time.Minute); !errors.Is(err, domain.ErrLeaseUnavailable) {
		t.Fatalf("live graph claim duplicated: %v", err)
	}
	if _, err = db.Exec(ctx, `UPDATE jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE job_id=$1`, claimed.JobID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `UPDATE jobs SET cancellation_requested=true WHERE job_id=$1`, binding.Source.SourceJobID); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.ClaimGraphJob(ctx, "owner:graph-retry", time.Minute); !errors.Is(err, domain.ErrLeaseUnavailable) {
		t.Fatalf("cancelled graph source reclaimed: %v", err)
	}
	if _, err = db.Exec(ctx, `UPDATE jobs SET cancellation_requested=false WHERE job_id=$1`, binding.Source.SourceJobID); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := repo.ClaimGraphJob(ctx, "owner:graph-retry", time.Minute)
	if err != nil || reclaimed.LeaseFence <= claimed.LeaseFence || reclaimed.Attempt <= claimed.Attempt {
		t.Fatalf("graph crash recovery did not advance fence/attempt: %+v %v", reclaimed, err)
	}
	checkGraphDispatchAuthority(t, ctx, single, db, pin, stored, inputs, reclaimed, claimed, fresh)
	if _, err = db.Exec(ctx, `UPDATE graph_job_inventories SET fence=fence WHERE publication_id=$1`, binding.PublicationID); err == nil {
		t.Fatal("graph inventory mutable")
	}
}
