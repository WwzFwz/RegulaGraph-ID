// Owns deadline, cancellation polling and bounded retry for inventory-owned batch
// jobs. Stage wrappers inject claim/processor ports; successful processors must
// have committed STAGED before returning. A poll observing lease release cannot
// overturn durable success. No stage-specific source algorithm lives here. Measure
// queue/attempt/poll p95/p99 and cancellation lag under benchmark-targets.yaml.
package workflows

import (
	"context"
	"errors"
	"fmt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
	"time"
)

type inventoryAttemptStore interface {
	CancellationRequested(context.Context, string, string, uint64) (bool, error)
	CompleteWorkerAttempt(context.Context, string, string, uint64, pb.JobState, time.Duration) (pb.JobState, error)
}
type BatchExecutorConfig struct {
	AuthScope                                                 string
	OwnerID                                                   string
	Lease, CallTimeout, CancellationPoll, RetryBase, RetryMax time.Duration
}
type inventoryExecutor struct {
	store     inventoryAttemptStore
	processor func(context.Context, domain.JobRecord) (*pb.ProcessBatchResponse, error)
	config    BatchExecutorConfig
	claim     func(context.Context, string, time.Duration) (domain.JobRecord, error)
	stage     string
}

func newInventoryExecutor(store inventoryAttemptStore, claim func(context.Context, string, time.Duration) (domain.JobRecord, error), processor func(context.Context, domain.JobRecord) (*pb.ProcessBatchResponse, error), cfg BatchExecutorConfig, stage string) (*inventoryExecutor, error) {
	if store == nil || claim == nil || processor == nil || cfg.OwnerID == "" || cfg.Lease <= 0 || cfg.CallTimeout <= 0 || cfg.CancellationPoll <= 0 || cfg.RetryBase <= 0 || cfg.RetryMax < cfg.RetryBase {
		return nil, errors.New("inventory executor dependencies and positive time budgets required")
	}
	return &inventoryExecutor{store: store, processor: processor, config: cfg, claim: claim, stage: stage}, nil
}

func (e *inventoryExecutor) RunOnce(ctx context.Context) (domain.JobRecord, *pb.ProcessBatchResponse, error) {
	job, err := e.claim(ctx, e.config.OwnerID, e.config.Lease)
	if err != nil {
		return job, nil, err
	}
	deadline := time.Now().Add(e.config.CallTimeout)
	if job.LeaseExpiresAt.Before(deadline) {
		deadline = job.LeaseExpiresAt
	}
	attempt, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	polled := make(chan error, 1)
	go e.monitor(attempt, cancel, job, polled)
	response, err := e.processor(attempt, job)
	cancel()
	pollErr := <-polled
	// The processor returns success only after atomic STAGED commit. A poll that
	// observes its released lease must not turn that committed success into failure.
	if err == nil && response != nil && response.Status == pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED {
		return job, response, nil
	}
	if err == nil {
		err = fmt.Errorf("%s processor did not commit a successful output: %w", e.stage, domain.ErrPersistentIntegrity)
	}
	if pollErr != nil {
		err = errors.Join(err, pollErr)
	}
	next := pb.JobState_JOB_STATE_RETRY_WAIT
	switch status.Code(err) {
	case codes.InvalidArgument, codes.FailedPrecondition, codes.PermissionDenied, codes.Unauthenticated, codes.Unimplemented, codes.OutOfRange, codes.DataLoss:
		next = pb.JobState_JOB_STATE_FAILED
	}
	if errors.Is(err, errJobCancellationRequested) {
		next = pb.JobState_JOB_STATE_CANCELLED
	}
	if errors.Is(err, domain.ErrPersistentIntegrity) || errors.Is(err, domain.ErrIndexReplan) || errors.Is(err, domain.ErrResolutionReplan) || errors.Is(err, domain.ErrGraphAssemblyUnresolved) {
		next = pb.JobState_JOB_STATE_FAILED
	}
	delay := time.Duration(0)
	if next == pb.JobState_JOB_STATE_RETRY_WAIT {
		delay = e.config.RetryBase
		for step := uint32(1); step < job.StageAttempt && delay < e.config.RetryMax; step++ {
			if delay > e.config.RetryMax/2 {
				delay = e.config.RetryMax
				break
			}
			delay *= 2
		}
		if delay > e.config.RetryMax {
			delay = e.config.RetryMax
		}
	}
	finish, done := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer done()
	_, completeErr := e.store.CompleteWorkerAttempt(finish, job.JobID, job.LeaseOwner, job.LeaseFence, next, delay)
	return job, nil, errors.Join(err, completeErr)
}

func (e *inventoryExecutor) monitor(ctx context.Context, cancel context.CancelFunc, job domain.JobRecord, result chan<- error) {
	ticker := time.NewTicker(e.config.CancellationPoll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			result <- nil
			return
		case <-ticker.C:
			requested, err := e.store.CancellationRequested(ctx, job.JobID, job.LeaseOwner, job.LeaseFence)
			if ctx.Err() != nil {
				result <- nil
				return
			}
			if requested {
				err = errJobCancellationRequested
			}
			if err != nil {
				result <- err
				cancel()
				return
			}
		}
	}
}
