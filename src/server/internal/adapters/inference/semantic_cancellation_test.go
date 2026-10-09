// Exercises semantic retry after direct/wrapped cancellation and deadline errors.
// Successful item sampling must be reused while interrupted items are retried;
// process-local cache recovery is not durable recovery across gateway restarts.
package inference

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

type interruptedExtraction struct {
	mu            sync.Mutex
	calls         map[string]int
	err           error
	cancel        context.CancelFunc
	interruptCall int
}

func (p *interruptedExtraction) Generate(ctx context.Context, request StructuredRequest) (StructuredResponse, error) {
	p.mu.Lock()
	p.calls[request.ItemID]++
	interrupted := p.calls[request.ItemID] == p.interruptCall
	p.mu.Unlock()
	if request.ItemID == "chunk:2" && interrupted {
		if p.cancel != nil {
			p.cancel()
		}
		return StructuredResponse{}, p.err
	}
	return StructuredResponse{JSON: validRawProposal(), InputTokens: 20, OutputTokens: 7}, nil
}

func TestSemanticExtractInterruptedItemCanRetry(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, fmt.Errorf("wrapped: %w", context.Canceled)} {
		t.Run(cause.Error(), func(t *testing.T) {
			provider := &interruptedExtraction{calls: map[string]int{}, err: cause, interruptCall: 1}
			service, request := semanticFixture(provider)
			second := proto.Clone(request.Items[0]).(*pb.TextItem)
			second.ItemId = "chunk:2"
			request.Items = append(request.Items, second)
			response, err := service.ExtractBatch(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			failure := response.Results[1].GetError()
			want := pb.ErrorCode_ERROR_CODE_CANCELLED
			if errors.Is(cause, context.DeadlineExceeded) {
				want = pb.ErrorCode_ERROR_CODE_DEADLINE_EXCEEDED
			}
			if failure == nil || !failure.Retryable || failure.Code != want {
				t.Fatalf("interruption became permanent: %+v", failure)
			}
			response, err = service.ExtractBatch(context.Background(), request)
			if err != nil || response.Results[1].GetProposal() == nil {
				t.Fatalf("retry did not recover: %v %+v", err, response)
			}
			if provider.calls["chunk:1"] != 1 || provider.calls["chunk:2"] != 2 {
				t.Fatal("wrong item retry counts", provider.calls)
			}
		})
	}
}

func TestSemanticExtractCancelledCallDoesNotPoisonCache(t *testing.T) {
	// Prepopulate the first item through a partial response, then cancel the
	// retried second item with an opaque adapter error before another retry.
	provider := &interruptedExtraction{calls: map[string]int{}, err: &ProviderError{Safe: "unavailable", Retryable: true}, interruptCall: 1}
	service, request := semanticFixture(provider)
	second := proto.Clone(request.Items[0]).(*pb.TextItem)
	second.ItemId = "chunk:2"
	request.Items = append(request.Items, second)
	if _, err := service.ExtractBatch(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	provider.interruptCall = 2
	provider.cancel = cancel
	provider.err = errors.New("opaque interrupted adapter")
	if _, err := service.ExtractBatch(ctx, request); status.Code(err) != codes.Canceled {
		t.Fatalf("expected cancelled RPC: %v", err)
	}
	provider.cancel = nil
	response, err := service.ExtractBatch(context.Background(), request)
	if err != nil || response.Results[1].GetProposal() == nil {
		t.Fatalf("cancellation poisoned replay: %v %+v", err, response)
	}
	if provider.calls["chunk:1"] != 1 || provider.calls["chunk:2"] != 3 {
		t.Fatal("successful item resampled or failed item cached", provider.calls)
	}
}

func TestSemanticOperationErrorPreservesContextCause(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		failure := operationError(&ProviderError{Code: "cancelled", Safe: "request ended", Retryable: true, cause: cause}, "item:test")
		want := pb.ErrorCode_ERROR_CODE_CANCELLED
		if cause == context.DeadlineExceeded {
			want = pb.ErrorCode_ERROR_CODE_DEADLINE_EXCEEDED
		}
		if failure.Code != want || !failure.Retryable || failure.GetItemId() != "item:test" {
			t.Fatalf("lost context cause: %+v", failure)
		}
	}
	// A deterministic invalid model result must remain terminal.
	failure := operationError(&ProviderError{Code: "context_window", Safe: "too long"}, "item:test")
	if failure.Retryable {
		t.Fatal("invalid request became retryable")
	}
}

func TestSemanticResolveDeadlineItemCanRetry(t *testing.T) {
	provider := &providerDouble{raw: json.RawMessage(validResolutionJSON), err: context.DeadlineExceeded}
	service, request := resolutionFixture(provider)
	first, err := service.ResolveBatch(context.Background(), request)
	if err != nil || first.Results[0].GetError().GetCode() != pb.ErrorCode_ERROR_CODE_DEADLINE_EXCEEDED || !first.Results[0].GetError().GetRetryable() {
		t.Fatalf("resolution deadline not transient: %v %+v", err, first)
	}
	provider.err = nil
	retry, err := service.ResolveBatch(context.Background(), request)
	if err != nil || retry.Results[0].GetProposal() == nil || provider.callCount() != 2 {
		t.Fatalf("resolution deadline poisoned cache: %v %+v", err, retry)
	}
}
