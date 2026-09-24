// Persists a checked registry receipt as an immutable RESOLVE batch and terminal checkpoint.
// Registry decisions precede artifact publication; retry must reuse the operation key so a
// crash between commit and checkpoint replays the same decision. The caller supplies a pinned
// RESOLVE model/producer and an authenticated review assertion for every LINK. Measure hash,
// storage, checkpoint, and completion p95/p99 plus retry wait; required targets remain
// REQUIRED_UNMEASURED in configs/benchmark-targets.yaml.
package workflows

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"reflect"
	"time"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

// SemanticResolutionOutputStore extends the fenced writer with durable stage primitives.
type SemanticResolutionOutputStore interface {
	SemanticResolutionStore
	SaveSemanticResolutionIntent(context.Context, domain.SemanticJobFence, *pb.ArtifactRef,
		*pb.ExtractionBatch, domain.SemanticResolutionIntent) error
	LoadSemanticResolutionIntent(context.Context, string, string) (domain.SemanticResolutionIntent, error)
	RegisterArtifact(context.Context, string, *pb.ArtifactRef) error
	ReplaceArtifactDependencyManifest(context.Context, string, string, *pb.DependencyManifest) error
	SaveCheckpoint(context.Context, *pb.Checkpoint, string) error
	CompleteWorkerAttempt(context.Context, string, string, uint64, pb.JobState, time.Duration) (pb.JobState, error)
}

type SemanticResolutionOutputArtifacts interface {
	SemanticResolutionArtifactReader
	Put(context.Context, *pb.ArtifactRef, io.Reader) (bool, error)
}

type SemanticResolutionOutput struct {
	Batch      *pb.ResolutionBatch
	Artifact   *pb.ArtifactRef
	Checkpoint *pb.Checkpoint
}

