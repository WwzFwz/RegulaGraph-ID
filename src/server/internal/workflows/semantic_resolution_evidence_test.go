// Exercises the actual candidate hydrator/gateway with two synthetic source documents.
// Checks source attribution, missing/forged supports, snapshot isolation, aggregate budgets,
// audit dependencies and replay without resampling. Fixture providers prove boundary behavior,
// not semantic identity quality; benchmark/gold acceptance remains REQUIRED_UNMEASURED.
package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func candidateEvidenceFixture(t *testing.T, store *modelProposalStore, artifacts *modelProposalArtifacts) (*pb.ArtifactRef, *pb.ArtifactRef) {
	t.Helper()
	source := new(pb.ExtractionBatch)
	if err := proto.Unmarshal(artifacts.contents["artifact:source-model"], source); err != nil {
		t.Fatal(err)
	}
	document := documentBatchFixture(pb.JobStage_JOB_STAGE_CHUNK, source.Meta.CorpusId, pb.Completeness_COMPLETENESS_COMPLETE)
	raw, err := protojson.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(raw)
	for _, prefix := range []string{"document-batch", "dependency-manifest", "source", "text", "normalized", "raw-text", "mapping", "structure", "regulation", "provision", "version", "chunk"} {
		encoded = strings.ReplaceAll(encoded, prefix+":fixture", prefix+":candidate")
	}
	if err = protojson.Unmarshal([]byte(encoded), document); err != nil {
		t.Fatal(err)
	}
	textRef := semanticRefForTest("normalized:candidate", []byte("izin"))
	textRef.StorageKey, textRef.MediaType = "text/candidate.txt", "text/plain;charset=utf-8"
	document.TextArtifacts[0].NormalizedTextRef = textRef
	document.Versions[0].TextRef = proto.Clone(textRef).(*pb.ArtifactRef)
	docRaw, _ := proto.Marshal(document)
	docRef := semanticRefForTest("artifact:document-candidate", docRaw)
	docRef.StorageKey, docRef.MediaType = "objects/candidate-document.pb", documentBatchMediaType
	other := extractionBatchFixture(source.Meta.CorpusId, docRef)
	other.Meta.RecordId = "extraction-batch:candidate"
	mention := proto.Clone(source.Mentions[0]).(*pb.Mention)
	mention.Meta.RecordId, mention.TextSpan.TextArtifactId = "mention:candidate", "text:candidate"
	mention.SourceRefs[0].SourceBlobId, mention.SourceRefs[0].ProvisionVersionId, mention.SourceRefs[0].RegulationId = "source:candidate", "version:candidate", "regulation:candidate"
	other.Mentions = []*pb.Mention{mention}
	otherRaw, _ := proto.Marshal(other)
	otherRef := semanticRefForTest("artifact:extract-candidate", otherRaw)
	otherRef.StorageKey = "objects/candidate-extract.pb"
	entity := &pb.CanonicalEntity{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: source.Meta.CorpusId, RecordId: "canonical:candidate"},
		EntityType: mention.CandidateType, PreferredLabel: "izin", Scope: "national", RegistryRevision: 3, ReviewState: pb.ReviewState_REVIEW_STATE_UNREVIEWED}
	alias := &pb.Alias{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: source.Meta.CorpusId, RecordId: "alias:candidate"},
		CanonicalId: entity.Meta.RecordId, Surface: "izin", NormalizedLookup: "izin", Language: "id", Scope: "national", SupportRefs: []string{mention.Meta.RecordId}}
	lookups := []*pb.CandidateLookup{{MentionId: source.Mentions[0].Meta.RecordId, Scopes: []*pb.CandidateLookupScope{{
		EntityType: mention.CandidateType, CanonicalScope: "national", NormalizedLookup: "izin", CandidateIds: []string{entity.Meta.RecordId},
		Revision: &pb.LookupScopeRevision{ScopeId: domain.RegistryLookupScopeID(mention.CandidateType, "national", "izin"), Revision: 3}}}}}
	candidates, err := domain.AssembleRegistryCandidateBatch(source, store.refs["artifact:source-model"], parseManifest(), "candidates:cross-doc", 3,
		lookups, []*pb.CanonicalEntity{entity}, []*pb.Alias{alias}, 1000, 10)
	if err != nil {
		t.Fatal(err)
	}
	candidateRaw, _ := proto.Marshal(candidates)
	candidateRef := semanticRefForTest("artifact:candidates-cross-doc", candidateRaw)
	candidateRef.StorageKey = "objects/candidates-cross-doc.pb"
	for _, entry := range []struct {
		ref *pb.ArtifactRef
		raw []byte
	}{{candidateRef, candidateRaw}, {otherRef, otherRaw}, {docRef, docRaw}, {textRef, []byte("izin")}} {
		store.refs[entry.ref.ArtifactId], artifacts.contents[entry.ref.ArtifactId] = entry.ref, entry.raw
	}
	store.evidenceRefs = []*pb.ArtifactRef{otherRef}
	return candidateRef, otherRef
}

