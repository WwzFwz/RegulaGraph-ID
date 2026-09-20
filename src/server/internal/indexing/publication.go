// Mengoordinasikan perubahan indeks berdasarkan chunk yang ditambah, berubah, atau dihapus.
//
// Peran dalam komponen:
// Menyediakan operasi indexing untuk workflow update yang dapat dipulihkan. Go menerima
// batch terstruktur dari worker Rust lalu mengoordinasikan adapter storage dan snapshot;
// transformasi embedding/BM25 berada di worker dan runtime model.
//
// Kontrak integrasi dan perhatian implementasi:
// Kesiapan indeks dicatat sebelum publikasi snapshot; jangan menganggap dua upsert ke
// layanan berbeda sebagai satu transaksi. Store memiliki durable state, sedangkan lapisan
// ini mengikat manifest ke reservation sequence, parent, dan fence, memvalidasi receipt,
// lalu meminta compare-and-swap pointer aktif.
//
// Benchmark dan gate penerimaan:
// [INDEX] Ukur konsistensi ID/versi antarindeks, freshness lag, throughput indexing, ukuran
// indeks, dan retrieval Recall@k. Gate: penghapusan/upsert idempotent dan model/dimensi
// representasi cocok.
// [UPDATE] Gate: update identik idempotent, dependensi terdampak diinvalidasi, bukti bersama
// tetap tersedia, dan crash/retry tidak membuka snapshot parsial. Backend writes dibatch
// dan diakui satu kali per generation.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: state machine S01 reserve, stage, acknowledge, publish, dan abort aktif melalui
// PublicationStore. Backend mutation serta retire/GC penuh dilanjutkan pada X01/U01/O01.
// Bukti verifikasi: receipt stale/search-unready ditolak dan VerifyPublicationReady harus
// berhasil sebelum commit; ikuti doc/verification.md.
package indexing

import (
	"context"
	"errors"
	"fmt"

	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type PublicationStore interface {
	ReservePublication(context.Context, string, string, string, string, string) (domain.PublicationReservation, error)
	StagePublication(context.Context, *pb.PublicationManifest) error
	RecordBackendReceipt(context.Context, *pb.BackendReceipt) error
	LoadPublicationManifest(context.Context, string) (*pb.PublicationManifest, error)
	LoadSnapshotRef(context.Context, string) (*pb.SnapshotRef, error)
	CommitPublication(context.Context, string) error
	AbortPublication(context.Context, string) error
}

type PublicationCoordinator struct {
	store PublicationStore
}

func NewPublicationCoordinator(store PublicationStore) (*PublicationCoordinator, error) {
	if store == nil {
		return nil, errors.New("publication store is required")
	}
	return &PublicationCoordinator{store: store}, nil
}

func (c *PublicationCoordinator) Reserve(ctx context.Context, publicationID, jobID, corpusID, snapshotID, parentSnapshotID string) (domain.PublicationReservation, error) {
	return c.store.ReservePublication(ctx, publicationID, jobID, corpusID, snapshotID, parentSnapshotID)
}

func (c *PublicationCoordinator) Stage(ctx context.Context, reservation domain.PublicationReservation, manifest *pb.PublicationManifest) error {
	if manifest == nil || manifest.Meta == nil || manifest.SnapshotRef == nil {
		return errors.New("complete publication manifest required")
	}
	parentID := ""
	if manifest.ParentRef != nil {
		parentID = manifest.ParentRef.SnapshotId
	}
	if manifest.Meta.RecordId != reservation.PublicationID || manifest.Meta.CorpusId != reservation.CorpusID ||
		manifest.SnapshotRef.SnapshotId != reservation.SnapshotID || manifest.SnapshotRef.Sequence != reservation.Sequence ||
		manifest.Fence != reservation.Fence || parentID != reservation.ParentSnapshotID {
		return errors.New("publication manifest does not match its reservation")
	}
	if err := domain.ValidateWire(manifest, domain.DefaultWireLimits); err != nil {
		return fmt.Errorf("validate publication manifest: %w", err)
	}
	return c.store.StagePublication(ctx, manifest)
}

func (c *PublicationCoordinator) Acknowledge(ctx context.Context, receipt *pb.BackendReceipt) error {
	return c.store.RecordBackendReceipt(ctx, receipt)
}

func (c *PublicationCoordinator) Publish(ctx context.Context, publicationID string) error {
	manifest, err := c.store.LoadPublicationManifest(ctx, publicationID)
	if err != nil {
		return err
	}
	var parent *pb.SnapshotRef
	if manifest.ParentRef != nil {
		parent, err = c.store.LoadSnapshotRef(ctx, manifest.ParentRef.SnapshotId)
		if err != nil {
			return err
		}
	}
	if err = domain.VerifyPublicationReady(manifest, parent, manifest.Fence); err != nil {
		return fmt.Errorf("publication pre-commit verification failed: %w", err)
	}
	return c.store.CommitPublication(ctx, publicationID)
}

func (c *PublicationCoordinator) Abort(ctx context.Context, publicationID string) error {
	return c.store.AbortPublication(ctx, publicationID)
}