// CommitAndCheckpoint completes a claimed RESOLVE attempt. Inputs must be reproducible across
// retries; this method never substitutes an unreviewed LINK when a review is absent.
func (handoff *SemanticResolutionHandoff) CommitAndCheckpoint(ctx context.Context,
	job domain.JobRecord, candidateRef *pb.ArtifactRef, request *pb.RegistryResolveRequest,
	approvals []domain.ReviewedLink, model *pb.ModelManifest, producer *pb.ProducerManifest,
	usage *pb.TokenUsage, recordID string) (*SemanticResolutionOutput, error) {
	if handoff == nil || ctx == nil || job.JobID == "" || job.CorpusID == "" {
		return nil, errors.New("RESOLVE output requires a claimed job")
	}
	store, storeOK := handoff.store.(SemanticResolutionOutputStore)
	artifacts, artifactsOK := handoff.artifacts.(SemanticResolutionOutputArtifacts)
	if !storeOK || !artifactsOK {
		return nil, errors.New("RESOLVE output requires the same durable store and artifact reader as the fenced writer")
	}
	intent, err := store.LoadSemanticResolutionIntent(ctx, job.CorpusID, job.JobID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return nil, fmt.Errorf("load durable RESOLVE intent: %w", err)
	}
	if err == nil {
		if candidateRef != nil && !proto.Equal(candidateRef, intent.CandidateRef) ||
			request != nil && !proto.Equal(request, intent.Request) ||
			approvals != nil && !reflect.DeepEqual(approvals, intent.Approvals) ||
			model != nil && !proto.Equal(model, intent.Preview.ModelManifest) ||
			producer != nil && !proto.Equal(producer, intent.Preview.GetDependencies().GetProducerManifest()) ||
			usage != nil && !proto.Equal(usage, intent.Preview.TokenUsage) ||
			recordID != "" && recordID != intent.Preview.GetMeta().GetRecordId() {
			return nil, errors.New("RESOLVE retry differs from the durable pre-CAS intent")
		}
		candidateRef, request, approvals = intent.CandidateRef, intent.Request, intent.Approvals
		model, producer, usage = intent.Preview.ModelManifest,
			intent.Preview.GetDependencies().GetProducerManifest(), intent.Preview.TokenUsage
		recordID = intent.Preview.GetMeta().GetRecordId()
	}
	if model == nil || producer == nil || usage == nil || recordID == "" ||
		model.Task != pb.ModelTask_MODEL_TASK_RESOLVE {
		return nil, errors.New("RESOLVE output requires bounded model, producer, usage, and identity")
	}
	for _, item := range []proto.Message{model, producer, usage} {
		if err := domain.ValidateWire(item, domain.DefaultWireLimits); err != nil {
			return nil, fmt.Errorf("invalid RESOLVE output manifest: %w", err)
		}
	}
	if err := domain.ValidateWire(&pb.RecordMeta{SchemaVersion: 1, CorpusId: job.CorpusID,
		RecordId: recordID}, domain.DefaultWireLimits); err != nil {
		return nil, fmt.Errorf("invalid RESOLVE output identity: %w", err)
	}
	modelPinned := false
	for _, pinned := range producer.Models {
		modelPinned = modelPinned || proto.Equal(pinned, model)
	}
	if !modelPinned {
		return nil, errors.New("RESOLVE producer does not pin the selected model")
	}
	if recovered, handled, recoveryErr := handoff.recoverOutput(ctx, store, artifacts, job, model, producer, intent.Preview, recordID); handled {
		return recovered, recoveryErr
	} else if recoveryErr != nil {
		return nil, recoveryErr
	}
	var batch *pb.ResolutionBatch
	var preview *pb.RegistryResolveResponse
	preflight := func(input domain.SemanticRegistryInputs, checkpointID string) error {
		source := new(pb.ExtractionBatch)
		if err := domain.DecodeWire(input.SourceBytes, source, domain.DefaultWireLimits); err != nil {
			return fmt.Errorf("decode EXTRACT bytes: %w", err)
		}
		candidates := new(pb.RegistryCandidateBatch)
		if err := domain.DecodeWire(input.CandidateBytes, candidates, domain.DefaultWireLimits); err != nil {
			return fmt.Errorf("decode candidate bytes: %w", err)
		}
		var err error
		preview, err = domain.PreviewSemanticResolutionReceipt(request, approvals)
		if err != nil {
			return err
		}
		batch, err = domain.AssembleResolutionBatchFromReceipt(source, input.SourceRef,
			candidates, candidateRef, request, preview, model, producer, usage,
			recordID, handoff.maximumReferences, handoff.maximumCandidatesPerMention)
		if err != nil {
			return err
		}
		if intent.Preview != nil && !proto.Equal(intent.Preview, batch) {
			return fmt.Errorf("durable RESOLVE intent differs from current verified bytes: %w", domain.ErrPersistentIntegrity)
		}
		proof := domain.SemanticJobFence{JobID: job.JobID, OwnerID: job.LeaseOwner,
			SourceCheckpointID: checkpointID, Fence: job.LeaseFence}
		return store.SaveSemanticResolutionIntent(ctx, proof, input.SourceRef, source,
			domain.SemanticResolutionIntent{SourceCheckpointID: checkpointID,
				CandidateRef: candidateRef, Request: request, Approvals: approvals, Preview: batch})
	}
	committed, err := handoff.commitVerified(ctx, job, candidateRef, request, approvals, preflight)
	if err != nil {
		return nil, err
	}
	if !proto.Equal(committed.response, preview) {
		return nil, fmt.Errorf("registry receipt differs from preflight: %w", domain.ErrPersistentIntegrity)
	}
	return persistSemanticResolutionBatch(ctx, store, artifacts, job, batch, producer)
}

