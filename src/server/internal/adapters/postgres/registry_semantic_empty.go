// Pins the registry revision for a complete EXTRACT batch with no mentions. The RESOLVE stage
// still emits an auditable terminal artifact, but never invents a proposal or increments the
// canonical registry. EXTRACT checkpoint and lease are verified in one transaction. Measure
// read/lock p95/p99; required targets remain REQUIRED_UNMEASURED.
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
	if r == nil || r.pool == nil || ctx == nil || source == nil || sourceRef == nil ||
		len(source.Mentions) != 0 || source.Completeness != pb.Completeness_COMPLETENESS_COMPLETE ||
		proof.JobID == "" || proof.OwnerID == "" || proof.Fence == 0 {
		return 0, errors.New("complete mention-free EXTRACT and claimed RESOLVE lease required")
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return 0, fmt.Errorf("begin empty RESOLVE read: %w", err)
	}
	defer tx.Rollback(ctx)
	var revision int64
	if err = tx.QueryRow(ctx, `SELECT registry_revision FROM corpus_state
		WHERE corpus_id=$1 FOR SHARE`, source.GetMeta().GetCorpusId()).Scan(&revision); err != nil {
		return 0, fmt.Errorf("read empty RESOLVE registry revision: %w", err)
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
		return 0, fmt.Errorf("commit empty RESOLVE revision read: %w", err)
	}
	return uint64(revision), nil
}
