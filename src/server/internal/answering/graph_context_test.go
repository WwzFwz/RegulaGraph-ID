// Exercises graph rendering through exact context packing and generator/final
// validation. Split source support must survive in full; forged text, removed
// obligations and metadata drift fail before generation. Fixture token counting
// and synthetic model output are not semantic-quality or latency acceptance.
package answering

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/inference"
	"regulagraph.local/server/internal/retrieval/graph"
)

func graphContextFixture(t *testing.T, partial bool) (*graph.TraversalResult, *graph.EvidenceMapping, *GraphContext) {
	t.Helper()
	b := contextFixture()
	b.Items[0].Text = "abc"
	b.Items[0].SourceSpans[0].StartByte = 0
	b.Items[0].SourceSpans[0].EndByte = 3
	b.Items[1].Text = "def"
	b.Items[1].SourceSpans[0].StartByte = 3
	b.Items[1].SourceSpans[0].EndByte = 6
	meta := func(id string) *pb.RecordMeta {
		return &pb.RecordMeta{SchemaVersion: 1, CorpusId: b.Meta.CorpusId, RecordId: id, Visibility: &pb.Visibility{FromSeq: b.Snapshot.Sequence}}
	}
	temporal := &pb.TemporalScope{Mode: pb.TemporalMode_TEMPORAL_MODE_AS_OF, EffectiveAt: &pb.CalendarDate{Year: 2026, Month: 1, Day: 1}, UnresolvedPolicy: pb.UnresolvedPolicy_UNRESOLVED_POLICY_REPORT}
	a := &pb.RelationAssertion{Meta: meta("assertion:one"), SubjectId: "node:one", ObjectId: "node:two", PredicateId: "requires", OntologyVersion: "v1", Origin: pb.AssertionOrigin_ASSERTION_ORIGIN_EXPLICIT, TemporalScope: proto.Clone(temporal).(*pb.TemporalScope)}
	if partial {
		a.TemporalScope.Mode = pb.TemporalMode_TEMPORAL_MODE_CURRENT
		a.TemporalScope.EffectiveAt = nil
	}
	s := &pb.SupportRecord{Meta: meta("support:one"), AssertionId: a.Meta.RecordId, IndependentSourceGroup: "source:one", ReviewState: pb.ReviewState_REVIEW_STATE_UNREVIEWED, ExtractionManifest: b.RetrievalManifest, SourceRefs: b.Items[0].SourceRefs, EvidenceSpans: []*pb.TextSpan{{TextArtifactId: "text:one", StartByte: 0, EndByte: 6}}}
	p := &pb.GraphPath{PathId: "path:one", OrderedNodeIds: []string{a.SubjectId, a.ObjectId}, OrderedAssertionIds: []string{a.Meta.RecordId}, SelectedSupportIds: []string{s.Meta.RecordId}, Coverage: pb.Completeness_COMPLETENESS_PARTIAL, Snapshot: b.Snapshot}
	tr := &graph.TraversalResult{Snapshot: b.Snapshot, Paths: []*pb.GraphPath{p}, Assertions: map[string]*pb.RelationAssertion{a.Meta.RecordId: a}, Supports: map[string]*pb.SupportRecord{s.Meta.RecordId: s}, Entities: map[string]*pb.CanonicalEntity{}, FrontierExhausted: true}
	for _, id := range p.OrderedNodeIds {
		tr.Entities[id] = &pb.CanonicalEntity{Meta: meta(id), EntityType: "organization", PreferredLabel: "Label " + id, Scope: "ID", RegistryRevision: 1, ReviewState: pb.ReviewState_REVIEW_STATE_UNREVIEWED}
	}
	q := &pb.QuestionRequest{Question: "Apa hubungannya?", CorpusId: b.Meta.CorpusId, TemporalScope: temporal, RequestedProfile: pb.RetrievalProfile_RETRIEVAL_PROFILE_GRAPH_RAG, ResponseMode: pb.ResponseMode_RESPONSE_MODE_COMPLETE}
	m, err := graph.AttachEvidence(tr, q, b, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	g, err := NewGraphContext(tr, m)
	if err != nil {
		t.Fatal(err)
	}
	return tr, m, g
}

func TestGraphContextRequiresEveryTextMember(t *testing.T) {
	_, m, g := graphContextFixture(t, false)
	hash := &pb.ContentHash{Sha256: strings.Repeat("b", 64)}
	full, err := BuildContext(context.Background(), m.Bundle, "context:graph", hash, 1<<20, 2, countBytes, g)
	if err != nil || full.Completeness != pb.Completeness_COMPLETENESS_COMPLETE {
		t.Fatal(full, err)
	}
	text := full.RenderedBlocks[0].RenderedText
	for _, needle := range []string{"requires", "node:one", "node:two", "support:one", "evidence:one", "evidence:two", "not a legal or semantic approval"} {
		if !strings.Contains(text, needle) {
			t.Fatal("graph projection lost", needle)
		}
	}
	partial, err := BuildContext(context.Background(), m.Bundle, "context:small", hash, uint64(len(text)), 2, countBytes, g)
	if err != nil || partial.Completeness != pb.Completeness_COMPLETENESS_PARTIAL {
		t.Fatal(partial, err)
	}
	if len(partial.OrderedEvidenceIds) != 1 || !containsID(partial.OmittedRequiredRefs, "path:one") || !containsID(partial.OmittedRequiredRefs, "evidence:two") {
		t.Fatal("one chunk substituted for complete path", partial)
	}
	m.Bundle.Items[0], m.Bundle.Items[1] = m.Bundle.Items[1], m.Bundle.Items[0]
	reordered, err := BuildContext(context.Background(), m.Bundle, "context:ranked", hash, 1<<20, 2, countBytes, g)
	if err != nil || reordered.Completeness != pb.Completeness_COMPLETENESS_COMPLETE {
		t.Fatal("ranking changed immutable evidence", err)
	}
}

func TestGraphContextRejectsObligationAndContentDrift(t *testing.T) {
	for _, scenario := range []string{"required_paths", "missing", "completeness", "text", "remove_item", "rendered", "omit_list"} {
		t.Run(scenario, func(t *testing.T) {
			_, m, g := graphContextFixture(t, true)
			generator, in := generatorFixture(t, draftProviderFunc(func(context.Context, inference.StructuredRequest) (inference.StructuredResponse, error) {
				t.Fatal("invalid graph reached provider")
				return inference.StructuredResponse{}, nil
			}))
			in.Evidence = m.Bundle
			in.GraphContext = g
			var err error
			in.Context, err = BuildContext(context.Background(), m.Bundle, "context:graph", in.Context.TokenizerHash, 1<<20, 2, countBytes, g)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "required_paths":
				m.Bundle.RequiredPathSets = nil
			case "missing":
				m.Bundle.MissingDependencies = nil
			case "completeness":
				m.Bundle.MissingDependencies = nil
				m.Bundle.Completeness = pb.Completeness_COMPLETENESS_COMPLETE
			case "text":
				m.Bundle.Items[0].Text = "xyz"
			case "remove_item":
				m.Bundle.Items = m.Bundle.Items[:1]
			case "rendered":
				in.Context.RenderedBlocks[0].RenderedText += " invented relation"
			case "omit_list":
				in.Context.OmittedRequiredRefs = nil
				in.Context.Completeness = pb.Completeness_COMPLETENESS_COMPLETE
			}
			if _, err = generator.Generate(context.Background(), in); err == nil {
				t.Fatal("graph drift accepted")
			}
		})
	}
}