func persistSemanticResolutionBatch(ctx context.Context, store SemanticResolutionOutputStore,
	artifacts SemanticResolutionOutputArtifacts, job domain.JobRecord, batch *pb.ResolutionBatch,
	producer *pb.ProducerManifest) (*SemanticResolutionOutput, error) {
	raw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(batch)
	if err != nil {
		return nil, fmt.Errorf("encode RESOLVE batch: %w", err)
	}
	digest := sha256.Sum256(raw)
	hexDigest := hex.EncodeToString(digest[:])
	ref := &pb.ArtifactRef{ArtifactId: "artifact:resolution-batch:" + hexDigest,
		ContentHash: &pb.ContentHash{Sha256: hexDigest},
		StorageKey:  "sha256/" + hexDigest[:2] + "/" + hexDigest[2:4] + "/" + hexDigest + ".bin",
		MediaType:   "application/x-protobuf", ByteSize: uint64(len(raw)), SchemaVersion: batch.Meta.SchemaVersion}
	if err = domain.ValidateWire(ref, domain.DefaultWireLimits); err != nil {
		return nil, fmt.Errorf("invalid RESOLVE output reference: %w", err)
	}
	attemptCtx, cancel := context.WithDeadline(ctx, job.LeaseExpiresAt)
	defer cancel()
	if _, err = artifacts.Put(attemptCtx, ref, bytes.NewReader(raw)); err != nil {
		return nil, fmt.Errorf("persist RESOLVE bytes: %w", err)
	}
	if err = store.RegisterArtifact(attemptCtx, job.CorpusID, ref); err != nil {
		return nil, fmt.Errorf("register RESOLVE artifact: %w", err)
	}
	if err = store.ReplaceArtifactDependencyManifest(attemptCtx, job.CorpusID, ref.ArtifactId,
		batch.Dependencies); err != nil {
		return nil, fmt.Errorf("register RESOLVE dependencies: %w", err)
	}
	checkpointDigest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%d\x00%s", job.JobID,
		job.Attempt, job.LeaseFence, ref.ArtifactId)))
	checkpoint := &pb.Checkpoint{Meta: &pb.RecordMeta{SchemaVersion: batch.Meta.SchemaVersion,
		CorpusId: job.CorpusID, RecordId: "checkpoint:resolve:" + hex.EncodeToString(checkpointDigest[:])},
		JobId: job.JobID, Stage: pb.JobStage_JOB_STAGE_RESOLVE, Fence: job.LeaseFence,
		CompletedBatchKeys: []string{ref.ArtifactId},
		ArtifactHashes:     []*pb.ContentHash{proto.Clone(ref.ContentHash).(*pb.ContentHash)},
		Manifest:           proto.Clone(producer).(*pb.ProducerManifest),
		TerminalStatus:     pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED}
	if err = domain.ValidateWire(checkpoint, domain.DefaultWireLimits); err != nil {
		return nil, fmt.Errorf("invalid RESOLVE checkpoint: %w", err)
	}
	if err = store.SaveCheckpoint(attemptCtx, checkpoint, job.LeaseOwner); err != nil {
		return nil, fmt.Errorf("save RESOLVE checkpoint: %w", err)
	}
	actual, err := store.CompleteWorkerAttempt(attemptCtx, job.JobID, job.LeaseOwner,
		job.LeaseFence, pb.JobState_JOB_STATE_STAGED, 0)
	if err != nil {
		return nil, fmt.Errorf("complete RESOLVE attempt: %w", err)
	}
	if actual != pb.JobState_JOB_STATE_STAGED {
		return nil, fmt.Errorf("RESOLVE completion became %s: %w", actual, errJobCancellationRequested)
	}
	return &SemanticResolutionOutput{Batch: batch, Artifact: ref, Checkpoint: checkpoint}, nil
}

