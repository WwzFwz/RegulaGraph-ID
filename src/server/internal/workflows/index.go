// Drives inventory-owned INDEX jobs without coupling workflow to indexing or
// database implementations. The injected processor owns verified output commit;
// this layer bounds work by lease/deadline, polls cancellation and records retry
// or terminal failure. Successful return requires committed STAGED output, not
// publication. Measure queue/poll/attempt p95/p99 with benchmark-targets.yaml.
package workflows

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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
type IndexExecutorConfig struct {
	OwnerID                                                   string
	Lease, CallTimeout, CancellationPoll, RetryBase, RetryMax time.Duration
}
type IndexExecutor struct {
	store     IndexAttemptStore
	processor IndexJobProcessor
	config    IndexExecutorConfig
}

func NewIndexExecutor(store IndexAttemptStore, processor IndexJobProcessor, cfg IndexExecutorConfig) (*IndexExecutor, error) {
	if store == nil || processor == nil || cfg.OwnerID == "" || cfg.Lease <= 0 || cfg.CallTimeout <= 0 || cfg.CancellationPoll <= 0 || cfg.RetryBase <= 0 || cfg.RetryMax < cfg.RetryBase {
		return nil, errors.New("INDEX dependencies and positive time budgets required")
	}
	return &IndexExecutor{store, processor, cfg}, nil
}

func (e *IndexExecutor) RunOnce(ctx context.Context) (domain.JobRecord, *pb.ProcessBatchResponse, error) {
	job, err := e.store.ClaimIndexJob(ctx, e.config.OwnerID, e.config.Lease)
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
	response, err := e.processor.ProcessIndexJob(attempt, job)
	cancel()
	pollErr := <-polled
	// The processor returns success only after atomic STAGED commit. A poll that
	// observes its released lease must not turn that committed success into failure.
	if err == nil && response != nil && response.Status == pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED {
		return job, response, nil
	}
	if err == nil {
		err = fmt.Errorf("INDEX processor did not commit a successful output: %w", domain.ErrPersistentIntegrity)
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
	if errors.Is(err, domain.ErrPersistentIntegrity) || errors.Is(err, domain.ErrIndexReplan) {
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

func (e *IndexExecutor) monitor(ctx context.Context, cancel context.CancelFunc, job domain.JobRecord, result chan<- error) {
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
