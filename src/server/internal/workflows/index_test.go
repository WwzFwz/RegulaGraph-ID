// Verifies INDEX deadline/cancellation and retry policy with a controlled
// processor. Real output validation and atomic commit have PostgreSQL tests;
// these fakes prove only workflow control flow, not durable storage or inference.
package workflows

import (
	"context"
	"errors"
	"testing"
	"time"

	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type indexAttemptFake struct {
	job       domain.JobRecord
	cancelled bool
	pollErr   error
	completed pb.JobState
	delay     time.Duration
	claimErr  error
}

func (s *indexAttemptFake) ClaimIndexJob(context.Context, string, time.Duration) (domain.JobRecord, error) {
	return s.job, s.claimErr
}
func (s *indexAttemptFake) CancellationRequested(context.Context, string, string, uint64) (bool, error) {
	return s.cancelled, s.pollErr
}
func (s *indexAttemptFake) CompleteWorkerAttempt(_ context.Context, _ string, _ string, _ uint64, state pb.JobState, delay time.Duration) (pb.JobState, error) {
	s.completed, s.delay = state, delay
	return state, nil
}

type indexProcessorFunc func(context.Context, domain.JobRecord) (*pb.ProcessBatchResponse, error)

func (f indexProcessorFunc) ProcessIndexJob(ctx context.Context, j domain.JobRecord) (*pb.ProcessBatchResponse, error) {
	return f(ctx, j)
}

func TestIndexExecutorLifecycle(t *testing.T) {
	for _, kind := range []string{"success", "transient", "authority", "cancel", "deadline", "empty", "committed-poll-race", "unavailable"} {
		t.Run(kind, func(t *testing.T) {
			store := &indexAttemptFake{job: domain.JobRecord{JobID: "job:index", LeaseOwner: "owner", LeaseFence: 1, StageAttempt: 20, LeaseExpiresAt: time.Now().Add(time.Minute)}}
			expected := pb.JobState_JOB_STATE_UNSPECIFIED
			switch kind {
			case "transient", "deadline":
				expected = pb.JobState_JOB_STATE_RETRY_WAIT
			case "authority", "empty":
				expected = pb.JobState_JOB_STATE_FAILED
			case "cancel":
				store.cancelled = true
				expected = pb.JobState_JOB_STATE_CANCELLED
			case "committed-poll-race":
				store.pollErr = domain.ErrLeaseUnavailable
			case "unavailable":
				store.claimErr = domain.ErrLeaseUnavailable
			}
			calls := 0
			processor := indexProcessorFunc(func(ctx context.Context, _ domain.JobRecord) (*pb.ProcessBatchResponse, error) {
				calls++
				switch kind {
				case "transient":
					return nil, errors.New("temporary I/O")
				case "authority":
					return nil, domain.ErrIndexReplan
				case "empty":
					return nil, nil
				case "cancel", "deadline":
					<-ctx.Done()
					return nil, ctx.Err()
				case "committed-poll-race":
					<-ctx.Done()
				}
				return &pb.ProcessBatchResponse{Status: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED}, nil
			})
			executor, err := NewIndexExecutor(store, processor, IndexExecutorConfig{OwnerID: "owner", Lease: time.Minute, CallTimeout: 25 * time.Millisecond, CancellationPoll: time.Millisecond, RetryBase: time.Second, RetryMax: 4 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = executor.RunOnce(context.Background())
			if store.completed != expected {
				t.Fatalf("state=%v expected=%v error=%v", store.completed, expected, err)
			}
			if expected == pb.JobState_JOB_STATE_RETRY_WAIT && store.delay != 4*time.Second {
				t.Fatal("backoff not bounded", store.delay)
			}
			if kind == "success" || kind == "committed-poll-race" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("missing error")
			}
			if kind == "unavailable" && calls != 0 {
				t.Fatal("unclaimed processor invoked")
			}
		})
	}
}
