// Checks exact graph support-to-text coverage, source/version isolation, split
// chunks, UTF-8 boundaries and explicit unresolved assertion applicability. These
// authenticated-evidence fixtures do not prove legal correctness or model quality.
package graph

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func mappingFixture(t *testing.T) (*TraversalResult, *pb.QuestionRequest, *pb.EvidenceBundle) {
	t.Helper()
	f, c := traversalFixture()
	f.assertions = f.assertions[:1]
	f.supports = f.supports[:1]
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	paths, err := Traverse(ctx, f, f.snapshot, []string{"a"}, c)
	if err != nil {
		t.Fatal(err)
	}
	question := &pb.QuestionRequest{Question: "Apa rujukannya?", CorpusId: f.snapshot.CorpusId, RequestedProfile: pb.RetrievalProfile_RETRIEVAL_PROFILE_GRAPH_RAG, ResponseMode: pb.ResponseMode_RESPONSE_MODE_COMPLETE, TemporalScope: &pb.TemporalScope{Mode: pb.TemporalMode_TEMPORAL_MODE_AS_OF, EffectiveAt: &pb.CalendarDate{Year: 2026, Month: 1, Day: 1}, UnresolvedPolicy: pb.UnresolvedPolicy_UNRESOLVED_POLICY_REPORT}}
	paths.Assertions["ab"].TemporalScope = proto.Clone(question.TemporalScope).(*pb.TemporalScope)
	meta := &pb.RecordMeta{SchemaVersion: 1, CorpusId: f.snapshot.CorpusId, RecordId: "evidence:one"}
	e := &pb.Evidence{Meta: meta, Text: "abcde", SourceRefs: []*pb.SourceVersionRef{proto.Clone(f.supports[0].SourceRefs[0]).(*pb.SourceVersionRef)}, SourceSpans: []*pb.TextSpan{{TextArtifactId: "text:one", StartByte: 0, EndByte: 5}}, SnapshotRef: proto.Clone(f.snapshot).(*pb.SnapshotRef), LegalStatus: pb.LegalStatus_LEGAL_STATUS_ACTIVE}
	b := &pb.EvidenceBundle{Meta: proto.Clone(meta).(*pb.RecordMeta), Snapshot: proto.Clone(f.snapshot).(*pb.SnapshotRef), Items: []*pb.Evidence{e}, RetrievalManifest: proto.Clone(f.supports[0].ExtractionManifest).(*pb.ProducerManifest), Completeness: pb.Completeness_COMPLETENESS_COMPLETE, CompletionStatus: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED}
	b.Meta.RecordId = "bundle:one"
	return paths, question, b
}

