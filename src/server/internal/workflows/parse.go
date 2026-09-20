// Dispatches durable PARSE, STRUCTURE, and CHUNK leases to the Rust worker and persists each fenced handoff.
//
// The executor rebuilds ProcessBatchRequest only from the claimed PostgreSQL job and its immutable
// IngestionRequest, binds corpus/attempt/fence/deadline, registers the returned artifact, saves the
// worker-produced checkpoint, then advances state. It never publishes a snapshot. Calls are bounded by
// the lease and configured timeout; retryable transport failures enter RETRY_WAIT while permanent input
// failures become FAILED. Measure queue/RPC/checkpoint p95/p99 and lease loss against benchmark targets.
package workflows

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type ParseMetadataStore interface {
	JobStore
	ClaimParseJob(context.Context, string, time.Duration) (domain.JobRecord, error)
	ClaimStructureJob(context.Context, string, time.Duration) (domain.JobRecord, error)
	ClaimChunkJob(context.Context, string, time.Duration) (domain.JobRecord, error)
	LoadIngestionRequest(context.Context, string) (*pb.IngestionRequest, error)
	LoadLatestCheckpoint(context.Context, string) (*pb.Checkpoint, error)
	LoadArtifact(context.Context, string, string) (*pb.ArtifactRef, error)
	RegisterArtifact(context.Context, string, *pb.ArtifactRef) error
	ReplaceArtifactDependencyManifest(context.Context, string, string, *pb.DependencyManifest) error
	CancellationRequested(context.Context, string, string, uint64) (bool, error)
	CompleteWorkerAttempt(context.Context, string, string, uint64, pb.JobState, time.Duration) (pb.JobState, error)
}

type DocumentArtifactReader interface {
	ReadVerified(context.Context, *pb.ArtifactRef, uint64) ([]byte, error)
}

type ParseExecutionStore interface {
	ParseMetadataStore
	DocumentArtifactReader
}

type combinedParseExecutionStore struct {
	ParseMetadataStore
	reader DocumentArtifactReader
}

func (store combinedParseExecutionStore) ReadVerified(ctx context.Context, ref *pb.ArtifactRef, maximumBytes uint64) ([]byte, error) {
	return store.reader.ReadVerified(ctx, ref, maximumBytes)
}

func CombineParseExecutionStore(metadata ParseMetadataStore, reader DocumentArtifactReader) (ParseExecutionStore, error) {
	if metadata == nil || reader == nil {
		return nil, errors.New("document metadata store and artifact reader are required")
	}
	return combinedParseExecutionStore{ParseMetadataStore: metadata, reader: reader}, nil
}

type ParseWorker interface {
	ProcessBatch(context.Context, *pb.ProcessBatchRequest) (*pb.ProcessBatchResponse, error)
	Cancel(context.Context, *pb.WorkerStatusRequest) (*pb.WorkerStatusResponse, error)
}

type ParseExecutorConfig struct {
	OwnerID           string
	AuthScope         string
	Lease             time.Duration
	CallTimeout       time.Duration
	CancellationPoll  time.Duration
	RetryBase         time.Duration
	RetryMax          time.Duration
	MaximumBatchBytes uint64
	WireLimits        domain.WireLimits
}

type ParseExecutor struct {
	store         ParseExecutionStore
	worker        ParseWorker
	config        ParseExecutorConfig
	claimSequence atomic.Uint64
}

