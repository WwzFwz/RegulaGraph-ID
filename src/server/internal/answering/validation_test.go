// Verifies the final structural retrieval-to-answer boundary against altered context, omitted
// evidence, forged URLs, and unsupported claims. These fixtures do not prove semantic entailment
// or answer-quality targets in configs/benchmark-targets.yaml.
package answering

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func groundedFixture(t *testing.T) (*pb.Answer, *pb.ContextBundle, *pb.EvidenceBundle) {
	t.Helper()
	bundle := contextFixture()
	contextBundle, err := BuildContext(context.Background(), bundle, "context:answer",
		&pb.ContentHash{Sha256: strings.Repeat("b", 64)}, 1000, 2, countBytes)
	if err != nil {
		t.Fatal(err)
	}
	answer := &pb.Answer{
		Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: bundle.Meta.CorpusId,
			RecordId: "answer:one"},
		RequestId: "request:one", Text: "Isi pasal pertama.",
		Claims: []*pb.Claim{{ClaimId: "claim:one",
			AnswerTextSpan: &pb.AnswerTextSpan{EndByte: uint64(len("Isi pasal pertama."))},
			EvidenceIds:    []string{"evidence:one"}, SupportStatus: pb.SupportStatus_SUPPORT_STATUS_SUPPORTED}},
		Citations: []*pb.Citation{{CitationId: "citation:one", ClaimIds: []string{"claim:one"},
			EvidenceId: "evidence:one", ProvisionVersionId: "provision:v1",
			SourceUrl:  "https://example.org/source.pdf",
			SourceSpan: proto.Clone(bundle.Items[0].SourceSpans[0]).(*pb.TextSpan)}},
		SemanticStatus:   pb.SemanticStatus_SEMANTIC_STATUS_COMPLETE,
		CompletionStatus: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED,
		Snapshot:         proto.Clone(bundle.Snapshot).(*pb.SnapshotRef),
		EffectiveDates:   []*pb.CalendarDate{{Year: 2026, Month: 1, Day: 1}},
		RunManifest:      proto.Clone(bundle.RetrievalManifest).(*pb.ProducerManifest),
	}
	return answer, contextBundle, bundle
}

func trustedURL(blob, version string) ([]string, error) {
	if blob == "source:one" && version == "provision:v1" {
		return []string{"https://example.org/source.pdf"}, nil
	}
	return nil, nil
}

