// Exercises snapshot-envelope composition using the ingestion CHUNK/EXTRACT fixtures.
// Restoring only the documented outer fields must reproduce every original model
// output and decision. This tests the pure handoff, not database membership or LLM quality.
package workflows

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func graphEnvelopeArtifact(t *testing.T, message proto.Message, id, media string) domain.GraphSourceArtifact {
	t.Helper()
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(raw))
	return domain.GraphSourceArtifact{Reference: &pb.ArtifactRef{ArtifactId: id, ContentHash: &pb.ContentHash{Sha256: hash},
		ByteSize: uint64(len(raw)), SchemaVersion: 1, MediaType: media, StorageKey: "objects/" + hash}, Bytes: raw}
}

func graphEnvelopeFixture(t *testing.T, nonempty bool) []domain.GraphSourceArtifact {
	t.Helper()
	document := documentBatchFixture(pb.JobStage_JOB_STAGE_CHUNK, "corpus:graph-envelope", pb.Completeness_COMPLETENESS_COMPLETE)
	doc := graphEnvelopeArtifact(t, document, "artifact:original-document", documentBatchMediaType)
	snapshot := &pb.SnapshotRef{CorpusId: document.Meta.CorpusId, SnapshotId: "snapshot:base", Sequence: 1,
		ManifestHash: parseHash("1"), RepresentationGeneration: "generation:base"}
	bound, err := domain.BindInitialSnapshotSource("job:source", document, doc.Reference, snapshot, document.Context.AuthScopeRef)
	if err != nil {
		t.Fatal(err)
	}
	boundDoc := graphEnvelopeArtifact(t, bound, "artifact:bound-document", documentBatchMediaType)
	extraction := extractionBatchFixture(document.Meta.CorpusId, doc.Reference)
	if nonempty {
		extraction.Mentions = []*pb.Mention{{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: document.Meta.CorpusId, RecordId: "mention:fixture"},
			CandidateType: "organization", SurfaceForm: "Test", TextSpan: &pb.TextSpan{TextArtifactId: "text:fixture", EndByte: 4},
			SourceRefs:         []*pb.SourceVersionRef{{SourceBlobId: "source:fixture", RegulationId: "regulation:fixture", ProvisionVersionId: "version:fixture"}},
			ExtractionManifest: proto.Clone(extraction.Dependencies.ProducerManifest).(*pb.ProducerManifest)}}
	}
	extract := graphEnvelopeArtifact(t, extraction, "artifact:original-extract", domain.ExtractionBatchMediaType)
	model := proto.Clone(extraction.ModelManifest).(*pb.ModelManifest)
	model.ModelId, model.Task = "model:resolver", pb.ModelTask_MODEL_TASK_RESOLVE
	producer := proto.Clone(extraction.Dependencies.ProducerManifest).(*pb.ProducerManifest)
	producer.Models = []*pb.ModelManifest{model}
	resolution := &pb.ResolutionBatch{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: document.Meta.CorpusId, RecordId: "resolution:original"},
		Context: proto.Clone(extraction.Context).(*pb.RequestContext), SourceExtractionBatch: extract.Reference,
		Dependencies: &pb.DependencyManifest{ArtifactId: "dependencies:resolution", ProducerManifest: producer,
			Dependencies:         []*pb.Dependency{{DependencyId: extract.Reference.ArtifactId, Fingerprint: extract.Reference.ContentHash}},
			LookupScopeRevisions: []*pb.LookupScopeRevision{{ScopeId: "lookup:absent", EmptyResult: true}}},
		Completeness: pb.Completeness_COMPLETENESS_COMPLETE, OntologyVersion: extraction.OntologyVersion, RegistryRevision: 7,
		ModelManifest: model, ItemCounts: &pb.Counts{}, TokenUsage: &pb.TokenUsage{TokenizerId: "fixture"}}
	if nonempty {
		mention := extraction.Mentions[0]
		resolution.ItemCounts = &pb.Counts{Expected: 1, Accepted: 1}
		resolution.Proposals = []*pb.ResolutionProposal{{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: document.Meta.CorpusId, RecordId: "proposal:fixture"},
			MentionIds: []string{mention.Meta.RecordId}, Action: pb.ResolutionAction_RESOLUTION_ACTION_DEFER,
			Evidence: &pb.Provenance{Sources: mention.SourceRefs, Spans: []*pb.TextSpan{mention.TextSpan}},
			Method:   "model", ExpectedRegistryRevision: 6, LocalCorrelationId: "correlation:fixture"}}
		resolution.Decisions = []*pb.ResolutionDecision{{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: document.Meta.CorpusId, RecordId: "decision:fixture"},
			ProposalId: "proposal:fixture", Action: pb.ResolutionAction_RESOLUTION_ACTION_DEFER, RegistryRevision: 7, Reason: "insufficient evidence", Actor: "registry:fixture"}}
		resolution.Issues = []*pb.ValidationIssue{{RecordId: mention.Meta.RecordId,
			Severity: pb.Severity_SEVERITY_WARNING, Code: "deferred", FieldPath: "proposals", Disposition: "retain_for_review",
			EvidenceRefs: []string{extraction.Meta.RecordId, resolution.Meta.RecordId, mention.Meta.RecordId}}}
	}
	return []domain.GraphSourceArtifact{doc, boundDoc, extract,
		graphEnvelopeArtifact(t, resolution, "artifact:original-resolve", "application/x-protobuf")}
}

