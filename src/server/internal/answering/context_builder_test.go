// Checks that context packing preserves source/version identity and reports every omitted input.
// A deterministic byte counter is used only to exercise budget handling; model-tokenizer parity,
// legal support, and p95/p99 remain REQUIRED_UNMEASURED.
package answering

import (
	"context"
	"errors"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func contextFixture() *pb.EvidenceBundle {
	hash := &pb.ContentHash{Sha256: strings.Repeat("a", 64)}
	snapshot := &pb.SnapshotRef{CorpusId: "corpus:test", SnapshotId: "snapshot:one", Sequence: 1,
		ManifestHash: hash, RepresentationGeneration: "generation:one"}
	meta := func(id string) *pb.RecordMeta {
		return &pb.RecordMeta{SchemaVersion: 1, CorpusId: "corpus:test", RecordId: id}
	}
	makeEvidence := func(id, text string) *pb.Evidence {
		return &pb.Evidence{Meta: meta(id), Text: text, SnapshotRef: proto.Clone(snapshot).(*pb.SnapshotRef),
			SourceRefs: []*pb.SourceVersionRef{{SourceBlobId: "source:one", RegulationId: "regulation:one",
				ProvisionVersionId: "provision:v1"}},
			SourceSpans: []*pb.TextSpan{{TextArtifactId: "text:one", StartByte: 1, EndByte: 10}},
			LegalStatus: pb.LegalStatus_LEGAL_STATUS_ACTIVE}
	}
	return &pb.EvidenceBundle{Meta: meta("bundle:one"), Snapshot: snapshot,
		Items: []*pb.Evidence{makeEvidence("evidence:one", "Isi pasal pertama."),
			makeEvidence("evidence:two", "Isi pasal kedua yang lebih panjang.")},
		RetrievalManifest: &pb.ProducerManifest{Software: "fixture", Build: "test", SchemaVersion: 1,
			ConfigHash: hash}, Completeness: pb.Completeness_COMPLETENESS_COMPLETE,
		CompletionStatus: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED}
}

func countBytes(ctx context.Context, value string) (uint64, error) {
	return uint64(len(value)), ctx.Err()
}

func TestBuildContextBudgetAndMissingEvidence(t *testing.T) {
	bundle := contextFixture()
	hash := &pb.ContentHash{Sha256: strings.Repeat("b", 64)}
	firstOnly, err := BuildContext(context.Background(), bundle, "context:one", hash, 90, 2, countBytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(firstOnly.OrderedEvidenceIds) != 1 || firstOnly.OrderedEvidenceIds[0] != "evidence:one" ||
		len(firstOnly.OmittedRequiredRefs) != 1 || firstOnly.OmittedRequiredRefs[0] != "evidence:two" ||
		firstOnly.Completeness != pb.Completeness_COMPLETENESS_PARTIAL {
		t.Fatalf("budget omission was hidden: %+v", firstOnly)
	}
	if !strings.Contains(firstOnly.RenderedBlocks[0].RenderedText, "provision:v1") ||
		!strings.Contains(firstOnly.RenderedBlocks[0].RenderedText, `"Isi pasal pertama."`) {
		t.Fatalf("source version or escaped text missing: %+v", firstOnly.RenderedBlocks[0])
	}
	complete, err := BuildContext(context.Background(), bundle, "context:two", hash, 1000, 2, countBytes)
	if err != nil || complete.Completeness != pb.Completeness_COMPLETENESS_COMPLETE ||
		len(complete.OrderedEvidenceIds) != 2 || complete.TokenCount <= firstOnly.TokenCount {
		t.Fatalf("full context not assembled: %+v %v", complete, err)
	}
	if len(bundle.Items[0].ParentRefs) != 0 || bundle.Items[0].Text != "Isi pasal pertama." {
		t.Fatal("builder mutated input evidence")
	}
}

func TestBuildContextMarksMissingParentPathAndDependency(t *testing.T) {
	bundle := contextFixture()
	bundle.Items[0].ParentRefs = []string{"parent:pasal"}
	bundle.RequiredPathSets = []*pb.RequiredPathSet{{PathIds: []string{"path:required"}}}
	bundle.MissingDependencies = []string{"dependency:source"}
	bundle.Completeness = pb.Completeness_COMPLETENESS_PARTIAL
	result, err := BuildContext(context.Background(), bundle, "context:missing",
		&pb.ContentHash{Sha256: strings.Repeat("b", 64)}, 1000, 2, countBytes)
	if err != nil || result.Completeness != pb.Completeness_COMPLETENESS_PARTIAL ||
		len(result.OmittedRequiredRefs) != 3 {
		t.Fatalf("unhydrated dependency became complete: %+v %v", result, err)
	}
}

func TestBuildContextDoesNotMistakePathIDForRenderedProof(t *testing.T) {
	bundle := contextFixture()
	bundle.Items[0].GraphPaths = []*pb.GraphPath{{PathId: "path:required",
		OrderedNodeIds:      []string{"node:one", "node:two"},
		OrderedAssertionIds: []string{"assertion:one"}, SelectedSupportIds: []string{"support:one"},
		Coverage: pb.Completeness_COMPLETENESS_COMPLETE,
		Snapshot: proto.Clone(bundle.Snapshot).(*pb.SnapshotRef)}}
	bundle.RequiredPathSets = []*pb.RequiredPathSet{{PathIds: []string{"path:required"}}}
	result, err := BuildContext(context.Background(), bundle, "context:path",
		&pb.ContentHash{Sha256: strings.Repeat("b", 64)}, 1000, 2, countBytes)
	if err != nil || result.Completeness != pb.Completeness_COMPLETENESS_PARTIAL ||
		len(result.OmittedRequiredRefs) != 1 || result.OmittedRequiredRefs[0] != "path:required" {
		t.Fatalf("path ID alone was treated as rendered proof: %+v %v", result, err)
	}
}

func TestBuildContextPreservesNoEvidenceState(t *testing.T) {
	bundle := contextFixture()
	bundle.Items = nil
	bundle.Completeness = pb.Completeness_COMPLETENESS_NONE
	result, err := BuildContext(context.Background(), bundle, "context:none",
		&pb.ContentHash{Sha256: strings.Repeat("b", 64)}, 1000, 2, countBytes)
	if err != nil || result.Completeness != pb.Completeness_COMPLETENESS_NONE || result.TokenCount != 0 {
		t.Fatalf("no-evidence result changed state: %+v %v", result, err)
	}
}

func TestBuildContextRejectsCrossSnapshotAndCounterFailure(t *testing.T) {
	bundle := contextFixture()
	hash := &pb.ContentHash{Sha256: strings.Repeat("b", 64)}
	bundle.Items[1].SnapshotRef.SnapshotId = "snapshot:other"
	if _, err := BuildContext(context.Background(), bundle, "context:bad", hash, 1000, 2, countBytes); err == nil {
		t.Fatal("mixed snapshot evidence accepted")
	}
	bundle = contextFixture()
	if _, err := BuildContext(context.Background(), bundle, "context:error", hash, 1000, 2,
		func(context.Context, string) (uint64, error) { return 0, errors.New("tokenizer failed") }); err == nil {
		t.Fatal("tokenizer failure was ignored")
	}
}
