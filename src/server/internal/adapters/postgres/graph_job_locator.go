// Resolves a live ASSEMBLE claim to its immutable publication inventory. This is
// a bounded locator read, not dispatch authority: admission and output commit
// still verify source/registry/pin under their own boundaries. Measure lookup/pool
// latency with configs/benchmark-targets.yaml before claiming production capacity.
package postgres

import (
	"context"
	"errors"
	"math"

	"github.com/jackc/pgx/v5"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func (r *Repository) GraphJobPublication(ctx context.Context, job domain.JobRecord) (string, error) {
	if job.Stage != pb.JobStage_JOB_STAGE_ASSEMBLE || job.State != pb.JobState_JOB_STATE_RUNNING || job.LeaseFence == 0 || job.LeaseFence > math.MaxInt64 || job.Attempt == 0 {
		return "", ErrStaleFence
	}
	var publication string
	err := r.pool.QueryRow(ctx, `SELECT a.publication_id FROM graph_job_assignments a
	 JOIN graph_job_inventories i ON i.publication_id=a.publication_id JOIN jobs j ON j.job_id=a.job_id
	 WHERE j.job_id=$1 AND j.corpus_id=$2 AND i.corpus_id=$2 AND j.lease_owner=$3 AND j.lease_fence=$4
	 AND j.attempt=$5 AND j.stage=$6 AND j.state=$7 AND NOT j.cancellation_requested
	 AND j.lease_expires_at>clock_timestamp() AND j.lease_expires_at>=$8`, job.JobID, job.CorpusID, job.LeaseOwner, int64(job.LeaseFence), int64(job.Attempt),
		int16(pb.JobStage_JOB_STAGE_ASSEMBLE), int16(pb.JobState_JOB_STATE_RUNNING), job.LeaseExpiresAt).Scan(&publication)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrStaleFence
	}
	return publication, err
}
