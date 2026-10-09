// Connects candidate retrieval, authoritative hydration, optional reranking and cited draft
// generation for a caller-owned snapshot lease. No model/index algorithms are
// copied here. Hydration must authenticate storage bytes and legal-time policy;
// this workflow rejects missing/extra accounting and cross-version evidence.
// Query admission/publication are external prerequisites. Measure full wall time
// and stage times under configs/benchmark-targets.yaml, without claiming quality
// or production readiness from fixture integration.
package workflows

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/qdrant"
	"regulagraph.local/server/internal/answering"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/retrieval"
	"regulagraph.local/server/internal/retrieval/graph"
)

// HydratedCandidates is local accounting, not another wire schema. Each searched
// record must appear in Evidence.Items or Rejected with a nonempty reason.
// Hydrators own storage hash checks, snapshot membership and temporal filtering;
// they prefetch trusted URLs so citation generation performs no per-claim I/O.
type HydratedCandidates struct {
	Evidence   *pb.EvidenceBundle
	Rejected   map[string]string
	SourceURLs domain.SourceURLLookup
}

type CandidateHydrator func(context.Context, *pb.QuestionRequest, *CandidateSearchResult) (*HydratedCandidates, error)

type RAGWorkflow struct {
	Search         *CandidateSearch
	Hydrate        CandidateHydrator
	Answer         *EvidenceAnswerWorkflow
	Reranker       *retrieval.EvidenceReranker
	profile        pb.RetrievalProfile                                // Nonzero pins a prepared factory's enabled profile.
	GraphAdmission func(context.Context, retrieval.SearchInput) error // Live graph authority, required for graph profiles.
}

type RAGResult struct {
	Search       *CandidateSearchResult
	Evidence     *pb.EvidenceBundle
	Answer       *EvidenceAnswerResult
	Reranking    *retrieval.EvidenceRerankResult
	Rejected     map[string]string
	Duration     time.Duration
	sourceURLs   domain.SourceURLLookup
	graphContext *answering.GraphContext
}

// AnswerPinnedQuestion is the shared composition point for CLI/API/evaluation.
// It requires a trusted admitted SearchInput: auth scope and snapshot are never
// taken directly from untrusted HTTP JSON. Neither child releases the owner pin.
func (w *RAGWorkflow) AnswerPinnedQuestion(ctx context.Context, request *pb.QuestionRequest, input retrieval.SearchInput) (*RAGResult, error) {
	started := time.Now()
	if w == nil || w.Answer == nil {
		return nil, errors.New("configured answering workflow required")
	}
	result, err := w.SearchPinnedQuestion(ctx, request, input)
	if err != nil {
		return nil, err
	}
	answer, err := w.Answer.AnswerEvidence(ctx, request, input.Context, result.Evidence, result.sourceURLs, result.graphContext)
	if err != nil {
		return nil, err
	}
	if result.graphContext != nil {
		if err = w.GraphAdmission(ctx, input); err != nil {
			return nil, err
		}
	}
	result.Answer, result.Duration = answer, time.Since(started)
	return result, nil
}

