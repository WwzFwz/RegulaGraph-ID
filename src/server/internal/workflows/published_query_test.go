// Checks catalog-controlled route selection, read-only preparation, isolated
// generation resources and evidence-only lease ownership. HTTP/storage doubles
// prove boundary failures before model use; real-store composition is in indexing.
// Benchmark and quality gates remain REQUIRED_UNMEASURED.
package workflows

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/retrieval"
)

type queryCatalogDouble struct{ *ragLeaseStore }

func (q queryCatalogDouble) LoadPinnedIndexRecords(context.Context, domain.SnapshotPin, []string, uint64) ([]domain.IndexCatalogRecord, error) {
	return nil, errors.New("unexpected record read")
}
func (q queryCatalogDouble) LoadArtifact(context.Context, string, string) (*pb.ArtifactRef, error) {
	return nil, errors.New("unexpected artifact read")
}

type queryReaderDouble struct{}

func (queryReaderDouble) ReadVerified(context.Context, *pb.ArtifactRef, uint64) ([]byte, error) {
	return nil, errors.New("unexpected artifact bytes")
}

type queryEmbeddingDouble struct{}

func (queryEmbeddingDouble) EmbedBatch(context.Context, *pb.EmbedBatchRequest) (*pb.EmbedBatchResponse, error) {
	return nil, errors.New("unexpected inference")
}

func TestPublishedQuerySelectsOnlyAuthorizedCatalogRoute(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet || r.URL.Path != "/collections/catalog_collection" || r.Header.Get("api-key") != "catalog-key" {
			t.Error("wrong query route or mutation", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"status":"ok","result":{"config":{"params":{"vectors":{"dense":{"size":2,"distance":"Cosine"}},"sparse_vectors":{"bm25":{}}},"metadata":{"regulagraph_corpus_id":"corpus:one","regulagraph_generation_id":"generation:one","regulagraph_filter_format":"PAIRED_PROVISION_V1","regulagraph_input_policy":"structure-labels-v1"}},"payload_schema":{"corpus_id":{"data_type":"keyword"},"generation_id":{"data_type":"keyword"},"from_seq":{"data_type":"integer"},"to_seq":{"data_type":"integer"},"provision_filters[].provision_version_id":{"data_type":"keyword"}}}}`))
	}))
	defer server.Close()
	w, _, input, _ := ragFixture(t)
	index := &domain.PinnedIndex{Snapshot: input.Context.SnapshotRef, Pin: domain.SnapshotPin{CorpusID: "corpus:one", SnapshotID: "snapshot:one", Sequence: 7, ExpiresAt: time.Now().Add(time.Minute)}, Binding: domain.IndexCatalogBinding{PublicationID: "publication:one", Fence: 1, Endpoint: server.URL, Collection: "catalog_collection", Generation: sessionGeneration()}}
	catalog := queryCatalogDouble{&ragLeaseStore{index: index}}
	config := PublishedQueryConfig{QdrantCredentials: map[string]string{}, HTTPClient: server.Client(), Profile: pb.RetrievalProfile_RETRIEVAL_PROFILE_VECTOR_RAG, Fusion: w.Search.Fusion, Hydration: retrieval.HydrationConfig{MaximumCandidates: 20, MaximumArtifactBytes: 1 << 20, MaximumEvidenceBytes: 1 << 20, Producer: &pb.ProducerManifest{Software: "fixture", Build: "test", SchemaVersion: 1, ConfigHash: input.Context.ConfigFingerprint}}}
	if _, err := PreparePublishedQuery(context.Background(), index, catalog, queryReaderDouble{}, queryEmbeddingDouble{}, nil, config); err == nil || requests != 0 {
		t.Fatal("unapproved catalog origin contacted")
	}
	config.QdrantCredentials[server.URL] = "catalog-key"
	prepared, err := PreparePublishedQuery(context.Background(), index, catalog, queryReaderDouble{}, queryEmbeddingDouble{}, nil, config)
	if err != nil {
		t.Fatal(err)
	}
	first, err := prepared.Bind(context.Background(), index)
	if err != nil {
		t.Fatal(err)
	}
	first.Search.Fusion.Weights[pb.RetrieverKind_RETRIEVER_KIND_DENSE] = 9
	config.Fusion.Weights[pb.RetrieverKind_RETRIEVER_KIND_DENSE] = 8
	second, err := prepared.Bind(context.Background(), index)
	if err != nil || second.Search.Fusion.Weights[pb.RetrieverKind_RETRIEVER_KIND_DENSE] != 1 || requests != 1 {
		t.Fatal("warm binding reloaded IO or shared mutable config", err, requests)
	}
	wrong := *index
	wrong.Binding.Collection = "different"
	if _, err = prepared.Bind(context.Background(), &wrong); err == nil {
		t.Fatal("different catalog route reused resources")
	}
	wrong = *index
	wrong.Binding.Generation = proto.Clone(index.Binding.Generation).(*pb.IndexGeneration)
	wrong.Binding.Generation.DenseManifest.Version = "changed"
	if _, err = prepared.Bind(context.Background(), &wrong); err == nil {
		t.Fatal("model drift reused resources")
	}
}

func TestSearchSessionReturnsEvidenceWithoutGenerator(t *testing.T) {
	w, request, input, provider := ragFixture(t)
	w.Answer = nil
	store := &ragLeaseStore{index: &domain.PinnedIndex{Snapshot: input.Context.SnapshotRef, Binding: domain.IndexCatalogBinding{PublicationID: "publication:one", Fence: 1, Endpoint: "http://fixture", Collection: "fixture", Generation: sessionGeneration()}}}
	call := proto.Clone(input.Context).(*pb.RequestContext)
	call.SnapshotRef = nil
	session := &RAGSession{Store: store, OwnerID: "reader:query", MaximumDuration: time.Second * 10, SearchLimit: 10, Factory: func(context.Context, *domain.PinnedIndex) (*RAGWorkflow, error) { return w, nil }}
	result, err := session.SearchQuestion(context.Background(), request, call)
	if err != nil || result == nil || result.Answer != nil || len(result.Evidence.Items) != 1 || provider.calls != 0 || store.releases != 1 {
		t.Fatal("evidence query lifecycle", err)
	}
	w.profile = pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_RAG
	store.releases = 0
	if _, err = session.SearchQuestion(context.Background(), request, call); err == nil || provider.calls != 0 || store.releases != 1 {
		t.Fatal("profile drift accepted or leaked lease", err)
	}
}