func NewParseExecutor(store ParseExecutionStore, worker ParseWorker, config ParseExecutorConfig) (*ParseExecutor, error) {
	if store == nil || worker == nil {
		return nil, errors.New("parse store and worker are required")
	}
	if config.OwnerID == "" || config.AuthScope == "" || config.Lease <= 0 || config.CallTimeout <= 0 || config.CallTimeout >= config.Lease {
		return nil, errors.New("parse executor requires owner, auth scope, and call timeout shorter than lease")
	}
	if config.CancellationPoll <= 0 {
		config.CancellationPoll = 250 * time.Millisecond
	}
	if config.RetryBase <= 0 {
		config.RetryBase = time.Second
	}
	if config.RetryMax <= 0 {
		config.RetryMax = time.Minute
	}
	if config.RetryMax < config.RetryBase {
		return nil, errors.New("parse retry maximum must not be shorter than retry base")
	}
	if config.MaximumBatchBytes == 0 {
		config.MaximumBatchBytes = 512 << 20
	}
	if config.WireLimits.MaxBytes <= 0 || config.WireLimits.MaxDepth <= 0 || config.WireLimits.MaxItems <= 0 {
		config.WireLimits = domain.DefaultWireLimits
	}
	return &ParseExecutor{store: store, worker: worker, config: config}, nil
}

// RunOnce rotates first preference across PARSE, STRUCTURE, and CHUNK so no ready stage can be
// starved by a sustained backlog in another stage.
func (e *ParseExecutor) RunOnce(ctx context.Context) (domain.JobRecord, *pb.ProcessBatchResponse, error) {
	claimParse := func() (domain.JobRecord, error) {
		return e.store.ClaimParseJob(ctx, e.config.OwnerID, e.config.Lease)
	}
	claimStructure := func() (domain.JobRecord, error) {
		return e.store.ClaimStructureJob(ctx, e.config.OwnerID, e.config.Lease)
	}
	claimChunk := func() (domain.JobRecord, error) {
		return e.store.ClaimChunkJob(ctx, e.config.OwnerID, e.config.Lease)
	}
	claims := [3]func() (domain.JobRecord, error){claimParse, claimStructure, claimChunk}
	start := int((e.claimSequence.Add(1) - 1) % uint64(len(claims)))
	var job domain.JobRecord
	var err error
	for offset := 0; offset < len(claims); offset++ {
		job, err = claims[(start+offset)%len(claims)]()
		if !errors.Is(err, domain.ErrLeaseUnavailable) {
			break
		}
	}
	if err != nil {
		return domain.JobRecord{}, nil, err
	}
	response, err := e.executeClaimed(ctx, job)
	return job, response, err
}

