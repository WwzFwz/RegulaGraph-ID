// Exercises query-to-index integration with controllable inference/backend ports.
// Tests distinguish empty retrieval from failures and enforce model/snapshot pins,
// deadlines and original query preservation. No fixture proves relevance or latency.
package retrieval

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/qdrant"
	"regulagraph.local/server/internal/domain"
)

type embeddingFunc func(context.Context, *pb.EmbedBatchRequest) (*pb.EmbedBatchResponse, error)

func (f embeddingFunc) EmbedBatch(c context.Context, r *pb.EmbedBatchRequest) (*pb.EmbedBatchResponse, error) {
	return f(c, r)
}

type indexDouble struct {
	binding qdrant.Binding
	hits    []qdrant.Hit
	calls   int
	failure error
}

func (i *indexDouble) Binding() qdrant.Binding { return i.binding }
func (i *indexDouble) SearchDense(ctx context.Context, _ []float32, _ qdrant.SearchScope) ([]qdrant.Hit, error) {
	i.calls++
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return i.hits, i.failure
}
func (i *indexDouble) SearchSparse(ctx context.Context, _ *pb.SparseVector, scope qdrant.SearchScope) ([]qdrant.Hit, error) {
	return i.SearchDense(ctx, nil, scope)
}

func searchFixture() (SearchInput, *indexDouble, embeddingFunc) {
	hash := &pb.ContentHash{Sha256: strings.Repeat("a", 64)}
	artifact := &pb.ArtifactRef{ArtifactId: "artifact:one", ContentHash: hash, StorageKey: "objects/one", MediaType: "application/x-protobuf", SchemaVersion: 1}
	generation := &pb.IndexGeneration{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: "corpus:one", RecordId: "generation:one"},
		DenseManifest: &pb.ModelManifest{ModelId: "model:one", Version: "1", WeightsHash: hash, TokenizerHash: hash, Task: pb.ModelTask_MODEL_TASK_EMBED,
			Dimensions: proto.Uint32(2), MaxTokens: 512, Precision: "fp32", Backend: "fixture", Normalization: "l2"},
		LexicalAnalyzer: artifact, LexicalDictionary: artifact, LexicalStatistics: artifact, OntologyVersion: "ontology:v1",
		FilterFormat: pb.IndexFilterFormat_INDEX_FILTER_FORMAT_PAIRED_PROVISION_V1, EmbeddingInputPolicy: "structure-labels-v1"}
	input := SearchInput{Context: &pb.RequestContext{SchemaVersion: 1, RequestId: "request:one", TraceId: "trace:one", CorpusId: "corpus:one", AuthScopeRef: "auth:one", ConfigFingerprint: hash,
		Deadline: timestamppb.New(time.Now().Add(time.Minute)), SnapshotRef: &pb.SnapshotRef{CorpusId: "corpus:one", SnapshotId: "snapshot:one", Sequence: 7, ManifestHash: hash, RepresentationGeneration: "generation:one"}},
		Question: "Pasal 12/2020 tidak wajib?", Generation: generation, Scope: qdrant.SearchScope{SnapshotSeq: 7, Limit: 10}}
	index := &indexDouble{binding: qdrant.Binding{CorpusID: "corpus:one", Collection: "one", Generation: proto.Clone(generation).(*pb.IndexGeneration)},
		hits: []qdrant.Hit{{PointID: "point:one", RecordID: "index:one", ChunkID: "chunk:one", ProvisionVersionIDs: []string{"version:one"}, Score: 0.8}}}
	embed := embeddingFunc(func(_ context.Context, r *pb.EmbedBatchRequest) (*pb.EmbedBatchResponse, error) {
		return &pb.EmbedBatchResponse{RequestId: r.Context.RequestId, Model: proto.Clone(r.Model).(*pb.ModelManifest),
			Results: []*pb.EmbeddingResult{{ItemId: "query", Result: &pb.EmbeddingResult_Embedding{Embedding: &pb.Embedding{Values: []float32{1, 0}, InputTokens: 8, Truncation: &pb.TruncationInfo{OriginalTokens: 8, RetainedTokens: 8}}}}}}, nil
	})
	return input, index, embed
}

