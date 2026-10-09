// Restores the complete source selection of a published initial index under an
// owned lease. Inventory membership, registered references and immutable source
// receipts must agree; missing sources never become a silently smaller graph.
// Reads are capped by the existing 256-plan inventory and 64KiB receipt limits.
// This is discovery, not ASSEMBLE authority: admission rechecks cancellation,
// resolution freshness and publication fencing. Measure SQL/bytes/p95 against
// configs/benchmark-targets.yaml; required performance remains unmeasured.
package postgres

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func (r *Repository) LoadPublishedGraphSources(ctx context.Context, pin domain.SnapshotPin, scope string) ([]domain.IndexSourceBinding, error) {
	if err := validateIndexPin(pin); err != nil {
		return nil, err
	}
	if !storageIDPattern.MatchString(scope) {
		return nil, ErrConflict
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
	// Updating an already graph-published base needs incremental membership
	// semantics; do not reinterpret an inherited index as a new initial source.
	if !proto.Equal(index.EvidenceSnapshot(), index.Snapshot) {
		return nil, ErrConflict
	}
	in, err := loadIndexJobInventory(ctx, tx, index.Binding.PublicationID)
	if err != nil {
		return nil, err
	}
	if in.AuthScope != scope || !proto.Equal(in.Snapshot, index.Snapshot) || !equalIndexBinding(in.Binding, index.Binding) {
		return nil, ErrConflict
	}
	wanted := map[string]*pb.ArtifactRef{}
	for _, a := range in.Assignments {
		wanted[a.SourceJobID] = a.Plan.DocumentBatch
	}
	jobs := make([]string, 0, len(wanted))
	for job := range wanted {
		jobs = append(jobs, job)
	}
	sort.Strings(jobs)
	var snapshotRaw []byte
	var hash, storedScope string
	var fence int64
	err = tx.QueryRow(ctx, `SELECT snapshot_payload,snapshot_hash,auth_scope,fence FROM index_source_snapshots
 WHERE publication_id=$1 AND corpus_id=$2 AND octet_length(snapshot_payload)<=65536`, in.Binding.PublicationID, pin.CorpusID).
		Scan(&snapshotRaw, &hash, &storedScope, &fence)
	if err != nil {
		return nil, err
	}
	snapshot := new(pb.SnapshotRef)
	if hash != fmt.Sprintf("%x", sha256.Sum256(snapshotRaw)) || domain.DecodeWire(snapshotRaw, snapshot, domain.DefaultWireLimits) != nil ||
		!proto.Equal(snapshot, index.Snapshot) || storedScope != scope || uint64(fence) != index.Binding.Fence {
		return nil, domain.ErrPersistentIntegrity
	}
	rows, err := tx.Query(ctx, `SELECT source_job_id,original_artifact_id,bound_artifact_id,
 CASE WHEN octet_length(original_reference)<=65536 THEN original_reference ELSE NULL END,
 CASE WHEN octet_length(bound_reference)<=65536 THEN bound_reference ELSE NULL END
 FROM index_source_bindings WHERE publication_id=$1 AND source_job_id=ANY($2) ORDER BY source_job_id LIMIT 257`, in.Binding.PublicationID, jobs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.IndexSourceBinding
	for rows.Next() {
		var job, originalID, boundID string
		var originalRaw, boundRaw []byte
		if err = rows.Scan(&job, &originalID, &boundID, &originalRaw, &boundRaw); err != nil {
			return nil, err
		}
		if len(result) >= 256 || len(originalRaw) > 65536 || len(boundRaw) > 65536 {
			return nil, domain.ErrPersistentIntegrity
		}
		original, bound := new(pb.ArtifactRef), new(pb.ArtifactRef)
		if domain.DecodeWire(originalRaw, original, domain.DefaultWireLimits) != nil || domain.DecodeWire(boundRaw, bound, domain.DefaultWireLimits) != nil ||
			original.ArtifactId != originalID || bound.ArtifactId != boundID {
			return nil, domain.ErrPersistentIntegrity
		}
		// Extra prepared receipts may exist without INDEX membership. They are
		// not graph sources; exact coverage is checked against the inventory.
		if wanted[job] == nil {
			continue
		}
		if !proto.Equal(wanted[job], bound) {
			return nil, domain.ErrPersistentIntegrity
		}
		binding := domain.IndexSourceBinding{PublicationID: in.Binding.PublicationID, Fence: uint64(fence), SourceJobID: job,
			Snapshot: proto.Clone(snapshot).(*pb.SnapshotRef), AuthScope: scope, Original: original, Bound: bound}
		if err = domain.ValidateIndexSourceBinding(binding); err != nil {
			return nil, err
		}
		result = append(result, binding)
		delete(wanted, job)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	rows.Close() // release the connection before reference queries (pool size 1).
	if len(wanted) != 0 || len(result) == 0 {
		return nil, domain.ErrPersistentIntegrity
	}
	for _, source := range result {
		for _, ref := range []*pb.ArtifactRef{source.Original, source.Bound} {
			registered, e := loadArtifact(ctx, tx, pin.CorpusID, ref.ArtifactId)
			if e != nil {
				return nil, e
			}
			if !proto.Equal(registered, ref) {
				return nil, domain.ErrPersistentIntegrity
			}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].SourceJobID < result[j].SourceJobID })
	if err = checkIndexLease(ctx, tx, pin); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}
