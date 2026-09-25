// Tests native RPC adapter correlation, immutable model pins, per-item failures and deadline propagation.
// Uses a transport double; real C++ model integration is opt-in in native_integration_test.go.
// Fixtures prove boundary correctness, never model quality or required benchmark performance.
package inference

import (
	"context"
	"errors"
	"fmt"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"math"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"testing"
	"time"
)

func TestNativeFullDimensionBatchAndCapabilities(t *testing.T) {
	model := nativeModel(pb.ModelTask_MODEL_TASK_EMBED)
	size := uint32(1024)
	model.Dimensions = &size
	request := &pb.EmbedBatchRequest{Context: nativeContext(), Model: model, Purpose: pb.EmbeddingPurpose_EMBEDDING_PURPOSE_DOCUMENT, OperationKey: "bulk"}
	for i := 0; i < 128; i++ {
		request.Items = append(request.Items, &pb.TextItem{ItemId: fmt.Sprintf("item-%d", i), Text: "Pasal 1"})
	}
	capability := &pb.CapabilitiesResponse{Ready: true, SupportedTasks: []pb.ModelTask{pb.ModelTask_MODEL_TASK_EMBED}, Models: []*pb.ModelCapability{{Model: model, Ready: true, Limits: &pb.BatchLimits{MaxItems: 128, MaxBytes: nativeMessageBytes, MaxTokens: 512}}}}
	transport := &nativeTransport{call: func(_ context.Context, _ string, in, out any) error {
		switch result := out.(type) {
		case *pb.CapabilitiesResponse:
			proto.Merge(result, capability)
		case *pb.EmbedBatchResponse:
			result.RequestId = request.Context.RequestId
			result.Model = proto.Clone(model).(*pb.ModelManifest)
			for _, input := range in.(*pb.EmbedBatchRequest).Items {
				value := nativeVector()
				value.Values = make([]float32, 1024)
				value.Values[0] = 1
				result.Results = append(result.Results, &pb.EmbeddingResult{ItemId: input.ItemId, Result: &pb.EmbeddingResult_Embedding{Embedding: value}})
			}
		}
		return nil
	}}
	client, err := NewNativeClient(transport, model)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := client.EmbedBatch(context.Background(), request); err != nil || len(result.GetResults()) != 128 {
		t.Fatal("valid full batch rejected", err)
	}
	capRequest := &pb.CapabilitiesRequest{Context: nativeContext()}
	if _, err := client.GetCapabilities(context.Background(), capRequest); err != nil {
		t.Fatal(err)
	}
	original := proto.Clone(capability).(*pb.CapabilitiesResponse)
	for name, mutate := range map[string]func(*pb.CapabilitiesResponse){
		"items":        func(r *pb.CapabilitiesResponse) { r.Models[0].Limits.MaxItems = 1 },
		"bytes":        func(r *pb.CapabilitiesResponse) { r.Models[0].Limits.MaxBytes = 1 },
		"unknown-task": func(r *pb.CapabilitiesResponse) { r.SupportedTasks = []pb.ModelTask{999} },
		"missing-task": func(r *pb.CapabilitiesResponse) { r.SupportedTasks = nil },
		"duplicate-task": func(r *pb.CapabilitiesResponse) {
			r.SupportedTasks = append(r.SupportedTasks, pb.ModelTask_MODEL_TASK_EMBED)
		},
		"duplicate-model": func(r *pb.CapabilitiesResponse) { r.Models = append(r.Models, r.Models[0]) },
		"unready":         func(r *pb.CapabilitiesResponse) { r.Models[0].Ready = false },
	} {
		t.Run(name, func(t *testing.T) {
			capability = proto.Clone(original).(*pb.CapabilitiesResponse)
			mutate(capability)
			if _, err := client.GetCapabilities(context.Background(), capRequest); err == nil {
				t.Fatal("invalid capability accepted")
			}
		})
	}
}

type nativeTransport struct {
	call  func(context.Context, string, any, any) error
	calls int
}