func (e *ParseExecutor) executeClaimed(ctx context.Context, job domain.JobRecord) (*pb.ProcessBatchResponse, error) {
	attemptCtx, cancelAttempt := context.WithDeadline(ctx, job.LeaseExpiresAt)
	defer cancelAttempt()
	request, err := e.store.LoadIngestionRequest(attemptCtx, job.JobID)
	if err != nil {
		return nil, e.finishAfterError(attemptCtx, job, fmt.Errorf("load ingestion request: %w", err))
	}
	response, recovered, recoveryErr := e.recoverDocumentOutput(attemptCtx, job)
	if recoveryErr != nil {
		if recovered {
			return response, recoveryErr
		}
		return nil, e.finishAfterError(attemptCtx, job, recoveryErr)
	}
	if recovered {
		return response, nil
	}
	batch, err := e.processRequest(attemptCtx, job, request)
	if err != nil {
		return nil, e.finishAfterError(attemptCtx, job, err)
	}
	callCtx, cancelCall := context.WithDeadline(attemptCtx, batch.Context.Deadline.AsTime())
	monitorResult := make(chan error, 1)
	go e.monitorCancellation(callCtx, cancelCall, job, monitorResult)
	response, err = e.worker.ProcessBatch(callCtx, batch)
	cancelCall()
	monitorErr := <-monitorResult
	if monitorErr != nil {
		e.cancelDetached(job, batch)
		return nil, e.finishAfterError(attemptCtx, job, monitorErr)
	}
	if err != nil {
		if attemptCtx.Err() != nil {
			e.cancelDetached(job, batch)
		}
		return nil, e.finishAfterError(attemptCtx, job, err)
	}
	if err = domain.VerifyWorkerResponse(batch, response); err != nil {
		cause := status.Error(codes.FailedPrecondition, fmt.Sprintf("verify PARSE response: %v", err))
		return nil, e.finishAfterError(attemptCtx, job, cause)
	}
	if response.GetCheckpoint() == nil || response.GetDocumentBatch() == nil || response.GetGraphDelta() != nil || response.GetIndexBatch() != nil || response.GetExtractionBatch() != nil || response.GetResolutionBatch() != nil {
		cause := status.Error(codes.FailedPrecondition, "document worker response requires only checkpoint and document batch outputs")
		return nil, e.finishAfterError(attemptCtx, job, cause)
	}
	outputBatch, err := e.verifyDocumentOutput(attemptCtx, job.Stage, job.CorpusID, response.DocumentBatch, response.Checkpoint.Manifest, response.Status)
	if err != nil {
		cause := fmt.Errorf("verify document output artifact: %w", err)
		if errors.Is(err, errInvalidDocumentOutput) {
			cause = status.Error(codes.FailedPrecondition, cause.Error())
		}
		return nil, e.finishAfterError(attemptCtx, job, cause)
	}
	if requested, pollErr := e.store.CancellationRequested(attemptCtx, job.JobID, job.LeaseOwner, job.LeaseFence); pollErr != nil {
		return nil, e.finishAfterError(attemptCtx, job, fmt.Errorf("confirm job cancellation: %w", pollErr))
	} else if requested {
		return nil, e.finishAfterError(attemptCtx, job, errJobCancellationRequested)
	}
	if err = e.store.RegisterArtifact(attemptCtx, job.CorpusID, response.DocumentBatch); err != nil {
		return nil, e.finishAfterError(attemptCtx, job, fmt.Errorf("register document batch: %w", err))
	}
	if err = e.store.ReplaceArtifactDependencyManifest(attemptCtx, job.CorpusID, response.DocumentBatch.ArtifactId, outputBatch.DependencyManifest); err != nil {
		return nil, e.finishAfterError(attemptCtx, job, fmt.Errorf("register document batch dependencies: %w", err))
	}
	if err = e.store.SaveCheckpoint(attemptCtx, response.Checkpoint, job.LeaseOwner); err != nil {
		return nil, e.finishAfterError(attemptCtx, job, fmt.Errorf("save document checkpoint: %w", err))
	}

	next, err := terminalState(response.Status)
	if err != nil {
		cause := status.Error(codes.FailedPrecondition, err.Error())
		return nil, e.finishAfterError(attemptCtx, job, cause)
	}
	actual, err := e.store.CompleteWorkerAttempt(attemptCtx, job.JobID, job.LeaseOwner, job.LeaseFence, next, 0)
	if err != nil {
		return nil, fmt.Errorf("advance document job state: %w", err)
	}
	if actual == pb.JobState_JOB_STATE_CANCELLED && next != pb.JobState_JOB_STATE_CANCELLED {
		return response, errJobCancellationRequested
	}
	return response, nil
}

var errJobCancellationRequested = errors.New("durable job cancellation requested")

