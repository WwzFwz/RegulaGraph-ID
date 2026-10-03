// Verifies concurrent candidate orchestration, explicit profile admission and
// failure propagation. Test doubles validate control flow, not backend readiness,
// source authenticity, retrieval relevance or required performance benchmarks.
package workflows

import (
	"context"
	"errors"
	"testing"
	"time"

	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/qdrant"
	"regulagraph.local/server/internal/retrieval"
)

func candidateWorkflowFixture() (*CandidateSearch, retrieval.SearchInput) {
	branch := func(kind pb.RetrieverKind) CandidateBranch {
		return func(context.Context, retrieval.SearchInput) (*retrieval.BranchOutput, error) {
			return &retrieval.BranchOutput{Ranking: retrieval.RankedBranch{Kind: kind, Candidates: []*pb.Candidate{{EvidenceKey: "index:one", Retriever: kind, RawScore: 1, Rank: 1, Representation: "fixture", FilterDecisions: []*pb.FilterDecision{{Rule: "snapshot", Accepted: true}}}}},
				Hits: []qdrant.Hit{{PointID: "point:one", RecordID: "index:one", ChunkID: "chunk:one", ProvisionVersionIDs: []string{"version:one"}}}}, nil
		}
	}
	s := &CandidateSearch{Dense: branch(pb.RetrieverKind_RETRIEVER_KIND_DENSE), Lexical: branch(pb.RetrieverKind_RETRIEVER_KIND_BM25), Fusion: retrieval.RRFConfig{K: 60, MaximumPerBranch: 10, MaximumTotalInputs: 20,
		Weights: map[pb.RetrieverKind]float64{pb.RetrieverKind_RETRIEVER_KIND_DENSE: 1, pb.RetrieverKind_RETRIEVER_KIND_BM25: 1}}}
	return s, retrieval.SearchInput{Context: &pb.RequestContext{SnapshotRef: &pb.SnapshotRef{SnapshotId: "snapshot:one"}}, Generation: &pb.IndexGeneration{}, Scope: qdrant.SearchScope{Limit: 10}}
}

func TestCandidateSearchParallelFusion(t *testing.T) {
	s, input := candidateWorkflowFixture()
	dense, lexical := s.Dense, s.Lexical
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	wrap := func(branch CandidateBranch) CandidateBranch {
		return func(ctx context.Context, input retrieval.SearchInput) (*retrieval.BranchOutput, error) {
			entered <- struct{}{}
			select {
			case <-release:
				return branch(ctx, input)
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
	s.Dense = wrap(dense)
	s.Lexical = wrap(lexical)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		result, err := s.SearchCandidates(ctx, input, pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_RAG)
		if err == nil && (len(result.Candidates) != 1 || len(result.Candidates[0].Provenance) != 2) {
			err = errors.New("lost fusion provenance")
		}
		done <- err
	}()
	for range 2 {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("branches did not start concurrently")
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestCandidateSearchFailureCancelsSibling(t *testing.T) {
	s, input := candidateWorkflowFixture()
	started := make(chan struct{})
	cancelled := make(chan struct{})
	failure := errors.New("backend failed")
	s.Dense = func(context.Context, retrieval.SearchInput) (*retrieval.BranchOutput, error) {
		<-started
		return nil, failure
	}
	s.Lexical = func(ctx context.Context, _ retrieval.SearchInput) (*retrieval.BranchOutput, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if result, err := s.SearchCandidates(ctx, input, pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_RAG); !errors.Is(err, failure) || result != nil {
		t.Fatal("failure converted to fallback", err)
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("sibling not drained")
	}
}

func TestCandidateSearchRejectsProfilesAndConflictingIdentity(t *testing.T) {
	s, input := candidateWorkflowFixture()
	for _, profile := range []pb.RetrievalProfile{pb.RetrievalProfile_RETRIEVAL_PROFILE_UNSPECIFIED, pb.RetrievalProfile_RETRIEVAL_PROFILE_GRAPH_RAG, pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_GRAPH_RAG} {
		if _, err := s.SearchCandidates(context.Background(), input, profile); err == nil {
			t.Fatal("unsupported profile admitted")
		}
	}
	lexical := s.Lexical
	s.Lexical = func(ctx context.Context, in retrieval.SearchInput) (*retrieval.BranchOutput, error) {
		result, err := lexical(ctx, in)
		result.Hits[0].ChunkID = "other"
		return result, err
	}
	if _, err := s.SearchCandidates(context.Background(), input, pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_RAG); err == nil {
		t.Fatal("contradictory hit merged")
	}
	s.Lexical = func(context.Context, retrieval.SearchInput) (*retrieval.BranchOutput, error) {
		t.Fatal("vector profile invoked BM25")
		return nil, nil
	}
	if _, err := s.SearchCandidates(context.Background(), input, pb.RetrievalProfile_RETRIEVAL_PROFILE_VECTOR_RAG); err != nil {
		t.Fatal(err)
	}
}