func TestGraphEvidenceSpanCoverage(t *testing.T) {
	for _, scenario := range []string{"whole", "split", "gap", "wrong_source", "wrong_version", "wrong_regulation", "unknown_date", "exception", "rejected_support", "inferred", "qualifier"} {
		t.Run(scenario, func(t *testing.T) {
			paths, q, b := mappingFixture(t)
			switch scenario {
			case "split", "gap":
				second := proto.Clone(b.Items[0]).(*pb.Evidence)
				second.Meta.RecordId = "evidence:two"
				second.SourceSpans[0].StartByte = 2
				second.Text = "cde"
				b.Items[0].SourceSpans[0].EndByte = 2
				b.Items[0].Text = "ab"
				if scenario == "gap" {
					second.SourceSpans[0].StartByte = 3
					second.Text = "de"
				}
				b.Items = append(b.Items, second)
			case "wrong_source":
				b.Items[0].SourceRefs[0].SourceBlobId = "blob:other"
			case "wrong_version":
				b.Items[0].SourceRefs[0].ProvisionVersionId = "version:other"
			case "wrong_regulation":
				b.Items[0].SourceRefs[0].RegulationId = "reg:other"
			case "unknown_date":
				paths.Assertions["ab"].TemporalScope.Mode = pb.TemporalMode_TEMPORAL_MODE_CURRENT
				paths.Assertions["ab"].TemporalScope.EffectiveAt = nil
			case "exception":
				paths.Assertions["ab"].ExceptionRefs = []string{"assertion:exception"}
			case "rejected_support":
				paths.Supports["support:ab"].ReviewState = pb.ReviewState_REVIEW_STATE_REJECTED
			case "inferred":
				paths.Assertions["ab"].Origin = pb.AssertionOrigin_ASSERTION_ORIGIN_INFERRED
			case "qualifier":
				paths.Assertions["ab"].Qualifiers = []*pb.Qualifier{{PredicateId: "condition", Value: &pb.Qualifier_CanonicalId{CanonicalId: "condition:other"}}}
			}
			original := proto.Clone(b)
			got, err := AttachEvidence(paths, q, b, 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			complete := scenario == "whole" || scenario == "split"
			if (got.Bundle.Completeness == pb.Completeness_COMPLETENESS_COMPLETE) != complete {
				t.Fatal("wrong graph completeness", got.Bundle)
			}
			if !proto.Equal(original, b) {
				t.Fatal("mapping mutated caller evidence")
			}
			if complete {
				if len(got.Bundle.RequiredPathSets) != 1 || len(got.SupportEvidence["support:ab"]) != len(b.Items) || got.Bundle.Items[0].GraphPaths[0].Coverage != pb.Completeness_COMPLETENESS_COMPLETE {
					t.Fatal("complete support path lost")
				}
			} else if len(got.Bundle.MissingDependencies) == 0 {
				t.Fatal("missing support hidden")
			}
		})
	}
}

func TestGraphEvidenceScopeAndAllocationGuards(t *testing.T) {
	for _, scenario := range []string{"foreign_assertion", "foreign_support", "closed_support", "old_assertion", "foreign_bundle", "path_copies"} {
		t.Run(scenario, func(t *testing.T) {
			paths, q, b := mappingFixture(t)
			budget := uint64(1 << 20)
			switch scenario {
			case "foreign_assertion":
				paths.Assertions["ab"].Meta.CorpusId = "corpus:other"
			case "foreign_support":
				paths.Supports["support:ab"].Meta.CorpusId = "corpus:other"
			case "closed_support":
				end := paths.Snapshot.Sequence + 1
				paths.Supports["support:ab"].Meta.Visibility.ToSeq = &end
			case "old_assertion":
				paths.Assertions["ab"].Meta.Visibility.FromSeq--
			case "foreign_bundle":
				b.Meta.CorpusId = "corpus:other"
			case "path_copies":
				budget = uint64(proto.Size(b)) + 1
			}
			_, err := AttachEvidence(paths, q, b, budget)
			if err == nil {
				t.Fatal("invalid scope/resource accepted")
			}
			if scenario == "path_copies" && !errors.Is(err, domain.ErrGraphReadBudget) {
				t.Fatal("expected incremental copy budget rejection", err)
			}
		})
	}
	paths, _, _ := mappingFixture(t)
	// Each repeated path is wire-valid, but their aggregate exceeds the lookup
	// envelope. This exercises aggregate accounting before cloning graph output.
	original := paths.Paths[0]
	original.OrderedNodeIds = nil
	original.OrderedAssertionIds = nil
	original.SelectedSupportIds = nil
	for i := 0; i <= 16; i++ {
		original.OrderedNodeIds = append(original.OrderedNodeIds, fmt.Sprintf("node:%d:%s", i, strings.Repeat("n", 230)))
	}
	for i := 0; i < 16; i++ {
		a := proto.Clone(paths.Assertions["ab"]).(*pb.RelationAssertion)
		a.Meta.RecordId = fmt.Sprintf("assertion:%d:%s", i, strings.Repeat("a", 230))
		a.SubjectId = original.OrderedNodeIds[i]
		a.ObjectId = original.OrderedNodeIds[i+1]
		paths.Assertions[a.Meta.RecordId] = a
		s := proto.Clone(paths.Supports["support:ab"]).(*pb.SupportRecord)
		s.Meta.RecordId = fmt.Sprintf("support:%d:%s", i, strings.Repeat("s", 230))
		s.AssertionId = a.Meta.RecordId
		paths.Supports[s.Meta.RecordId] = s
		original.OrderedAssertionIds = append(original.OrderedAssertionIds, a.Meta.RecordId)
		original.SelectedSupportIds = append(original.SelectedSupportIds, s.Meta.RecordId)
	}
	paths.Paths = nil
	for i := 0; i < 2048; i++ {
		p := proto.Clone(original).(*pb.GraphPath)
		p.PathId = fmt.Sprintf("path:%d", i)
		if err := domain.ValidateWire(p, domain.DefaultWireLimits); err != nil {
			t.Fatal("invalid allocation fixture", err)
		}
		paths.Paths = append(paths.Paths, p)
	}
	if _, err := SourcesForPaths(paths, 128); !errors.Is(err, domain.ErrGraphReadBudget) {
		t.Fatal("aggregate graph input budget ignored", err)
	}
}

func TestGraphEvidenceKeepsAlternateSupportText(t *testing.T) {
	paths, q, b := mappingFixture(t)
	alternate := proto.Clone(paths.Supports["support:ab"]).(*pb.SupportRecord)
	alternate.Meta.RecordId = "support:alternate"
	alternate.SourceRefs[0].SourceBlobId = "blob:alternate"
	alternate.EvidenceSpans[0].TextArtifactId = "text:alternate"
	paths.Supports[alternate.Meta.RecordId] = alternate
	item := proto.Clone(b.Items[0]).(*pb.Evidence)
	item.Meta.RecordId = "evidence:alternate"
	item.SourceRefs[0] = proto.Clone(alternate.SourceRefs[0]).(*pb.SourceVersionRef)
	item.SourceSpans[0] = proto.Clone(alternate.EvidenceSpans[0]).(*pb.TextSpan)
	b.Items = append(b.Items, item)
	got, err := AttachEvidence(paths, q, b, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Bundle.Items) != 2 || len(got.PathEvidence[paths.Paths[0].PathId]) != 2 || len(got.SupportEvidence[alternate.Meta.RecordId]) != 1 {
		t.Fatal("alternate support text lost", got)
	}
}

func TestGraphEvidenceRejectsInvalidSpanAndPath(t *testing.T) {
	paths, q, b := mappingFixture(t)
	b.Items[0].Text = "éabc"
	paths.Supports["support:ab"].EvidenceSpans[0].StartByte = 1
	if _, err := AttachEvidence(paths, q, b, 1<<20); err == nil {
		t.Fatal("mid-rune support accepted")
	}
	paths, q, b = mappingFixture(t)
	paths.Paths[0].OrderedNodeIds[1] = "foreign"
	if _, err := AttachEvidence(paths, q, b, 1<<20); err == nil {
		t.Fatal("foreign path endpoint accepted")
	}
	paths, q, b = mappingFixture(t)
	if _, err := AttachEvidence(paths, q, b, 1); err == nil {
		t.Fatal("output budget ignored")
	}
}