// recoverDocumentOutput closes the crash window between SaveCheckpoint and CompleteWorkerAttempt.
// A reclaimed lease never reruns Rust when a checkpoint durably binds both output and terminal outcome; it writes a
// checkpoint carrying the new fence and completes the attempt through the normal cancellation gate.
func (e *ParseExecutor) recoverDocumentOutput(ctx context.Context, job domain.JobRecord) (*pb.ProcessBatchResponse, bool, error) {
	checkpoint, err := e.store.LoadLatestCheckpoint(ctx, job.JobID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("load document recovery checkpoint: %w", err)
	}
	if checkpoint.Stage != job.Stage {
		return nil, false, nil
	}
	if checkpoint.TerminalStatus == pb.CompletionStatus_COMPLETION_STATUS_UNSPECIFIED {
		return nil, false, nil
	}
	if checkpoint.GetMeta().GetCorpusId() != job.CorpusID || checkpoint.JobId != job.JobID || checkpoint.Fence == 0 ||
		checkpoint.Fence >= job.LeaseFence || len(checkpoint.CompletedBatchKeys) != 1 || len(checkpoint.ArtifactHashes) != 1 {
		return nil, false, status.Error(codes.FailedPrecondition, "reclaimed document checkpoint is inconsistent")
	}
	artifact, err := e.store.LoadArtifact(ctx, job.CorpusID, checkpoint.CompletedBatchKeys[0])
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrPersistentIntegrity) {
			return nil, false, status.Error(codes.FailedPrecondition, fmt.Sprintf("load recovered document artifact: %v", err))
		}
		return nil, false, fmt.Errorf("load recovered document artifact: %w", err)
	}
	if artifact.GetContentHash() == nil || !proto.Equal(artifact.ContentHash, checkpoint.ArtifactHashes[0]) {
		return nil, false, status.Error(codes.FailedPrecondition, "recovered document artifact differs from checkpoint")
	}
	recoveredBatch, verifyErr := e.verifyDocumentOutput(ctx, job.Stage, job.CorpusID, artifact, checkpoint.Manifest, checkpoint.TerminalStatus)
	if verifyErr != nil {
		if errors.Is(verifyErr, errInvalidDocumentOutput) {
			return nil, false, status.Error(codes.FailedPrecondition, fmt.Sprintf("verify recovered document artifact: %v", verifyErr))
		}
		return nil, false, fmt.Errorf("verify recovered document artifact: %w", verifyErr)
	}
	if requested, pollErr := e.store.CancellationRequested(ctx, job.JobID, job.LeaseOwner, job.LeaseFence); pollErr != nil {
		return nil, false, fmt.Errorf("confirm document recovery cancellation: %w", pollErr)
	} else if requested {
		return nil, false, errJobCancellationRequested
	}

	recoveredCheckpoint := proto.Clone(checkpoint).(*pb.Checkpoint)
	recoveredCheckpoint.Fence = job.LeaseFence
	recoveryDigest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%d\x00%s", job.JobID, job.Attempt, job.LeaseFence, checkpoint.Meta.RecordId)))
	recoveredCheckpoint.Meta.RecordId = "checkpoint:recovery:" + hex.EncodeToString(recoveryDigest[:])
	requestID := documentRequestID(job)
	response := &pb.ProcessBatchResponse{
		RequestId: requestID, JobId: job.JobID, Attempt: job.Attempt, Fence: job.LeaseFence,
		Checkpoint: recoveredCheckpoint, DocumentBatch: proto.Clone(artifact).(*pb.ArtifactRef),
		Status: checkpoint.TerminalStatus,
	}
	if err = domain.ValidateWire(response, domain.DefaultWireLimits); err != nil {
		return nil, false, status.Error(codes.FailedPrecondition, fmt.Sprintf("recovered document response is invalid: %v", err))
	}
	if err = e.store.ReplaceArtifactDependencyManifest(ctx, job.CorpusID, artifact.ArtifactId, recoveredBatch.DependencyManifest); err != nil {
		return nil, false, fmt.Errorf("restore recovered document dependencies: %w", err)
	}
	if err = e.store.SaveCheckpoint(ctx, recoveredCheckpoint, job.LeaseOwner); err != nil {
		return nil, false, fmt.Errorf("save recovered document checkpoint: %w", err)
	}
	next, err := terminalState(checkpoint.TerminalStatus)
	if err != nil {
		return nil, false, status.Error(codes.FailedPrecondition, err.Error())
	}
	actual, err := e.store.CompleteWorkerAttempt(ctx, job.JobID, job.LeaseOwner, job.LeaseFence, next, 0)
	if err != nil {
		return nil, true, fmt.Errorf("complete recovered document attempt: %w", err)
	}
	if actual == pb.JobState_JOB_STATE_CANCELLED && next != pb.JobState_JOB_STATE_CANCELLED {
		return response, true, errJobCancellationRequested
	}
	return response, true, nil
}

