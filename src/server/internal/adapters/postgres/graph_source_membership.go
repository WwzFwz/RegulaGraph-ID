// Proves that an initial CHUNK snapshot envelope belongs to a published index inventory.
// ASSEMBLE preparation uses the exact original-to-bound receipt under a live base-snapshot
// lease. A registered binding alone is insufficient: the published, receipted generation
// must contain an assignment for this source job and bound artifact. This read does not
// authorize graph output, refresh registry decisions, or keep a lease alive after return.
// Inventory reads are bounded by the existing 256-plan/64MiB contract; measure read/lease
// p95/p99 and RSS under benchmark-targets.yaml before required performance acceptance.
package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

// VerifyPublishedGraphSourceBinding checks historical membership. The supplied
// binding fence belongs to the published base, not the new graph publication.
// Pure envelope validation must also authenticate the corresponding artifact bytes.
func (r *Repository) VerifyPublishedGraphSourceBinding(ctx context.Context, pin domain.SnapshotPin, binding domain.IndexSourceBinding) error {
	if err := validateIndexPin(pin); err != nil {
		return err
	}
	if err := domain.ValidateIndexSourceBinding(binding); err != nil {
		return err
	}
	if binding.Snapshot.CorpusId != pin.CorpusID || binding.Snapshot.SnapshotId != pin.SnapshotID || binding.Snapshot.Sequence != pin.Sequence {
		return ErrConflict
	}
	bounded, cancel := context.WithDeadline(ctx, pin.ExpiresAt)
	defer cancel()
	tx, err := r.pool.BeginTx(bounded, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer tx.Rollback(bounded)
	if err = verifyPublishedGraphSourceBinding(bounded, tx, pin, binding); err != nil {
		return err
	}
	return tx.Commit(bounded)
}

// Writers can compose membership with their own publication/source locks and
// must repeat the live lease check immediately before committing their receipt.
func verifyPublishedGraphSourceBinding(bounded context.Context, tx pgx.Tx, pin domain.SnapshotPin, binding domain.IndexSourceBinding) error {
	index, err := readPinnedIndex(bounded, tx, pin)
	if err != nil {
		return err
	}
	if index.Binding.PublicationID != binding.PublicationID || index.Binding.Fence != binding.Fence || !proto.Equal(index.Snapshot, binding.Snapshot) {
		return ErrConflict
	}
	inventory, err := loadIndexJobInventory(bounded, tx, binding.PublicationID)
	if err != nil {
		return err
	}
	if !proto.Equal(inventory.Snapshot, binding.Snapshot) || inventory.AuthScope != binding.AuthScope || !equalIndexBinding(inventory.Binding, index.Binding) {
		return ErrConflict
	}
	found := false
	for _, assignment := range inventory.Assignments {
		if assignment.SourceJobID == binding.SourceJobID && proto.Equal(assignment.Plan.DocumentBatch, binding.Bound) {
			found = true
		}
	}
	if !found {
		return ErrNotFound
	}
	var snapshotRaw, originalRaw, boundRaw []byte
	var hash, scope string
	var fence int64
	err = tx.QueryRow(bounded, `SELECT s.snapshot_payload,s.snapshot_hash,s.auth_scope,s.fence,b.original_reference,b.bound_reference
 FROM index_source_snapshots s JOIN index_source_bindings b ON b.publication_id=s.publication_id
 WHERE s.publication_id=$1 AND s.corpus_id=$2 AND b.source_job_id=$3
 AND b.original_artifact_id=$4 AND b.bound_artifact_id=$5
 AND octet_length(s.snapshot_payload)<=65536 AND octet_length(b.original_reference)<=65536 AND octet_length(b.bound_reference)<=65536`,
		binding.PublicationID, pin.CorpusID, binding.SourceJobID, binding.Original.ArtifactId, binding.Bound.ArtifactId).
		Scan(&snapshotRaw, &hash, &scope, &fence, &originalRaw, &boundRaw)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if scope != binding.AuthScope || uint64(fence) != binding.Fence || hash != fmt.Sprintf("%x", sha256.Sum256(snapshotRaw)) {
		return domain.ErrPersistentIntegrity
	}
	snapshot, original, bound := new(pb.SnapshotRef), new(pb.ArtifactRef), new(pb.ArtifactRef)
	for _, pair := range []struct {
		raw     []byte
		message proto.Message
	}{{snapshotRaw, snapshot}, {originalRaw, original}, {boundRaw, bound}} {
		if err = domain.DecodeWire(pair.raw, pair.message, domain.DefaultWireLimits); err != nil {
			return domain.ErrPersistentIntegrity
		}
	}
	if !proto.Equal(snapshot, binding.Snapshot) || !proto.Equal(original, binding.Original) || !proto.Equal(bound, binding.Bound) {
		return ErrConflict
	}
	for _, ref := range []*pb.ArtifactRef{original, bound} {
		registered, e := loadArtifact(bounded, tx, pin.CorpusID, ref.ArtifactId)
		if e != nil {
			return e
		}
		if !proto.Equal(registered, ref) {
			return domain.ErrPersistentIntegrity
		}
	}
	if err = checkIndexLease(bounded, tx, pin); err != nil {
		return err
	}
	return nil
}
