// Checks four-profile graph composition, shared candidate provenance, explicit
// false-positive accounting and post-generation authority. Synthetic branch and
// model fixtures test control/data boundaries, not entity linking or relevance.
package workflows

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/retrieval"
	"regulagraph.local/server/internal/retrieval/graph"
)

func graphRAGFixture(t *testing.T) (*RAGWorkflow, *pb.QuestionRequest, retrieval.SearchInput, *ragProvider, *graph.TraversalResult) {
	w, q, input, provider := ragFixture(t)
	baseHydrate := w.Hydrate
	w.Hydrate = func(ctx context.Context, q *pb.QuestionRequest, found *CandidateSearchResult) (*HydratedCandidates, error) {
		h, err := baseHydrate(ctx, q, found)
		if err == nil {
			for _, item := range h.Evidence.Items {
				item.SourceSpans[0].EndByte = item.SourceSpans[0].StartByte + uint64(len(item.Text))
			}
		}
		return h, err
	}
	h, err := w.Hydrate(context.Background(), q, nil)
	if err != nil {
		t.Fatal(err)
	}
	meta := func(id string) *pb.RecordMeta {
		return &pb.RecordMeta{SchemaVersion: 1, CorpusId: q.CorpusId, RecordId: id, Visibility: &pb.Visibility{FromSeq: input.Context.SnapshotRef.Sequence}}
	}
	a := &pb.RelationAssertion{Meta: meta("assertion:one"), SubjectId: "node:one", ObjectId: "node:two", PredicateId: "requires", OntologyVersion: "v1", Origin: pb.AssertionOrigin_ASSERTION_ORIGIN_EXPLICIT, TemporalScope: proto.Clone(q.TemporalScope).(*pb.TemporalScope)}
	s := &pb.SupportRecord{Meta: meta("support:one"), AssertionId: a.Meta.RecordId, IndependentSourceGroup: "source:one", ReviewState: pb.ReviewState_REVIEW_STATE_UNREVIEWED, ExtractionManifest: h.Evidence.RetrievalManifest, SourceRefs: h.Evidence.Items[0].SourceRefs, EvidenceSpans: h.Evidence.Items[0].SourceSpans}
	p := &pb.GraphPath{PathId: "path:one", OrderedNodeIds: []string{a.SubjectId, a.ObjectId}, OrderedAssertionIds: []string{a.Meta.RecordId}, SelectedSupportIds: []string{s.Meta.RecordId}, Coverage: pb.Completeness_COMPLETENESS_PARTIAL, Snapshot: input.Context.SnapshotRef}
	tr := &graph.TraversalResult{Snapshot: input.Context.SnapshotRef, Paths: []*pb.GraphPath{p}, Assertions: map[string]*pb.RelationAssertion{a.Meta.RecordId: a}, Supports: map[string]*pb.SupportRecord{s.Meta.RecordId: s}, Entities: map[string]*pb.CanonicalEntity{}, FrontierExhausted: true}
	for _, id := range p.OrderedNodeIds {
		tr.Entities[id] = &pb.CanonicalEntity{Meta: meta(id), EntityType: "organization", PreferredLabel: id, Scope: "ID", RegistryRevision: 1, ReviewState: pb.ReviewState_REVIEW_STATE_UNREVIEWED}
	}
	dense := w.Search.Dense
	w.Search.Graph = func(ctx context.Context, in retrieval.SearchInput) (*retrieval.BranchOutput, error) {
		out, err := dense(ctx, in)
		if err != nil {
			return nil, err
		}
		out.Ranking.Kind = pb.RetrieverKind_RETRIEVER_KIND_GRAPH
		for _, c := range out.Ranking.Candidates {
			c.Retriever = out.Ranking.Kind
		}
		out.Graph = tr
		return out, nil
	}
	w.Search.Fusion.Weights[pb.RetrieverKind_RETRIEVER_KIND_GRAPH] = 1
	w.Search.Fusion.MaximumTotalInputs = 30
	w.Answer.MaximumContextTokens = 16000
	w.GraphAdmission = func(context.Context, retrieval.SearchInput) error { return nil }
	q.RequestedProfile = pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_GRAPH_RAG
	return w, q, input, provider, tr
}

func TestGraphCandidateOwnershipBudgetIncludesUnusedRecords(t *testing.T) {
	_, _, _, _, tr := graphRAGFixture(t)
	large := strings.Repeat("x", 1<<20)
	for i := 0; i < 17; i++ {
		entity := proto.Clone(tr.Entities["node:one"]).(*pb.CanonicalEntity)
		entity.Meta.RecordId = fmt.Sprintf("unused:%d", i)
		entity.PreferredLabel = large
		tr.Entities[entity.Meta.RecordId] = entity
	}
	if err := validateCandidateTraversal(tr); !errors.Is(err, domain.ErrGraphReadBudget) {
		t.Fatal("unbounded unused entity copies admitted", err)
	}
	_, _, _, _, tr = graphRAGFixture(t)
	tr.StopReasons = []string{large}
	if err := validateCandidateTraversal(tr); !errors.Is(err, domain.ErrGraphReadBudget) {
		t.Fatal("unbounded stop reason admitted", err)
	}
}

