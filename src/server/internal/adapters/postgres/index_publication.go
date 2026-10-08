// Rechecks durable INDEX inventory authority at snapshot activation. Holding
// source/child row locks through pointer commit prevents cancellation from
// crossing the final check. NOWAIT avoids a lock inversion with checkpoint
// writers, which acquire job rows before publication rows; contention is a
// recoverable not-ready result. Existing publications without an INDEX inventory
// continue to use their existing backend protocol. Measure contention/commit p95
// under configs/benchmark-targets.yaml; this does not certify model quality.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func verifyIndexPublicationJobs(ctx context.Context, tx pgx.Tx, publication string) error {
	var expected int
	err := tx.QueryRow(ctx, `SELECT job_count FROM index_job_inventories WHERE publication_id=$1`, publication).Scan(&expected)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT j.cancellation_requested,j.state,
	 EXISTS(SELECT 1 FROM index_job_assignments a WHERE a.publication_id=$1 AND a.job_id=j.job_id),
	 EXISTS(SELECT 1 FROM job_checkpoints c WHERE c.checkpoint_id=j.latest_checkpoint_id AND c.job_id=j.job_id AND c.fence=j.lease_fence AND c.stage=$2 AND c.terminal_status=$3)
	 FROM jobs j WHERE j.job_id IN (
	 SELECT job_id FROM index_job_assignments WHERE publication_id=$1
	 UNION SELECT source_job_id FROM index_job_assignments WHERE publication_id=$1)
	 ORDER BY j.job_id FOR SHARE OF j NOWAIT`, publication, int16(pb.JobStage_JOB_STAGE_INDEX), int16(pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED))
	if err != nil {
		return indexPublicationLockError(err)
	}
	defer rows.Close()
	children := 0
	for rows.Next() {
		var cancelled, child, checkpoint bool
		var state int16
		if err = rows.Scan(&cancelled, &state, &child, &checkpoint); err != nil {
			return err
		}
		if child {
			children++
		}
		if cancelled || child && (state != int16(pb.JobState_JOB_STATE_STAGED) || !checkpoint) {
			return ErrPublicationNotReady
		}
	}
	if err = rows.Err(); err != nil {
		return indexPublicationLockError(err)
	}
	if expected == 0 || children != expected {
		return ErrPublicationNotReady
	}
	return nil
}

func indexPublicationLockError(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "55P03" {
		return fmt.Errorf("INDEX jobs are changing: %w", ErrPublicationNotReady)
	}
	return err
}
