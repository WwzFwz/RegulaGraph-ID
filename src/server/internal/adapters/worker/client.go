// Package worker provides the bounded gRPC boundary from the Go coordinator to the Rust batch worker.
//
// Requests and responses use the generated C01 schema. Every call validates wire invariants, propagates
// the earlier caller/request deadline, and rejects stale attempt or lease fences before output is accepted.
// Keep payloads as artifact references and measure queue/RPC p50/p95/p99 against benchmark-targets.yaml.
package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

const DefaultMaxMessageBytes = 16 << 20

type Client struct {
	rpc    pb.WorkerClient
	closer io.Closer
}

// New creates a lazy gRPC connection. Callers must choose transport credentials explicitly; use TLS in
// deployment and insecure credentials only for a trusted local development listener.
func New(target string, transportCredentials credentials.TransportCredentials, maxMessageBytes int) (*Client, error) {
	if target == "" {
		return nil, errors.New("worker target is required")
	}
	if transportCredentials == nil {
		return nil, errors.New("worker transport credentials are required")
	}
	if maxMessageBytes <= 0 {
		maxMessageBytes = DefaultMaxMessageBytes
	}
	conn, err := grpc.NewClient(
		target,
		grpc.WithTransportCredentials(transportCredentials),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallSendMsgSize(maxMessageBytes),
			grpc.MaxCallRecvMsgSize(maxMessageBytes),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("create worker client: %w", err)
	}
	return &Client{rpc: pb.NewWorkerClient(conn), closer: conn}, nil
}

// NewWithRPC injects a generated client for tests or an already managed connection.
func NewWithRPC(rpc pb.WorkerClient) (*Client, error) {
	if rpc == nil {
		return nil, errors.New("worker RPC client is required")
	}
	return &Client{rpc: rpc}, nil
}

func (c *Client) Close() error {
	if c == nil || c.closer == nil {
		return nil
	}
	return c.closer.Close()
}

func (c *Client) ProcessBatch(ctx context.Context, request *pb.ProcessBatchRequest) (*pb.ProcessBatchResponse, error) {
	if c == nil || c.rpc == nil {
		return nil, errors.New("worker client is not initialized")
	}
	if err := domain.ValidateWire(request, domain.DefaultWireLimits); err != nil {
		return nil, fmt.Errorf("validate process batch request: %w", err)
	}
	callCtx, cancel, err := boundedContext(ctx, request.GetContext())
	if err != nil {
		return nil, err
	}
	defer cancel()

	response, err := c.rpc.ProcessBatch(callCtx, request)
	if err != nil {
		return nil, fmt.Errorf("process worker batch: %w", err)
	}
	if err := domain.VerifyWorkerResponse(request, response); err != nil {
		return nil, fmt.Errorf("verify worker response: %w", err)
	}
	return response, nil
}

func (c *Client) GetStatus(ctx context.Context, request *pb.WorkerStatusRequest) (*pb.WorkerStatusResponse, error) {
	if c == nil || c.rpc == nil {
		return nil, errors.New("worker client is not initialized")
	}
	return c.statusCall(ctx, request, c.rpc.GetStatus, "get worker status")
}

func (c *Client) Cancel(ctx context.Context, request *pb.WorkerStatusRequest) (*pb.WorkerStatusResponse, error) {
	if c == nil || c.rpc == nil {
		return nil, errors.New("worker client is not initialized")
	}
	return c.statusCall(ctx, request, c.rpc.Cancel, "cancel worker batch")
}

type statusRPC func(context.Context, *pb.WorkerStatusRequest, ...grpc.CallOption) (*pb.WorkerStatusResponse, error)

func (c *Client) statusCall(ctx context.Context, request *pb.WorkerStatusRequest, call statusRPC, operation string) (*pb.WorkerStatusResponse, error) {
	if err := domain.ValidateWire(request, domain.DefaultWireLimits); err != nil {
		return nil, fmt.Errorf("validate worker status request: %w", err)
	}
	callCtx, cancel, err := boundedContext(ctx, request.GetContext())
	if err != nil {
		return nil, err
	}
	defer cancel()

	response, err := call(callCtx, request)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", operation, err)
	}
	if err := verifyStatusResponse(request, response); err != nil {
		return nil, fmt.Errorf("verify worker status response: %w", err)
	}
	return response, nil
}

func verifyStatusResponse(request *pb.WorkerStatusRequest, response *pb.WorkerStatusResponse) error {
	if err := domain.ValidateWire(response, domain.DefaultWireLimits); err != nil {
		return err
	}
	if request.GetJobId() != response.GetJobId() || request.GetAttempt() != response.GetAttempt() || request.GetFence() != response.GetFence() {
		return errors.New("stale or mismatched worker status response")
	}
	if response.GetCompletedItems() > response.GetTotalItems() {
		return errors.New("worker status completed items exceed total items")
	}
	return nil
}

func boundedContext(parent context.Context, requestContext *pb.RequestContext) (context.Context, context.CancelFunc, error) {
	if parent == nil {
		return nil, nil, errors.New("call context is required")
	}
	if err := parent.Err(); err != nil {
		return nil, nil, err
	}
	if requestContext == nil || requestContext.GetDeadline() == nil {
		return nil, nil, errors.New("request deadline is required")
	}
	deadline := requestContext.GetDeadline().AsTime()
	if !deadline.After(time.Now()) {
		return nil, nil, context.DeadlineExceeded
	}
	if callerDeadline, ok := parent.Deadline(); ok && callerDeadline.Before(deadline) {
		deadline = callerDeadline
	}
	callContext, cancel := context.WithDeadline(parent, deadline)
	return callContext, cancel, nil
}
