// Runs verified artifact hydration through the actual contextual gateway with a deterministic
// provider, then checks durable replay and rejection of stale/corrupt model results. This fixture
// proves the ingestion boundary, not real model accuracy or release latency.
package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/inference"
	"regulagraph.local/server/internal/domain"
)

type modelProposalStore struct {
	*candidateRaceStore
	revision     uint64
	dependencies map[string]*pb.DependencyManifest
	evidenceRefs []*pb.ArtifactRef
	evidenceErr  error
}

func (s *modelProposalStore) LoadResolutionEvidenceSources(_ context.Context, _ *pb.RequestContext, _ []string, _ int) ([]*pb.ArtifactRef, error) {
	return s.evidenceRefs, s.evidenceErr
}

func (s *modelProposalStore) LoadArtifact(_ context.Context, _, id string) (*pb.ArtifactRef, error) {
	ref := s.refs[id]
	if ref == nil {
		return nil, domain.ErrNotFound
	}
	return proto.Clone(ref).(*pb.ArtifactRef), nil
}
func (s *modelProposalStore) RegisterArtifact(_ context.Context, _ string, ref *pb.ArtifactRef) error {
	if old := s.refs[ref.ArtifactId]; old != nil && !proto.Equal(old, ref) {
		return domain.ErrPersistentIntegrity
	}
	s.refs[ref.ArtifactId] = proto.Clone(ref).(*pb.ArtifactRef)
	return nil
}
func (s *modelProposalStore) ReplaceArtifactDependencyManifest(_ context.Context, _, id string, manifest *pb.DependencyManifest) error {
	seen := map[string]bool{}
	for _, dependency := range manifest.Dependencies {
		if seen[dependency.DependencyId] {
			return errors.New("duplicate persisted dependency")
		}
		seen[dependency.DependencyId] = true
	}
	s.dependencies[id] = proto.Clone(manifest).(*pb.DependencyManifest)
	return nil
}
func (s *modelProposalStore) ReadFencedResolveRevision(_ context.Context, _ domain.SemanticJobFence, _ *pb.ArtifactRef, _ *pb.ExtractionBatch) (uint64, error) {
	return s.revision, nil
}

type modelProposalArtifacts struct{ *semanticArtifactReaderFake }

func (a *modelProposalArtifacts) Put(_ context.Context, ref *pb.ArtifactRef, reader io.Reader) (bool, error) {
	raw, err := io.ReadAll(reader)
	if err != nil {
		return false, err
	}
	a.contents[ref.ArtifactId] = raw
	return false, nil
}

type modelProposalProvider struct {
	calls int
	raw   json.RawMessage
	input string
}

func (p *modelProposalProvider) Generate(_ context.Context, request inference.StructuredRequest) (inference.StructuredResponse, error) {
	p.calls++
	p.input = request.Text
	if p.raw != nil {
		return inference.StructuredResponse{JSON: p.raw, InputTokens: 10, OutputTokens: 5}, nil
	}
	return inference.StructuredResponse{JSON: json.RawMessage(`{"action":"DEFER","candidate_id":"","rationale":"No candidate is available.","evidence_item_ids":["chunk:fixture"]}`), InputTokens: 10, OutputTokens: 5}, nil
}

type modelResponseMutator struct {
	model  SemanticResolutionModel
	mutate func(*pb.SemanticResolveResponse)
}