func TestGraphRAGFusionToCitedDraft(t *testing.T) {
	for _, profile := range []pb.RetrievalProfile{pb.RetrievalProfile_RETRIEVAL_PROFILE_GRAPH_RAG, pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_GRAPH_RAG} {
		t.Run(profile.String(), func(t *testing.T) {
			w, q, in, provider, _ := graphRAGFixture(t)
			q.RequestedProfile = profile
			want := 3
			if profile == pb.RetrievalProfile_RETRIEVAL_PROFILE_GRAPH_RAG {
				want = 1
				w.Search.Dense = nil
				w.Search.Lexical = nil
			}
			checks := 0
			w.GraphAdmission = func(context.Context, retrieval.SearchInput) error { checks++; return nil }
			out, err := w.AnswerPinnedQuestion(context.Background(), q, in)
			if err != nil {
				t.Fatal(err)
			}
			if len(out.Search.Branches) != want || len(out.Evidence.Items[0].CandidateProvenance) != want || out.graphContext == nil || provider.calls != 1 || checks != 2 || len(out.Answer.Draft.Answer.Citations) != 1 {
				t.Fatal("graph composition lost branch/proof/authority", out, checks)
			}
			if !strings.Contains(out.Answer.Context.RenderedBlocks[0].RenderedText, "requires") {
				t.Fatal("graph was not rendered")
			}
		})
	}
}

func TestGraphRAGRejectsLostAuthorityAndMetadata(t *testing.T) {
	for _, scenario := range []string{"no_admission", "final_admission", "foreign_graph", "nil_entity", "nil_unused_assertion", "callback_mutation", "graph_failure"} {
		t.Run(scenario, func(t *testing.T) {
			w, q, in, provider, tr := graphRAGFixture(t)
			switch scenario {
			case "graph_failure":
				w.Search.Graph = func(context.Context, retrieval.SearchInput) (*retrieval.BranchOutput, error) {
					return nil, errors.New("graph unavailable")
				}
			case "no_admission":
				w.GraphAdmission = nil
			case "final_admission":
				checks := 0
				w.GraphAdmission = func(context.Context, retrieval.SearchInput) error {
					checks++
					if checks == 2 {
						return errors.New("lease revoked")
					}
					return nil
				}
			case "foreign_graph":
				tr.Snapshot = proto.Clone(tr.Snapshot).(*pb.SnapshotRef)
				tr.Snapshot.SnapshotId = "snapshot:foreign"
			case "nil_entity":
				tr.Entities["bad"] = nil
			case "nil_unused_assertion":
				tr.Assertions["unused"] = nil
			case "callback_mutation":
				original := w.Hydrate
				w.Hydrate = func(ctx context.Context, r *pb.QuestionRequest, found *CandidateSearchResult) (*HydratedCandidates, error) {
					found.Graph.Assertions["assertion:one"].PredicateId = "forged"
					found.Branches[2].Graph.Entities["node:one"].PreferredLabel = "forged"
					return original(ctx, r, found)
				}
			}
			out, err := w.AnswerPinnedQuestion(context.Background(), q, in)
			if scenario == "callback_mutation" {
				if err != nil || strings.Contains(out.Answer.Context.RenderedBlocks[0].RenderedText, "forged") {
					t.Fatal("callback mutated owned graph", err)
				}
				return
			}
			if err == nil || out != nil {
				t.Fatal("invalid graph result accepted")
			}
			if scenario != "final_admission" && provider.calls != 0 {
				t.Fatal("invalid graph reached provider")
			}
			if scenario == "final_admission" && provider.calls != 1 {
				t.Fatal("final authority test failed before generation")
			}
		})
	}
}

func TestGraphFusionKeepsLexicalEvidenceOutsidePaths(t *testing.T) {
	for _, lexical := range []bool{false, true} {
		w, q, _, _, tr := graphRAGFixture(t)
		h, err := w.Hydrate(context.Background(), q, nil)
		if err != nil {
			t.Fatal(err)
		}
		second := proto.Clone(h.Evidence.Items[0]).(*pb.Evidence)
		second.Meta.RecordId = "index:two"
		second.SourceSpans[0].StartByte = 30
		second.SourceSpans[0].EndByte = 30 + uint64(len(second.Text))
		kind := pb.RetrieverKind_RETRIEVER_KIND_GRAPH
		if lexical {
			kind = pb.RetrieverKind_RETRIEVER_KIND_BM25
		}
		second.CandidateProvenance = []*pb.Candidate{{EvidenceKey: "index:two", Retriever: kind, Rank: 1, Representation: "fixture"}}
		h.Evidence.Items = append(h.Evidence.Items, second)
		m, err := mergeGraphEvidence(tr, q, h)
		if err != nil {
			t.Fatal(err)
		}
		if lexical && len(m.Bundle.Items) != 2 || !lexical && (len(m.Bundle.Items) != 1 || h.Rejected["index:two"] == "") {
			t.Fatal("graph mapping lost branch accounting", m, h.Rejected)
		}
	}
}
