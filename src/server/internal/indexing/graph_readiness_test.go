// Uses the native graph fixture to verify atomic receipt/intent/authority commit,
// acknowledgement recovery, stale source rejection and unchanged active snapshot.
// Qdrant carry-forward is not fabricated here. These are backend correctness tests,
// not graph quality, latency acceptance or a complete Hybrid GraphRAG publication.
package indexing

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/neo4j"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/workflows"
)

func checkGraphReadiness(t *testing.T, ctx context.Context, repo *postgres.Repository, db *pgx.Conn, authority *postgres.GraphJobAdmission, pin domain.SnapshotPin, prepared *workflows.PreparedGraphOutputs, backend *neo4j.Store) {
	t.Helper()
	publication := backend.Binding().PublicationID
	if _, err := db.Exec(ctx, `CREATE FUNCTION reject_graph_ready_fixture() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected readiness failure'; END $$;
 CREATE TRIGGER graph_ready_fixture BEFORE INSERT ON graph_readiness_authority FOR EACH ROW EXECUTE FUNCTION reject_graph_ready_fixture()`); err != nil {
		t.Fatal(err)
	}
	if receipt, err := AcknowledgePreparedGraph(ctx, authority, backend, prepared, pin); err == nil || receipt != nil {
		t.Fatal("receipt SQL failure exposed success", err)
	}
	var count int
	var status string
	if err := db.QueryRow(ctx, `SELECT count(*) FROM backend_receipts WHERE publication_id=$1`, publication).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial receipt persisted", count, err)
	}
	if err := db.QueryRow(ctx, `SELECT status FROM publication_operations WHERE operation_key=$1`, postgres.GraphWriteOperation(publication)).Scan(&status); err != nil || status != "planned" {
		t.Fatal("intent not rolled back", status, err)
	}
	if _, err := db.Exec(ctx, `DROP TRIGGER graph_ready_fixture ON graph_readiness_authority; DROP FUNCTION reject_graph_ready_fixture()`); err != nil {
		t.Fatal(err)
	}
	lost := &lostGraphReadinessAck{GraphJobAdmission: authority}
	if receipt, err := AcknowledgePreparedGraph(ctx, lost, backend, prepared, pin); !errors.Is(err, errLostGraphReadinessAck) || receipt != nil {
		t.Fatal("lost receipt ack exposed success", err)
	}
	receipt, err := AcknowledgePreparedGraph(ctx, authority, backend, prepared, pin)
	if err != nil {
		t.Fatal("recover committed graph readiness", err)
	}
	replay, err := AcknowledgePreparedGraph(ctx, authority, backend, prepared, pin)
	if err != nil || !proto.Equal(receipt, replay) {
		t.Fatal("receipt replay changed", err)
	}
	checkGraphReadinessOutputSwap(t, ctx, repo, db, authority, pin, prepared, backend, receipt)
	if err = db.QueryRow(ctx, `SELECT status FROM publication_operations WHERE operation_key=$1`, postgres.GraphWriteOperation(publication)).Scan(&status); err != nil || status != "applied" {
		t.Fatal("intent not acknowledged", status, err)
	}
	if err = db.QueryRow(ctx, `SELECT count(*) FROM graph_readiness_authority WHERE publication_id=$1`, publication).Scan(&count); err != nil || count != 1 {
		t.Fatal("authority missing", count, err)
	}
	completed := prepared.Completed()
	child := completed.Inventory.Assignments[0].JobID
	if _, err = db.Exec(ctx, `UPDATE jobs SET cancellation_requested=true WHERE job_id=$1`, child); err != nil {
		t.Fatal(err)
	}
	if bad, e := AcknowledgePreparedGraph(ctx, authority, backend, prepared, pin); e == nil || bad != nil {
		t.Fatal("cancelled child renewed readiness")
	}
	if _, err = db.Exec(ctx, `UPDATE jobs SET cancellation_requested=false WHERE job_id=$1`, child); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `UPDATE corpus_state SET registry_revision=registry_revision+1 WHERE corpus_id=$1`, pin.CorpusID); err != nil {
		t.Fatal(err)
	}
	if bad, e := AcknowledgePreparedGraph(ctx, authority, backend, prepared, pin); e == nil || bad != nil {
		t.Fatal("stale registry admission renewed readiness")
	}
	if _, err = db.Exec(ctx, `UPDATE corpus_state SET registry_revision=registry_revision-1 WHERE corpus_id=$1`, pin.CorpusID); err != nil {
		t.Fatal(err)
	}
	if err = repo.CommitPublication(ctx, publication); err == nil {
		t.Fatal("graph receipt bypassed missing Qdrant carry-forward receipt")
	}
	active, _, err := repo.ActiveSnapshot(ctx, pin.CorpusID)
	if err != nil || active != pin.SnapshotID {
		t.Fatal("graph receipt activated snapshot", active, err)
	}
	checkGraphActivationAuthority(t, ctx, repo, db, authority, pin, prepared, backend)
}

