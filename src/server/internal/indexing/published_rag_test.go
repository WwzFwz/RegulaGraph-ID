// Connects the initial publication fixture to production lease, dense retrieval,
// source hydration and cited draft workflows using real PostgreSQL/Qdrant. Model
// output and token counts are synthetic: this verifies storage-to-answer identity
// and cleanup, not relevance, tokenizer parity, legal support or benchmarks.
package indexing

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/inference"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/adapters/qdrant"
	"regulagraph.local/server/internal/answering"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/retrieval"
	"regulagraph.local/server/internal/workflows"
)

type publishedEmbedding struct{ values []float32 }

func (e publishedEmbedding) EmbedBatch(_ context.Context, r *pb.EmbedBatchRequest) (*pb.EmbedBatchResponse, error) {
	return &pb.EmbedBatchResponse{RequestId: r.Context.RequestId, Model: proto.Clone(r.Model).(*pb.ModelManifest), Results: []*pb.EmbeddingResult{{ItemId: "query", Result: &pb.EmbeddingResult_Embedding{Embedding: &pb.Embedding{Values: append([]float32(nil), e.values...), InputTokens: 8, Truncation: &pb.TruncationInfo{OriginalTokens: 8, RetainedTokens: 8}}}}}}, nil
}

type publishedGenerator struct {
	evidenceID string
	calls      int
}

type publishedReranker struct{ calls int }

func (r *publishedReranker) RerankBatch(_ context.Context, req *pb.RerankBatchRequest) (*pb.RerankBatchResponse, error) {
	r.calls++
	out := &pb.RerankBatchResponse{RequestId: req.Context.RequestId, Model: proto.Clone(req.Model).(*pb.ModelManifest)}
	for i, pair := range req.Pairs {
		out.Results = append(out.Results, &pb.RerankItemResult{PairId: pair.PairId, Result: &pb.RerankItemResult_Score{Score: &pb.RerankScore{Score: float64(i), InputTokens: 12, Truncation: &pb.TruncationInfo{OriginalTokens: 12, RetainedTokens: 12}}}})
	}
	return out, nil
}

func (g *publishedGenerator) Generate(_ context.Context, _ inference.StructuredRequest) (inference.StructuredResponse, error) {
	g.calls++
	raw, err := json.Marshal(map[string]any{"status": "answer", "claims": []any{map[string]any{"text": "Perizinan usaha memerlukan bukti.", "evidence_ids": []string{g.evidenceID}}}})
	return inference.StructuredResponse{JSON: raw, InputTokens: 100, OutputTokens: 20}, err
}

