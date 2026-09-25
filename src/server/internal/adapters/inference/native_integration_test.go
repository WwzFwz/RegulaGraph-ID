// Opt-in integration against the real C++ gRPC executable and supplied model bundles.
// Tests both tasks, per-item limits, manifest drift and concurrent correlation using actual transport.
// Fixture bundles validate integration only; BGE numeric/quality evidence is recorded by tooling/models.
package inference

import (
	"context"
	"fmt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"net"
	"os"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNativeExecutableIntegration(t *testing.T) {
	endpoint := os.Getenv("REGULAGRAPH_TEST_NATIVE_ENDPOINT")
	if endpoint == "" {
		t.Skip("native endpoint and pinned model files required")
	}
	host, _, err := net.SplitHostPort(endpoint)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		t.Fatal("integration endpoint must be loopback")
	}
	load := func(name string) *pb.ModelManifest {
		bytes, err := os.ReadFile(os.Getenv(name))
		if err != nil {
			t.Fatal(err)
		}
		model := new(pb.ModelManifest)
		if err := protojson.Unmarshal(bytes, model); err != nil {
			t.Fatal(err)
		}
		return model
	}
	embedding := load("REGULAGRAPH_TEST_NATIVE_EMBED_MANIFEST")
	reranker := load("REGULAGRAPH_TEST_NATIVE_RERANK_MANIFEST")
	connection, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	client, err := NewNativeClient(connection, embedding, reranker)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	rc := nativeContext()
	rc.Deadline = timestamppb.New(time.Now().Add(2 * time.Minute))
	if _, err := client.GetCapabilities(ctx, &pb.CapabilitiesRequest{Context: rc}); err != nil {
		t.Fatal(err)
	}
	request := &pb.EmbedBatchRequest{Context: rc, Model: embedding, Purpose: pb.EmbeddingPurpose_EMBEDDING_PURPOSE_QUERY, OperationKey: "native-integration", Items: []*pb.TextItem{{ItemId: "first", Text: "Pasal 1 izin usaha."}, {ItemId: "second", Text: "izin izin usaha"}}}
	result, err := client.EmbedBatch(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range result.Results {
		if item.GetEmbedding() == nil {
			t.Fatal("native embedding failed", item)
		}
	}
	pairs := &pb.RerankBatchRequest{Context: rc, Model: reranker, OperationKey: "native-rerank", Pairs: []*pb.RerankPair{{PairId: "p1", Query: "izin", Text: "Pasal 1 izin usaha."}, {PairId: "p2", Query: "usaha", Text: "izin izin"}}}
	reranked, err := client.RerankBatch(ctx, pairs)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range reranked.Results {
		if item.GetScore() == nil {
			t.Fatal("native rerank failed", item)
		}
	}
	oversized := proto.Clone(request).(*pb.EmbedBatchRequest)
	oversized.Items[0].Text = strings.Repeat("izin ", int(embedding.MaxTokens)+1)
	partial, err := client.EmbedBatch(ctx, oversized)
	if err != nil {
		t.Fatal(err)
	}
	if partial.Results[0].GetError().GetCode() != pb.ErrorCode_ERROR_CODE_TOO_LARGE || partial.Results[1].GetEmbedding() == nil {
		t.Fatal("partial overlength result not explicit", partial)
	}
	// Distinct calls may reuse item IDs; transport request identity must prevent cross-request mixing.
	var group sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			req := proto.Clone(request).(*pb.EmbedBatchRequest)
			req.Context.RequestId = fmt.Sprintf("concurrent-%d", i)
			if res, err := client.EmbedBatch(ctx, req); err != nil {
				errs <- err
			} else if res.RequestId != req.Context.RequestId {
				errs <- fmt.Errorf("cross-request result")
			}
		}(i)
	}
	group.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	bad := proto.Clone(request).(*pb.EmbedBatchRequest)
	bad.Model.Version = "wrong-version"
	if _, err := pb.NewInferenceClient(connection).EmbedBatch(ctx, bad); err == nil {
		t.Fatal("server accepted model drift")
	}
	duplicate := proto.Clone(request).(*pb.EmbedBatchRequest)
	duplicate.Items[1].ItemId = duplicate.Items[0].ItemId
	if _, err := pb.NewInferenceClient(connection).EmbedBatch(ctx, duplicate); err == nil {
		t.Fatal("server accepted duplicate IDs")
	}
	stopped, stop := context.WithCancel(ctx)
	stop()
	if _, err := client.EmbedBatch(stopped, request); err == nil {
		t.Fatal("cancelled call accepted")
	}
}
