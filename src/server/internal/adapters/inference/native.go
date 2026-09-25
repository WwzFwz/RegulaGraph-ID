// Provides reusable, model-pinned C01 native inference clients for query and indexing callers.
// Connections are supplied by the owner; no model loads, hidden retries or automatic truncation.
// Validation binds request/result IDs, exact manifests and deadlines before outputs cross the adapter.
// Measure client p95/p99 including transport and queue against configs/benchmark-targets.yaml.
package inference

import (
	"context"
	"errors"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
	"time"
)

const nativeMessageBytes = 4 * 1024 * 1024

type NativeClient struct {
	client pb.InferenceClient
	models map[pb.ModelTask]*pb.ModelManifest
}

// NewNativeClient freezes expected manifests without opening connections or querying capabilities.
func NewNativeClient(connection grpc.ClientConnInterface, models ...*pb.ModelManifest) (*NativeClient, error) {
	if connection == nil || len(models) == 0 || len(models) > 2 {
		return nil, errors.New("native connection and one or two model manifests required")
	}
	result := &NativeClient{client: pb.NewInferenceClient(connection), models: make(map[pb.ModelTask]*pb.ModelManifest)}
	for _, m := range models {
		if err := domain.ValidateWire(m, domain.DefaultWireLimits); err != nil {
			return nil, err
		}
		if m.Task != pb.ModelTask_MODEL_TASK_EMBED && m.Task != pb.ModelTask_MODEL_TASK_RERANK {
			return nil, errors.New("unsupported native model task")
		}
		if m.GetDimensions() > 4096 || m.MaxTokens > 8192 {
			return nil, errors.New("native model exceeds client capacity")
		}
		if result.models[m.Task] != nil {
			return nil, errors.New("duplicate native model task")
		}
		result.models[m.Task] = proto.Clone(m).(*pb.ModelManifest)
	}
	return result, nil
}

func (c *NativeClient) request(ctx context.Context, message proto.Message, rc *pb.RequestContext, model *pb.ModelManifest, count int) (context.Context, context.CancelFunc, error) {
	if ctx == nil {
		return nil, nil, errors.New("context required")
	}
	limits := domain.DefaultWireLimits
	limits.MaxBytes = nativeMessageBytes
	if err := domain.ValidateWire(message, limits); err != nil {
		return nil, nil, err
	}
	if rc.SchemaVersion != 1 || count < 1 || count > 128 || !proto.Equal(c.models[model.Task], model) {
		return nil, nil, errors.New("native request schema/model/item limit mismatch")
	}
	if !rc.Deadline.AsTime().After(time.Now()) {
		return nil, nil, context.DeadlineExceeded
	}
	call, cancel := context.WithDeadline(ctx, rc.Deadline.AsTime())
	return call, cancel, nil
}

func nativeTokens(model *pb.ModelManifest, input uint32, t *pb.TruncationInfo) error {
	if t == nil || input == 0 || input > model.MaxTokens || t.RetainedTokens != uint64(input) ||
		t.OriginalTokens < t.RetainedTokens || t.Truncated != (t.RetainedTokens < t.OriginalTokens) {
		return errors.New("native token/truncation accounting invalid")
	}
	return nil
}

// GetCapabilities checks readiness and exact configured manifests before an owner advertises ready.
func (c *NativeClient) GetCapabilities(ctx context.Context, request *pb.CapabilitiesRequest) (*pb.CapabilitiesResponse, error) {
	if ctx == nil {
		return nil, errors.New("context required")
	}
	if err := domain.ValidateWire(request, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	if request.Context.SchemaVersion != 1 {
		return nil, errors.New("unsupported schema")
	}
	call, cancel := context.WithDeadline(ctx, request.Context.Deadline.AsTime())
	defer cancel()
	result, err := c.client.GetCapabilities(call, request, grpc.MaxCallRecvMsgSize(nativeMessageBytes))
	if err != nil {
		return nil, err
	}
	if err = domain.ValidateWire(result, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	if !result.Ready {
		return nil, errors.New("native service not ready")
	}
	found := make(map[pb.ModelTask]bool)
	tasks := make(map[pb.ModelTask]bool)
	for _, task := range result.SupportedTasks {
		if tasks[task] || (task != pb.ModelTask_MODEL_TASK_EMBED && task != pb.ModelTask_MODEL_TASK_RERANK) {
			return nil, errors.New("invalid native supported tasks")
		}
		tasks[task] = true
	}
	for _, cap := range result.Models {
		if found[cap.Model.Task] {
			return nil, errors.New("duplicate capability task")
		}
		found[cap.Model.Task] = true
		if !tasks[cap.Model.Task] {
			return nil, errors.New("capability task missing from supported tasks")
		}
		if expected := c.models[cap.Model.Task]; expected != nil && (!cap.Ready || !proto.Equal(expected, cap.Model) || cap.Limits.MaxTokens != expected.MaxTokens || cap.Limits.MaxItems < 128 || cap.Limits.MaxBytes < nativeMessageBytes) {
			return nil, errors.New("native capability model/limit drift")
		}
	}
	if len(tasks) != len(found) {
		return nil, errors.New("supported task has no model capability")
	}
	for task := range c.models {
		if !found[task] {
			return nil, errors.New("configured native model unavailable")
		}
	}
	return result, nil
}
