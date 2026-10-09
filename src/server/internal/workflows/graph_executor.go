// Drives inventory-owned ASSEMBLE claims through the shared batch lifecycle.
// The processor authenticates inputs and commits STAGED; this wrapper supplies
// only graph claim/processor ports. Cancellation and retry are durable, and no
// success here claims Neo4j publication. Benchmark queue/RPC/recovery p95/p99 and
// resource isolation against configs/benchmark-targets.yaml before acceptance.
package workflows

import (
	"context"
	"errors"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
	"time"
)

type GraphAttemptStore interface {
	inventoryAttemptStore
	ClaimGraphJobInScope(context.Context, string, time.Duration, string) (domain.JobRecord, error)
}
type GraphJobProcessing interface {
	ProcessGraphJob(context.Context, domain.JobRecord) (*pb.ProcessBatchResponse, error)
}
type GraphExecutorConfig = BatchExecutorConfig
type GraphExecutor struct{ *inventoryExecutor }

func NewGraphExecutor(store GraphAttemptStore, processor GraphJobProcessing, cfg GraphExecutorConfig) (*GraphExecutor, error) {
	if store == nil || processor == nil || cfg.AuthScope == "" {
		return nil, errors.New("ASSEMBLE store and processor required")
	}
	claim := func(ctx context.Context, owner string, lease time.Duration) (domain.JobRecord, error) {
		return store.ClaimGraphJobInScope(ctx, owner, lease, cfg.AuthScope)
	}
	executor, err := newInventoryExecutor(store, claim, processor.ProcessGraphJob, cfg, "ASSEMBLE")
	if err != nil {
		return nil, err
	}
	return &GraphExecutor{executor}, nil
}