// Inject a different, internally valid current checkpoint/output without changing
// the sealed catalog. This is storage fault injection, not a second model run.
func checkGraphReadinessOutputSwap(t *testing.T, ctx context.Context, repo *postgres.Repository, db *pgx.Conn, authority *postgres.GraphJobAdmission, pin domain.SnapshotPin, prepared *workflows.PreparedGraphOutputs, backend *neo4j.Store, receipt *pb.BackendReceipt) {
	t.Helper()
	b, err := DescribePreparedGraph(prepared, backend)
	if err != nil {
		t.Fatal(err)
	}
	replacement := prepared.Completed()
	originalID := replacement.Checkpoints[0].Meta.RecordId
	ref := replacement.Outputs[0]
	ref.ArtifactId += ":replacement"
	ref.StorageKey += ".replacement"
	ref.ContentHash = &pb.ContentHash{Sha256: fmt.Sprintf("%x", sha256.Sum256([]byte(ref.ArtifactId)))}
	cp := replacement.Checkpoints[0]
	cp.Meta.RecordId += ":replacement"
	cp.CompletedBatchKeys[0] = ref.ArtifactId
	cp.ArtifactHashes[0] = proto.Clone(ref.ContentHash).(*pb.ContentHash)
	if err = domain.ValidateCompletedGraphInventory(replacement); err != nil {
		t.Fatal("invalid replacement fixture", err)
	}
	if err = repo.RegisterArtifact(ctx, pin.CorpusID, ref); err != nil {
		t.Fatal(err)
	}
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(cp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO job_checkpoints(checkpoint_id,job_id,stage,fence,payload,payload_hash,terminal_status) VALUES($1,$2,$3,$4,$5,$6,$7)`, cp.Meta.RecordId, cp.JobId, int16(cp.Stage), int64(cp.Fence), raw, fmt.Sprintf("%x", sha256.Sum256(raw)), int16(cp.TerminalStatus)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `UPDATE jobs SET latest_checkpoint_id=$2 WHERE job_id=$1`, cp.JobId, cp.Meta.RecordId); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, e := db.Exec(ctx, `UPDATE jobs SET latest_checkpoint_id=$2 WHERE job_id=$1`, cp.JobId, originalID); e != nil {
			t.Error(e)
		}
		if _, e := db.Exec(ctx, `DELETE FROM job_checkpoints WHERE checkpoint_id=$1`, cp.Meta.RecordId); e != nil {
			t.Error(e)
		}
	}()
	current, err := authority.ReadCompletedGraph(ctx, pin)
	if err != nil || !proto.Equal(current.Outputs[0], ref) {
		t.Fatal("replacement is not current admitted output", err)
	}
	if err = authority.RecordGraphReadiness(ctx, pin, replacement, b, receipt); !errors.Is(err, postgres.ErrConflict) {
		t.Fatal("old graph receipt accepted different current output", err)
	}
}

var errLostGraphReadinessAck = errors.New("injected lost graph receipt acknowledgement")

type lostGraphReadinessAck struct{ *postgres.GraphJobAdmission }

func (a *lostGraphReadinessAck) RecordGraphReadiness(ctx context.Context, pin domain.SnapshotPin, c domain.CompletedGraphInventory, b domain.GraphCatalogBinding, r *pb.BackendReceipt) error {
	if err := a.GraphJobAdmission.RecordGraphReadiness(ctx, pin, c, b, r); err != nil {
		return err
	}
	return errLostGraphReadinessAck
}
