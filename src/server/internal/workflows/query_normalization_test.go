// Verifies optional mechanical search text reaches only dense/BM25 while graph
// linking, hydration and draft generation retain the original question. Trace
// ownership, invalid-input admission and profile semantics use production flows
// with synthetic backend/model fixtures, not retrieval-quality benchmarks.
package workflows

import (
	"context"
	"encoding/json"
	"testing"

	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/retrieval"
	"regulagraph.local/server/internal/retrieval/query"
)

func TestRAGQuestionNormalizationPreservesOriginalBoundaries(t *testing.T) {
	w, request, input, provider, _ := graphRAGFixture(t)
	const original = "  Pasal\t11 bukan 1; Cafe\u0301 ‘izin  usaha’  "
	const search = "Pasal 11 bukan 1; Café ‘izin  usaha’"
	request.Question, input.Question = original, original
	w.Search.Normalization = query.MechanicalQuestion
	seen := make(chan string, 3)
	wrap := func(branch CandidateBranch, want, kind string) CandidateBranch {
		return func(ctx context.Context, in retrieval.SearchInput) (*retrieval.BranchOutput, error) {
			if in.Question != want {
				t.Errorf("%s received %q; want %q", kind, in.Question, want)
			}
			seen <- kind
			return branch(ctx, in)
		}
	}
	w.Search.Dense = wrap(w.Search.Dense, search, "dense")
	w.Search.Lexical = wrap(w.Search.Lexical, search, "bm25")
	w.Search.Graph = wrap(w.Search.Graph, original, "graph")
	hydrate := w.Hydrate
	w.Hydrate = func(ctx context.Context, q *pb.QuestionRequest, found *CandidateSearchResult) (*HydratedCandidates, error) {
		if q.Question != original || found.Normalization.Original != original || found.Normalization.Search != search {
			t.Fatal("hydration lost original or search trace")
		}
		found.Normalization.Edits[0].Kind = "caller-mutation"
		found.Normalization.Search = "caller-mutation"
		return hydrate(ctx, q, found)
	}
	result, err := w.AnswerPinnedQuestion(context.Background(), request, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 3 || result.Search.Profile != pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_GRAPH_RAG || len(result.Search.Branches) != 3 {
		t.Fatal("normalization changed branch plan")
	}
	if result.Search.Normalization.Search != search || result.Search.Normalization.Edits[0].Kind != "whitespace" || request.Question != original || input.Question != original {
		t.Fatal("caller mutation leaked or input changed")
	}
	var payload struct {
		Question string `json:"question"`
	}
	if err = json.Unmarshal([]byte(provider.request.Text), &payload); err != nil || payload.Question != original {
		t.Fatal("generator received rewritten question", err)
	}
}

func TestCandidateNormalizationRejectsBeforeBranches(t *testing.T) {
	for _, tc := range []struct {
		text string
		mode query.NormalizationMode
	}{{"\xff", query.MechanicalQuestion}, {"x\x00", query.MechanicalQuestion}, {"izin", "guess"}} {
		s, input := candidateWorkflowFixture()
		input.Question, s.Normalization = tc.text, tc.mode
		s.Dense = func(context.Context, retrieval.SearchInput) (*retrieval.BranchOutput, error) {
			t.Error("invalid normalization reached a branch")
			return nil, nil
		}
		if _, err := s.SearchCandidates(context.Background(), input, pb.RetrievalProfile_RETRIEVAL_PROFILE_VECTOR_RAG); err == nil {
			t.Fatal("invalid normalization admitted")
		}
	}
}
