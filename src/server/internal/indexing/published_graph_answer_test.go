// Extends actual published graph/source hydration to production context packing,
// structured draft generation and citation validation. Graph relation bytes must
// reach the provider prompt, source URLs must remain authenticated, and unresolved
// applicability remains PARTIAL. Synthetic generator/token counts do not establish
// model quality, legal correctness, tokenizer parity or required performance gates.
package indexing

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/protobuf/types/known/timestamppb"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/inference"
	"regulagraph.local/server/internal/answering"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/retrieval/graph"
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

func checkPublishedGraphAnswer(t *testing.T, ctx context.Context, pin domain.SnapshotPin, scope string, request *pb.QuestionRequest, paths *graph.TraversalResult, hydrated *workflows.HydratedGraph) {
	t.Helper()
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
}