const candidateLinkJSON = `{"action":"LINK","candidate_id":"canonical:candidate","rationale":"The two source contexts support identity.","evidence_item_ids":["chunk:fixture","chunk:candidate"]}`

func TestCandidateEvidenceHydratesBothDocumentsAndReplays(t *testing.T) {
	h, job, _, batch, producer, gateway, provider, store, artifacts := modelWorkflowFixture(t)
	candidate, source := candidateEvidenceFixture(t, store, artifacts)
	provider.raw = json.RawMessage(candidateLinkJSON)
	output, err := h.ProposeWithModel(context.Background(), job, candidate, batch, producer, gateway)
	if err != nil {
		t.Fatal(err)
	}
	if provider.calls != 1 || store.commits != 0 || output.Request.Proposals[0].Action != pb.ResolutionAction_RESOLUTION_ACTION_LINK {
		t.Fatal("lost advisory LINK boundary")
	}
	item := new(pb.AmbiguousMention)
	if err = protojson.Unmarshal([]byte(provider.input), item); err != nil {
		t.Fatal(err)
	}
	if len(item.CandidateContexts) != 1 || item.CandidateContexts[0].SupportMention.SourceRefs[0].SourceBlobId != "source:candidate" ||
		item.Mention.SourceRefs[0].SourceBlobId != "source:fixture" {
		t.Fatal("candidate evidence lost its separate source attribution")
	}
	deps := store.dependencies[output.InputArtifact.ArtifactId]
	found := map[string]bool{}
	for _, dep := range deps.Dependencies {
		found[dep.DependencyId] = true
	}
	for _, id := range []string{source.ArtifactId, "artifact:document-candidate", "normalized:candidate"} {
		if !found[id] {
			t.Fatalf("missing model input dependency %s", id)
		}
	}
	delete(store.dependencies, output.OutputArtifact.ArtifactId)
	replay, err := h.ProposeWithModel(context.Background(), job, candidate, batch, producer, modelResponseMutator{
		model: gateway, mutate: func(*pb.SemanticResolveResponse) { t.Fatal("durable replay called provider") }})
	if err != nil || provider.calls != 1 || !proto.Equal(replay.Response, output.Response) || store.dependencies[output.OutputArtifact.ArtifactId] == nil {
		t.Fatalf("candidate model replay failed: %v", err)
	}
}

