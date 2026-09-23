// Exercises RESOLVE closure against malformed model output and registry decision bindings.
// These deterministic fixtures prove fail-closed record accounting, not semantic identity quality
// or production latency; the corresponding gold and benchmark gates remain REQUIRED_UNMEASURED.
package domain

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func resolutionClosureFixture() (*pb.ResolutionBatch, *pb.ExtractionBatch, *pb.ArtifactRef) {
	corpus := "corpus:test"
	ref := &pb.SourceVersionRef{SourceBlobId: "source:one", ProvisionVersionId: "version:one", RegulationId: "regulation:one"}
	span := &pb.TextSpan{TextArtifactId: "text:one", StartByte: 4, EndByte: 14}
	mention := &pb.Mention{
		Meta:     &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpus, RecordId: "mention:one"},
		TextSpan: span, SourceRefs: []*pb.SourceVersionRef{ref}, SurfaceForm: "instansi A", CandidateType: "organization",
	}
	source := &pb.ExtractionBatch{
		Meta:            &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpus, RecordId: "extraction:one"},
		Context:         &pb.RequestContext{SchemaVersion: 1, CorpusId: corpus, AuthScopeRef: "scope:one", ConfigFingerprint: &pb.ContentHash{Sha256: strings.Repeat("c", 64)}},
		OntologyVersion: "ontology-v1", Mentions: []*pb.Mention{mention},
		Supports: []*pb.SupportRecord{{SourceRefs: []*pb.SourceVersionRef{ref}, EvidenceSpans: []*pb.TextSpan{{TextArtifactId: "text:one", StartByte: 30, EndByte: 40}}}},
	}
	sourceRef := &pb.ArtifactRef{ArtifactId: "artifact:extract", ContentHash: &pb.ContentHash{Sha256: strings.Repeat("a", 64)}}
	model := &pb.ModelManifest{ModelId: "resolver", Task: pb.ModelTask_MODEL_TASK_RESOLVE}
	proposal := &pb.ResolutionProposal{
		Meta:       &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpus, RecordId: "proposal:one"},
		MentionIds: []string{"mention:one"}, CandidateIds: []string{"canonical:one"},
		Action: pb.ResolutionAction_RESOLUTION_ACTION_LINK, ExpectedRegistryRevision: 7,
		Evidence: &pb.Provenance{Sources: []*pb.SourceVersionRef{ref}, Spans: []*pb.TextSpan{span}},
	}
	batch := &pb.ResolutionBatch{
		Meta:                  &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpus, RecordId: "resolution:one"},
		Context:               proto.Clone(source.Context).(*pb.RequestContext),
		SourceExtractionBatch: sourceRef, OntologyVersion: "ontology-v1", RegistryRevision: 7,
		ModelManifest: model, ItemCounts: &pb.Counts{Expected: 1, Accepted: 1},
		Completeness: pb.Completeness_COMPLETENESS_COMPLETE,
		Dependencies: &pb.DependencyManifest{
			Dependencies:     []*pb.Dependency{{DependencyId: sourceRef.ArtifactId, Fingerprint: sourceRef.ContentHash}},
			ProducerManifest: &pb.ProducerManifest{Models: []*pb.ModelManifest{model}},
		},
		Proposals: []*pb.ResolutionProposal{proposal},
		Decisions: []*pb.ResolutionDecision{{
			Meta:       &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpus, RecordId: "decision:one"},
			ProposalId: "proposal:one", AssignedCanonicalIds: []string{"canonical:one"},
			Action: pb.ResolutionAction_RESOLUTION_ACTION_LINK, RegistryRevision: 7,
		}},
	}
	return batch, source, sourceRef
}