func TestRetrieveDensePreservesQueryAndPins(t *testing.T) {
	input, index, embed := searchFixture()
	wrapped := embeddingFunc(func(ctx context.Context, r *pb.EmbedBatchRequest) (*pb.EmbedBatchResponse, error) {
		if r.Purpose != pb.EmbeddingPurpose_EMBEDDING_PURPOSE_QUERY || r.Items[0].Text != input.Question || !proto.Equal(r.Model, input.Generation.DenseManifest) {
			t.Fatal("query/model changed")
		}
		deadline, ok := ctx.Deadline()
		if !ok || deadline.After(input.Context.Deadline.AsTime()) {
			t.Fatal("deadline missing")
		}
		return embed(ctx, r)
	})
	out, err := RetrieveDense(context.Background(), input, wrapped, index)
	if err != nil || len(out.Ranking.Candidates) != 1 || index.calls != 1 {
		t.Fatalf("search: %v %v", out, err)
	}
	index.hits[0].ProvisionVersionIDs[0] = "mutated"
	if out.Hits[0].ProvisionVersionIDs[0] != "version:one" {
		t.Fatal("aliased backend hits")
	}
}

func TestRetrieveDenseRejectsInvalidInputBeforeInference(t *testing.T) {
	for name, mutate := range map[string]func(*SearchInput){
		"foreign corpus":   func(i *SearchInput) { i.Context.CorpusId = "foreign" },
		"wrong snapshot":   func(i *SearchInput) { i.Scope.SnapshotSeq++ },
		"wrong generation": func(i *SearchInput) { i.Context.SnapshotRef.RepresentationGeneration = "other" },
		"model drift":      func(i *SearchInput) { i.Generation.DenseManifest.Version = "other" },
		"expired":          func(i *SearchInput) { i.Context.Deadline = timestamppb.New(time.Now().Add(-time.Second)) },
		"empty":            func(i *SearchInput) { i.Question = "  " },
	} {
		t.Run(name, func(t *testing.T) {
			input, index, _ := searchFixture()
			mutate(&input)
			never := embeddingFunc(func(context.Context, *pb.EmbedBatchRequest) (*pb.EmbedBatchResponse, error) {
				t.Fatal("invalid request called model")
				return nil, nil
			})
			if _, err := RetrieveDense(context.Background(), input, never, index); err == nil {
				t.Fatal("invalid input accepted")
			}
		})
	}
}

func TestRetrieveDenseFailureIsNotEmptySuccess(t *testing.T) {
	for _, mode := range []string{"missing", "truncated", "zero", "backend", "duplicates", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			input, index, embed := searchFixture()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			wrapped := embeddingFunc(func(c context.Context, r *pb.EmbedBatchRequest) (*pb.EmbedBatchResponse, error) {
				v, e := embed(c, r)
				switch mode {
				case "missing":
					v.Results = nil
				case "truncated":
					v.Results[0].GetEmbedding().Truncation.Truncated = true
				case "zero":
					v.Results[0].GetEmbedding().Values = []float32{0, 0}
				case "cancelled":
					cancel()
				}
				return v, e
			})
			if mode == "backend" {
				index.failure = errors.New("unavailable")
			}
			if mode == "duplicates" {
				index.hits = append(index.hits, index.hits[0])
			}
			if out, err := RetrieveDense(ctx, input, wrapped, index); err == nil || out != nil {
				t.Fatal("failure became successful candidates")
			}
		})
	}
	input, index, embed := searchFixture()
	index.hits = nil
	if out, err := RetrieveDense(context.Background(), input, embed, index); err != nil || len(out.Hits) != 0 {
		t.Fatal("valid empty rejected", err)
	}
}

func TestLexicalOOVSkipsBackendAndPinsGeneration(t *testing.T) {
	input, index, _ := searchFixture()
	reader := lexicalArtifacts(t, input.Generation)
	index.binding.Generation = proto.Clone(input.Generation).(*pb.IndexGeneration)
	r, err := LoadLexicalRetriever(context.Background(), reader, input.Generation, index, nil, nil, 1<<20, domain.DefaultWireLimits)
	if err != nil {
		t.Fatal(err)
	}
	input.Question = "unknown"
	result, err := r.Retrieve(context.Background(), input)
	if err != nil || result.OOV != 1 || index.calls != 0 {
		t.Fatal("OOV was not explicit/no-I/O", err)
	}
	input.Question = "pasal"
	if _, err = r.Retrieve(context.Background(), input); err != nil || index.calls != 1 {
		t.Fatal("BM25 branch not called", err)
	}
	input.Generation = proto.Clone(input.Generation).(*pb.IndexGeneration)
	input.Generation.LexicalStatistics = proto.Clone(input.Generation.LexicalStatistics).(*pb.ArtifactRef)
	input.Generation.LexicalStatistics.ArtifactId = "other"
	if _, err = r.Retrieve(context.Background(), input); err == nil {
		t.Fatal("statistics drift accepted")
	}
}
