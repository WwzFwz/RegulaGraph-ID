// Coordinates all four snapshot-bound RAG profiles through production retrieval
// branches. Hybrid branches run concurrently; an error
// cancels its sibling and never silently changes the effective profile. The
// caller owns authorization/read lease, generation admission and later evidence
// hydration/legal filtering. These candidates are not yet an EvidenceBundle.
// Measure critical-path p95/p99 including embedding/queue/backend and branch
// durations under configs/benchmark-targets.yaml; required gates are unmeasured.
package workflows

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/qdrant"
	"regulagraph.local/server/internal/retrieval"
	"regulagraph.local/server/internal/retrieval/graph"
)

type CandidateBranch func(context.Context, retrieval.SearchInput) (*retrieval.BranchOutput, error)

type CandidateSearch struct {
	Dense   CandidateBranch
	Lexical CandidateBranch
	Graph   CandidateBranch
	Fusion  retrieval.RRFConfig
}

type CandidateSearchResult struct {
	Profile    pb.RetrievalProfile
	Snapshot   *pb.SnapshotRef
	Candidates []retrieval.FusedCandidate
	Hits       map[string]qdrant.Hit
	Branches   []*retrieval.BranchOutput
	Duration   time.Duration
	Graph      *graph.TraversalResult
}

// SearchCandidates retains every returned candidate up to the explicit branch
// budgets. Every branch required by the explicit profile must be configured;
// graph discovery travels with its ranked source candidates. It never acquires
// or releases the caller's pin, and does not infer a fallback profile.
func (s *CandidateSearch) SearchCandidates(ctx context.Context, input retrieval.SearchInput, profile pb.RetrievalProfile) (*CandidateSearchResult, error) {
	started := time.Now()
	if ctx == nil || s == nil || input.Context == nil || input.Context.SnapshotRef == nil || input.Generation == nil {
		return nil, errors.New("candidate search dependencies and pinned input required")
	}
	var branches []CandidateBranch
	var kinds []pb.RetrieverKind
	add := func(branch CandidateBranch, kind pb.RetrieverKind) {
		branches = append(branches, branch)
		kinds = append(kinds, kind)
	}
	switch profile {
	case pb.RetrievalProfile_RETRIEVAL_PROFILE_VECTOR_RAG:
		add(s.Dense, pb.RetrieverKind_RETRIEVER_KIND_DENSE)
	case pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_RAG:
		add(s.Dense, pb.RetrieverKind_RETRIEVER_KIND_DENSE)
		add(s.Lexical, pb.RetrieverKind_RETRIEVER_KIND_BM25)
	case pb.RetrievalProfile_RETRIEVAL_PROFILE_GRAPH_RAG:
		add(s.Graph, pb.RetrieverKind_RETRIEVER_KIND_GRAPH)
	case pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_GRAPH_RAG:
		add(s.Dense, pb.RetrieverKind_RETRIEVER_KIND_DENSE)
		add(s.Lexical, pb.RetrieverKind_RETRIEVER_KIND_BM25)
		add(s.Graph, pb.RetrieverKind_RETRIEVER_KIND_GRAPH)
	default:
		return nil, errors.New("unsupported retrieval profile")
	}
	for _, branch := range branches {
		if branch == nil {
			return nil, errors.New("requested profile requires every configured branch")
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	call, cancel := context.WithCancel(ctx)
	defer cancel()
	type branchRun struct {
		index  int
		result *retrieval.BranchOutput
		err    error
	}
	preflight := make([]retrieval.RankedBranch, len(kinds))
	for i, kind := range kinds {
		preflight[i].Kind = kind
	}
	if _, err := retrieval.FuseRRF(preflight, s.Fusion); err != nil {
		return nil, err
	}
	if input.Scope.Limit <= 0 || input.Scope.Limit > s.Fusion.MaximumPerBranch ||
		input.Scope.Limit > s.Fusion.MaximumTotalInputs/len(branches) {
		return nil, errors.New("branch budget exceeds fusion capacity")
	}
	results := make(chan branchRun, len(branches))
	for i, branch := range branches {
		owned := input
		owned.Context = proto.Clone(input.Context).(*pb.RequestContext)
		owned.Generation = proto.Clone(input.Generation).(*pb.IndexGeneration)
		go func(i int, branch CandidateBranch, owned retrieval.SearchInput) {
			result, err := branch(call, owned)
			results <- branchRun{i, result, err}
		}(i, branch, owned)
	}
	ordered := make([]*retrieval.BranchOutput, len(branches))
	var firstErr error
	for range branches {
		r := <-results
		if r.err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("%s branch: %w", kinds[r.index], r.err)
			}
			cancel()
		}
		ordered[r.index] = r.result
	}
	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ranked := make([]retrieval.RankedBranch, len(ordered))
	hits := map[string]qdrant.Hit{}
	var paths *graph.TraversalResult
	for i, branch := range ordered {
		if branch == nil || branch.Ranking.Kind != kinds[i] || len(branch.Hits) != len(branch.Ranking.Candidates) {
			return nil, errors.New("branch result identity or cardinality mismatch")
		}
		ranked[i] = branch.Ranking
		if kinds[i] == pb.RetrieverKind_RETRIEVER_KIND_GRAPH {
			if branch.Linking != nil {
				if err := branch.Linking.CheckBudget(); err != nil {
					return nil, err
				}
				if branch.Linking.CorpusID != input.Context.CorpusId || branch.Linking.SnapshotID != input.Context.SnapshotRef.SnapshotId || branch.Linking.RegistryRevision == 0 {
					return nil, errors.New("graph linking report differs from snapshot")
				}
				owned := *branch
				owned.Linking = branch.Linking.Clone()
				ordered[i] = &owned
			}
			if branch.Graph == nil || !proto.Equal(branch.Graph.Snapshot, input.Context.SnapshotRef) {
				return nil, errors.New("graph branch lost pinned discovery")
			}
			if err := validateCandidateTraversal(branch.Graph); err != nil {
				return nil, err
			}
			paths = cloneTraversal(branch.Graph)
		} else if branch.Graph != nil || branch.Linking != nil {
			return nil, errors.New("non-graph branch supplied graph proof")
		}
		for j, hit := range branch.Hits {
			candidate := branch.Ranking.Candidates[j]
			if candidate == nil || hit.RecordID != candidate.EvidenceKey {
				return nil, errors.New("branch hit is not bound to its ranked candidate")
			}
			if previous, ok := hits[hit.RecordID]; ok && (previous.PointID != hit.PointID || previous.ChunkID != hit.ChunkID || !reflect.DeepEqual(previous.ProvisionVersionIDs, hit.ProvisionVersionIDs)) {
				return nil, errors.New("branches disagree on the identity of shared evidence")
			}
			hit.ProvisionVersionIDs = append([]string(nil), hit.ProvisionVersionIDs...)
			hits[hit.RecordID] = hit
		}
	}
	fused, err := retrieval.FuseRRF(ranked, s.Fusion)
	if err != nil {
		return nil, err
	}
	return &CandidateSearchResult{Profile: profile, Snapshot: proto.Clone(input.Context.SnapshotRef).(*pb.SnapshotRef), Candidates: fused, Hits: hits, Branches: ordered, Duration: time.Since(started), Graph: paths}, nil
}
