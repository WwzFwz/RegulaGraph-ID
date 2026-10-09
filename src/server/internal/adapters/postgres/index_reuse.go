// Acknowledges reuse of a fully read-back published index under graph source
// authority. The immutable mapping and target Qdrant receipt commit together.
// Ownership stays with the original index publication; no vector is reinserted.
// Final CAS verifies source index jobs and mapping again. Reads authenticate the
// mapping/hash and original published manifest, never accept a generation ID
// alone as provenance. Measure receipt/read/page p95 and RSS under required gates.
package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func (a *GraphJobAdmission) RecordIndexReuse(ctx context.Context, pin domain.SnapshotPin, b domain.IndexReuseBinding) error {
	if err := domain.ValidateIndexReuseBinding(b); err != nil {
		return err
	}
	if a == nil || len(a.inventory.Assignments) == 0 {
		return ErrConflict
	}
	p := a.inventory.Assignments[0].Plan
	if b.PublicationID != p.PublicationId || b.Fence != p.PublicationFence || b.Target.Sequence != p.TargetSequence || !proto.Equal(b.Parent, p.Context.SnapshotRef) {
		return ErrConflict
	}
	raw, err := json.Marshal(b)
	if err != nil {
		return err
	}
	if len(raw) > domain.DefaultWireLimits.MaxBytes {
		return ErrResultLimit
	}
	_, err = a.withCompletedGraph(ctx, pin, func(ctx context.Context, tx pgx.Tx, _ domain.CompletedGraphInventory) error {
		base, e := readPinnedIndex(ctx, tx, pin)
		if e != nil {
			return e
		}
		if !equalIndexBinding(base.Binding, b.Index) || !proto.Equal(base.Snapshot, b.Parent) || !proto.Equal(base.EvidenceSnapshot(), b.Source) {
			return ErrConflict
		}
		var payload []byte
		if e = tx.QueryRow(ctx, `SELECT manifest_payload FROM snapshots WHERE publication_id=$1`, b.PublicationID).Scan(&payload); e != nil {
			return e
		}
		manifest, e := unmarshalManifest(payload)
		if e != nil {
			return e
		}
		if e = validateIndexReuseManifest(b, manifest); e != nil {
			return e
		}
		if e = verifyIndexReuseOrigin(ctx, tx, b); e != nil {
			return e
		}
		if e = verifyIndexPublicationJobs(ctx, tx, b.Index.PublicationID); e != nil {
			return e
		}
		_, e = tx.Exec(ctx, `INSERT INTO snapshot_index_reuse(publication_id,corpus_id,source_publication_id,payload,payload_hash)
 VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, b.PublicationID, b.Target.CorpusId, b.Index.PublicationID, raw, fmt.Sprintf("%x", sha256.Sum256(raw)))
		if e != nil {
			return e
		}
		saved, e := loadIndexReuse(ctx, tx, b.PublicationID)
		if e != nil {
			return e
		}
		actual, e := json.Marshal(saved)
		if e != nil {
			return e
		}
		if !bytes.Equal(raw, actual) {
			return ErrConflict
		}
		return recordBackendReceiptTx(ctx, tx, &pb.BackendReceipt{PublicationId: b.PublicationID, Fence: b.Fence, Backend: b.Expected.Backend, Generation: b.Expected.Generation, OperationsChecksum: b.Expected.OperationsChecksum, Counts: b.Expected.ExpectedCounts, DurableAck: true, SearchReady: true})
	})
	return err
}

func loadIndexReuse(ctx context.Context, tx pgx.Tx, publication string) (domain.IndexReuseBinding, error) {
	var b domain.IndexReuseBinding
	var raw []byte
	var hash, corpus, source string
	err := tx.QueryRow(ctx, `SELECT CASE WHEN octet_length(payload)<=16777216 THEN payload ELSE NULL END,payload_hash,corpus_id,source_publication_id FROM snapshot_index_reuse WHERE publication_id=$1`, publication).Scan(&raw, &hash, &corpus, &source)
	if errors.Is(err, pgx.ErrNoRows) {
		return b, ErrPublicationNotReady
	}
	if err != nil {
		return b, err
	}
	if len(raw) == 0 || fmt.Sprintf("%x", sha256.Sum256(raw)) != hash || json.Unmarshal(raw, &b) != nil || domain.ValidateIndexReuseBinding(b) != nil {
		return domain.IndexReuseBinding{}, domain.ErrPersistentIntegrity
	}
	canonical, e := json.Marshal(b)
	if e != nil || !bytes.Equal(raw, canonical) || b.PublicationID != publication || b.Target.CorpusId != corpus || b.Index.PublicationID != source {
		return domain.IndexReuseBinding{}, domain.ErrPersistentIntegrity
	}
	return b, nil
}

func validateIndexReuseManifest(b domain.IndexReuseBinding, m *pb.PublicationManifest) error {
	if m == nil || m.Meta.RecordId != b.PublicationID || m.Meta.CorpusId != b.Target.CorpusId || m.Fence != b.Fence || !proto.Equal(m.ParentRef, b.Parent) || !proto.Equal(m.SnapshotRef, b.Target) || len(m.Closures) != 0 {
		return ErrConflict
	}
	for _, expected := range m.BackendGenerations {
		if expected.Backend == pb.BackendKind_BACKEND_KIND_QDRANT && proto.Equal(expected, b.Expected) {
			return nil
		}
	}
	return ErrConflict
}

func verifyIndexReuseOrigin(ctx context.Context, tx pgx.Tx, b domain.IndexReuseBinding) error {
	stored, err := loadIndexBinding(ctx, tx, b.Target.CorpusId, b.Expected.Generation)
	if err != nil {
		return err
	}
	if !equalIndexBinding(stored, b.Index) {
		return ErrConflict
	}
	var raw, receiptRaw []byte
	err = tx.QueryRow(ctx, `SELECT s.manifest_payload,r.payload FROM snapshots s JOIN backend_receipts r USING(publication_id)
 WHERE s.publication_id=$1 AND s.corpus_id=$2 AND s.state=$3 AND r.backend=$4
 AND octet_length(s.manifest_payload)<=16777216 AND octet_length(r.payload)<=65536`, b.Index.PublicationID, b.Target.CorpusId, int16(pb.SnapshotState_SNAPSHOT_STATE_PUBLISHED), int16(pb.BackendKind_BACKEND_KIND_QDRANT)).Scan(&raw, &receiptRaw)
	if err != nil {
		return err
	}
	m, receipt := new(pb.PublicationManifest), new(pb.BackendReceipt)
	if domain.DecodeWire(raw, m, domain.DefaultWireLimits) != nil || domain.DecodeWire(receiptRaw, receipt, domain.DefaultWireLimits) != nil {
		return domain.ErrPersistentIntegrity
	}
	if m.Meta.RecordId != b.Index.PublicationID || m.Meta.CorpusId != b.Target.CorpusId || m.Fence != b.Index.Fence || !proto.Equal(m.SnapshotRef, b.Source) || m.ParentRef != nil || len(m.Closures) != 0 ||
		receipt.PublicationId != b.Index.PublicationID || receipt.Backend != b.Expected.Backend || receipt.Fence != b.Index.Fence || receipt.Generation != b.Expected.Generation || !proto.Equal(receipt.OperationsChecksum, b.Expected.OperationsChecksum) || !proto.Equal(receipt.Counts, b.Expected.ExpectedCounts) || !receipt.DurableAck || !receipt.SearchReady {
		return ErrConflict
	}
	for _, generation := range m.BackendGenerations {
		if proto.Equal(generation, b.Expected) {
			return nil
		}
	}
	return ErrConflict
}

func verifyIndexReusePublication(ctx context.Context, tx pgx.Tx, manifest *pb.PublicationManifest) error {
	// Initial writers retain their original protocol. A child snapshot with an
	// inherited generation requires this explicit mapping, never a copied receipt.
	if manifest.ParentRef == nil {
		return nil
	}
	var graph bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM graph_job_inventories WHERE publication_id=$1)`, manifest.Meta.RecordId).Scan(&graph); err != nil {
		return err
	}
	if !graph {
		return nil
	}
	b, err := loadIndexReuse(ctx, tx, manifest.Meta.RecordId)
	if err != nil {
		return err
	}
	if err = validateIndexReuseManifest(b, manifest); err != nil {
		return err
	}
	if err = verifyIndexReuseOrigin(ctx, tx, b); err != nil {
		return err
	}
	return verifyIndexPublicationJobs(ctx, tx, b.Index.PublicationID)
}