func (e *ParseExecutor) verifyDocumentOutput(
	ctx context.Context,
	stage pb.JobStage,
	corpusID string,
	ref *pb.ArtifactRef,
	manifest *pb.ProducerManifest,
	terminal pb.CompletionStatus,
) (*pb.DocumentBatch, error) {
	if ref == nil || ref.GetContentHash() == nil || ref.MediaType != documentBatchMediaType || ref.SchemaVersion != 1 || manifest == nil {
		return nil, invalidDocumentOutput("complete DocumentBatch reference and producer manifest are required")
	}
	raw, err := e.store.ReadVerified(ctx, ref, e.config.MaximumBatchBytes)
	if err != nil {
		return nil, err
	}
	batch := &pb.DocumentBatch{}
	if err = proto.Unmarshal(raw, batch); err != nil {
		return nil, invalidDocumentOutput(fmt.Sprintf("decode DocumentBatch: %v", err))
	}
	if err = domain.ValidateWire(batch, e.config.WireLimits); err != nil {
		return nil, invalidDocumentOutput(fmt.Sprintf("validate DocumentBatch: %v", err))
	}
	if err = domain.ValidateDocumentBatchClosure(batch, e.config.WireLimits.MaxItems); err != nil {
		return nil, invalidDocumentOutput(fmt.Sprintf("validate DocumentBatch reference closure: %v", err))
	}
	if batch.GetMeta().GetCorpusId() != corpusID || batch.GetContext().GetCorpusId() != corpusID ||
		batch.GetDependencyManifest().GetProducerManifest() == nil ||
		!proto.Equal(batch.DependencyManifest.ProducerManifest, manifest) {
		return nil, invalidDocumentOutput("DocumentBatch corpus or producer manifest differs from its checkpoint")
	}
	switch stage {
	case pb.JobStage_JOB_STAGE_PARSE:
		if len(batch.Sources) == 0 || len(batch.TextArtifacts) == 0 || len(batch.Structures) != 0 || len(batch.Provisions) != 0 || len(batch.Versions) != 0 || len(batch.Chunks) != 0 {
			return nil, invalidDocumentOutput("PARSE output contains records owned by a later stage")
		}
	case pb.JobStage_JOB_STAGE_STRUCTURE:
		if len(batch.Sources) == 0 || len(batch.TextArtifacts) == 0 || len(batch.Structures) == 0 || len(batch.Provisions) != 0 || len(batch.Versions) != 0 || len(batch.Chunks) != 0 {
			return nil, invalidDocumentOutput("STRUCTURE output has invalid stage-owned records")
		}
	case pb.JobStage_JOB_STAGE_CHUNK:
		if len(batch.Sources) == 0 || len(batch.TextArtifacts) == 0 || len(batch.Structures) == 0 || len(batch.Regulations) == 0 || len(batch.Provisions) == 0 || len(batch.Versions) == 0 || len(batch.Chunks) == 0 {
			return nil, invalidDocumentOutput("CHUNK output lacks bound structure, version, or chunk records")
		}
	default:
		return nil, invalidDocumentOutput("unsupported document output stage")
	}
	if terminal == pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED && batch.Completeness != pb.Completeness_COMPLETENESS_COMPLETE {
		return nil, invalidDocumentOutput("successful document output is not complete")
	}
	if terminal == pb.CompletionStatus_COMPLETION_STATUS_FAILED && batch.Completeness == pb.Completeness_COMPLETENESS_COMPLETE {
		return nil, invalidDocumentOutput("failed document output is marked complete")
	}
	return batch, nil
}

var errInvalidDocumentOutput = errors.New("invalid immutable document output")

func invalidDocumentOutput(message string) error {
	return errors.Join(errors.New(message), errInvalidDocumentOutput)
}