func TestValidateGroundedAnswerRejectsFabricationAndOmission(t *testing.T) {
	answer, rendered, bundle := groundedFixture(t)
	if err := ValidateGroundedAnswer(answer, rendered, bundle, trustedURL); err != nil {
		t.Fatal("valid cited answer rejected:", err)
	}
	mutations := map[string]func(*pb.Answer, *pb.ContextBundle, *pb.EvidenceBundle){
		"forged URL": func(a *pb.Answer, _ *pb.ContextBundle, _ *pb.EvidenceBundle) {
			a.Citations[0].SourceUrl = "https://example.org/forged.pdf"
		},
		"empty citation span": func(a *pb.Answer, _ *pb.ContextBundle, _ *pb.EvidenceBundle) {
			a.Citations[0].SourceSpan.EndByte = a.Citations[0].SourceSpan.StartByte
		},
		"ambiguous source span": func(_ *pb.Answer, _ *pb.ContextBundle, b *pb.EvidenceBundle) {
			b.Items[0].SourceRefs = append(b.Items[0].SourceRefs, &pb.SourceVersionRef{
				SourceBlobId: "source:two", RegulationId: "regulation:two",
				ProvisionVersionId: "provision:v2"})
		},
		"altered context": func(_ *pb.Answer, c *pb.ContextBundle, _ *pb.EvidenceBundle) {
			c.RenderedBlocks[0].RenderedText = "invented article"
		},
		"unrendered claim evidence": func(_ *pb.Answer, c *pb.ContextBundle, _ *pb.EvidenceBundle) {
			c.OrderedEvidenceIds = c.OrderedEvidenceIds[1:]
			c.RenderedBlocks = c.RenderedBlocks[1:]
		},
		"unverified complete claim": func(a *pb.Answer, _ *pb.ContextBundle, _ *pb.EvidenceBundle) {
			a.Claims[0].SupportStatus = pb.SupportStatus_SUPPORT_STATUS_UNREVIEWED
		},
		"no citation": func(a *pb.Answer, _ *pb.ContextBundle, _ *pb.EvidenceBundle) {
			a.Citations = nil
		},
		"partial context called complete": func(_ *pb.Answer, c *pb.ContextBundle, _ *pb.EvidenceBundle) {
			c.Completeness = pb.Completeness_COMPLETENESS_PARTIAL
		},
		"silent evidence drop": func(_ *pb.Answer, c *pb.ContextBundle, _ *pb.EvidenceBundle) {
			c.OrderedEvidenceIds = c.OrderedEvidenceIds[:1]
			c.RenderedBlocks = c.RenderedBlocks[:1]
		},
		"fabricated path": func(a *pb.Answer, _ *pb.ContextBundle, b *pb.EvidenceBundle) {
			a.Paths = []*pb.GraphPath{{PathId: "path:invented",
				OrderedNodeIds: []string{"node:one", "node:two"},
				OrderedAssertionIds: []string{"assertion:one"},
				SelectedSupportIds: []string{"support:one"},
				Coverage: pb.Completeness_COMPLETENESS_COMPLETE,
				Snapshot: proto.Clone(b.Snapshot).(*pb.SnapshotRef)}}
		},
		"empty supported claim span": func(a *pb.Answer, _ *pb.ContextBundle, _ *pb.EvidenceBundle) {
			a.Claims[0].AnswerTextSpan.EndByte = 0
		},
		"fabricated conflict evidence": func(a *pb.Answer, _ *pb.ContextBundle, _ *pb.EvidenceBundle) {
			a.SemanticStatus = pb.SemanticStatus_SEMANTIC_STATUS_CONFLICT
			a.Conflicts = []*pb.EvidenceConflict{{Reason: "contradiction",
				EvidenceIds: []string{"evidence:one", "evidence:invented"}}}
		},
		"duplicate conflict evidence": func(a *pb.Answer, _ *pb.ContextBundle, _ *pb.EvidenceBundle) {
			a.SemanticStatus = pb.SemanticStatus_SEMANTIC_STATUS_CONFLICT
			a.Conflicts = []*pb.EvidenceConflict{{Reason: "contradiction",
				EvidenceIds: []string{"evidence:one", "evidence:one"}}}
		},
		"conflict without details": func(a *pb.Answer, _ *pb.ContextBundle, _ *pb.EvidenceBundle) {
			a.SemanticStatus = pb.SemanticStatus_SEMANTIC_STATUS_CONFLICT
		},
		"mismatched bundle schema": func(_ *pb.Answer, _ *pb.ContextBundle, b *pb.EvidenceBundle) {
			b.Meta.SchemaVersion = 2
		},
		"mismatched item schema": func(_ *pb.Answer, _ *pb.ContextBundle, b *pb.EvidenceBundle) {
			b.Items[0].Meta.SchemaVersion = 2
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			a := proto.Clone(answer).(*pb.Answer)
			c := proto.Clone(rendered).(*pb.ContextBundle)
			b := proto.Clone(bundle).(*pb.EvidenceBundle)
			mutate(a, c, b)
			if err := ValidateGroundedAnswer(a, c, b, trustedURL); err == nil {
				t.Fatal("invalid answer accepted")
			}
		})
	}
}

func TestValidateGroundedAnswerRequiresDisclosureOfOmissions(t *testing.T) {
	answer, rendered, bundle := groundedFixture(t)
	bundle.MissingDependencies = []string{"path:required"}
	bundle.Completeness = pb.Completeness_COMPLETENESS_PARTIAL
	rendered.Completeness = pb.Completeness_COMPLETENESS_PARTIAL
	rendered.OmittedRequiredRefs = []string{"path:required"}
	answer.SemanticStatus = pb.SemanticStatus_SEMANTIC_STATUS_PARTIAL
	if err := ValidateGroundedAnswer(answer, rendered, bundle, trustedURL); err == nil {
		t.Fatal("omitted path hidden from final answer")
	}
	answer.MissingEvidence = []string{"path:required"}
	if err := ValidateGroundedAnswer(answer, rendered, bundle, trustedURL); err != nil {
		t.Fatal("honestly partial answer rejected:", err)
	}
}

func TestValidateGroundedAnswerRejectsHiddenParentAndPath(t *testing.T) {
	for _, testCase := range []struct {
		name string
		add func(*pb.EvidenceBundle)
	}{
		{"parent", func(b *pb.EvidenceBundle) { b.Items[0].ParentRefs = []string{"parent:one"} }},
		{"required path", func(b *pb.EvidenceBundle) {
			b.RequiredPathSets = []*pb.RequiredPathSet{{PathIds: []string{"path:one"}}}
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			answer, _, bundle := groundedFixture(t)
			testCase.add(bundle)
			partial, err := BuildContext(context.Background(), bundle, "context:partial",
				&pb.ContentHash{Sha256: strings.Repeat("b", 64)}, 1000, 2, countBytes)
			if err != nil {
				t.Fatal(err)
			}
			partial.Completeness = pb.Completeness_COMPLETENESS_COMPLETE
			partial.OmittedRequiredRefs = nil
			if err := ValidateGroundedAnswer(answer, partial, bundle, trustedURL); err == nil {
				t.Fatal("forged complete context accepted")
			}
		})
	}
}