func TestValidateResolutionBatchClosureRejectsForgedOrUnaccountedResults(t *testing.T) {
	base, source, sourceRef := resolutionClosureFixture()
	if err := ValidateResolutionBatchClosure(base, source, sourceRef, 16); err != nil {
		t.Fatal("valid fixture rejected:", err)
	}
	cases := map[string]func(*pb.ResolutionBatch){
		"wrong extraction bytes": func(batch *pb.ResolutionBatch) {
			batch.SourceExtractionBatch.ContentHash.Sha256 = strings.Repeat("b", 64)
		},
		"changed auth scope": func(batch *pb.ResolutionBatch) { batch.Context.AuthScopeRef = "scope:other" },
		"changed config":     func(batch *pb.ResolutionBatch) { batch.Context.ConfigFingerprint.Sha256 = strings.Repeat("d", 64) },
		"changed snapshot": func(batch *pb.ResolutionBatch) {
			batch.Context.SnapshotRef = &pb.SnapshotRef{SnapshotId: "snapshot:other"}
		},
		"stale proposal revision": func(batch *pb.ResolutionBatch) { batch.Proposals[0].ExpectedRegistryRevision = 8 },
		"invented mention":        func(batch *pb.ResolutionBatch) { batch.Proposals[0].MentionIds[0] = "mention:other" },
		"proposal reuses mention ID": func(batch *pb.ResolutionBatch) {
			batch.Proposals[0].Meta.RecordId = "mention:one"
			batch.Decisions[0].ProposalId = "mention:one"
		},
		"decision reuses proposal ID": func(batch *pb.ResolutionBatch) {
			batch.Decisions[0].Meta.RecordId = "proposal:one"
		},
		"invented evidence": func(batch *pb.ResolutionBatch) { batch.Proposals[0].Evidence.Spans[0].StartByte = 100 },
		"one-byte overlap":  func(batch *pb.ResolutionBatch) { batch.Proposals[0].Evidence.Spans[0].StartByte = 13 },
		"forged locator": func(batch *pb.ResolutionBatch) {
			batch.Proposals[0].Evidence.Locators = []*pb.PageLocator{{SourceBlobId: "source:forged", PageNumber: 999}}
		},
		"invalid issue evidence ref": func(batch *pb.ResolutionBatch) {
			batch.Issues = []*pb.ValidationIssue{{RecordId: "proposal:one", EvidenceRefs: []string{"record:unknown"}}}
		},
		"unrelated known evidence": func(batch *pb.ResolutionBatch) {
			batch.Proposals[0].Evidence.Spans[0].StartByte = 30
			batch.Proposals[0].Evidence.Spans[0].EndByte = 40
		},
		"wrong link assignment": func(batch *pb.ResolutionBatch) { batch.Decisions[0].AssignedCanonicalIds[0] = "canonical:other" },
		"missing decision":      func(batch *pb.ResolutionBatch) { batch.Decisions = nil },
		"missing proposal":      func(batch *pb.ResolutionBatch) { batch.Proposals = nil },
		"silent failure": func(batch *pb.ResolutionBatch) {
			batch.Proposals = nil
			batch.Decisions = nil
			batch.ItemCounts.Accepted = 0
			batch.ItemCounts.Rejected = 1
			batch.Completeness = pb.Completeness_COMPLETENESS_NONE
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			batch := proto.Clone(base).(*pb.ResolutionBatch)
			mutate(batch)
			if err := ValidateResolutionBatchClosure(batch, source, sourceRef, 16); err == nil {
				t.Fatal("invalid resolution batch was accepted")
			}
		})
	}
}

func TestValidateResolutionBatchClosureAllowsExplicitRejectedMention(t *testing.T) {
	batch, source, sourceRef := resolutionClosureFixture()
	batch.Proposals = nil
	batch.Decisions = nil
	batch.ItemCounts.Accepted = 0
	batch.ItemCounts.Rejected = 1
	batch.Completeness = pb.Completeness_COMPLETENESS_NONE
	batch.Issues = []*pb.ValidationIssue{{RecordId: "mention:one", Severity: pb.Severity_SEVERITY_ERROR}}
	if err := ValidateResolutionBatchClosure(batch, source, sourceRef, 16); err != nil {
		t.Fatal("explicit failed item was rejected:", err)
	}
}