func verifyPublishedRAG(t *testing.T, ctx context.Context, repo *postgres.Repository, physical *qdrant.Store, p *PreparedInitialIndex, artifacts indexMemoryArtifacts, producer *pb.ProducerManifest) {
	t.Helper()
	hash := &pb.ContentHash{Sha256: strings.Repeat("a", 64)}
	model := &pb.ModelManifest{ModelId: "generator:fixture", Version: "1", WeightsHash: hash, TokenizerHash: hash, Task: pb.ModelTask_MODEL_TASK_GENERATE, MaxTokens: 32768, Precision: "fp32", Backend: "fixture", PromptHash: answering.DraftPromptHash()}
	provider := &publishedGenerator{evidenceID: p.records[0].Meta.RecordId}
	rankProvider := &publishedReranker{}
	ranker, err := retrieval.NewEvidenceReranker(rankProvider, retrieval.EvidenceRerankConfig{Model: &pb.ModelManifest{ModelId: "reranker:fixture", Version: "1", WeightsHash: hash, TokenizerHash: hash, Task: pb.ModelTask_MODEL_TASK_RERANK, MaxTokens: 8192, Precision: "fp32", Backend: "fixture"}, MaximumCandidates: 4, PairsPerBatch: 2, MaximumRequestBytes: 4 << 20})
	if err != nil {
		t.Fatal(err)
	}
	generator, err := answering.NewDraftGenerator(provider, func(context.Context, inference.StructuredRequest) (uint64, error) { return 100, nil }, answering.DraftGeneratorConfig{Model: model, Producer: &pb.ProducerManifest{Software: "fixture", Build: "test", SchemaVersion: 1, ConfigHash: hash, Models: []*pb.ModelManifest{model}, PromptHashes: []*pb.ContentHash{answering.DraftPromptHash()}}, MaximumInputBytes: 1 << 20, MaximumOutputBytes: 64 << 10, MaximumClaims: 8, MaximumCitations: 32, MaximumConcurrent: 1, OutputTokens: 512, AllowUnreviewedDrafts: true})
	if err != nil {
		t.Fatal(err)
	}
	var observed domain.SnapshotPin
	var prepared *workflows.PreparedQuery
	session := &workflows.RAGSession{Store: repo, OwnerID: "reader:published-rag", MaximumDuration: 20 * time.Second, SearchLimit: 2,
		Factory: func(c context.Context, index *domain.PinnedIndex) (*workflows.RAGWorkflow, error) {
			observed = index.Pin
			if prepared == nil {
				var err error
				prepared, err = workflows.PreparePublishedQuery(c, index, repo, artifacts, publishedEmbedding{p.records[0].DenseVector.Values},
					&workflows.EvidenceAnswerWorkflow{Generator: generator, ContextTokenizer: hash, MaximumContextTokens: 30000, MaximumEvidence: 2, CountContext: func(_ context.Context, s string) (uint64, error) { return uint64(len(s)), nil }},
					workflows.PublishedQueryConfig{Reranker: ranker, QdrantCredentials: map[string]string{physical.Endpoint(): ""}, HTTPClient: &http.Client{Timeout: 10 * time.Second}, Profile: pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_RAG, MaximumLexicalBytes: 16 << 20,
						Fusion: retrieval.RRFConfig{K: 60, MaximumPerBranch: 2, MaximumTotalInputs: 4, Weights: map[pb.RetrieverKind]float64{pb.RetrieverKind_RETRIEVER_KIND_DENSE: 1, pb.RetrieverKind_RETRIEVER_KIND_BM25: 1}}, Hydration: retrieval.HydrationConfig{MaximumCandidates: 4, MaximumArtifactBytes: 16 << 20, MaximumEvidenceBytes: 1 << 20, Producer: producer}})
				if err != nil {
					return nil, err
				}
			}
			return prepared.Bind(c, index)
		}}
	request := &pb.QuestionRequest{Question: "Apa ketentuan perizinan?", CorpusId: p.snapshot.CorpusId, ResponseMode: pb.ResponseMode_RESPONSE_MODE_COMPLETE, RequestedProfile: pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_RAG, TemporalScope: &pb.TemporalScope{Mode: pb.TemporalMode_TEMPORAL_MODE_AS_OF, EffectiveAt: &pb.CalendarDate{Year: 2026, Month: 1, Day: 1}, UnresolvedPolicy: pb.UnresolvedPolicy_UNRESOLVED_POLICY_REPORT}}
	call := &pb.RequestContext{SchemaVersion: 1, RequestId: "request:published-rag", TraceId: "trace:published-rag", CorpusId: p.snapshot.CorpusId, AuthScopeRef: "auth:fixture", ConfigFingerprint: hash, Deadline: timestamppb.New(time.Now().Add(time.Minute))}
	result, err := session.AnswerQuestion(ctx, request, call)
	if err != nil {
		t.Fatal(err)
	}
	if provider.calls != 1 || len(result.Answer.Draft.Answer.Citations) == 0 || len(result.Evidence.Items) != 2 || !proto.Equal(result.Evidence.Snapshot, p.snapshot) {
		t.Fatal("published search did not reach cited draft")
	}
	if rankProvider.calls != 1 || result.Reranking == nil || len(result.Reranking.Scores) != 2 || result.Reranking.Scores[0].Score <= result.Reranking.Scores[1].Score {
		t.Fatal("published answer skipped reranking")
	}
	if result.Answer.Draft.Answer.Claims[0].SupportStatus != pb.SupportStatus_SUPPORT_STATUS_UNREVIEWED || result.Answer.Context.Completeness != pb.Completeness_COMPLETENESS_PARTIAL {
		t.Fatal("fixture draft overstated support/completeness")
	}
	for _, citation := range result.Answer.Draft.Answer.Citations {
		if citation.EvidenceId != provider.evidenceID || citation.SourceUrl != "https://example.org/fixture.pdf" {
			t.Fatal("draft citation lost evidence/source identity", citation)
		}
		matched := false
		for _, evidence := range result.Evidence.Items {
			if evidence.Meta.RecordId == citation.EvidenceId {
				for _, ref := range evidence.SourceRefs {
					matched = matched || ref.ProvisionVersionId == citation.ProvisionVersionId
				}
			}
		}
		if !matched {
			t.Fatal("draft citation names a foreign provision version")
		}
	}
	if _, err = repo.LoadPinnedIndex(ctx, observed); err == nil {
		t.Fatal("completed RAG request leaked read lease")
	}
	searched, err := session.SearchQuestion(ctx, request, call)
	if err != nil || searched == nil || searched.Answer != nil || len(searched.Evidence.Items) != 2 || len(searched.Search.Branches) != 2 || provider.calls != 1 {
		t.Fatal("warm evidence query changed generation/model behavior", err)
	}
	if rankProvider.calls != 2 || searched.Reranking == nil || len(searched.Reranking.Scores) != 2 {
		t.Fatal("warm evidence query skipped reranking")
	}
	if _, err = repo.LoadPinnedIndex(ctx, observed); err == nil {
		t.Fatal("evidence query leaked lease")
	}
}