func TestGraphContextReachesCitedDraft(t *testing.T) {
	_, m, g := graphContextFixture(t, false)
	calls := 0
	generator, in := generatorFixture(t, draftProviderFunc(func(_ context.Context, r inference.StructuredRequest) (inference.StructuredResponse, error) {
		calls++
		if !strings.Contains(r.Text, "requires") {
			t.Error("graph missing from provider prompt")
		}
		return inference.StructuredResponse{JSON: []byte(`{"status":"answer","claims":[{"text":"Hubungan didukung kedua bagian.","evidence_ids":["evidence:one","evidence:two"]}]}`), InputTokens: 100, OutputTokens: 20}, nil
	}))
	in.Evidence = m.Bundle
	in.GraphContext = g
	var err error
	in.Context, err = BuildContext(context.Background(), m.Bundle, "context:graph", in.Context.TokenizerHash, 1<<20, 2, countBytes, g)
	if err != nil {
		t.Fatal(err)
	}
	out, err := generator.Generate(context.Background(), in)
	if err != nil || calls != 1 || len(out.Answer.Citations) != 2 || len(out.Answer.Paths) != 1 || out.Answer.SemanticStatus != pb.SemanticStatus_SEMANTIC_STATUS_PARTIAL {
		t.Fatal(out, err)
	}
	path := proto.Clone(m.Bundle.Items[0].GraphPaths[0]).(*pb.GraphPath)
	out.Answer.Paths = []*pb.GraphPath{path}
	if err = ValidateGroundedAnswer(out.Answer, in.Context, in.Evidence, in.SourceURLs, g); err != nil {
		t.Fatal("exact rendered path rejected", err)
	}
	path.OrderedNodeIds[0] = "forged"
	if err = ValidateGroundedAnswer(out.Answer, in.Context, in.Evidence, in.SourceURLs, g); err == nil {
		t.Fatal("forged answer path accepted")
	}
}