func (m modelResponseMutator) ResolveBatch(ctx context.Context, r *pb.SemanticResolveRequest) (*pb.SemanticResolveResponse, error) {
	response, err := m.model.ResolveBatch(ctx, r)
	if err == nil {
		m.mutate(response)
	}
	return response, err
}
func modelWorkflowFixture(t *testing.T) (*SemanticResolutionHandoff, domain.JobRecord, *pb.ArtifactRef, *pb.SemanticBatchContext, *pb.ProducerManifest, *inference.SemanticService, *modelProposalProvider, *modelProposalStore, *modelProposalArtifacts) {
	t.Helper()
	corpus := "corpus:fixture"
	document := documentBatchFixture(pb.JobStage_JOB_STAGE_CHUNK, corpus, pb.Completeness_COMPLETENESS_COMPLETE)
	textRef := semanticRefForTest("normalized:fixture", []byte("izin"))
	textRef.StorageKey = "text/normalized.txt"
	textRef.MediaType = "text/plain;charset=utf-8"
	document.TextArtifacts[0].NormalizedTextRef = textRef
	document.Versions[0].TextRef = proto.Clone(textRef).(*pb.ArtifactRef)
	documentRaw, err := proto.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	documentRef := semanticRefForTest("artifact:document-model", documentRaw)
	documentRef.MediaType = documentBatchMediaType
	documentRef.StorageKey = "objects/document-model.pb"
	source := extractionBatchFixture(corpus, documentRef)
	source.Mentions = []*pb.Mention{{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpus, RecordId: "mention:fixture"},
		SurfaceForm: "izin", CandidateType: "permit", TextSpan: &pb.TextSpan{TextArtifactId: "text:fixture", StartByte: 0, EndByte: 4},
		SourceRefs:         []*pb.SourceVersionRef{{SourceBlobId: "source:fixture", ProvisionVersionId: "version:fixture", RegulationId: "regulation:fixture"}},
		ExtractionManifest: extractionManifest()}}
	sourceRaw, err := proto.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	sourceRef := semanticRefForTest("artifact:source-model", sourceRaw)
	sourceRef.StorageKey = "objects/source-model.pb"
	scopeID := domain.RegistryLookupScopeID("permit", "national", "izin")
	candidates, err := domain.AssembleRegistryCandidateBatch(source, sourceRef, parseManifest(), "candidates:model", 3,
		[]*pb.CandidateLookup{{MentionId: "mention:fixture", Scopes: []*pb.CandidateLookupScope{{
			Revision:   &pb.LookupScopeRevision{ScopeId: scopeID, Revision: 0, EmptyResult: true},
			EntityType: "permit", CanonicalScope: "national", NormalizedLookup: "izin"}}}}, nil, nil, 1000, 10)
	if err != nil {
		t.Fatal(err)
	}
	candidateRaw, err := proto.Marshal(candidates)
	if err != nil {
		t.Fatal(err)
	}
	candidateRef := semanticRefForTest("artifact:candidates-model", candidateRaw)
	candidateRef.StorageKey = "objects/candidates-model.pb"
	job := domain.JobRecord{JobID: "job:model", CorpusID: corpus, State: pb.JobState_JOB_STATE_RUNNING,
		Stage: pb.JobStage_JOB_STAGE_RESOLVE, LeaseOwner: "owner:model", LeaseFence: 3, LeaseExpiresAt: time.Now().Add(time.Minute)}
	checkpoint := &pb.Checkpoint{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpus, RecordId: "checkpoint:model"},
		JobId: job.JobID, Stage: pb.JobStage_JOB_STAGE_EXTRACT, Fence: 2, TerminalStatus: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED,
		CompletedBatchKeys: []string{sourceRef.ArtifactId}, ArtifactHashes: []*pb.ContentHash{sourceRef.ContentHash}}
	store := &modelProposalStore{candidateRaceStore: &candidateRaceStore{semanticHandoffStoreFake: &semanticHandoffStoreFake{
		checkpoint: checkpoint, refs: map[string]*pb.ArtifactRef{sourceRef.ArtifactId: sourceRef, candidateRef.ArtifactId: candidateRef}}},
		revision: 3, dependencies: map[string]*pb.DependencyManifest{}}
	artifacts := &modelProposalArtifacts{&semanticArtifactReaderFake{contents: map[string][]byte{
		sourceRef.ArtifactId: sourceRaw, candidateRef.ArtifactId: candidateRaw, documentRef.ArtifactId: documentRaw, textRef.ArtifactId: []byte("izin")}}}
	model := proto.Clone(source.ModelManifest).(*pb.ModelManifest)
	model.Task = pb.ModelTask_MODEL_TASK_RESOLVE
	prompt, schema := "Resolution fixture", json.RawMessage(`{"type":"object"}`)
	model.PromptHash = semanticRefForTest("prompt", []byte(prompt)).ContentHash
	schemaRef := semanticRefForTest("schema", schema)
	schemaRef.MediaType = "application/schema+json"
	producer := &pb.ProducerManifest{Software: "fixture-gateway", Build: "test", SchemaVersion: 1, ConfigHash: parseHash("c"),
		Models: []*pb.ModelManifest{model}, PromptHashes: []*pb.ContentHash{model.PromptHash},
		InputHashes: []*pb.ContentHash{schemaRef.ContentHash, parseTestOntology().ContentHash()}}
	batch := &pb.SemanticBatchContext{Context: proto.Clone(source.Context).(*pb.RequestContext), Model: model,
		OperationKey: "model:caller", OntologyVersion: source.OntologyVersion, OutputSchema: schemaRef}
	provider := &modelProposalProvider{}
	gateway, err := inference.NewSemanticService(provider, inference.SemanticConfig{
		Model: model, Ontology: parseTestOntology(), OutputSchemaHash: schemaRef.ContentHash, SystemPrompt: prompt, OutputSchema: schema,
		SchemaName: "resolve", Software: producer.Software, Build: producer.Build, ConfigHash: producer.ConfigHash,
		TokenizerID: "fixture:tokenizer", MaximumItems: 10, MaximumInputBytes: 1 << 20, MaximumConcurrent: 2,
		MaximumCacheEntries: 10, MaximumCacheBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	handoff, err := NewSemanticResolutionHandoff(store, artifacts, 1<<20, 1000, 10)
	if err != nil {
		t.Fatal(err)
	}
	return handoff, job, candidateRef, batch, producer, gateway, provider, store, artifacts
}

func TestModelWorkflowHydratesArchivesAndReplaysWithoutProvider(t *testing.T) {
	h, job, candidate, batch, producer, gateway, provider, store, artifacts := modelWorkflowFixture(t)
	// A fresh read nearly exhausts the source budget. Audit replay has a separate bounded
	// allowance and must succeed for the same inputs after a process restart.
	var sourceBytes uint64
	for _, raw := range artifacts.contents {
		sourceBytes += uint64(len(raw))
	}
	h.maximumBytes = sourceBytes + 1
	first, err := h.ProposeWithModel(context.Background(), job, candidate, batch, producer, gateway)
	if err != nil {
		t.Fatal(err)
	}
	if provider.calls != 1 || len(first.Request.Proposals) != 1 || first.Request.Proposals[0].Action != pb.ResolutionAction_RESOLUTION_ACTION_DEFER ||
		first.InputArtifact == nil || first.OutputArtifact == nil || store.commits != 0 {
		t.Fatalf("model workflow lost output or committed unreviewed proposal: %+v", first)
	}
	delete(store.dependencies, first.OutputArtifact.ArtifactId)
	second, err := h.ProposeWithModel(context.Background(), job, candidate, batch, producer, modelResponseMutator{
		model: gateway, mutate: func(*pb.SemanticResolveResponse) { t.Fatal("persisted output should bypass model") }})
	if err != nil {
		t.Fatal(err)
	}
	if provider.calls != 1 || !proto.Equal(first.Response, second.Response) || store.dependencies[first.OutputArtifact.ArtifactId] == nil {
		t.Fatal("durable replay resampled, changed response, or failed dependency repair")
	}
}

func TestModelWorkflowRejectsStaleAndForgedResponses(t *testing.T) {
	for _, name := range []string{"stale before", "stale after", "model drift", "swapped item", "foreign context", "foreign source", "wrong bytes"} {
		t.Run(name, func(t *testing.T) {
			h, job, candidate, batch, producer, gateway, provider, store, artifacts := modelWorkflowFixture(t)
			if name == "stale before" {
				store.revision++
			}
			if name == "wrong bytes" {
				artifacts.contents["normalized:fixture"] = []byte("abcd")
			}
			model := modelResponseMutator{model: gateway, mutate: func(response *pb.SemanticResolveResponse) {
				switch name {
				case "stale after":
					store.revision++
				case "model drift":
					response.Model.Version = "unapproved"
				case "swapped item":
					response.Results[0].ItemId = "other"
				case "foreign context":
					response.Results[0].GetProposal().SupportingContextIds = []string{"unknown"}
				case "foreign source":
					response.Results[0].GetProposal().Evidence.Sources[0].SourceBlobId = "foreign"
				}
			}}
			_, err := h.ProposeWithModel(context.Background(), job, candidate, batch, producer, model)
			if err == nil || store.commits != 0 {
				t.Fatalf("unsafe result accepted: %v", err)
			}
			if (name == "stale before" || name == "wrong bytes") && provider.calls != 0 {
				t.Fatal("invalid input reached model")
			}
			if (name == "stale before" || name == "stale after") && !errors.Is(err, domain.ErrCandidateViewChanged) {
				t.Fatalf("lost retry classification: %v", err)
			}
		})
	}
}

func TestHydrationPreservesChunkVersionAndBoundsExpansion(t *testing.T) {
	h, _, _, batch, _, _, _, store, artifacts := modelWorkflowFixture(t)
	source := new(pb.ExtractionBatch)
	if err := proto.Unmarshal(artifacts.contents[store.checkpoint.CompletedBatchKeys[0]], source); err != nil {
		t.Fatal(err)
	}
	document := new(pb.DocumentBatch)
	if err := proto.Unmarshal(artifacts.contents[source.SourceDocumentBatch.ArtifactId], document); err != nil {
		t.Fatal(err)
	}
	candidates := new(pb.RegistryCandidateBatch)
	if err := proto.Unmarshal(artifacts.contents["artifact:candidates-model"], candidates); err != nil {
		t.Fatal(err)
	}
	broader := proto.Clone(document.Versions[0]).(*pb.ProvisionVersion)
	broader.Meta.RecordId = "version:parent"
	document.Versions = append(document.Versions, broader)
	document.Chunks[0].ProvisionVersionRefs = []string{"version:parent"}
	remaining := uint64(1000)
	request, err := hydrateResolutionRequest(context.Background(), batch, source, candidates, document, artifacts, &remaining, h.maximumBytes, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if request.Items[0].ContextItems[0].Provenance.Sources[0].ProvisionVersionId != "version:parent" ||
		request.Items[0].Evidence.Sources[0].ProvisionVersionId != "version:fixture" {
		t.Fatal("broader context was mislabeled as mention version")
	}
	remaining = 1000
	if _, err = hydrateResolutionRequest(context.Background(), batch, source, candidates, document, artifacts, &remaining, 3, 1000); err == nil {
		t.Fatal("context allocation exceeded incremental byte budget")
	}
	remaining = 1000
	if _, err = hydrateResolutionRequest(context.Background(), batch, source, candidates, document, artifacts, &remaining, 1000, 0); err == nil {
		t.Fatal("coverage work was not bounded")
	}
	shared := &pb.CanonicalEntity{Meta: &pb.RecordMeta{RecordId: "canonical:large"}, PreferredLabel: strings.Repeat("x", 256<<10)}
	candidates.Candidates = []*pb.CanonicalEntity{shared}
	candidates.Lookups[0].Scopes[0].CandidateIds = []string{"canonical:large"}
	other := proto.Clone(source.Mentions[0]).(*pb.Mention)
	other.Meta.RecordId = "mention:second"
	source.Mentions = append(source.Mentions, other)
	lookup := proto.Clone(candidates.Lookups[0]).(*pb.CandidateLookup)
	lookup.MentionId = other.Meta.RecordId
	candidates.Lookups = append(candidates.Lookups, lookup)
	remaining = 1000
	if _, err = hydrateResolutionRequest(context.Background(), batch, source, candidates, document, artifacts, &remaining, 300<<10, 1000); err == nil || !strings.Contains(err.Error(), "candidate expansion") {
		t.Fatalf("shared candidate amplification was not rejected before cloning: %v", err)
	}
}