func (e *ParseExecutor) monitorCancellation(ctx context.Context, cancel context.CancelFunc, job domain.JobRecord, result chan<- error) {
	ticker := time.NewTicker(e.config.CancellationPoll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			result <- nil
			return
		case <-ticker.C:
			requested, err := e.store.CancellationRequested(ctx, job.JobID, job.LeaseOwner, job.LeaseFence)
			if err != nil {
				if ctx.Err() != nil {
					result <- nil
					return
				}
				result <- fmt.Errorf("poll job cancellation: %w", err)
				cancel()
				return
			}
			if requested {
				result <- errJobCancellationRequested
				cancel()
				return
			}
		}
	}
}

func (e *ParseExecutor) processRequest(ctx context.Context, job domain.JobRecord, request *pb.IngestionRequest) (*pb.ProcessBatchRequest, error) {
	if request == nil {
		return nil, status.Error(codes.FailedPrecondition, "persisted ingestion request is missing")
	}
	if err := domain.ValidateWire(request, domain.DefaultWireLimits); err != nil {
		return nil, status.Error(codes.FailedPrecondition, fmt.Sprintf("invalid persisted ingestion request: %v", err))
	}
	if request.ConfigManifest == nil || request.ConfigManifest.ConfigHash == nil || job.State != pb.JobState_JOB_STATE_RUNNING ||
		(job.Stage != pb.JobStage_JOB_STAGE_PARSE && job.Stage != pb.JobStage_JOB_STAGE_STRUCTURE && job.Stage != pb.JobStage_JOB_STAGE_CHUNK) ||
		job.Attempt == 0 || job.LeaseFence == 0 || job.LeaseOwner != e.config.OwnerID || job.CorpusID != request.GetCorpusId() {
		return nil, status.Error(codes.FailedPrecondition, "claimed job and ingestion request are inconsistent")
	}
	now := time.Now()
	deadline := now.Add(e.config.CallTimeout)
	if job.LeaseExpiresAt.Before(deadline) {
		deadline = job.LeaseExpiresAt
	}
	if !deadline.After(now) {
		return nil, status.Error(codes.DeadlineExceeded, "claimed job lease has elapsed")
	}
	var sources []*pb.ArtifactRef
	var observations []*pb.SourceObservation
	if job.Stage == pb.JobStage_JOB_STAGE_PARSE {
		sources = make([]*pb.ArtifactRef, 0, len(request.Sources))
		for _, locator := range request.Sources {
			if locator == nil || locator.GetBlob() == nil {
				return nil, status.Error(codes.FailedPrecondition, "PARSE dispatch requires acquired blob sources; URL locators remain in ACQUIRE")
			}
			sources = append(sources, proto.Clone(locator.GetBlob()).(*pb.ArtifactRef))
		}
		var observationErr error
		observations, observationErr = parseObservations(request)
		if observationErr != nil {
			return nil, status.Error(codes.FailedPrecondition, observationErr.Error())
		}
	} else {
		expectedInputStage := pb.JobStage_JOB_STAGE_PARSE
		inputLabel := "PARSE"
		outputLabel := "STRUCTURE"
		if job.Stage == pb.JobStage_JOB_STAGE_CHUNK {
			expectedInputStage = pb.JobStage_JOB_STAGE_BIND
			inputLabel = "BIND"
			outputLabel = "CHUNK"
		}
		checkpoint, checkpointErr := e.store.LoadLatestCheckpoint(ctx, job.JobID)
		if checkpointErr != nil {
			if errors.Is(checkpointErr, domain.ErrNotFound) || errors.Is(checkpointErr, domain.ErrPersistentIntegrity) {
				return nil, status.Error(codes.FailedPrecondition, fmt.Sprintf("load %s checkpoint: %v", inputLabel, checkpointErr))
			}
			return nil, fmt.Errorf("load %s checkpoint: %w", inputLabel, checkpointErr)
		}
		if checkpoint.Stage != expectedInputStage || checkpoint.TerminalStatus != pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED ||
			len(checkpoint.CompletedBatchKeys) != 1 || len(checkpoint.ArtifactHashes) != 1 {
			return nil, status.Errorf(codes.FailedPrecondition, "%s requires one successful %s checkpoint output", outputLabel, inputLabel)
		}
		artifact, artifactErr := e.store.LoadArtifact(ctx, job.CorpusID, checkpoint.CompletedBatchKeys[0])
		if artifactErr != nil {
			if errors.Is(artifactErr, domain.ErrNotFound) || errors.Is(artifactErr, domain.ErrPersistentIntegrity) {
				return nil, status.Error(codes.FailedPrecondition, fmt.Sprintf("load %s input artifact: %v", outputLabel, artifactErr))
			}
			return nil, fmt.Errorf("load %s input artifact: %w", outputLabel, artifactErr)
		}
		if artifact.GetContentHash() == nil || !proto.Equal(artifact.ContentHash, checkpoint.ArtifactHashes[0]) {
			return nil, status.Errorf(codes.FailedPrecondition, "%s input artifact is missing or differs from %s checkpoint", outputLabel, inputLabel)
		}
		sources = []*pb.ArtifactRef{artifact}
	}
	requestID := documentRequestID(job)
	batch := &pb.ProcessBatchRequest{
		Context: &pb.RequestContext{
			SchemaVersion:     1,
			RequestId:         requestID,
			TraceId:           requestID,
			CorpusId:          job.CorpusID,
			Deadline:          timestamppb.New(deadline),
			ConfigFingerprint: proto.Clone(request.ConfigManifest.ConfigHash).(*pb.ContentHash),
			AuthScopeRef:      e.config.AuthScope,
		},
		JobId:   job.JobID,
		Attempt: job.Attempt,
		Lease: &pb.Lease{
			OwnerId:   job.LeaseOwner,
			Fence:     job.LeaseFence,
			ExpiresAt: timestamppb.New(job.LeaseExpiresAt),
		},
		Sources:      sources,
		Manifest:     proto.Clone(request.ConfigManifest).(*pb.ProducerManifest),
		Stages:       []pb.JobStage{job.Stage},
		Observations: observations,
	}
	if err := domain.ValidateWire(batch, domain.DefaultWireLimits); err != nil {
		return nil, status.Error(codes.FailedPrecondition, fmt.Sprintf("constructed document request is invalid: %v", err))
	}
	return batch, nil
}

