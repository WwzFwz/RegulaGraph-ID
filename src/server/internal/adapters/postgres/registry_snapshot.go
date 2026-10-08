// Binds a publication to one retained registry revision before graph/query use.
// The coordinator supplies trusted corpus/publication/fence and an expected current
// revision; PostgreSQL serializes admission with publication and registry mutation.
// Replay preserves the original view even after the registry advances. This does
// not validate GraphDelta bytes or imply graph readiness. Measure bind lock wait,
// historical lookup p95/p99 and ledger growth against benchmark-targets.yaml;
// required performance remains UNMEASURED.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

// BindPublicationRegistry is a control-plane operation, never a query-time write.
// Existing published bindings can be replayed but cannot be introduced or changed.
func (r *Repository) BindPublicationRegistry(ctx context.Context, publication, corpus string, fence, expectedRevision uint64) error {
	if !storageIDPattern.MatchString(publication) || !storageIDPattern.MatchString(corpus) ||
		fence == 0 || fence > math.MaxInt64 || expectedRevision == 0 || expectedRevision > math.MaxInt64 {
		return errors.New("complete bounded registry publication binding required")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var storedCorpus string
	var storedFence int64
	var state int16
	err = tx.QueryRow(ctx, `SELECT corpus_id,fence,state FROM snapshots WHERE publication_id=$1 FOR UPDATE`, publication).
		Scan(&storedCorpus, &storedFence, &state)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if storedCorpus != corpus || uint64(storedFence) != fence {
		return ErrConflict
	}
	var boundCorpus string
	var boundFence, boundRevision int64
	err = tx.QueryRow(ctx, `SELECT corpus_id,fence,registry_revision FROM snapshot_registry_bindings WHERE publication_id=$1`, publication).
		Scan(&boundCorpus, &boundFence, &boundRevision)
	if err == nil {
		if boundCorpus != corpus || uint64(boundFence) != fence || uint64(boundRevision) != expectedRevision {
			return fmt.Errorf("registry publication binding changed: %w", ErrConflict)
		}
		return tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if state != int16(pb.SnapshotState_SNAPSHOT_STATE_STAGING) && state != int16(pb.SnapshotState_SNAPSHOT_STATE_VALIDATING) {
		return fmt.Errorf("registry binding requires an open publication: %w", ErrConflict)
	}
	// Match publication's snapshot-then-corpus lock order; registry writers do not
	// acquire snapshot locks. Both values must remain stable through the insert.
	var currentFence, currentRevision, floor int64
	err = tx.QueryRow(ctx, `SELECT publisher_fence,registry_revision,registry_history_floor FROM corpus_state WHERE corpus_id=$1 FOR UPDATE`, corpus).
		Scan(&currentFence, &currentRevision, &floor)
	if err != nil {
		return err
	}
	if uint64(currentFence) != fence || uint64(currentRevision) != expectedRevision || currentRevision < floor {
		return fmt.Errorf("registry or publisher changed: %w", ErrConflict)
	}
	_, err = tx.Exec(ctx, `INSERT INTO snapshot_registry_bindings(publication_id,corpus_id,fence,registry_revision) VALUES($1,$2,$3,$4)`,
		publication, corpus, int64(fence), int64(expectedRevision))
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
