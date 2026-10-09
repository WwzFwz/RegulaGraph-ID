// Extends actual published graph/source hydration to production context packing,
// structured draft generation and citation validation. Graph relation bytes must
// reach the provider prompt, source URLs must remain authenticated, and unresolved
// applicability remains PARTIAL. Synthetic generator/token counts do not establish
// model quality, legal correctness, tokenizer parity or required performance gates.
package indexing

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/inference"
	"regulagraph.local/server/internal/adapters/neo4j"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/adapters/qdrant"
	"regulagraph.local/server/internal/answering"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/retrieval"
	"regulagraph.local/server/internal/retrieval/graph"
	"regulagraph.local/server/internal/retrieval/query"
	"regulagraph.local/server/internal/workflows"
)

type publishedGraphGenerator struct {
	publishedGenerator
	prompt string
}

func (g *publishedGraphGenerator) Generate(ctx context.Context, r inference.StructuredRequest) (inference.StructuredResponse, error) {
	g.prompt = r.Text
	return g.publishedGenerator.Generate(ctx, r)
}

func checkPublishedGraphAnswer(t *testing.T, ctx context.Context, repo *postgres.Repository, backend *neo4j.Store, index *domain.PinnedIndex, scope string, request *pb.QuestionRequest, paths *graph.TraversalResult, hydrated *workflows.HydratedGraph, artifacts indexMemoryArtifacts) {
	t.Helper()
	pin := index.Pin
	plan, err := answering.NewGraphContext(paths, hydrated.Mapping)
	if err != nil {
		t.Fatal("native graph render plan", err)
	}
	hash := paths.Snapshot.ManifestHash
	model := &pb.ModelManifest{ModelId: "generator:graph-fixture", Version: "1", WeightsHash: hash, TokenizerHash: hash, Task: pb.ModelTask_MODEL_TASK_GENERATE, MaxTokens: 32768, Precision: "fp32", Backend: "fixture", PromptHash: answering.DraftPromptHash()}
	provider := &publishedGraphGenerator{publishedGenerator: publishedGenerator{evidenceID: hydrated.Mapping.Bundle.Items[0].Meta.RecordId}}
	generator, err := answering.NewDraftGenerator(provider, func(context.Context, inference.StructuredRequest) (uint64, error) { return 100, nil }, answering.DraftGeneratorConfig{Model: model, Producer: &pb.ProducerManifest{Software: "fixture", Build: "graph-answer", SchemaVersion: 1, ConfigHash: hash, Models: []*pb.ModelManifest{model}, PromptHashes: []*pb.ContentHash{answering.DraftPromptHash()}}, MaximumInputBytes: 1 << 20, MaximumOutputBytes: 64 << 10, MaximumClaims: 8, MaximumCitations: 32, MaximumConcurrent: 1, OutputTokens: 512, AllowUnreviewedDrafts: true})
	if err != nil {
		t.Fatal(err)
	}
	w := &workflows.EvidenceAnswerWorkflow{Generator: generator, ContextTokenizer: hash, MaximumContextTokens: 30000, MaximumEvidence: 256, CountContext: func(_ context.Context, s string) (uint64, error) { return uint64(len(s)), nil }}
	call := &pb.RequestContext{SchemaVersion: 1, RequestId: "request:published-graph-answer", TraceId: "trace:published-graph-answer", CorpusId: pin.CorpusID, AuthScopeRef: scope, ConfigFingerprint: hash, SnapshotRef: paths.Snapshot, Deadline: timestamppb.New(pin.ExpiresAt)}
	got, err := w.AnswerEvidence(ctx, request, call, hydrated.Mapping.Bundle, hydrated.SourceURLs, plan)
	if err != nil {
		t.Fatal("native graph-to-answer", err)
	}
	if provider.calls != 1 || !strings.Contains(provider.prompt, "Graph source relation") || len(got.Draft.Answer.Citations) == 0 || got.Context.Completeness != pb.Completeness_COMPLETENESS_PARTIAL || got.Draft.Answer.SemanticStatus != pb.SemanticStatus_SEMANTIC_STATUS_PARTIAL {
		t.Fatal("graph prompt/citations/partial state lost", got)
	}
	for _, citation := range got.Draft.Answer.Citations {
		if citation.SourceUrl != "https://example.org/fixture.pdf" || citation.EvidenceId != provider.evidenceID {
			t.Fatal("graph citation lost source", citation)
		}
	}
	if err = answering.ValidateGroundedAnswer(got.Draft.Answer, got.Context, hydrated.Mapping.Bundle, hydrated.SourceURLs, plan); err != nil {
		t.Fatal(err)
	}
	t.Log("actual graph traversal/source text rendered into production generator prompt; cited fixture draft retains unresolved graph dependencies")
	for _, profile := range []pb.RetrievalProfile{pb.RetrievalProfile_RETRIEVAL_PROFILE_GRAPH_RAG, pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_GRAPH_RAG} {
		seedResolver, err := workflows.NewQueryGraphSeedResolver(repo, query.EntityLinkingPolicy{Namespaces: []query.EntityNamespace{{EntityType: "organization", Scope: "ID:national"}}, MaximumQueryBytes: 4096, MaximumPhraseTokens: 4, MaximumPhrases: 128, MaximumLookups: 128, MaximumAliasesPerLookup: 32, MaximumSeeds: 64})
		if err != nil {
			t.Fatal(err)
		}
		vector := make([]float32, index.Binding.Generation.DenseManifest.GetDimensions())
		vector[0] = 1
		var embed retrieval.EmbeddingClient = publishedEmbedding{vector}
		if profile == pb.RetrievalProfile_RETRIEVAL_PROFILE_GRAPH_RAG {
			embed = nil
		}
		rankProvider := &publishedReranker{}
		ranker, err := retrieval.NewEvidenceReranker(rankProvider, retrieval.EvidenceRerankConfig{Model: &pb.ModelManifest{ModelId: "reranker:graph-fixture", Version: "1", WeightsHash: hash, TokenizerHash: hash, Task: pb.ModelTask_MODEL_TASK_RERANK, MaxTokens: 8192, Precision: "fp32", Backend: "fixture"}, MaximumCandidates: 32, PairsPerBatch: 8, MaximumRequestBytes: 4 << 20})
		if err != nil {
			t.Fatal(err)
		}
		prepared, err := workflows.PreparePublishedQuery(ctx, index, repo, artifacts, embed, w, workflows.PublishedQueryConfig{
			Profile: profile, HTTPClient: &http.Client{Timeout: 5 * time.Second}, QdrantCredentials: map[string]string{index.Binding.Endpoint: ""}, MaximumLexicalBytes: 16 << 20, Reranker: ranker,
			Fusion:    retrieval.RRFConfig{K: 60, MaximumPerBranch: 8, MaximumTotalInputs: 24, Weights: map[pb.RetrieverKind]float64{pb.RetrieverKind_RETRIEVER_KIND_DENSE: 1, pb.RetrieverKind_RETRIEVER_KIND_BM25: 1, pb.RetrieverKind_RETRIEVER_KIND_GRAPH: 1}},
			Hydration: retrieval.HydrationConfig{MaximumCandidates: 32, MaximumArtifactBytes: 16 << 20, MaximumEvidenceBytes: 1 << 20, Producer: hydrated.Mapping.Bundle.RetrievalManifest},
			Graph:     &workflows.GraphQueryConfig{Backend: backend, Seeds: seedResolver, Traversal: graph.TraversalConfig{MaximumHops: 3, MaximumPaths: 100, Read: domain.GraphReadLimits{Assertions: 128, Supports: 256, Bytes: 1 << 20}}},
		})
		if err != nil {
			t.Fatal("prepare graph profile", profile, err)
		}
		changed := *index
		changed.Snapshot = proto.Clone(index.Snapshot).(*pb.SnapshotRef)
		changed.Snapshot.SnapshotId = "snapshot:changed"
		changed.Pin.SnapshotID = changed.Snapshot.SnapshotId
		if _, err := prepared.Bind(ctx, &changed); err == nil {
			t.Fatal("graph snapshot rollover reused old prepared backend")
		}
		ownedInput := *index
		ownedInput.Snapshot = proto.Clone(index.Snapshot).(*pb.SnapshotRef)
		ownedInput.Binding.Generation = proto.Clone(index.Binding.Generation).(*pb.IndexGeneration)
		bound, err := prepared.Bind(ctx, &ownedInput)
		if err != nil {
			t.Fatal(err)
		}
		ownedInput.Snapshot.SnapshotId = "snapshot:caller-mutated"
		ownedInput.Binding.Generation.Meta.RecordId = "generation:caller-mutated"
		q := proto.Clone(request).(*pb.QuestionRequest)
		q.RequestedProfile = profile
		// Fixture aliases are the first two ASCII bytes of the source chunk.
		// The real linker must discover this alias from question text, not a supplied ID.
		q.Question = "Apa hubungan " + hydrated.Mapping.Bundle.Items[0].Text[:1] + "?"
		result, err := bound.AnswerPinnedQuestion(ctx, q, retrieval.SearchInput{Context: call, Question: q.Question, Generation: index.Binding.Generation, Scope: qdrant.SearchScope{SnapshotSeq: index.Snapshot.Sequence, Limit: 8}})
		if err != nil {
			t.Fatal("published graph RAG", profile, err)
		}
		branches := 1
		if profile == pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_GRAPH_RAG {
			branches = 3
		}
		if len(result.Search.Branches) != branches || result.Search.Graph == nil || result.Reranking == nil || rankProvider.calls == 0 || len(result.Answer.Draft.Answer.Citations) == 0 {
			t.Fatal("graph fusion/reranking/citation incomplete", profile, result)
		}
		graphOrigin := false
		linked := false
		for _, b := range result.Search.Branches {
			if b.Linking != nil {
				linked = b.Linking.Method == query.AliasLinkingMethod && len(b.Linking.CanonicalIDs) > 0 && len(b.Linking.Matches) > 0
			}
		}
		if !linked {
			t.Fatal("query did not use pinned alias linking")
		}
		if result.Search.Graph.FrontierExhausted {
			t.Fatal("unreviewed query aliases lost incomplete status")
		}
		for _, p := range result.Search.Graph.Paths {
			if p.FrontierExhausted {
				t.Fatal("path contradicts unresolved query linking")
			}
		}
		for _, item := range result.Evidence.Items {
			for _, origin := range item.CandidateProvenance {
				graphOrigin = graphOrigin || origin.Retriever == pb.RetrieverKind_RETRIEVER_KIND_GRAPH
			}
		}
		if !graphOrigin || !strings.Contains(provider.prompt, "Graph source relation") {
			t.Fatal("graph branch disappeared from cited draft")
		}
		t.Logf("published %s: %d branches fused, %d source evidence items reranked, graph prompt and source citations retained", profile, branches, len(result.Evidence.Items))
		if profile == pb.RetrievalProfile_RETRIEVAL_PROFILE_GRAPH_RAG {
			q.Question = "aliasbelumtersedia"
			calls := provider.calls
			empty, err := bound.AnswerPinnedQuestion(ctx, q, retrieval.SearchInput{Context: call, Question: q.Question, Generation: index.Binding.Generation, Scope: qdrant.SearchScope{SnapshotSeq: index.Snapshot.Sequence, Limit: 8}})
			if err != nil || len(empty.Evidence.Items) != 0 || len(empty.Evidence.MissingDependencies) == 0 || empty.Answer.Draft.Answer.SemanticStatus != pb.SemanticStatus_SEMANTIC_STATUS_ABSTAIN || provider.calls != calls {
				t.Fatal("unresolved seed silently fell back or reached model", err)
			}
		}
	}
}