// parseObservations binds acquisition provenance to immutable source content before dispatch.
// Empty observations remain valid for legacy jobs, but any supplied record must refer only to a
// source/portal present in the request so metadata cannot be attached to unrelated PDF bytes.
func parseObservations(request *pb.IngestionRequest) ([]*pb.SourceObservation, error) {
	portals := map[string]bool{}
	bindings := map[string]map[string]bool{}
	for _, locator := range request.Sources {
		if locator == nil || locator.GetBlob() == nil || locator.GetBlob().GetContentHash() == nil {
			continue
		}
		portals[locator.PortalId] = true
		blobID := "source-blob:" + locator.GetBlob().GetContentHash().GetSha256()
		if bindings[blobID] == nil {
			bindings[blobID] = map[string]bool{}
		}
		bindings[blobID][locator.PortalId] = true
	}
	result := make([]*pb.SourceObservation, 0, len(request.Observations))
	seen := map[string]bool{}
	for _, observation := range request.Observations {
		if observation == nil || observation.GetMeta() == nil || seen[observation.GetMeta().GetRecordId()] {
			return nil, errors.New("ingestion observations require unique complete identities")
		}
		seen[observation.GetMeta().GetRecordId()] = true
		if !portals[observation.PortalId] {
			return nil, errors.New("ingestion observation portal is absent from source locators")
		}
		if observation.SourceBlobId != nil && !bindings[observation.GetSourceBlobId()][observation.PortalId] {
			return nil, errors.New("ingestion observation is not bound to source bytes from the same portal")
		}
		result = append(result, proto.Clone(observation).(*pb.SourceObservation))
	}
	return result, nil
}