func TestGraphSourceEnvelopesPreserveModelOutputs(t *testing.T) {
	for _, nonempty := range []bool{false, true} {
		t.Run(fmt.Sprint(nonempty), func(t *testing.T) {
			inputs := graphEnvelopeFixture(t, nonempty)
			originalBytes := make([][]byte, len(inputs))
			for i := range inputs {
				originalBytes[i] = append([]byte(nil), inputs[i].Bytes...)
			}
			extract, resolve, err := domain.BindGraphSourceEnvelopes("job:source", inputs[0], inputs[1], inputs[2], inputs[3], 1024)
			if err != nil {
				t.Fatal(err)
			}
			extractAgain, resolveAgain, err := domain.BindGraphSourceEnvelopes("job:source", inputs[0], inputs[1], inputs[2], inputs[3], 1024)
			if err != nil || !bytes.Equal(extract.Bytes, extractAgain.Bytes) || !bytes.Equal(resolve.Bytes, resolveAgain.Bytes) {
				t.Fatal("envelope replay changed", err)
			}
			oldE, newE := new(pb.ExtractionBatch), new(pb.ExtractionBatch)
			oldR, newR := new(pb.ResolutionBatch), new(pb.ResolutionBatch)
			for _, pair := range []struct {
				raw     []byte
				message proto.Message
			}{{inputs[2].Bytes, oldE}, {extract.Bytes, newE}, {inputs[3].Bytes, oldR}, {resolve.Bytes, newR}} {
				if err = domain.DecodeWire(pair.raw, pair.message, domain.DefaultWireLimits); err != nil {
					t.Fatal(err)
				}
			}
			if newE.Context.SnapshotRef.SnapshotId != "snapshot:base" || !proto.Equal(newR.Context.SnapshotRef, newE.Context.SnapshotRef) ||
				!proto.Equal(newR.SourceExtractionBatch, extract.Reference) || newR.RegistryRevision != oldR.RegistryRevision ||
				!proto.Equal(newE.Dependencies.ProducerManifest, oldE.Dependencies.ProducerManifest) ||
				!proto.Equal(newR.Dependencies.ProducerManifest, oldR.Dependencies.ProducerManifest) {
				t.Fatal("snapshot, source chain, producer or recorded revision drift")
			}
			for _, pair := range []struct {
				current, original *pb.DependencyManifest
				ref               *pb.ArtifactRef
			}{
				{newE.Dependencies, oldE.Dependencies, inputs[2].Reference}, {newR.Dependencies, oldR.Dependencies, inputs[3].Reference},
			} {
				found := false
				for _, d := range pair.current.Dependencies {
					found = found || d.DependencyId == pair.ref.ArtifactId && proto.Equal(d.Fingerprint, pair.ref.ContentHash)
				}
				if !found || len(pair.current.LookupScopeRevisions) != len(pair.original.LookupScopeRevisions) {
					t.Fatal("original dependency lost")
				}
				for i, observation := range pair.original.LookupScopeRevisions {
					if !proto.Equal(observation, pair.current.LookupScopeRevisions[i]) {
						t.Fatal("lookup observation changed")
					}
				}
				for _, original := range pair.original.Dependencies {
					retained := false
					for _, current := range pair.current.Dependencies {
						retained = retained || proto.Equal(original, current)
					}
					if !retained {
						t.Fatal("upstream dependency changed")
					}
				}
			}
			if nonempty {
				refs := newR.Issues[0].EvidenceRefs
				if len(refs) != 3 || refs[0] != newE.Meta.RecordId || refs[1] != newR.Meta.RecordId || refs[2] != oldR.Issues[0].EvidenceRefs[2] {
					t.Fatal("diagnostic root references were not rebound exactly")
				}
				newR.Issues[0].EvidenceRefs[0], newR.Issues[0].EvidenceRefs[1] = oldE.Meta.RecordId, oldR.Meta.RecordId
			}
			newE.Meta, newE.Context, newE.SourceDocumentBatch, newE.Dependencies = oldE.Meta, oldE.Context, oldE.SourceDocumentBatch, oldE.Dependencies
			newR.Meta, newR.Context, newR.SourceExtractionBatch, newR.Dependencies = oldR.Meta, oldR.Context, oldR.SourceExtractionBatch, oldR.Dependencies
			if !proto.Equal(newE, oldE) || !proto.Equal(newR, oldR) {
				t.Fatal("model output or decision changed")
			}
			for i := range inputs {
				if !bytes.Equal(inputs[i].Bytes, originalBytes[i]) {
					t.Fatal("input bytes mutated")
				}
			}
		})
	}
}

