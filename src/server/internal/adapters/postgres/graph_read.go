// Admits published graph reads using a live snapshot pin, exact Neo4j receipt,
// immutable route, original index authorization scope and retained registry view.
// Initial index-only snapshots cannot produce a graph capability. No active
// pointer substitution, model call or backend mutation occurs. A returned view
// is not permanent authority: callers bound work by the pin deadline and recheck
// before returning evidence. Measure cold/warm admission p95/p99 under required gates.
package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func (r *Repository) LoadPinnedGraph(ctx context.Context, pin domain.SnapshotPin, authScope string) (*domain.PinnedGraph, error) {
	if err := validateIndexPin(pin); err != nil {
		return nil, err
	}
	if !storageIDPattern.MatchString(authScope) {
		return nil, errors.New("trusted graph authorization scope required")
	}
	ctx, cancel := context.WithDeadline(ctx, pin.ExpiresAt)
	defer cancel()
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	index, err := readPinnedIndex(ctx, tx, pin)
	if err != nil {
		return nil, err
	}
	var publication, parent, scope, operationStatus string
	var fence, revision, floor, current int64
	var receiptRaw []byte
	if err = tx.QueryRow(ctx, `SELECT publication_id FROM snapshots WHERE snapshot_id=$1 AND corpus_id=$2`, pin.SnapshotID, pin.CorpusID).Scan(&publication); err != nil {
		return nil, err
	}
	err = tx.QueryRow(ctx, `SELECT s.publication_id,s.parent_snapshot_id,s.fence,v.registry_revision,c.registry_history_floor,c.registry_revision,
 src.auth_scope,CASE WHEN octet_length(b.payload)<=65536 THEN b.payload ELSE NULL END,o.status
 FROM snapshots s JOIN snapshot_registry_bindings v ON v.publication_id=s.publication_id AND v.corpus_id=s.corpus_id AND v.fence=s.fence
 JOIN corpus_state c ON c.corpus_id=s.corpus_id
 JOIN index_source_snapshots src ON src.publication_id=$3 AND src.corpus_id=s.corpus_id
 JOIN backend_receipts b ON b.publication_id=s.publication_id AND b.backend=$4
 JOIN publication_operations o ON o.publication_id=s.publication_id AND o.backend=$4 AND o.fence=s.fence AND o.operation_key=$5
 WHERE s.snapshot_id=$1 AND s.corpus_id=$2`, pin.SnapshotID, pin.CorpusID, index.Binding.PublicationID, int16(pb.BackendKind_BACKEND_KIND_NEO4J),
		GraphWriteOperation(publication)).Scan(&publication, &parent, &fence, &revision, &floor, &current, &scope, &receiptRaw, &operationStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if scope != authScope {
		return nil, ErrConflict
	}
	if revision < floor || revision > current || operationStatus != "applied" {
		return nil, ErrPublicationNotReady
	}
	catalog, err := loadGraphCatalog(ctx, tx, pin.CorpusID, publication)
	if err != nil {
		return nil, err
	}
	base, err := loadSnapshotRefTx(ctx, tx, parent)
	if err != nil {
		return nil, err
	}
	b := catalog.Binding
	if b.Sequence != pin.Sequence || b.Fence != uint64(fence) || b.RegistryRevision != uint64(revision) || !proto.Equal(b.BaseSnapshot, base) {
		return nil, domain.ErrPersistentIntegrity
	}
	receipt := new(pb.BackendReceipt)
	if len(receiptRaw) == 0 || domain.DecodeWire(receiptRaw, receipt, domain.DefaultWireLimits) != nil {
		return nil, domain.ErrPersistentIntegrity
	}
	expected := catalog.ExpectedBackend()
	if receipt.PublicationId != publication || receipt.Fence != b.Fence || receipt.Backend != expected.Backend || receipt.Generation != expected.Generation || !proto.Equal(receipt.OperationsChecksum, expected.OperationsChecksum) || !proto.Equal(receipt.Counts, expected.ExpectedCounts) || !receipt.DurableAck || !receipt.SearchReady {
		return nil, domain.ErrPersistentIntegrity
	}
	if err = checkIndexLease(ctx, tx, pin); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &domain.PinnedGraph{Pin: pin, Snapshot: index.Snapshot, Catalog: catalog, AuthScope: scope}, nil
}