func containsID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func TestGraphContextOwnsProjectionAndRejectsIncompleteMapping(t *testing.T) {
	for _, scenario := range []string{"member", "entity_closed", "entity_foreign"} {
		t.Run(scenario, func(t *testing.T) {
			tr, m, _ := graphContextFixture(t, false)
			switch scenario {
			case "member":
				m.PathEvidence["path:one"] = m.PathEvidence["path:one"][:1]
			case "entity_closed":
				end := tr.Snapshot.Sequence + 1
				tr.Entities["node:one"].Meta.Visibility.ToSeq = &end
			case "entity_foreign":
				tr.Entities["node:one"].Meta.CorpusId = "corpus:foreign"
			}
			if _, err := NewGraphContext(tr, m); err == nil {
				t.Fatal("incomplete graph handoff accepted")
			}
		})
	}
	tr, m, g := graphContextFixture(t, false)
	before := renderContextEvidence(m.Bundle.Items[0], g)
	tr.Entities["node:one"].PreferredLabel = "mutated label"
	tr.Assertions["assertion:one"].PredicateId = "mutated_predicate"
	tr.Paths[0].OrderedNodeIds[0] = "mutated_node"
	m.PathEvidence["path:one"][0] = "mutated_member"
	if renderContextEvidence(m.Bundle.Items[0], g) != before {
		t.Fatal("render plan retained mutable caller state")
	}
}

func TestGraphPathsRequireAllMembersInOneClaim(t *testing.T) {
	for _, scenario := range []string{"missing_member", "split_claims", "abstain"} {
		t.Run(scenario, func(t *testing.T) {
			_, m, g := graphContextFixture(t, false)
			raw := `{"status":"answer","claims":[{"text":"Satu bagian.","evidence_ids":["evidence:one"]}]}`
			if scenario == "split_claims" {
				raw = `{"status":"answer","claims":[{"text":"Bagian satu.","evidence_ids":["evidence:one"]},{"text":"Bagian dua.","evidence_ids":["evidence:two"]}]}`
			}
			if scenario == "abstain" {
				raw = `{"status":"abstain","claims":[]}`
			}
			generator, in := generatorFixture(t, draftProviderFunc(func(context.Context, inference.StructuredRequest) (inference.StructuredResponse, error) {
				return inference.StructuredResponse{JSON: []byte(raw), InputTokens: 100, OutputTokens: 20}, nil
			}))
			in.Evidence = m.Bundle
			in.GraphContext = g
			var err error
			in.Context, err = BuildContext(context.Background(), m.Bundle, "context:claim-members", in.Context.TokenizerHash, 1<<20, 2, countBytes, g)
			if err != nil {
				t.Fatal(err)
			}
			out, err := generator.Generate(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			if len(out.Answer.Paths) != 0 {
				t.Fatal("unclaimed path was attached")
			}
			out.Answer.Paths = []*pb.GraphPath{proto.Clone(m.Bundle.Items[0].GraphPaths[0]).(*pb.GraphPath)}
			if err = ValidateGroundedAnswer(out.Answer, in.Context, in.Evidence, in.SourceURLs, g); err == nil {
				t.Fatal("manually attached unclaimed path accepted")
			}
		})
	}
}
