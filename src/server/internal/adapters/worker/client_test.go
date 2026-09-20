// Tests for worker transport correlation, deadline propagation, cancellation and status invariants.
// Fakes isolate the adapter contract; generated gRPC interoperability is checked separately at the milestone.
package worker

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

type fakeWorkerRPC struct {
	process func(context.Context, *pb.ProcessBatchRequest) (*pb.ProcessBatchResponse, error)
	status  func(context.Context, *pb.WorkerStatusRequest) (*pb.WorkerStatusResponse, error)
	cancel  func(context.Context, *pb.WorkerStatusRequest) (*pb.WorkerStatusResponse, error)
}

func (f *fakeWorkerRPC) ProcessBatch(ctx context.Context, req *pb.ProcessBatchRequest, _ ...grpc.CallOption) (*pb.ProcessBatchResponse, error) {
	return f.process(ctx, req)
}
func (f *fakeWorkerRPC) GetStatus(ctx context.Context, req *pb.WorkerStatusRequest, _ ...grpc.CallOption) (*pb.WorkerStatusResponse, error) {
	return f.status(ctx, req)
}
func (f *fakeWorkerRPC) Cancel(ctx context.Context, req *pb.WorkerStatusRequest, _ ...grpc.CallOption) (*pb.WorkerStatusResponse, error) {
	return f.cancel(ctx, req)
}

func TestProcessBatchRejectsStaleFence(t *testing.T) {
	req := processRequest(time.Now().Add(time.Minute))
	rpc := &fakeWorkerRPC{process: func(context.Context, *pb.ProcessBatchRequest) (*pb.ProcessBatchResponse, error) {
		return successResponse(req, req.Lease.Fence+1), nil
	}}
	client, err := NewWithRPC(rpc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.ProcessBatch(context.Background(), req); status.Code(err) != codes.DataLoss || !strings.Contains(err.Error(), "stale or mismatched") {
		t.Fatalf("expected stale fence rejection, got %v", err)
	}
}

func TestProcessBatchUsesEarlierRequestDeadline(t *testing.T) {
	req := processRequest(time.Now().Add(40 * time.Millisecond))
	rpc := &fakeWorkerRPC{process: func(ctx context.Context, _ *pb.ProcessBatchRequest) (*pb.ProcessBatchResponse, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	client, _ := NewWithRPC(rpc)
	caller, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started := time.Now()
	_, err := client.ProcessBatch(caller, req)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected request deadline, got %v", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("request deadline was not propagated: %s", elapsed)
	}
}

func TestStatusAndCancelRejectMismatchedOrImpossibleProgress(t *testing.T) {
	req := statusRequest(time.Now().Add(time.Minute))
	rpc := &fakeWorkerRPC{
		status: func(context.Context, *pb.WorkerStatusRequest) (*pb.WorkerStatusResponse, error) {
			return &pb.WorkerStatusResponse{JobId: "other", Attempt: req.Attempt, Fence: req.Fence}, nil
		},
		cancel: func(context.Context, *pb.WorkerStatusRequest) (*pb.WorkerStatusResponse, error) {
			return &pb.WorkerStatusResponse{JobId: req.JobId, Attempt: req.Attempt, Fence: req.Fence, CompletedItems: 2, TotalItems: 1}, nil
		},
	}
	client, _ := NewWithRPC(rpc)
	if _, err := client.GetStatus(context.Background(), req); err == nil || !strings.Contains(err.Error(), "mismatched") {
		t.Fatalf("expected mismatched status rejection, got %v", err)
	}
	if _, err := client.Cancel(context.Background(), req); err == nil || !strings.Contains(err.Error(), "exceed") {
		t.Fatalf("expected impossible progress rejection, got %v", err)
	}
}

func TestExpiredDeadlinePreventsRPC(t *testing.T) {
	called := false
	req := statusRequest(time.Now().Add(-time.Second))
	rpc := &fakeWorkerRPC{status: func(context.Context, *pb.WorkerStatusRequest) (*pb.WorkerStatusResponse, error) {
		called = true
		return nil, nil
	}}
	client, _ := NewWithRPC(rpc)
	_, err := client.GetStatus(context.Background(), req)
	if !errors.Is(err, context.DeadlineExceeded) || called {
		t.Fatalf("expected local deadline rejection before RPC, called=%v err=%v", called, err)
	}
}

func TestClientRejectsOversizedRequestBeforeServerHandler(t *testing.T) {
	listener := bufconn.Listen(64 << 10)
	server := grpc.NewServer()
	handler := &countingWorkerServer{}
	pb.RegisterWorkerServer(server, handler)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	client, err := newClient(
		"passthrough:///worker-test",
		insecure.NewCredentials(),
		512,
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	req := processRequest(time.Now().Add(time.Minute))
	req.Sources[0].StorageKey = "objects/" + strings.Repeat("a", 2048) + ".pdf"
	_, err = client.ProcessBatch(context.Background(), req)
	if status.Code(err) != codes.OutOfRange {
		t.Fatalf("expected deterministic size rejection, got %v", err)
	}
	if handler.calls != 0 {
		t.Fatalf("oversized request reached server handler %d times", handler.calls)
	}
}

type countingWorkerServer struct {
	pb.UnimplementedWorkerServer
	calls int
}

func (s *countingWorkerServer) ProcessBatch(context.Context, *pb.ProcessBatchRequest) (*pb.ProcessBatchResponse, error) {
	s.calls++
	return nil, status.Error(codes.Internal, "unexpected handler call")
}

func processRequest(deadline time.Time) *pb.ProcessBatchRequest {
	return &pb.ProcessBatchRequest{
		Context:  requestContext(deadline),
		JobId:    "job-1",
		Attempt:  1,
		Lease:    &pb.Lease{OwnerId: "worker-1", Fence: 7, ExpiresAt: timestamppb.New(deadline)},
		Sources:  []*pb.ArtifactRef{artifact("source-1", "objects/source-1.pdf")},
		Manifest: &pb.ProducerManifest{Software: "test", Build: "test", SchemaVersion: 1, ConfigHash: hash()},
		Stages:   []pb.JobStage{pb.JobStage_JOB_STAGE_PARSE},
	}
}

func statusRequest(deadline time.Time) *pb.WorkerStatusRequest {
	return &pb.WorkerStatusRequest{Context: requestContext(deadline), JobId: "job-1", Attempt: 1, Fence: 7}
}

func requestContext(deadline time.Time) *pb.RequestContext {
	return &pb.RequestContext{
		SchemaVersion: 1, RequestId: "request-1", TraceId: "trace-1", CorpusId: "corpus-1",
		Deadline: timestamppb.New(deadline), ConfigFingerprint: hash(), AuthScopeRef: "scope-1",
	}
}

func hash() *pb.ContentHash { return &pb.ContentHash{Sha256: strings.Repeat("a", 64)} }

func artifact(id, key string) *pb.ArtifactRef {
	return &pb.ArtifactRef{ArtifactId: id, ContentHash: hash(), StorageKey: key, MediaType: "application/octet-stream", ByteSize: 1, SchemaVersion: 1}
}

func successResponse(req *pb.ProcessBatchRequest, fence uint64) *pb.ProcessBatchResponse {
	return &pb.ProcessBatchResponse{
		RequestId: req.Context.RequestId, JobId: req.JobId, Attempt: req.Attempt, Fence: fence,
		DocumentBatch: artifact("document-batch-1", "batches/document-batch-1.pb"),
		Status:        pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED,
	}
}