// SearchPinnedQuestion returns authenticated evidence and candidate accounting
// without calling a generator. It is the same retrieval path used by answering;
// absence of an answer does not imply abstention or a model-quality judgment.
func (w *RAGWorkflow) SearchPinnedQuestion(ctx context.Context, request *pb.QuestionRequest, input retrieval.SearchInput) (*RAGResult, error) {
	started := time.Now()
	if ctx == nil || w == nil || w.Search == nil || w.Hydrate == nil || request == nil || input.Context == nil {
		return nil, errors.New("RAG workflow requires search, hydrator and admitted request")
	}
	if err := domain.ValidateWire(request, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	if w.profile != pb.RetrievalProfile_RETRIEVAL_PROFILE_UNSPECIFIED && w.profile != request.RequestedProfile {
		return nil, errors.New("request profile differs from prepared query")
	}
	if (request.RequestedProfile == pb.RetrievalProfile_RETRIEVAL_PROFILE_GRAPH_RAG || request.RequestedProfile == pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_GRAPH_RAG) && w.GraphAdmission == nil {
		return nil, errors.New("graph RAG requires final live authority check")
	}
	if err := domain.ValidateWire(input.Context, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	if request.Question != input.Question || request.CorpusId != input.Context.CorpusId || input.Context.SnapshotRef == nil ||
		request.ResponseMode != pb.ResponseMode_RESPONSE_MODE_COMPLETE || request.TemporalScope == nil || request.TemporalScope.Mode != pb.TemporalMode_TEMPORAL_MODE_AS_OF ||
		request.TemporalScope.EffectiveAt == nil || len(request.TemporalScope.CompareDates) != 0 ||
		(request.SnapshotId != nil && *request.SnapshotId != input.Context.SnapshotRef.SnapshotId) ||
		(request.TemporalScope.KnowledgeSnapshot != nil && !proto.Equal(request.TemporalScope.KnowledgeSnapshot, input.Context.SnapshotRef)) {
		return nil, errors.New("question/snapshot/date differs from admitted RAG request")
	}
	call, cancel := context.WithDeadline(ctx, input.Context.Deadline.AsTime())
	defer cancel()
	if err := call.Err(); err != nil {
		return nil, err
	}
	found, err := w.Search.SearchCandidates(call, input, request.RequestedProfile)
	if err != nil {
		return nil, err
	}
	hydrated, err := w.Hydrate(call, proto.Clone(request).(*pb.QuestionRequest), cloneCandidateSearch(found))
	if err != nil {
		return nil, fmt.Errorf("hydrate candidate evidence: %w", err)
	}
	if err = call.Err(); err != nil {
		return nil, err
	}
	if err = validateHydratedCandidates(found, hydrated); err != nil {
		return nil, err
	}
	result := &RAGResult{Search: found, Evidence: hydrated.Evidence, Rejected: hydrated.Rejected, sourceURLs: hydrated.SourceURLs}
	var mapping *graph.EvidenceMapping
	if found.Graph != nil {
		mapping, err = mergeGraphEvidence(found.Graph, request, hydrated)
		if err != nil {
			return nil, err
		}
		result.Evidence = mapping.Bundle
		result.Rejected = hydrated.Rejected
	}
	if w.Reranker != nil {
		result.Reranking, err = w.Reranker.Rank(call, input.Context, request.Question, result.Evidence)
		if err != nil {
			return nil, fmt.Errorf("rerank authenticated evidence: %w", err)
		}
		result.Evidence = result.Reranking.Evidence
	}
	if mapping != nil {
		mapping.Bundle = result.Evidence
		result.graphContext, err = answering.NewGraphContext(found.Graph, mapping)
		if err != nil {
			return nil, err
		}
		if err = w.GraphAdmission(call, input); err != nil {
			return nil, err
		}
	}
	result.Duration = time.Since(started)
	return result, nil
}

// Hydration receives its own search view: its callback must not be able to
// rewrite the expected version/identity/provenance used by the boundary gate.
func cloneCandidateSearch(found *CandidateSearchResult) *CandidateSearchResult {
	copy := *found
	copy.Graph = cloneTraversal(found.Graph)
	copy.Snapshot = proto.Clone(found.Snapshot).(*pb.SnapshotRef)
	copy.Candidates = make([]retrieval.FusedCandidate, len(found.Candidates))
	cloneRanking := func(candidates []*pb.Candidate) []*pb.Candidate {
		out := make([]*pb.Candidate, len(candidates))
		for i, candidate := range candidates {
			out[i] = proto.Clone(candidate).(*pb.Candidate)
		}
		return out
	}
	for i, candidate := range found.Candidates {
		copy.Candidates[i] = candidate
		copy.Candidates[i].Provenance = cloneRanking(candidate.Provenance)
	}
	copy.Hits = make(map[string]qdrant.Hit, len(found.Hits))
	for id, hit := range found.Hits {
		hit.ProvisionVersionIDs = append([]string(nil), hit.ProvisionVersionIDs...)
		copy.Hits[id] = hit
	}
	copy.Branches = make([]*retrieval.BranchOutput, len(found.Branches))
	for i, branch := range found.Branches {
		owned := *branch
		owned.Graph = cloneTraversal(branch.Graph)
		owned.Linking = branch.Linking.Clone()
		owned.Ranking = retrieval.RankedBranch{Kind: branch.Ranking.Kind, Candidates: cloneRanking(branch.Ranking.Candidates)}
		owned.Hits = make([]qdrant.Hit, len(branch.Hits))
		for j, hit := range branch.Hits {
			owned.Hits[j] = hit
			owned.Hits[j].ProvisionVersionIDs = append([]string(nil), hit.ProvisionVersionIDs...)
		}
		copy.Branches[i] = &owned
	}
	return &copy
}

func validateHydratedCandidates(found *CandidateSearchResult, hydrated *HydratedCandidates) error {
	if found == nil || hydrated == nil || hydrated.Evidence == nil || hydrated.SourceURLs == nil {
		return errors.New("hydration returned incomplete dependencies")
	}
	bundle := hydrated.Evidence
	if err := domain.ValidateWire(bundle, domain.DefaultWireLimits); err != nil {
		return err
	}
	if !proto.Equal(bundle.Snapshot, found.Snapshot) || bundle.CompletionStatus != pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED {
		return errors.New("hydration changed snapshot or did not finish")
	}
	seen := map[string]bool{}
	ranking := map[string]retrieval.FusedCandidate{}
	for _, candidate := range found.Candidates {
		ranking[candidate.EvidenceKey] = candidate
	}
	for _, item := range bundle.Items {
		if item == nil || item.Meta == nil {
			return errors.New("hydration evidence identity absent")
		}
		id := item.Meta.RecordId
		hit, ok := found.Hits[id]
		if !ok || seen[id] || hydrated.Rejected[id] != "" || !proto.Equal(item.SnapshotRef, found.Snapshot) || item.Meta.CorpusId != found.Snapshot.CorpusId {
			return errors.New("hydration returned unexpected, duplicate or out-of-snapshot evidence")
		}
		versions := map[string]bool{}
		for _, source := range item.SourceRefs {
			versions[source.ProvisionVersionId] = true
		}
		if len(versions) == 0 {
			return errors.New("hydration omitted source versions")
		}
		for _, version := range hit.ProvisionVersionIDs {
			if !versions[version] {
				return errors.New("hydration omitted a candidate provision version")
			}
		}
		for version := range versions {
			matched := false
			for _, expected := range hit.ProvisionVersionIDs {
				matched = matched || version == expected
			}
			if !matched {
				return errors.New("hydration added a foreign provision version")
			}
		}
		// Rank provenance is rebuilt from actual branch output, never accepted
		// from a storage or model-provided claim about how evidence was found.
		item.CandidateProvenance = nil
		for _, candidate := range ranking[id].Provenance {
			item.CandidateProvenance = append(item.CandidateProvenance, proto.Clone(candidate).(*pb.Candidate))
		}
		seen[id] = true
	}
	for id, reason := range hydrated.Rejected {
		if _, ok := found.Hits[id]; !ok || seen[id] || reason == "" {
			return errors.New("invalid hydration rejection accounting")
		}
		seen[id] = true
	}
	if len(seen) != len(found.Hits) {
		return errors.New("hydration silently lost candidates")
	}
	byID := make(map[string]*pb.Evidence, len(bundle.Items))
	for _, item := range bundle.Items {
		byID[item.Meta.RecordId] = item
	}
	ordered := make([]*pb.Evidence, 0, len(bundle.Items))
	for _, candidate := range found.Candidates {
		if item := byID[candidate.EvidenceKey]; item != nil {
			ordered = append(ordered, item)
		}
	}
	bundle.Items = ordered
	return nil
}