func (t *nativeTransport) Invoke(ctx context.Context, method string, in, out any, _ ...grpc.CallOption) error {
	t.calls++
	return t.call(ctx, method, in, out)
}
func (t *nativeTransport) NewStream(context.Context, *grpc.StreamDesc, string, ...grpc.CallOption) (grpc.ClientStream, error) {
	return nil, errors.New("not used")
}
func nativeModel(task pb.ModelTask) *pb.ModelManifest {
	model := &pb.ModelManifest{ModelId: "fixture", Version: "v1", WeightsHash: semanticHash("a"), TokenizerHash: semanticHash("b"), Task: task, MaxTokens: 512, Precision: "fp32", Backend: "test", Pooling: "logit", Normalization: "none"}
	if task == pb.ModelTask_MODEL_TASK_EMBED {
		n := uint32(2)
		model.Dimensions = &n
		model.Pooling = "cls"
		model.Normalization = "l2"
	}
	return model
}
func nativeContext() *pb.RequestContext {
	return &pb.RequestContext{SchemaVersion: 1, RequestId: "req", TraceId: "trace", CorpusId: "corpus", Deadline: timestamppb.New(time.Now().Add(time.Minute)), ConfigFingerprint: semanticHash("c"), AuthScopeRef: "local"}
}
func nativeVector() *pb.Embedding {
	return &pb.Embedding{Values: []float32{0.6, 0.8}, InputTokens: 4, Truncation: &pb.TruncationInfo{OriginalTokens: 4, RetainedTokens: 4}}
}
func TestNativeEmbeddingBoundary(t *testing.T) {
	model := nativeModel(pb.ModelTask_MODEL_TASK_EMBED)
	request := &pb.EmbedBatchRequest{Context: nativeContext(), Model: model, Purpose: pb.EmbeddingPurpose_EMBEDDING_PURPOSE_QUERY, OperationKey: "op", Items: []*pb.TextItem{{ItemId: "one", Text: "Pasal 1"}, {ItemId: "two", Text: "izin usaha"}}}
	valid := &pb.EmbedBatchResponse{RequestId: "req", Model: model, Results: []*pb.EmbeddingResult{{ItemId: "two", Result: &pb.EmbeddingResult_Embedding{Embedding: nativeVector()}}, {ItemId: "one", Result: &pb.EmbeddingResult_Embedding{Embedding: nativeVector()}}}}
	cases := map[string]func(*pb.EmbedBatchResponse){
		"reordered":  func(*pb.EmbedBatchResponse) {},
		"missing":    func(r *pb.EmbedBatchResponse) { r.Results = r.Results[:1] },
		"duplicate":  func(r *pb.EmbedBatchResponse) { r.Results[1].ItemId = "two" },
		"model":      func(r *pb.EmbedBatchResponse) { r.Model.Version = "drift" },
		"dimension":  func(r *pb.EmbedBatchResponse) { r.Results[0].GetEmbedding().Values = []float32{1} },
		"nan":        func(r *pb.EmbedBatchResponse) { r.Results[0].GetEmbedding().Values[0] = float32(math.NaN()) },
		"norm":       func(r *pb.EmbedBatchResponse) { r.Results[0].GetEmbedding().Values = []float32{0, 0} },
		"tokens":     func(r *pb.EmbedBatchResponse) { r.Results[0].GetEmbedding().InputTokens = 5 },
		"truncation": func(r *pb.EmbedBatchResponse) { r.Results[0].GetEmbedding().Truncation.Truncated = true },
		"empty":      func(r *pb.EmbedBatchResponse) { r.Results[0].Result = nil },
		"partial": func(r *pb.EmbedBatchResponse) {
			id := "two"
			r.Results[0].Result = &pb.EmbeddingResult_Error{Error: &pb.OperationError{Code: pb.ErrorCode_ERROR_CODE_TOO_LARGE, SafeMessage: "too long", ItemId: &id}}
		},
		"wrong-error-id": func(r *pb.EmbedBatchResponse) {
			id := "one"
			r.Results[0].Result = &pb.EmbeddingResult_Error{Error: &pb.OperationError{Code: pb.ErrorCode_ERROR_CODE_TOO_LARGE, SafeMessage: "too long", ItemId: &id}}
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			response := proto.Clone(valid).(*pb.EmbedBatchResponse)
			change(response)
			transport := &nativeTransport{call: func(ctx context.Context, _ string, _ any, out any) error {
				deadline, ok := ctx.Deadline()
				if !ok || deadline.After(request.Context.Deadline.AsTime()) {
					t.Fatal("wire deadline missing")
				}
				proto.Merge(out.(*pb.EmbedBatchResponse), response)
				return nil
			}}
			client, err := NewNativeClient(transport, model)
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.EmbedBatch(context.Background(), request)
			if (err == nil) != (name == "reordered" || name == "partial") {
				t.Fatalf("unexpected result: %v", err)
			}
		})
	}
}
func TestNativeRejectsBeforeRPCAndFreezesManifest(t *testing.T) {
	transport := &nativeTransport{call: func(context.Context, string, any, any) error { return errors.New("unexpected call") }}
	model := nativeModel(pb.ModelTask_MODEL_TASK_EMBED)
	client, err := NewNativeClient(transport, model)
	if err != nil {
		t.Fatal(err)
	}
	model.Version = "changed"
	request := &pb.EmbedBatchRequest{Context: nativeContext(), Model: model, Purpose: pb.EmbeddingPurpose_EMBEDDING_PURPOSE_QUERY, OperationKey: "op", Items: []*pb.TextItem{{ItemId: "one", Text: "pasal"}}}
	if _, err := client.EmbedBatch(context.Background(), request); err == nil || transport.calls != 0 {
		t.Fatal("manifest mutation accepted")
	}
	request.Model = proto.Clone(client.models[pb.ModelTask_MODEL_TASK_EMBED]).(*pb.ModelManifest)
	request.Context.Deadline = timestamppb.New(time.Now().Add(-time.Second))
	if _, err := client.EmbedBatch(context.Background(), request); !errors.Is(err, context.DeadlineExceeded) || transport.calls != 0 {
		t.Fatal("expired request sent", err)
	}
}
func TestNativeRerankAccounting(t *testing.T) {
	model := nativeModel(pb.ModelTask_MODEL_TASK_RERANK)
	request := &pb.RerankBatchRequest{Context: nativeContext(), Model: model, OperationKey: "op", Pairs: []*pb.RerankPair{{PairId: "p", Query: "izin", Text: "Pasal 1"}}}
	response := &pb.RerankBatchResponse{RequestId: "req", Model: model, Results: []*pb.RerankItemResult{{PairId: "p", Result: &pb.RerankItemResult_Score{Score: &pb.RerankScore{Score: -2, InputTokens: 4, Truncation: &pb.TruncationInfo{OriginalTokens: 4, RetainedTokens: 4}}}}}}
	transport := &nativeTransport{call: func(_ context.Context, _ string, _ any, out any) error {
		proto.Merge(out.(*pb.RerankBatchResponse), response)
		return nil
	}}
	client, err := NewNativeClient(transport, model)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.RerankBatch(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	response.Results[0].GetScore().InputTokens = 513
	if _, err = client.RerankBatch(context.Background(), request); err == nil {
		t.Fatal("invalid token accounting accepted")
	}
}
