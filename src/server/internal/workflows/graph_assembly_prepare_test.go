// Checks worker-input budgeting for graph plans: shared evidence text is charged
// once per logical text artifact, missing descriptors fail, and MIME spelling
// matches the Rust ASSEMBLE contract exactly. Backend/replay tests live in indexing.
package workflows

import (
	"errors"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"testing"
)

func TestGraphAssemblySelectionRejectsUnresolvedMention(t *testing.T) {
	inputs := graphEnvelopeFixture(t, true)
	resolution := new(pb.ResolutionBatch)
	if err := proto.Unmarshal(inputs[3].Bytes, resolution); err != nil {
		t.Fatal(err)
	}
	if _, err := graphAssemblyCanonicalSelection(resolution); !errors.Is(err, ErrGraphAssemblyUnresolved) {
		t.Fatalf("DEFER is not executable: %v", err)
	}
	resolution.Decisions[0].Action = pb.ResolutionAction_RESOLUTION_ACTION_LINK
	resolution.Decisions[0].AssignedCanonicalIds = []string{"canonical:existing"}
	selection, err := graphAssemblyCanonicalSelection(resolution)
	if err != nil || len(selection) != 1 || selection[0] != "canonical:existing" {
		t.Fatalf("LINK selection: %v %v", selection, err)
	}
	resolution.Decisions[0].AssignedCanonicalIds = append(resolution.Decisions[0].AssignedCanonicalIds, "canonical:other")
	if _, err = graphAssemblyCanonicalSelection(resolution); !errors.Is(err, ErrGraphAssemblyUnresolved) {
		t.Fatalf("multiple assignments admitted: %v", err)
	}
}

func TestGraphAssemblyTextBudgetCountsSharedEvidenceOnce(t *testing.T) {
	doc := &pb.DocumentBatch{TextArtifacts: []*pb.TextArtifact{{Meta: &pb.RecordMeta{RecordId: "text:one"},
		NormalizedTextRef: &pb.ArtifactRef{MediaType: "text/plain;charset=utf-8", ByteSize: 10}}}}
	extract := &pb.ExtractionBatch{Mentions: []*pb.Mention{{TextSpan: &pb.TextSpan{TextArtifactId: "text:one"}},
		{TextSpan: &pb.TextSpan{TextArtifactId: "text:one"}}}, Supports: []*pb.SupportRecord{{EvidenceSpans: []*pb.TextSpan{{TextArtifactId: "text:one"}}}}}
	remaining := uint64(10)
	if err := budgetGraphAssemblyTexts(doc, extract, &remaining); err != nil || remaining != 0 {
		t.Fatalf("shared text budget: remaining=%d err=%v", remaining, err)
	}
	for _, name := range []string{"missing", "duplicate", "budget", "media"} {
		t.Run(name, func(t *testing.T) {
			bad := proto.Clone(doc).(*pb.DocumentBatch)
			remaining := uint64(10)
			switch name {
			case "missing":
				bad.TextArtifacts = nil
			case "duplicate":
				bad.TextArtifacts = append(bad.TextArtifacts, proto.Clone(bad.TextArtifacts[0]).(*pb.TextArtifact))
			case "budget":
				remaining = 9
			case "media":
				bad.TextArtifacts[0].NormalizedTextRef.MediaType = "text/plain"
			}
			if err := budgetGraphAssemblyTexts(bad, extract, &remaining); err == nil {
				t.Fatal("invalid worker text input accepted")
			}
		})
	}
}
