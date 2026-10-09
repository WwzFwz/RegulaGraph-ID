// Exercises catalog/intent atomicity and immutable replay on the actual native
// graph fixture. Publication is staged with both backend requirements but no
// readiness receipt is fabricated. Tests intentionally leave the active pointer
// at its parent; graph activation/carry-forward index admission remain separate.
package indexing

import (
	"context"
	"crypto/sha256"
	"encoding/json"
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

func checkGraphCatalogWrite(t *testing.T, ctx context.Context, repo *postgres.Repository, db *pgx.Conn, authority *postgres.GraphJobAdmission,
	pin domain.SnapshotPin, prepared *workflows.PreparedGraphOutputs, backend *neo4j.Store) neo4j.ReadyProof {
	t.Helper()
	b, err := DescribePreparedGraph(prepared, backend)
	if err != nil {
		t.Fatal(err)
	}
	var snapshotID, parentPublication string
	if err = db.QueryRow(ctx, `SELECT snapshot_id FROM snapshots WHERE publication_id=$1`, b.Binding.PublicationID).Scan(&snapshotID); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(ctx, `SELECT publication_id FROM snapshots WHERE snapshot_id=$1`, pin.SnapshotID).Scan(&parentPublication); err != nil {
		t.Fatal(err)
	}
	parent, err := repo.LoadPublicationManifest(ctx, parentPublication)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	target := proto.Clone(b.Binding.BaseSnapshot).(*pb.SnapshotRef)
	target.SnapshotId = snapshotID
	target.Sequence = b.Binding.Sequence
	target.ManifestHash = &pb.ContentHash{Sha256: fmt.Sprintf("%x", sha256.Sum256(raw))}
	manifest := &pb.PublicationManifest{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: pin.CorpusID, RecordId: b.Binding.PublicationID}, SnapshotRef: target, ParentRef: b.Binding.BaseSnapshot, Fence: b.Binding.Fence,
		BackendGenerations: []*pb.BackendGeneration{b.ExpectedBackend()}, ValidationReport: &pb.ValidationReport{Valid: true, CheckedRecords: b.Records + b.Edges}}
	for _, generation := range parent.BackendGenerations {
		if generation.Backend == pb.BackendKind_BACKEND_KIND_QDRANT {
			manifest.BackendGenerations = append(manifest.BackendGenerations, proto.Clone(generation).(*pb.BackendGeneration))
		}
	}
	if len(manifest.BackendGenerations) != 2 {
		t.Fatal("base Qdrant requirement absent")
	}
	if err = repo.StagePublication(ctx, manifest); err != nil {
		t.Fatal("stage both graph/index requirements", err)
	}
	// A late SQL failure must roll back the catalog as well as the operation.
	if _, err = db.Exec(ctx, `CREATE FUNCTION reject_graph_intent_fixture() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected graph intent failure'; END $$;
CREATE TRIGGER graph_intent_fixture BEFORE INSERT ON publication_operations FOR EACH ROW EXECUTE FUNCTION reject_graph_intent_fixture()`); err != nil {
		t.Fatal(err)
	}
	if err = authority.ReserveGraphGeneration(ctx, pin, prepared.Completed(), b); err == nil {
		t.Fatal("intent failure ignored")
	}
	var count int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM graph_generations WHERE publication_id=$1`, b.Binding.PublicationID).Scan(&count); err != nil || count != 0 {
		t.Fatal("catalog survived failed intent", count, err)
	}
	if _, err = db.Exec(ctx, `DROP TRIGGER graph_intent_fixture ON publication_operations; DROP FUNCTION reject_graph_intent_fixture()`); err != nil {
		t.Fatal(err)
	}
	interrupted := &interruptedGraphWrite{Store: backend, failAfterWrite: true}
	if failed, e := WritePreparedGraph(ctx, authority, interrupted, prepared, pin); !errors.Is(e, errGraphWriteLostAck) || failed != (neo4j.ReadyProof{}) {
		t.Fatal("lost graph write ack exposed proof", e)
	}
	var pending string
	if err = db.QueryRow(ctx, `SELECT status FROM publication_operations WHERE operation_key=$1`, postgres.GraphWriteOperation(b.Binding.PublicationID)).Scan(&pending); err != nil || pending != "planned" {
		t.Fatal("failed graph write lost recoverable intent", pending, err)
	}
	proof, err := WritePreparedGraph(ctx, authority, backend, prepared, pin)
	if err != nil {
		t.Fatal("write graph through durable catalog", err)
	}
	replay, err := WritePreparedGraph(ctx, authority, backend, prepared, pin)
	if err != nil || replay != proof {
		t.Fatal("graph catalog/write replay changed", err)
	}
	wrongProof := &interruptedGraphWrite{Store: backend, wrongProof: true}
	if bad, e := WritePreparedGraph(ctx, authority, wrongProof, prepared, pin); !errors.Is(e, domain.ErrPersistentIntegrity) || bad != (neo4j.ReadyProof{}) {
		t.Fatal("mismatched backend proof accepted", e)
	}
	stored, err := repo.LoadGraphGeneration(ctx, pin.CorpusID, b.Binding.PublicationID)
	if err != nil {
		t.Fatal(err)
	}
	storedRaw, _ := json.Marshal(stored)
	if string(storedRaw) != string(raw) {
		t.Fatal("graph catalog differs from prepared route")
	}
	changed := b
	changed.Database = "other_database"
	if err = authority.ReserveGraphGeneration(ctx, pin, prepared.Completed(), changed); err == nil {
		t.Fatal("route changed on replay")
	}
	changed = b
	changed.InventoryHash = fmt.Sprintf("%064x", 1)
	if err = authority.ReserveGraphGeneration(ctx, pin, prepared.Completed(), changed); err == nil {
		t.Fatal("foreign inventory accepted")
	}
	if _, err = db.Exec(ctx, `UPDATE graph_generations SET database_name='changed' WHERE publication_id=$1`, b.Binding.PublicationID); err == nil {
		t.Fatal("immutable catalog updated")
	}
	var operationStatus, active string
	if err = db.QueryRow(ctx, `SELECT status FROM publication_operations WHERE operation_key=$1`, postgres.GraphWriteOperation(b.Binding.PublicationID)).Scan(&operationStatus); err != nil || operationStatus != "planned" {
		t.Fatal("intent state changed without readiness receipt", operationStatus, err)
	}
	if err = repo.CommitPublication(ctx, b.Binding.PublicationID); err == nil {
		t.Fatal("graph write activated snapshot without receipts")
	}
	if err = db.QueryRow(ctx, `SELECT active_snapshot_id FROM corpus_state WHERE corpus_id=$1`, pin.CorpusID).Scan(&active); err != nil || active != pin.SnapshotID {
		t.Fatal("unready graph changed active snapshot", err)
	}
	checkGraphReadiness(t, ctx, repo, db, authority, pin, prepared, backend)
	return proof
}

var errGraphWriteLostAck = errors.New("injected lost acknowledgement after actual Neo4j commit")

type interruptedGraphWrite struct {
	*neo4j.Store
	failAfterWrite, wrongProof bool
}

func (b *interruptedGraphWrite) ApplyGraphDelta(ctx context.Context, delta *pb.GraphDelta) error {
	if err := b.Store.ApplyGraphDelta(ctx, delta); err != nil {
		return err
	}
	if b.failAfterWrite {
		return errGraphWriteLostAck
	}
	return nil
}

func (b *interruptedGraphWrite) VerifyAndSeal(ctx context.Context, deltas []*pb.GraphDelta) (neo4j.ReadyProof, error) {
	proof, err := b.Store.VerifyAndSeal(ctx, deltas)
	if err == nil && b.wrongProof {
		proof.Edges++
	}
	return proof, err
}
