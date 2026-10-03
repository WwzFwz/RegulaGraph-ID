// Opt-in integration against a disposable loopback Qdrant. Uses production
// collection/upsert/readback and retrieval code with synthetic vectors/model
// output. Proves backend protocol and visibility, not native model relevance,
// source authenticity, publication readiness or release performance targets.
package retrieval

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/qdrant"
	"regulagraph.local/server/internal/domain"
)

func TestRetrieveBranchesAgainstQdrant(t *testing.T) {
	endpoint := os.Getenv("REGULAGRAPH_TEST_QDRANT_ENDPOINT")
	if endpoint == "" {
		t.Skip("REGULAGRAPH_TEST_QDRANT_ENDPOINT is not set")
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Path != "" {
		t.Fatal("test requires a disposable loopback HTTP Qdrant")
	}
	input, index, embed := searchFixture()
	reader := lexicalArtifacts(t, input.Generation)
	index.binding.Generation = proto.Clone(input.Generation).(*pb.IndexGeneration)
	binding := index.binding
	binding.Collection = fmt.Sprintf("regulagraph_test_%d", time.Now().UnixNano())
	client := &http.Client{Timeout: 10 * time.Second}
	store, err := qdrant.New(endpoint, "", client, binding)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		request, _ := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint+"/collections/"+binding.Collection, nil)
		response, err := client.Do(request)
		if err != nil {
			t.Error("cleanup:", err)
			return
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Error("cleanup status", response.StatusCode)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = store.EnsureCollection(ctx); err != nil {
		t.Fatal(err)
	}
	visibility := &pb.Visibility{FromSeq: 7}
	interval := &pb.LegalInterval{Start: &pb.DateAssertion{Knowledge: pb.DateKnowledge_DATE_KNOWLEDGE_UNKNOWN}, End: &pb.DateAssertion{Knowledge: pb.DateKnowledge_DATE_KNOWLEDGE_UNKNOWN}}
	point := qdrant.Point{ID: "b1786f40-3fa9-4b84-bcb9-71b05ad3c289", Record: &pb.IndexRecord{
		Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: input.Context.CorpusId, RecordId: "index:one", Visibility: proto.Clone(visibility).(*pb.Visibility)}, GenerationId: input.Generation.Meta.RecordId, ChunkId: "chunk:one", ProvisionVersionRefs: []string{"version:one"},
		DenseVector: &pb.DenseVector{Values: []float32{1, 0}, Dimensions: 2, ModelId: input.Generation.DenseManifest.ModelId}, SparseVector: &pb.SparseVector{Indices: []uint32{2}, Values: []float32{1}},
		FilterMetadata: &pb.FilterMetadata{Visibility: visibility, ProvisionFilters: []*pb.IndexProvisionFilter{{ProvisionVersionId: "version:one", RegulationId: "regulation:one", SourceBlobId: "blob:one", Jurisdiction: "ID", LegalInterval: interval, LegalStatus: pb.LegalStatus_LEGAL_STATUS_UNKNOWN}}},
		Dependencies:   &pb.DependencyManifest{ArtifactId: "dependency:one", ProducerManifest: &pb.ProducerManifest{Software: "fixture", Build: "test", SchemaVersion: 1, ConfigHash: input.Context.ConfigFingerprint}},
	}}
	if err = store.Upsert(ctx, []qdrant.Point{point}); err != nil {
		t.Fatal(err)
	}
	if err = store.VerifyPoints(ctx, []qdrant.Point{point}); err != nil {
		t.Fatal(err)
	}
	result, err := RetrieveDense(ctx, input, embed, store)
	if err != nil || len(result.Hits) != 1 || result.Hits[0].RecordID != "index:one" {
		t.Fatalf("actual dense retrieval: %v %v", result, err)
	}
	lexical, err := LoadLexicalRetriever(ctx, reader, input.Generation, store, nil, nil, 1<<20, domain.DefaultWireLimits)
	if err != nil {
		t.Fatal(err)
	}
	input.Question = "pasal"
	result, err = lexical.Retrieve(ctx, input)
	if err != nil || len(result.Hits) != 1 || result.Hits[0].RecordID != "index:one" || result.Ranking.Kind != pb.RetrieverKind_RETRIEVER_KIND_BM25 {
		t.Fatalf("actual lexical retrieval: %v %v", result, err)
	}
	input.Context.SnapshotRef.Sequence = 6
	input.Scope.SnapshotSeq = 6
	result, err = RetrieveDense(ctx, input, embed, store)
	if err != nil || len(result.Hits) != 0 {
		t.Fatalf("future point leaked into old snapshot: %v %v", result, err)
	}
	result, err = lexical.Retrieve(ctx, input)
	if err == nil || result != nil {
		t.Fatalf("future statistics accepted for old snapshot: %v %v", result, err)
	}
	t.Log("real Qdrant create/upsert/exact readback/dense and lexical retrieval/snapshot exclusion passed; synthetic model/data; verified lexical artifact binding")
}