func TestGraphSourceEnvelopesPreserveCanonicalAssignment(t *testing.T) {
	in := graphEnvelopeFixture(t, true)
	original := new(pb.ResolutionBatch)
	if err := proto.Unmarshal(in[3].Bytes, original); err != nil {
		t.Fatal(err)
	}
	original.Proposals[0].Action = pb.ResolutionAction_RESOLUTION_ACTION_LINK
	original.Proposals[0].CandidateIds = []string{"canonical:existing"}
	original.Decisions[0].Action = pb.ResolutionAction_RESOLUTION_ACTION_LINK
	original.Decisions[0].AssignedCanonicalIds = []string{"canonical:existing"}
	in[3] = graphEnvelopeArtifact(t, original, in[3].Reference.ArtifactId, in[3].Reference.MediaType)
	_, result, err := domain.BindGraphSourceEnvelopes("job:source", in[0], in[1], in[2], in[3], 1024)
	if err != nil {
		t.Fatal(err)
	}
	bound := new(pb.ResolutionBatch)
	if err = proto.Unmarshal(result.Bytes, bound); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(bound.Decisions[0], original.Decisions[0]) || !proto.Equal(bound.Proposals[0], original.Proposals[0]) {
		t.Fatal("canonical assignment or proposal changed")
	}
}

func TestGraphSourceEnvelopesRejectSourceDrift(t *testing.T) {
	for _, name := range []string{"hash", "foreign job", "document body", "extraction source", "resolution source", "prior snapshot", "partial", "unknown", "unknown ref", "unknown hash", "root collision", "budget", "media"} {
		t.Run(name, func(t *testing.T) {
			in := graphEnvelopeFixture(t, true)
			job, limit := "job:source", 1024
			rewrite := func(index int, message proto.Message, change func()) {
				if err := proto.Unmarshal(in[index].Bytes, message); err != nil {
					t.Fatal(err)
				}
				change()
				in[index] = graphEnvelopeArtifact(t, message, in[index].Reference.ArtifactId, in[index].Reference.MediaType)
			}
			switch name {
			case "hash":
				in[2].Bytes[0] ^= 1
			case "foreign job":
				job = "job:other"
			case "document body":
				m := new(pb.DocumentBatch)
				rewrite(1, m, func() { m.Regulations[0].Title = "Changed" })
			case "extraction source":
				m := new(pb.ExtractionBatch)
				rewrite(2, m, func() { m.SourceDocumentBatch.ArtifactId = "artifact:other" })
			case "resolution source":
				m := new(pb.ResolutionBatch)
				rewrite(3, m, func() { m.SourceExtractionBatch.ArtifactId = "artifact:other" })
			case "prior snapshot":
				m := new(pb.DocumentBatch)
				rewrite(0, m, func() {
					m.Context.SnapshotRef = &pb.SnapshotRef{CorpusId: m.Meta.CorpusId, SnapshotId: "snapshot:other", Sequence: 2, ManifestHash: parseHash("a"), RepresentationGeneration: "generation:other"}
				})
			case "partial":
				m := new(pb.ResolutionBatch)
				rewrite(3, m, func() { m.Completeness = pb.Completeness_COMPLETENESS_PARTIAL })
			case "unknown":
				m := new(pb.ExtractionBatch)
				rewrite(2, m, func() { m.ProtoReflect().SetUnknown([]byte{0x98, 0x06, 0x01}) })
			case "unknown ref":
				in[1].Reference.ProtoReflect().SetUnknown([]byte{0x98, 0x06, 0x01})
			case "unknown hash":
				in[1].Reference.ContentHash.ProtoReflect().SetUnknown([]byte{0x98, 0x06, 0x01})
			case "root collision":
				e := new(pb.ExtractionBatch)
				rewrite(2, e, func() { e.Meta.RecordId = e.Mentions[0].Meta.RecordId })
				r := new(pb.ResolutionBatch)
				rewrite(3, r, func() {
					r.SourceExtractionBatch = in[2].Reference
					r.Dependencies.Dependencies[0].Fingerprint = in[2].Reference.ContentHash
					r.Issues[0].EvidenceRefs[0] = e.Meta.RecordId
				})
			case "budget":
				limit = 1
			case "media":
				in[2].Reference.MediaType = "application/pdf"
			}
			if _, _, err := domain.BindGraphSourceEnvelopes(job, in[0], in[1], in[2], in[3], limit); err == nil {
				t.Fatal("source drift accepted")
			}
		})
	}
}
