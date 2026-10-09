// Persists sealed-graph readiness, applied intent and the current source authority
// atomically. Caller is the trusted generation writer after exact Neo4j readback;
// a supplied receipt alone is not a substitute for that remote verification.
// Complete output/source/registry admission is repeated while locks are held;
// final publication repeats the recorded authority fingerprint. Replay may refresh
// authority after fresh admission but cannot change catalog or receipt bytes.
// Benchmark receipt/lock p95 and recovery under configs/benchmark-targets.yaml.
package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func (admission *GraphJobAdmission) RecordGraphReadiness(ctx context.Context, pin domain.SnapshotPin, expected domain.CompletedGraphInventory,
	binding domain.GraphCatalogBinding, receipt *pb.BackendReceipt) error {
	if err := domain.ValidateCompletedGraphInventory(expected); err != nil {
		return err
	}
	if err := domain.ValidateGraphCatalogBinding(binding); err != nil {
		return err
	}
	if receipt == nil {
		return ErrPublicationNotReady
	}
	if err := domain.ValidateWire(receipt, domain.DefaultWireLimits); err != nil {
		return err
	}
	b := binding.Binding
	if admission == nil || admission.repository == nil || binding.InventoryHash != admission.digest {
		return ErrConflict
	}
	first := expected.Inventory.Assignments[0].Plan
	if first.PublicationId != b.PublicationID || first.Meta.CorpusId != b.CorpusID || first.PublicationFence != b.Fence || first.TargetSequence != b.Sequence || first.RegistryRevision != b.RegistryRevision || !proto.Equal(first.Context.SnapshotRef, b.BaseSnapshot) {
		return ErrConflict
	}
	if len(binding.Outputs) != len(expected.Outputs) {
		return ErrConflict
	}
	for i, ref := range binding.Outputs {
		if !proto.Equal(ref, expected.Outputs[i]) {
			return ErrConflict
		}
	}
	want := binding.ExpectedBackend()
	if receipt.PublicationId != b.PublicationID || receipt.Backend != want.Backend || receipt.Fence != b.Fence || receipt.Generation != want.Generation ||
		!proto.Equal(receipt.OperationsChecksum, want.OperationsChecksum) || !proto.Equal(receipt.Counts, want.ExpectedCounts) || !receipt.DurableAck || !receipt.SearchReady {
		return ErrConflict
	}
	prepared, err := json.Marshal(expected)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(binding)
	if err != nil {
		return err
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	_, err = admission.withCompletedGraph(ctx, pin, func(ctx context.Context, tx pgx.Tx, current domain.CompletedGraphInventory) error {
		actual, e := json.Marshal(current)
		if e != nil {
			return e
		}
		if !bytes.Equal(prepared, actual) {
			return ErrConflict
		}
		catalog, e := loadGraphCatalog(ctx, tx, b.CorpusID, b.PublicationID)
		if e != nil {
			return e
		}
		stored, e := json.Marshal(catalog)
		if e != nil {
			return e
		}
		if !bytes.Equal(raw, stored) {
			return ErrConflict
		}
		jobsHash, e := graphPublicationJobsHash(ctx, tx, b.PublicationID, len(current.Inventory.Assignments))
		if e != nil {
			return e
		}
		tag, e := tx.Exec(ctx, `UPDATE publication_operations SET status='applied',updated_at=clock_timestamp()
 WHERE operation_key=$1 AND publication_id=$2 AND backend=$3 AND payload_hash=$4 AND fence=$5 AND status IN ('planned','applied')`,
			GraphWriteOperation(b.PublicationID), b.PublicationID, int16(receipt.Backend), binding.OperationsHash, int64(b.Fence))
		if e != nil {
			return e
		}
		if tag.RowsAffected() != 1 {
			return ErrConflict
		}
		if e = recordBackendReceiptTx(ctx, tx, receipt); e != nil {
			return e
		}
		_, e = tx.Exec(ctx, `INSERT INTO graph_readiness_authority(publication_id,catalog_hash,registry_revision,history_floor,jobs_hash)
 VALUES($1,$2,$3,$4,$5) ON CONFLICT(publication_id) DO UPDATE SET registry_revision=EXCLUDED.registry_revision,
 history_floor=EXCLUDED.history_floor,jobs_hash=EXCLUDED.jobs_hash,updated_at=clock_timestamp()
 WHERE graph_readiness_authority.catalog_hash=EXCLUDED.catalog_hash`, b.PublicationID, digest, admission.revision, admission.historyFloor, jobsHash)
		if e != nil {
			return e
		}
		return verifyGraphPublication(ctx, tx, b.PublicationID)
	})
	return err
}
