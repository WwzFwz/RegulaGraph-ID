// Connects a claimed RESOLVE job to the contextual model gateway and immutable audit artifacts.
// Reads checkpoint/candidate/document/text bytes with bounded verified storage, checks the registry
// fence before/after inference, and returns advisory proposals for the existing commit workflow.
// This does not manufacture review approval or allocate canonical identities. Stable request
// fingerprints reuse persisted terminal responses after restart; prompt/model/input drift cannot
// reuse an old result. Measure hydration/queue/model p95/p99, bytes and candidate/merge quality;
// configs/benchmark-targets.yaml remains REQUIRED_UNMEASURED until a valid production run.
package workflows

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"
	"unicode/utf8"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

// SemanticResolutionModel deliberately has no database/reviewer authority.
type SemanticResolutionModel interface {
	ResolveBatch(context.Context, *pb.SemanticResolveRequest) (*pb.SemanticResolveResponse, error)
}

type SemanticModelProposals struct {
	Request                       *pb.RegistryResolveRequest
	Response                      *pb.SemanticResolveResponse
	InputArtifact, OutputArtifact *pb.ArtifactRef
}

// ProposeWithModel prepares one complete, bounded batch. It never truncates candidates/context
// to silently fit a limit: oversize input fails explicitly so the coordinator can partition it.
// The caller pins the gateway producer, including model, prompt, schema and ontology hashes.
func (h *SemanticResolutionHandoff) ProposeWithModel(ctx context.Context, job domain.JobRecord,
	candidateRef *pb.ArtifactRef, batch *pb.SemanticBatchContext, producer *pb.ProducerManifest,
	model SemanticResolutionModel) (*SemanticModelProposals, error) {
	if h == nil || ctx == nil || model == nil || batch == nil || batch.Context == nil ||
		batch.Context.CorpusId != job.CorpusID || batch.GetModel().GetTask() != pb.ModelTask_MODEL_TASK_RESOLVE ||
		job.State != pb.JobState_JOB_STATE_RUNNING || job.Stage != pb.JobStage_JOB_STAGE_RESOLVE ||
		job.CancellationRequested || !job.LeaseExpiresAt.After(time.Now()) || candidateRef == nil {
		return nil, errors.New("model proposal requires a live RESOLVE claim and pinned model")
	}
	store, ok := h.store.(SemanticCandidateStore)
	artifacts, artifactOK := h.artifacts.(SemanticCandidateArtifacts)
	if !ok || !artifactOK {
		return nil, errors.New("model proposal requires fenced candidate store and artifact writer")
	}
	if err := domain.ValidateWire(batch, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	if err := domain.ValidateWire(producer, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	if !modelProducerMatches(producer, batch) {
		return nil, errors.New("resolution producer does not pin model, prompt and schema")
	}
	deadline := job.LeaseExpiresAt
	if batch.Context.Deadline.AsTime().Before(deadline) {
		deadline = batch.Context.Deadline.AsTime()
	}
	attempt, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	checkpoint, err := store.LoadLatestCheckpoint(attempt, job.JobID)
	if err != nil {
		return nil, err
	}
	if checkpoint.GetMeta().GetCorpusId() != job.CorpusID || checkpoint.JobId != job.JobID ||
		checkpoint.Stage != pb.JobStage_JOB_STAGE_EXTRACT || checkpoint.TerminalStatus != pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED ||
		checkpoint.Fence == 0 || checkpoint.Fence >= job.LeaseFence ||
		len(checkpoint.CompletedBatchKeys) != 1 || len(checkpoint.ArtifactHashes) != 1 {
		return nil, errors.New("model resolution requires complete EXTRACT checkpoint")
	}
	sourceRef, err := store.LoadArtifact(attempt, job.CorpusID, checkpoint.CompletedBatchKeys[0])
	if err != nil {
		return nil, err
	}
	if !proto.Equal(sourceRef.GetContentHash(), checkpoint.ArtifactHashes[0]) {
		return nil, domain.ErrPersistentIntegrity
	}
	registered, err := store.LoadArtifact(attempt, job.CorpusID, candidateRef.ArtifactId)
	if err != nil {
		return nil, err
	}
	if !proto.Equal(registered, candidateRef) {
		return nil, domain.ErrPersistentIntegrity
	}
	remaining := h.maximumBytes
	readAudit := func(ref *pb.ArtifactRef, message proto.Message) error {
		if ref == nil || ref.MediaType != "application/x-protobuf" || ref.ByteSize == 0 || ref.ByteSize > h.maximumBytes {
			return errors.New("resolution audit artifact exceeds byte budget")
		}
		raw, e := artifacts.ReadVerified(attempt, ref, h.maximumBytes)
		if e != nil {
			return e
		}
		return domain.DecodeWire(raw, message, domain.DefaultWireLimits)
	}
	read := func(ref *pb.ArtifactRef, message proto.Message) error {
		if ref == nil || !resolutionProtoMedia(ref.MediaType, message) || ref.ByteSize == 0 || ref.ByteSize > remaining {
			return errors.New("resolution artifact exceeds combined byte budget or media contract")
		}
		raw, readErr := artifacts.ReadVerified(attempt, ref, remaining)
		if readErr != nil {
			return readErr
		}
		remaining -= ref.ByteSize
		return domain.DecodeWire(raw, message, domain.DefaultWireLimits)
	}
	source, candidates, document := new(pb.ExtractionBatch), new(pb.RegistryCandidateBatch), new(pb.DocumentBatch)
	if err = read(sourceRef, source); err != nil {
		return nil, err
	}
	if err = read(candidateRef, candidates); err != nil {
		return nil, err
	}
	if err = domain.ValidateRegistryCandidateBatch(candidates, source, sourceRef, h.maximumReferences, h.maximumCandidatesPerMention); err != nil {
		return nil, err
	}
	if source.Completeness != pb.Completeness_COMPLETENESS_COMPLETE || len(source.Mentions) == 0 ||
		batch.OntologyVersion != source.OntologyVersion || !proto.Equal(batch.Context.SnapshotRef, source.Context.SnapshotRef) ||
		batch.Context.AuthScopeRef != source.Context.AuthScopeRef {
		return nil, errors.New("model input must retain complete source, snapshot, authorization and ontology")
	}
	proof := domain.SemanticJobFence{JobID: job.JobID, OwnerID: job.LeaseOwner, Fence: job.LeaseFence, SourceCheckpointID: checkpoint.Meta.RecordId}
	checkRevision := func() error {
		revision, e := store.ReadFencedResolveRevision(attempt, proof, sourceRef, source)
		if e != nil {
			return e
		}
		if revision != candidates.RegistryRevision {
			return domain.ErrCandidateViewChanged
		}
		return nil
	}
	if err = checkRevision(); err != nil {
		return nil, err
	}
	if err = read(source.SourceDocumentBatch, document); err != nil {
		return nil, err
	}
	if err = domain.ValidateDocumentBatchClosure(document, h.maximumReferences); err != nil {
		return nil, err
	}
	if err = domain.ValidateExtractionBatchClosure(source, document, h.maximumReferences); err != nil {
		return nil, err
	}
	budget := newResolutionHydrationBudget(h.maximumBytes, h.maximumReferences)
	request, err := hydrateResolutionRequestWithBudget(attempt, batch, source, candidates, document, artifacts, &remaining, budget)
	if err != nil {
		return nil, err
	}
	evidenceRefs, err := h.hydrateCandidateEvidence(attempt, request, candidates, artifacts, &remaining, budget)
	if err != nil {
		return nil, err
	}
	for _, item := range request.Items {
		if err = domain.ValidateSemanticResolutionItem(item, job.CorpusID, h.maximumReferences); err != nil {
			return nil, err
		}
	}
	modelDeps, err := uniqueModelDependencies(append([]*pb.ArtifactRef{candidateRef, sourceRef}, evidenceRefs...))
	if err != nil {
		return nil, err
	}
	if proto.Size(request) > domain.DefaultWireLimits.MaxBytes || uint64(proto.Size(request)) > h.maximumBytes {
		return nil, errors.New("hydrated resolution request exceeds byte budget")
	}
	fingerprint, err := resolutionModelFingerprint(request, producer, modelDeps...)
	if err != nil {
		return nil, err
	}
	request.Batch.OperationKey = "resolve-model:" + fingerprint
	request.Batch.Context.RequestId = "resolve-request:" + fingerprint
	request.Batch.Context.TraceId = batch.Context.TraceId
	request.Batch.Context.Deadline = timestamppb.New(job.LeaseExpiresAt)
	if batch.Context.Deadline.AsTime().Before(job.LeaseExpiresAt) {
		request.Batch.Context.Deadline = proto.Clone(batch.Context.Deadline).(*timestamppb.Timestamp)
	}
	inputID, outputID := "artifact:resolve-input:"+fingerprint, "artifact:resolve-output:"+fingerprint
	inputRef, err := store.LoadArtifact(attempt, job.CorpusID, inputID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}
	if errors.Is(err, domain.ErrNotFound) {
		inputRef, err = h.saveModelArtifact(attempt, job.CorpusID, inputID, request, producer, modelDeps...)
		if err != nil {
			return nil, err
		}
	} else {
		// An existing audit request may carry an earlier deadline; only execution metadata varies.
		existing := new(pb.SemanticResolveRequest)
		if err = readAudit(inputRef, existing); err != nil {
			return nil, err
		}
		actual, e := resolutionModelFingerprint(existing, producer, modelDeps...)
		if e != nil || actual != fingerprint {
			return nil, domain.ErrPersistentIntegrity
		}
	}
	outputRef, err := store.LoadArtifact(attempt, job.CorpusID, outputID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}
	response := new(pb.SemanticResolveResponse)
	reused := err == nil
	if reused {
		if err = readAudit(outputRef, response); err != nil {
			return nil, err
		}
	} else {
		response, err = model.ResolveBatch(attempt, request)
		if err != nil {
			return nil, err
		}
	}
	if err = validateModelResolutionResponse(request, response, producer, source, sourceRef, candidates,
		h.maximumReferences, h.maximumCandidatesPerMention); err != nil {
		return nil, err
	}
	if err = checkRevision(); err != nil {
		return nil, err
	}
	if !reused {
		outputRef, err = h.saveModelArtifact(attempt, job.CorpusID, outputID, response, producer, append([]*pb.ArtifactRef{inputRef}, modelDeps...)...)
		if err != nil {
			return nil, err
		}
	}
	// Reinstall dependencies on replay too: a crash may occur between artifact registration and
	// its dependency write. The artifact remains advisory until the normal registry commit.
	for _, entry := range []struct {
		ref  *pb.ArtifactRef
		deps []*pb.ArtifactRef
	}{
		{inputRef, modelDeps},
		{outputRef, append([]*pb.ArtifactRef{inputRef}, modelDeps...)},
	} {
		if err = store.ReplaceArtifactDependencyManifest(attempt, job.CorpusID, entry.ref.ArtifactId,
			modelArtifactDependencies(entry.ref, producer, entry.deps...)); err != nil {
			return nil, err
		}
	}
	proposals := make([]*pb.ResolutionProposal, len(response.Results))
	for i, result := range response.Results {
		proposals[i] = proto.Clone(result.GetProposal()).(*pb.ResolutionProposal)
	}
	return &SemanticModelProposals{Request: &pb.RegistryResolveRequest{
		Context: proto.Clone(request.Batch.Context).(*pb.RequestContext), OperationKey: request.Batch.OperationKey,
		ExpectedRevision: candidates.RegistryRevision, Proposals: proposals},
		Response: response, InputArtifact: inputRef, OutputArtifact: outputRef}, nil
}

func hydrateResolutionRequest(ctx context.Context, batch *pb.SemanticBatchContext, source *pb.ExtractionBatch,
	candidates *pb.RegistryCandidateBatch, document *pb.DocumentBatch, artifacts SemanticResolutionArtifactReader,
	remaining *uint64, maximumRequestBytes uint64, maximumWork int) (*pb.SemanticResolveRequest, error) {
	return hydrateResolutionRequestWithBudget(ctx, batch, source, candidates, document, artifacts, remaining,
		newResolutionHydrationBudget(maximumRequestBytes, maximumWork))
}

func hydrateResolutionRequestWithBudget(ctx context.Context, batch *pb.SemanticBatchContext, source *pb.ExtractionBatch,
	candidates *pb.RegistryCandidateBatch, document *pb.DocumentBatch, artifacts SemanticResolutionArtifactReader,
	remaining *uint64, budget *resolutionHydrationBudget) (*pb.SemanticResolveRequest, error) {
	request := &pb.SemanticResolveRequest{Batch: proto.Clone(batch).(*pb.SemanticBatchContext)}
	texts := map[string]*pb.TextArtifact{}
	for _, text := range document.TextArtifacts {
		texts[text.Meta.RecordId] = text
	}
	entities := map[string]*pb.CanonicalEntity{}
	for _, entity := range candidates.Candidates {
		entities[entity.Meta.RecordId] = entity
	}
	allowed := map[string]map[string]bool{}
	for _, lookup := range candidates.Lookups {
		allowed[lookup.MentionId] = map[string]bool{}
		for _, scope := range lookup.Scopes {
			for _, id := range scope.CandidateIds {
				allowed[lookup.MentionId][id] = true
			}
		}
	}
	loaded := budget.texts
	reserve := budget.reserve
	if err := reserve(uint64(proto.Size(request)) + 32); err != nil {
		return nil, err
	}
	if budget.work <= 0 || len(document.Chunks) != 0 && len(source.Mentions) > budget.work/len(document.Chunks) {
		return nil, errors.New("resolution context coverage work exceeds budget")
	}
	budget.work -= len(source.Mentions) * len(document.Chunks)
	versions := map[string]*pb.ProvisionVersion{}
	provisions := map[string]*pb.Provision{}
	for _, version := range document.Versions {
		versions[version.Meta.RecordId] = version
	}
	for _, provision := range document.Provisions {
		provisions[provision.Meta.RecordId] = provision
	}
	for _, mention := range source.Mentions {
		// Reserve duplicated mention/evidence metadata before any clone. Shared canonical records
		// and context are repeated on the wire per item and must consume the same aggregate budget.
		metadataBytes := uint64(proto.Size(mention)+proto.Size(mention.TextSpan)+len(mention.Meta.RecordId)) + 128
		for _, sourceRef := range mention.SourceRefs {
			metadataBytes += uint64(proto.Size(sourceRef)) + 16
		}
		if err := reserve(metadataBytes); err != nil {
			return nil, err
		}
		span := mention.TextSpan
		text := texts[span.TextArtifactId]
		if text == nil || text.NormalizedTextRef == nil {
			return nil, errors.New("mention lacks normalized text artifact")
		}
		ref := text.NormalizedTextRef
		raw, ok := loaded[ref.ArtifactId]
		if ok && !proto.Equal(budget.textRefs[ref.ArtifactId], ref) {
			return nil, domain.ErrPersistentIntegrity
		}
		if !ok {
			if ref.ByteSize == 0 || ref.ByteSize > *remaining || !isUTF8TextMediaType(ref.MediaType) {
				return nil, errors.New("resolution text exceeds byte budget or media contract")
			}
			var err error
			raw, err = artifacts.ReadVerified(ctx, ref, *remaining)
			if err != nil {
				return nil, err
			}
			if uint64(len(raw)) != ref.ByteSize || !utf8.Valid(raw) {
				return nil, errors.New("resolution text size/UTF-8 differs")
			}
			*remaining -= ref.ByteSize
			loaded[ref.ArtifactId] = raw
			budget.textRefs[ref.ArtifactId] = ref
		}
		// All covering structural chunks are included: overlapping chunks may supply a definition
		// or exception absent from the smallest window. Oversize is reported rather than truncated.
		contexts := []*pb.TextItem{}
		for _, chunk := range document.Chunks {
			cs := chunk.TextSpan
			if cs.TextArtifactId != span.TextArtifactId || cs.StartByte > span.StartByte || cs.EndByte < span.EndByte {
				continue
			}
			if cs.EndByte > uint64(len(raw)) || cs.StartByte >= cs.EndByte || !utf8.Valid(raw[cs.StartByte:cs.EndByte]) {
				return nil, errors.New("resolution chunk has invalid UTF-8 span")
			}
			if err := reserve(cs.EndByte - cs.StartByte + uint64(proto.Size(cs)+len(chunk.Meta.RecordId)) + 64); err != nil {
				return nil, err
			}
			chunkSources := make([]*pb.SourceVersionRef, 0, len(chunk.ProvisionVersionRefs))
			for _, versionID := range chunk.ProvisionVersionRefs {
				version := versions[versionID]
				if version == nil || provisions[version.ProvisionId] == nil {
					return nil, errors.New("chunk version has no provision binding")
				}
				sourceRef := &pb.SourceVersionRef{SourceBlobId: text.SourceBlobId,
					ProvisionVersionId: versionID, RegulationId: provisions[version.ProvisionId].RegulationId}
				if err := reserve(uint64(proto.Size(sourceRef)) + 16); err != nil {
					return nil, err
				}
				chunkSources = append(chunkSources, sourceRef)
			}
			contexts = append(contexts, &pb.TextItem{ItemId: chunk.Meta.RecordId, Text: string(raw[cs.StartByte:cs.EndByte]),
				Provenance: &pb.Provenance{Sources: chunkSources, Spans: []*pb.TextSpan{proto.Clone(cs).(*pb.TextSpan)}}})
		}
		if len(contexts) == 0 {
			return nil, errors.New("mention has no covering structural chunk")
		}
		if span.EndByte > uint64(len(raw)) || span.StartByte >= span.EndByte || string(raw[span.StartByte:span.EndByte]) != mention.SurfaceForm {
			return nil, errors.New("mention differs from verified text")
		}
		item := &pb.AmbiguousMention{ItemId: mention.Meta.RecordId, Mention: proto.Clone(mention).(*pb.Mention),
			ExpectedRegistryRevision: candidates.RegistryRevision, ContextItems: contexts,
			Evidence: &pb.Provenance{Sources: cloneModelSources(mention.SourceRefs), Spans: []*pb.TextSpan{proto.Clone(span).(*pb.TextSpan)}}}
		ids := make([]string, 0, len(allowed[mention.Meta.RecordId]))
		for id := range allowed[mention.Meta.RecordId] {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			if entities[id] == nil {
				return nil, errors.New("resolution candidate is missing")
			}
			if err := reserve(uint64(proto.Size(entities[id])) + 16); err != nil {
				return nil, err
			}
			item.Candidates = append(item.Candidates, proto.Clone(entities[id]).(*pb.CanonicalEntity))
		}
		request.Items = append(request.Items, item)
	}
	return request, nil
}

func resolutionProtoMedia(media string, message proto.Message) bool {
	if media == "application/x-protobuf" {
		return true
	}
	switch message.(type) {
	case *pb.DocumentBatch:
		return media == documentBatchMediaType
	case *pb.ExtractionBatch:
		return media == extractionBatchMediaType
	default:
		return false
	}
}
func cloneModelSources(values []*pb.SourceVersionRef) []*pb.SourceVersionRef {
	output := make([]*pb.SourceVersionRef, len(values))
	for i, value := range values {
		output[i] = proto.Clone(value).(*pb.SourceVersionRef)
	}
	return output
}

func modelProducerMatches(producer *pb.ProducerManifest, batch *pb.SemanticBatchContext) bool {
	model, prompt, schema := false, false, false
	for _, value := range producer.Models {
		model = model || proto.Equal(value, batch.Model)
	}
	for _, value := range producer.PromptHashes {
		prompt = prompt || proto.Equal(value, batch.Model.PromptHash)
	}
	for _, value := range producer.InputHashes {
		schema = schema || proto.Equal(value, batch.OutputSchema.ContentHash)
	}
	return model && prompt && schema
}
func resolutionModelFingerprint(request *pb.SemanticResolveRequest, producer *pb.ProducerManifest, refs ...*pb.ArtifactRef) (string, error) {
	stable := proto.Clone(request).(*pb.SemanticResolveRequest)
	stable.Batch.Context.RequestId = ""
	stable.Batch.Context.TraceId = ""
	stable.Batch.Context.Deadline = nil
	stable.Batch.OperationKey = ""
	hasher := sha256.New()
	messages := []proto.Message{stable, producer}
	for _, ref := range refs {
		messages = append(messages, ref)
	}
	for _, message := range messages {
		raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(hasher, "%d:", len(raw))
		hasher.Write(raw)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}
func (h *SemanticResolutionHandoff) saveModelArtifact(ctx context.Context, corpus, id string, message proto.Message,
	producer *pb.ProducerManifest, deps ...*pb.ArtifactRef) (*pb.ArtifactRef, error) {
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || uint64(len(raw)) > h.maximumBytes {
		return nil, errors.New("model artifact exceeds byte budget")
	}
	digest := sha256.Sum256(raw)
	hash := hex.EncodeToString(digest[:])
	ref := &pb.ArtifactRef{ArtifactId: id, ContentHash: &pb.ContentHash{Sha256: hash},
		StorageKey: "sha256/" + hash[:2] + "/" + hash[2:4] + "/" + hash + ".bin", MediaType: "application/x-protobuf", ByteSize: uint64(len(raw)), SchemaVersion: 1}
	artifacts := h.artifacts.(SemanticCandidateArtifacts)
	store := h.store.(SemanticCandidateStore)
	if _, err = artifacts.Put(ctx, ref, bytes.NewReader(raw)); err != nil {
		return nil, err
	}
	if err = store.RegisterArtifact(ctx, corpus, ref); err != nil {
		return nil, err
	}
	if err = store.ReplaceArtifactDependencyManifest(ctx, corpus, id, modelArtifactDependencies(ref, producer, deps...)); err != nil {
		return nil, err
	}
	return ref, nil
}
func modelArtifactDependencies(ref *pb.ArtifactRef, producer *pb.ProducerManifest, deps ...*pb.ArtifactRef) *pb.DependencyManifest {
	manifest := &pb.DependencyManifest{ArtifactId: ref.ArtifactId, ProducerManifest: proto.Clone(producer).(*pb.ProducerManifest)}
	for _, dep := range deps {
		manifest.Dependencies = append(manifest.Dependencies, &pb.Dependency{DependencyId: dep.ArtifactId, Fingerprint: proto.Clone(dep.ContentHash).(*pb.ContentHash)})
	}
	return manifest
}
func validateModelResolutionResponse(request *pb.SemanticResolveRequest, response *pb.SemanticResolveResponse,
	producer *pb.ProducerManifest, source *pb.ExtractionBatch, sourceRef *pb.ArtifactRef, candidates *pb.RegistryCandidateBatch,
	references, maximumCandidates int) error {
	if err := domain.ValidateWire(response, domain.DefaultWireLimits); err != nil {
		return err
	}
	if response.RequestId != request.Batch.Context.RequestId || !proto.Equal(response.Model, request.Batch.Model) ||
		!proto.Equal(response.ProducerManifest, producer) || len(response.Results) != len(request.Items) {
		return errors.New("model response identity, manifest or coverage differs")
	}
	expected := map[string]*pb.AmbiguousMention{}
	for _, item := range request.Items {
		expected[item.ItemId] = item
	}
	proposals := make([]*pb.ResolutionProposal, 0, len(response.Results))
	for _, result := range response.Results {
		item := expected[result.GetItemId()]
		if item == nil {
			return errors.New("model response has unexpected or repeated item")
		}
		delete(expected, result.ItemId)
		if failure := result.GetError(); failure != nil {
			return fmt.Errorf("resolution item %s failed (%s, retryable=%t)", result.ItemId, failure.Code, failure.Retryable)
		}
		proposal := result.GetProposal()
		if proposal == nil || len(proposal.MentionIds) != 1 || proposal.MentionIds[0] != item.Mention.Meta.RecordId ||
			proposal.GetRationale() == "" || len(proposal.SupportingContextIds) == 0 {
			return errors.New("model proposal lost correlation or explanation")
		}
		if err := domain.ValidateResolutionContextReferences(item, proposal, references); err != nil {
			return err
		}
		proposals = append(proposals, proposal)
	}
	return domain.ValidateResolutionProposalsAgainstCandidates(source, sourceRef, candidates, proposals, references, maximumCandidates)
}