func documentRequestID(job domain.JobRecord) string {
	correlation := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%d\x00%d", job.JobID, job.Stage, job.Attempt, job.LeaseFence)))
	return "document:" + hex.EncodeToString(correlation[:])
}

func terminalState(completion pb.CompletionStatus) (pb.JobState, error) {
	switch completion {
	case pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED:
		return pb.JobState_JOB_STATE_STAGED, nil
	case pb.CompletionStatus_COMPLETION_STATUS_FAILED:
		return pb.JobState_JOB_STATE_WAITING_REVIEW, nil
	case pb.CompletionStatus_COMPLETION_STATUS_CANCELLED:
		return pb.JobState_JOB_STATE_CANCELLED, nil
	default:
		return pb.JobState_JOB_STATE_UNSPECIFIED, errors.New("worker returned unspecified completion status")
	}
}

func (e *ParseExecutor) finishAfterError(ctx context.Context, job domain.JobRecord, cause error) error {
	next := pb.JobState_JOB_STATE_RETRY_WAIT
	switch status.Code(cause) {
	case codes.InvalidArgument, codes.FailedPrecondition, codes.PermissionDenied, codes.Unauthenticated, codes.Unimplemented, codes.OutOfRange, codes.DataLoss:
		next = pb.JobState_JOB_STATE_FAILED
	}
	if errors.Is(cause, errJobCancellationRequested) {
		next = pb.JobState_JOB_STATE_CANCELLED
	}
	if errors.Is(cause, domain.ErrPersistentIntegrity) {
		next = pb.JobState_JOB_STATE_FAILED
	}
	if errors.Is(cause, errInvalidDocumentOutput) {
		next = pb.JobState_JOB_STATE_FAILED
	}
	transitionCtx := ctx
	cancel := func() {}
	if ctx.Err() != nil {
		transitionCtx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
	}
	defer cancel()
	retryDelay := time.Duration(0)
	if next == pb.JobState_JOB_STATE_RETRY_WAIT {
		retryDelay = e.retryDelay(job.StageAttempt)
	}
	actual, err := e.store.CompleteWorkerAttempt(transitionCtx, job.JobID, job.LeaseOwner, job.LeaseFence, next, retryDelay)
	if err != nil {
		return errors.Join(cause, fmt.Errorf("record PARSE failure as %s: %w", next, err))
	}
	if actual == pb.JobState_JOB_STATE_CANCELLED && next != pb.JobState_JOB_STATE_CANCELLED {
		return errors.Join(cause, errJobCancellationRequested)
	}
	return cause
}

func (e *ParseExecutor) retryDelay(attempt uint32) time.Duration {
	delay := e.config.RetryBase
	for step := uint32(1); step < attempt && delay < e.config.RetryMax; step++ {
		if delay > e.config.RetryMax/2 {
			return e.config.RetryMax
		}
		delay *= 2
	}
	if delay > e.config.RetryMax {
		return e.config.RetryMax
	}
	return delay
}

func (e *ParseExecutor) cancelDetached(job domain.JobRecord, batch *pb.ProcessBatchRequest) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	deadline := time.Now().Add(2 * time.Second)
	if job.LeaseExpiresAt.Before(deadline) {
		deadline = job.LeaseExpiresAt
	}
	if !deadline.After(time.Now()) {
		return
	}
	_, _ = e.worker.Cancel(ctx, &pb.WorkerStatusRequest{
		Context: &pb.RequestContext{
			SchemaVersion:     1,
			RequestId:         batch.Context.RequestId + ":cancel",
			TraceId:           batch.Context.TraceId,
			CorpusId:          job.CorpusID,
			Deadline:          timestamppb.New(deadline),
			ConfigFingerprint: proto.Clone(batch.Context.ConfigFingerprint).(*pb.ContentHash),
			AuthScopeRef:      e.config.AuthScope,
		},
		JobId: job.JobID, Attempt: job.Attempt, Fence: job.LeaseFence,
	})
}