// recoverOutput rebinds a durable terminal checkpoint to the newly claimed fence. It never
// repeats the registry decision after a checkpoint has become the job's latest pointer.
func (handoff *SemanticResolutionHandoff) recoverOutput(ctx context.Context,
	store SemanticResolutionOutputStore, artifacts SemanticResolutionOutputArtifacts,
	job domain.JobRecord, model *pb.ModelManifest, producer *pb.ProducerManifest,
	expected *pb.ResolutionBatch, expectedRecordID string,
) (*SemanticResolutionOutput, bool, error) {
	if job.State != pb.JobState_JOB_STATE_RUNNING || job.Stage != pb.JobStage_JOB_STAGE_RESOLVE ||
		job.LeaseOwner == "" || job.LeaseFence == 0 || job.CancellationRequested {
		return nil, true, errors.New("live RESOLVE claim is required for recovery")
	}
	attemptCtx, cancel := context.WithDeadline(ctx, job.LeaseExpiresAt)
	defer cancel()
	checkpoint, err := store.LoadLatestCheckpoint(attemptCtx, job.JobID)
	if err != nil {
		return nil, false, fmt.Errorf("load RESOLVE checkpoint: %w", err)
	}
	if checkpoint.Stage != pb.JobStage_JOB_STAGE_RESOLVE {
		return nil, false, nil
	}
	if checkpoint.GetMeta().GetCorpusId() != job.CorpusID || checkpoint.JobId != job.JobID ||
		checkpoint.TerminalStatus != pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED ||
		checkpoint.Fence == 0 || checkpoint.Fence > job.LeaseFence ||
		len(checkpoint.CompletedBatchKeys) != 1 || len(checkpoint.ArtifactHashes) != 1 ||
		!proto.Equal(checkpoint.Manifest, producer) {
		return nil, true, fmt.Errorf("RESOLVE checkpoint identity or producer differs: %w", domain.ErrPersistentIntegrity)
	}
	ref, err := store.LoadArtifact(attemptCtx, job.CorpusID, checkpoint.CompletedBatchKeys[0])
	if err != nil {
		return nil, true, fmt.Errorf("load recovered RESOLVE artifact: %w", err)
	}
	if ref.MediaType != "application/x-protobuf" || !proto.Equal(ref.ContentHash, checkpoint.ArtifactHashes[0]) {
		return nil, true, fmt.Errorf("RESOLVE artifact differs from checkpoint: %w", domain.ErrPersistentIntegrity)
	}
	raw, err := artifacts.ReadVerified(attemptCtx, ref, uint64(domain.DefaultWireLimits.MaxBytes))
	if err != nil {
		return nil, true, fmt.Errorf("read recovered RESOLVE artifact: %w", err)
	}
	batch := new(pb.ResolutionBatch)
	if err = domain.DecodeWire(raw, batch, domain.DefaultWireLimits); err != nil {
		return nil, true, fmt.Errorf("decode recovered RESOLVE artifact: %w", errors.Join(err, domain.ErrPersistentIntegrity))
	}
	if batch.GetMeta().GetRecordId() != expectedRecordID {
		return nil, true, fmt.Errorf("recovered RESOLVE output identity differs: %w", domain.ErrPersistentIntegrity)
	}
	if expected != nil && !proto.Equal(batch, expected) {
		return nil, true, fmt.Errorf("recovered RESOLVE bytes differ from durable intent: %w", domain.ErrPersistentIntegrity)
	}
	if batch.GetMeta().GetCorpusId() != job.CorpusID || !proto.Equal(batch.ModelManifest, model) ||
		!proto.Equal(batch.GetDependencies().GetProducerManifest(), producer) ||
		batch.GetSourceExtractionBatch() == nil {
		return nil, true, fmt.Errorf("recovered RESOLVE metadata differs: %w", domain.ErrPersistentIntegrity)
	}
	sourceRef, err := store.LoadArtifact(attemptCtx, job.CorpusID, batch.SourceExtractionBatch.ArtifactId)
	if err != nil || !proto.Equal(sourceRef, batch.SourceExtractionBatch) {
		return nil, true, fmt.Errorf("recovered EXTRACT reference differs: %w", errors.Join(err, domain.ErrPersistentIntegrity))
	}
	sourceRaw, err := artifacts.ReadVerified(attemptCtx, sourceRef, uint64(domain.DefaultWireLimits.MaxBytes))
	if err != nil {
		return nil, true, fmt.Errorf("read recovered EXTRACT artifact: %w", err)
	}
	source := new(pb.ExtractionBatch)
	if err = domain.DecodeWire(sourceRaw, source, domain.DefaultWireLimits); err != nil {
		return nil, true, fmt.Errorf("decode recovered EXTRACT artifact: %w", errors.Join(err, domain.ErrPersistentIntegrity))
	}
	dependencies := batch.GetDependencies().GetDependencies()
	if len(dependencies) == 0 || dependencies[0].GetDependencyId() != sourceRef.ArtifactId ||
		!proto.Equal(dependencies[0].GetFingerprint(), sourceRef.ContentHash) {
		return nil, true, fmt.Errorf("recovered RESOLVE dependencies differ: %w", domain.ErrPersistentIntegrity)
	}
	if len(source.Mentions) == 0 {
		if len(dependencies) != 1 || len(batch.Proposals) != 0 || len(batch.Decisions) != 0 {
			return nil, true, fmt.Errorf("mention-free RESOLVE gained candidate or decision: %w", domain.ErrPersistentIntegrity)
		}
	} else {
		if len(dependencies) != 2 || dependencies[1].GetDependencyId() == "" ||
			dependencies[1].GetDependencyId() == sourceRef.ArtifactId {
			return nil, true, fmt.Errorf("recovered candidate dependency differs: %w", domain.ErrPersistentIntegrity)
		}
		candidateRef, err := store.LoadArtifact(attemptCtx, job.CorpusID, dependencies[1].DependencyId)
		if err != nil || !proto.Equal(candidateRef.GetContentHash(), dependencies[1].Fingerprint) {
			return nil, true, fmt.Errorf("recovered candidate reference differs: %w", errors.Join(err, domain.ErrPersistentIntegrity))
		}
		candidateRaw, err := artifacts.ReadVerified(attemptCtx, candidateRef, uint64(domain.DefaultWireLimits.MaxBytes))
		if err != nil {
			return nil, true, fmt.Errorf("read recovered candidate artifact: %w", err)
		}
		candidates := new(pb.RegistryCandidateBatch)
		if err = domain.DecodeWire(candidateRaw, candidates, domain.DefaultWireLimits); err != nil {
			return nil, true, fmt.Errorf("decode recovered candidate artifact: %w", errors.Join(err, domain.ErrPersistentIntegrity))
		}
		if err = domain.ValidateRegistryCandidateBatch(candidates, source, sourceRef,
			handoff.maximumReferences, handoff.maximumCandidatesPerMention); err != nil {
			return nil, true, fmt.Errorf("recovered candidate closure differs: %w", errors.Join(err, domain.ErrPersistentIntegrity))
		}
		recoveryKey := sha256.Sum256([]byte(job.JobID))
		request := &pb.RegistryResolveRequest{Context: proto.Clone(batch.Context).(*pb.RequestContext),
			OperationKey: "recovery:" + hex.EncodeToString(recoveryKey[:]), ExpectedRevision: candidates.RegistryRevision,
			Proposals: batch.Proposals}
		response := &pb.RegistryResolveResponse{RequestId: batch.Context.RequestId,
			RegistryRevision: batch.RegistryRevision}
		correlations := make(map[string]string, len(batch.Proposals))
		for _, proposal := range batch.Proposals {
			correlations[proposal.GetMeta().GetRecordId()] = proposal.LocalCorrelationId
		}
		for _, decision := range batch.Decisions {
			response.Assignments = append(response.Assignments, &pb.RegistryAssignment{
				ProposalId: decision.ProposalId, LocalCorrelationId: correlations[decision.ProposalId],
				Result: &pb.RegistryAssignment_Decision{Decision: decision}})
		}
		if err = domain.ValidateRegistryResolveReceipt(source, sourceRef, candidates, request,
			response, handoff.maximumReferences, handoff.maximumCandidatesPerMention); err != nil {
			return nil, true, fmt.Errorf("recovered registry receipt differs: %w", errors.Join(err, domain.ErrPersistentIntegrity))
		}
	}
	if err = domain.ValidateResolutionBatchClosure(batch, source, sourceRef,
		handoff.maximumReferences); err != nil {
		return nil, true, fmt.Errorf("recovered RESOLVE closure differs: %w", errors.Join(err, domain.ErrPersistentIntegrity))
	}
	if checkpoint.Fence != job.LeaseFence {
		rebound := proto.Clone(checkpoint).(*pb.Checkpoint)
		rebound.Fence = job.LeaseFence
		fingerprint := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%d\x00%s", job.JobID,
			job.Attempt, job.LeaseFence, ref.ArtifactId)))
		rebound.Meta.RecordId = "checkpoint:resolve:" + hex.EncodeToString(fingerprint[:])
		if err = store.SaveCheckpoint(attemptCtx, rebound, job.LeaseOwner); err != nil {
			return nil, true, fmt.Errorf("rebind recovered RESOLVE checkpoint: %w", err)
		}
		checkpoint = rebound
	}
	actual, err := store.CompleteWorkerAttempt(attemptCtx, job.JobID, job.LeaseOwner,
		job.LeaseFence, pb.JobState_JOB_STATE_STAGED, 0)
	if err != nil {
		return nil, true, fmt.Errorf("complete recovered RESOLVE attempt: %w", err)
	}
	if actual != pb.JobState_JOB_STATE_STAGED {
		return nil, true, fmt.Errorf("recovered RESOLVE became %s: %w", actual, errJobCancellationRequested)
	}
	return &SemanticResolutionOutput{Batch: batch, Artifact: ref, Checkpoint: checkpoint}, true, nil
}
