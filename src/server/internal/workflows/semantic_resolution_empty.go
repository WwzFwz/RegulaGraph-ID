// Completes RESOLVE for a verified EXTRACT batch with zero mentions, preserving the observed
// registry revision without fabricating LINK/DEFER proposals. The same fenced artifact and
// checkpoint path is used, and a terminal checkpoint is recoverable after a crash. Measure
// stage latency, storage bytes, and lock wait; required targets remain REQUIRED_UNMEASURED.
package workflows

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type SemanticEmptyResolutionStore interface {
	SemanticResolutionOutputStore
	ReadEmptyResolveRevision(context.Context, domain.SemanticJobFence,
		*pb.ArtifactRef, *pb.ExtractionBatch) (uint64, error)
}

// CompleteEmptyResolution has no registry side effect and requires no candidate artifact.
func (handoff *SemanticResolutionHandoff) CompleteEmptyResolution(ctx context.Context,
	job domain.JobRecord, model *pb.ModelManifest, producer *pb.ProducerManifest,
	usage *pb.TokenUsage, recordID string) (*SemanticResolutionOutput, error) {
	if handoff == nil || ctx == nil || job.JobID == "" || job.CorpusID == "" ||
		job.State != pb.JobState_JOB_STATE_RUNNING || job.Stage != pb.JobStage_JOB_STAGE_RESOLVE ||
		job.LeaseOwner == "" || job.LeaseFence == 0 || job.CancellationRequested ||
		model == nil || producer == nil || usage == nil || recordID == "" ||
		model.Task != pb.ModelTask_MODEL_TASK_RESOLVE {
		return nil, errors.New("claimed mention-free RESOLVE and pinned producer required")
	}
	store, storeOK := handoff.store.(SemanticEmptyResolutionStore)
	artifacts, artifactsOK := handoff.artifacts.(SemanticResolutionOutputArtifacts)
	if !storeOK || !artifactsOK {
		return nil, errors.New("same durable store and artifact reader required for empty RESOLVE")
	}
	for _, item := range []proto.Message{model, producer, usage} {
		if err := domain.ValidateWire(item, domain.DefaultWireLimits); err != nil {
			return nil, fmt.Errorf("invalid empty RESOLVE producer: %w", err)
		}
	}
	if recovered, handled, err := handoff.recoverOutput(ctx, store, artifacts, job, model, producer, nil, recordID); handled {
		return recovered, err
	} else if err != nil {
		return nil, err
	}
	checkpoint, err := store.LoadLatestCheckpoint(ctx, job.JobID)
	if err != nil || checkpoint == nil || checkpoint.Stage != pb.JobStage_JOB_STAGE_EXTRACT ||
		checkpoint.TerminalStatus != pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED ||
		checkpoint.GetMeta().GetCorpusId() != job.CorpusID || checkpoint.JobId != job.JobID ||
		checkpoint.Fence == 0 || checkpoint.Fence >= job.LeaseFence ||
		len(checkpoint.CompletedBatchKeys) != 1 || len(checkpoint.ArtifactHashes) != 1 {
		return nil, fmt.Errorf("empty RESOLVE requires successful EXTRACT checkpoint: %w", errors.Join(err, domain.ErrPersistentIntegrity))
	}
	sourceRef, err := store.LoadArtifact(ctx, job.CorpusID, checkpoint.CompletedBatchKeys[0])
	if err != nil || !proto.Equal(sourceRef.GetContentHash(), checkpoint.ArtifactHashes[0]) ||
		!resolutionProtoMedia(sourceRef.MediaType, new(pb.ExtractionBatch)) {
		return nil, fmt.Errorf("empty RESOLVE source differs from checkpoint: %w", errors.Join(err, domain.ErrPersistentIntegrity))
	}
	sourceRaw, err := artifacts.ReadVerified(ctx, sourceRef, handoff.maximumBytes)
	if err != nil {
		return nil, fmt.Errorf("read empty RESOLVE source: %w", err)
	}
	source := new(pb.ExtractionBatch)
	if err = domain.DecodeWire(sourceRaw, source, domain.DefaultWireLimits); err != nil ||
		len(source.Mentions) != 0 || len(source.Assertions) != 0 || len(source.Supports) != 0 ||
		source.Completeness != pb.Completeness_COMPLETENESS_COMPLETE ||
		source.GetItemCounts().GetExpected() == 0 ||
		source.GetItemCounts().GetAccepted() != source.GetItemCounts().GetExpected() ||
		source.GetItemCounts().GetRejected() != 0 ||
		source.GetModelManifest().GetTask() != pb.ModelTask_MODEL_TASK_EXTRACT ||
		!proto.Equal(source.GetModelManifest().GetPromptHash(), source.GetPromptHash()) {
		return nil, fmt.Errorf("EXTRACT is not a complete mention-free batch: %w", errors.Join(err, domain.ErrPersistentIntegrity))
	}
	producerPinned, promptPinned, sourcePinned := false, false, false
	for _, pinned := range source.GetDependencies().GetProducerManifest().GetModels() {
		producerPinned = producerPinned || proto.Equal(pinned, source.ModelManifest)
	}
	for _, pinned := range source.GetDependencies().GetProducerManifest().GetPromptHashes() {
		promptPinned = promptPinned || proto.Equal(pinned, source.PromptHash)
	}
	for _, pinned := range source.GetDependencies().GetDependencies() {
		sourcePinned = sourcePinned || pinned.GetDependencyId() == source.GetSourceDocumentBatch().GetArtifactId() &&
			proto.Equal(pinned.GetFingerprint(), source.GetSourceDocumentBatch().GetContentHash())
	}
	if !producerPinned || !promptPinned || !sourcePinned {
		return nil, fmt.Errorf("mention-free EXTRACT lost producer or document dependency: %w", domain.ErrPersistentIntegrity)
	}
	proof := domain.SemanticJobFence{JobID: job.JobID, OwnerID: job.LeaseOwner,
		SourceCheckpointID: checkpoint.Meta.RecordId, Fence: job.LeaseFence}
	revision, err := store.ReadEmptyResolveRevision(ctx, proof, sourceRef, source)
	if err != nil {
		return nil, err
	}
	manifestDigest := sha256.Sum256([]byte(recordID))
	batch := &pb.ResolutionBatch{Meta: &pb.RecordMeta{SchemaVersion: source.Meta.SchemaVersion,
		CorpusId: job.CorpusID, RecordId: recordID}, Context: proto.Clone(source.Context).(*pb.RequestContext),
		SourceExtractionBatch: proto.Clone(sourceRef).(*pb.ArtifactRef),
		Dependencies: &pb.DependencyManifest{ArtifactId: "dependencies:" + hex.EncodeToString(manifestDigest[:]),
			Dependencies: []*pb.Dependency{{DependencyId: sourceRef.ArtifactId,
				Fingerprint: proto.Clone(sourceRef.ContentHash).(*pb.ContentHash)}},
			ProducerManifest: proto.Clone(producer).(*pb.ProducerManifest)},
		Completeness: pb.Completeness_COMPLETENESS_COMPLETE, OntologyVersion: source.OntologyVersion,
		RegistryRevision: revision, ModelManifest: proto.Clone(model).(*pb.ModelManifest),
		ItemCounts: &pb.Counts{}, TokenUsage: proto.Clone(usage).(*pb.TokenUsage)}
	if err = domain.ValidateResolutionBatchClosure(batch, source, sourceRef,
		handoff.maximumReferences); err != nil {
		return nil, fmt.Errorf("empty RESOLVE closure: %w", err)
	}
	if err = domain.ValidateWire(batch, domain.DefaultWireLimits); err != nil {
		return nil, fmt.Errorf("empty RESOLVE wire: %w", err)
	}
	return persistSemanticResolutionBatch(ctx, store, artifacts, job, batch, producer)
}
