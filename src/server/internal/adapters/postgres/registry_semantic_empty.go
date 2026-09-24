// Pins the registry revision under the claimed RESOLVE lease and EXTRACT checkpoint.
// Empty EXTRACT uses the same fenced read to emit an auditable output without registry writes;
// candidate preparation uses it around a batched alias lookup to detect revision changes.
// Measure read/lock p95/p99; required targets remain REQUIRED_UNMEASURED.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func (r *Repository) ReadEmptyResolveRevision(ctx context.Context,
	proof domain.SemanticJobFence, sourceRef *pb.ArtifactRef,
	source *pb.ExtractionBatch) (uint64, error) {
	if source == nil || len(source.Mentions) != 0 {
		return 0, errors.New("mention-free EXTRACT required")
	}
	return r.ReadFencedResolveRevision(ctx, proof, sourceRef, source)
}

// ReadFencedResolveRevision checks the same EXTRACT checkpoint and live lease used by
// registry CAS. Candidate preparation calls it around the registry snapshot read.
func (r *Repository) ReadFencedResolveRevision(ctx context.Context,
	proof domain.SemanticJobFence, sourceRef *pb.ArtifactRef,
	source *pb.ExtractionBatch) (uint64, error) {
	if r == nil || r.pool == nil || ctx == nil || source == nil || sourceRef == nil ||
		source.Completeness != pb.Completeness_COMPLETENESS_COMPLETE ||
		proof.JobID == "" || proof.OwnerID == "" || proof.Fence == 0 {
		return 0, errors.New("complete EXTRACT and claimed RESOLVE lease required")
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return 0, fmt.Errorf("begin fenced RESOLVE read: %w", err)
	}
	defer tx.Rollback(ctx)
	var revision int64
	if err = tx.QueryRow(ctx, `SELECT registry_revision FROM corpus_state
		WHERE corpus_id=$1 FOR SHARE`, source.GetMeta().GetCorpusId()).Scan(&revision); err != nil {
		return 0, fmt.Errorf("read fenced RESOLVE registry revision: %w", err)
	}
	if revision <= 0 {
		return 0, fmt.Errorf("invalid registry revision: %w", domain.ErrPersistentIntegrity)
	}
	if err = verifySemanticJobFence(ctx, tx, proof, source.Meta.CorpusId, sourceRef, source); err != nil {
		return 0, err
	}
	if err = verifySemanticLeaseStillLive(ctx, tx, proof); err != nil {
		return 0, err
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit fenced RESOLVE revision read: %w", err)
	}
	return uint64(revision), nil
}
