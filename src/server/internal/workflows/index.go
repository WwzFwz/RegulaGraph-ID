// Drives inventory-owned INDEX jobs without coupling workflow to indexing or
// database implementations. The injected processor owns verified output commit;
// this layer bounds work by lease/deadline, polls cancellation and records retry
// or terminal failure. Successful return requires committed STAGED output, not
// publication. Measure queue/poll/attempt p95/p99 with benchmark-targets.yaml.
package workflows

import (
	"context"
	"errors"
	"time"

	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type IndexAttemptStore interface {
	ClaimIndexJob(context.Context, string, time.Duration) (domain.JobRecord, error)
	CancellationRequested(context.Context, string, string, uint64) (bool, error)
	CompleteWorkerAttempt(context.Context, string, string, uint64, pb.JobState, time.Duration) (pb.JobState, error)
}
type IndexJobProcessor interface {
	ProcessIndexJob(context.Context, domain.JobRecord) (*pb.ProcessBatchResponse, error)
}

// Alias preserves the existing INDEX configuration API.
type IndexExecutorConfig = BatchExecutorConfig
type IndexExecutor struct{ *inventoryExecutor }

func NewIndexExecutor(store IndexAttemptStore, processor IndexJobProcessor, cfg IndexExecutorConfig) (*IndexExecutor, error) {
	if store == nil || processor == nil {
		return nil, errors.New("INDEX store and processor required")
	}
	executor, err := newInventoryExecutor(store, store.ClaimIndexJob, processor.ProcessIndexJob, cfg, "INDEX")
	if err != nil {
		return nil, err
	}
	return &IndexExecutor{executor}, nil
}