func TestCandidateEvidenceRejectsUntrustedSourcesBeforeModel(t *testing.T) {
	for _, name := range []string{"missing catalog", "missing mention", "foreign corpus", "foreign auth", "foreign snapshot", "document auth", "document snapshot", "wrong surface", "partial source", "duplicate artifact", "byte budget", "work budget"} {
		t.Run(name, func(t *testing.T) {
			h, job, _, batch, producer, gateway, provider, store, artifacts := modelWorkflowFixture(t)
			candidate, ref := candidateEvidenceFixture(t, store, artifacts)
			source := new(pb.ExtractionBatch)
			if err := proto.Unmarshal(artifacts.contents[ref.ArtifactId], source); err != nil {
				t.Fatal(err)
			}
			switch name {
			case "missing catalog":
				store.evidenceErr = domain.ErrNotFound
			case "missing mention":
				source.Mentions[0].Meta.RecordId = "mention:missing"
			case "foreign corpus":
				source.Meta.CorpusId = "corpus:foreign"
			case "foreign auth":
				source.Context.AuthScopeRef = "scope:foreign"
			case "foreign snapshot":
				source.Context.SnapshotRef = &pb.SnapshotRef{CorpusId: job.CorpusID, SnapshotId: "snapshot:foreign", Sequence: 1, ManifestHash: parseHash("d"), RepresentationGeneration: "generation:foreign"}
			case "document auth", "document snapshot":
				document := new(pb.DocumentBatch)
				if err := proto.Unmarshal(artifacts.contents[source.SourceDocumentBatch.ArtifactId], document); err != nil {
					t.Fatal(err)
				}
				if name == "document auth" {
					document.Context.AuthScopeRef = "scope:foreign"
				} else {
					document.Context.SnapshotRef = &pb.SnapshotRef{CorpusId: job.CorpusID, SnapshotId: "snapshot:foreign", Sequence: 1, ManifestHash: parseHash("d"), RepresentationGeneration: "generation:foreign"}
				}
				docRaw, _ := proto.Marshal(document)
				docRef := semanticRefForTest(source.SourceDocumentBatch.ArtifactId, docRaw)
				docRef.StorageKey, docRef.MediaType = source.SourceDocumentBatch.StorageKey, documentBatchMediaType
				source.SourceDocumentBatch = docRef
				source.Dependencies.Dependencies[0].Fingerprint = docRef.ContentHash
				artifacts.contents[docRef.ArtifactId] = docRaw
			case "wrong surface":
				source.Mentions[0].SurfaceForm = "abcd"
			case "partial source":
				source.Completeness = pb.Completeness_COMPLETENESS_PARTIAL
			case "duplicate artifact":
				store.evidenceRefs = append(store.evidenceRefs, ref)
			case "byte budget":
				h.maximumBytes = ref.ByteSize
			case "work budget":
				h.maximumReferences = 1
			}
			raw, _ := proto.Marshal(source)
			updated := semanticRefForTest(ref.ArtifactId, raw)
			updated.StorageKey = ref.StorageKey
			store.evidenceRefs[0], artifacts.contents[ref.ArtifactId] = updated, raw
			_, err := h.ProposeWithModel(context.Background(), job, candidate, batch, producer, gateway)
			if err == nil || provider.calls != 0 {
				t.Fatalf("untrusted input reached model: %v", err)
			}
			if name == "missing catalog" && !errors.Is(err, domain.ErrNotFound) {
				t.Fatal("missing dependency lost classification")
			}
		})
	}
}

func TestCandidateEvidenceFromSameDocumentHasUniqueDependencies(t *testing.T) {
	h, job, _, batch, producer, gateway, provider, store, artifacts := modelWorkflowFixture(t)
	candidate, _ := candidateEvidenceFixture(t, store, artifacts)
	candidates := new(pb.RegistryCandidateBatch)
	if err := proto.Unmarshal(artifacts.contents[candidate.ArtifactId], candidates); err != nil {
		t.Fatal(err)
	}
	candidates.Aliases[0].SupportRefs = []string{"mention:fixture"}
	raw, _ := proto.Marshal(candidates)
	updated := semanticRefForTest(candidate.ArtifactId, raw)
	updated.StorageKey = candidate.StorageKey
	store.refs[updated.ArtifactId], artifacts.contents[updated.ArtifactId] = updated, raw
	store.evidenceRefs = []*pb.ArtifactRef{store.refs["artifact:source-model"]}
	provider.raw = json.RawMessage(strings.Replace(candidateLinkJSON, `,"chunk:candidate"`, "", 1))
	result, err := h.ProposeWithModel(context.Background(), job, updated, batch, producer, gateway)
	if err != nil || result.Request.Proposals[0].Action != pb.ResolutionAction_RESOLUTION_ACTION_LINK {
		t.Fatalf("self-source candidate failed: %v", err)
	}
	for _, manifest := range store.dependencies {
		seen := map[string]bool{}
		for _, dep := range manifest.Dependencies {
			if seen[dep.DependencyId] {
				t.Fatal("duplicate dependency would violate PostgreSQL primary key")
			}
			seen[dep.DependencyId] = true
		}
	}
}

func TestCandidateEvidenceChecksReturnedLinkAtWorkflowBoundary(t *testing.T) {
	h, job, _, batch, producer, gateway, provider, store, artifacts := modelWorkflowFixture(t)
	candidate, _ := candidateEvidenceFixture(t, store, artifacts)
	provider.raw = json.RawMessage(candidateLinkJSON)
	_, err := h.ProposeWithModel(context.Background(), job, candidate, batch, producer, modelResponseMutator{model: gateway, mutate: func(response *pb.SemanticResolveResponse) {
		response.Results[0].GetProposal().SupportingContextIds = []string{"chunk:fixture"}
	}})
	if err == nil || store.commits != 0 {
		t.Fatal("model transport bypassed two-sided evidence validation")
	}
}
