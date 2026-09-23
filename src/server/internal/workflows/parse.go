// Dispatches durable PARSE, STRUCTURE, CHUNK, and EXTRACT leases to the Rust worker and persists each fenced handoff.
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
	"mime"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

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
	ClaimExtractJob(context.Context, string, time.Duration) (domain.JobRecord, error)
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
	Ontology          *domain.Ontology
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
	if store == nil || worker == nil || config.Ontology == nil {
		return nil, errors.New("parse store, worker, and ontology are required")
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

// RunOnce rotates first preference across PARSE, STRUCTURE, CHUNK, and EXTRACT so no ready stage can be
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
	claimExtract := func() (domain.JobRecord, error) {
		return e.store.ClaimExtractJob(ctx, e.config.OwnerID, e.config.Lease)
	}
	claims := [4]func() (domain.JobRecord, error){claimParse, claimStructure, claimChunk, claimExtract}
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
	response, recovered, recoveryErr := e.recoverWorkerOutput(attemptCtx, job, request.ConfigManifest)
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
		cause := status.Error(codes.FailedPrecondition, fmt.Sprintf("verify worker response: %v", err))
		return nil, e.finishAfterError(attemptCtx, job, cause)
	}
	if response.GetCheckpoint() == nil || response.GetGraphDelta() != nil || response.GetIndexBatch() != nil || response.GetResolutionBatch() != nil {
		cause := status.Error(codes.FailedPrecondition, "worker response contains an unsupported output combination")
		return nil, e.finishAfterError(attemptCtx, job, cause)
	}
	var outputRef *pb.ArtifactRef
	var dependencies *pb.DependencyManifest
	if job.Stage == pb.JobStage_JOB_STAGE_EXTRACT {
		if response.GetExtractionBatch() == nil || response.GetDocumentBatch() != nil || len(batch.Sources) != 1 {
			cause := status.Error(codes.FailedPrecondition, "EXTRACT response requires only checkpoint and extraction batch outputs")
			return nil, e.finishAfterError(attemptCtx, job, cause)
		}
		output, verifyErr := e.verifyExtractionOutput(attemptCtx, job.CorpusID, response.ExtractionBatch, batch.Sources[0], response.Checkpoint.Manifest, request.ConfigManifest, response.Status)
		if verifyErr != nil {
			cause := fmt.Errorf("verify extraction output artifact: %w", verifyErr)
			if errors.Is(verifyErr, errInvalidExtractionOutput) {
				cause = status.Error(codes.FailedPrecondition, cause.Error())
			}
			return nil, e.finishAfterError(attemptCtx, job, cause)
		}
		outputRef, dependencies = response.ExtractionBatch, output.Dependencies
	} else {
		if response.GetDocumentBatch() == nil || response.GetExtractionBatch() != nil {
			cause := status.Error(codes.FailedPrecondition, "document response requires only checkpoint and document batch outputs")
			return nil, e.finishAfterError(attemptCtx, job, cause)
		}
		output, verifyErr := e.verifyDocumentOutput(attemptCtx, job.Stage, job.CorpusID, response.DocumentBatch, response.Checkpoint.Manifest, response.Status)
		if verifyErr != nil {
			cause := fmt.Errorf("verify document output artifact: %w", verifyErr)
			if errors.Is(verifyErr, errInvalidDocumentOutput) {
				cause = status.Error(codes.FailedPrecondition, cause.Error())
			}
			return nil, e.finishAfterError(attemptCtx, job, cause)
		}
		outputRef, dependencies = response.DocumentBatch, output.DependencyManifest
	}
	if requested, pollErr := e.store.CancellationRequested(attemptCtx, job.JobID, job.LeaseOwner, job.LeaseFence); pollErr != nil {
		return nil, e.finishAfterError(attemptCtx, job, fmt.Errorf("confirm job cancellation: %w", pollErr))
	} else if requested {
		return nil, e.finishAfterError(attemptCtx, job, errJobCancellationRequested)
	}
	if err = e.store.RegisterArtifact(attemptCtx, job.CorpusID, outputRef); err != nil {
		return nil, e.finishAfterError(attemptCtx, job, fmt.Errorf("register worker artifact: %w", err))
	}
	if err = e.store.ReplaceArtifactDependencyManifest(attemptCtx, job.CorpusID, outputRef.ArtifactId, dependencies); err != nil {
		return nil, e.finishAfterError(attemptCtx, job, fmt.Errorf("register worker artifact dependencies: %w", err))
	}
	if err = e.store.SaveCheckpoint(attemptCtx, response.Checkpoint, job.LeaseOwner); err != nil {
		return nil, e.finishAfterError(attemptCtx, job, fmt.Errorf("save worker checkpoint: %w", err))
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

// recoverWorkerOutput closes the crash window between SaveCheckpoint and CompleteWorkerAttempt.
// A reclaimed lease never reruns Rust when a checkpoint durably binds both output and terminal outcome; it writes a
// checkpoint carrying the new fence and completes the attempt through the normal cancellation gate.
func (e *ParseExecutor) recoverWorkerOutput(ctx context.Context, job domain.JobRecord, expectedConfig *pb.ProducerManifest) (*pb.ProcessBatchResponse, bool, error) {
	checkpoint, err := e.store.LoadLatestCheckpoint(ctx, job.JobID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("load worker recovery checkpoint: %w", err)
	}
	if checkpoint.Stage != job.Stage {
		return nil, false, nil
	}
	if checkpoint.TerminalStatus == pb.CompletionStatus_COMPLETION_STATUS_UNSPECIFIED {
		return nil, false, nil
	}
	if checkpoint.GetMeta().GetCorpusId() != job.CorpusID || checkpoint.JobId != job.JobID || checkpoint.Fence == 0 ||
		checkpoint.Fence >= job.LeaseFence || len(checkpoint.CompletedBatchKeys) != 1 || len(checkpoint.ArtifactHashes) != 1 {
		return nil, false, status.Error(codes.FailedPrecondition, "reclaimed worker checkpoint is inconsistent")
	}
	artifact, err := e.store.LoadArtifact(ctx, job.CorpusID, checkpoint.CompletedBatchKeys[0])
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrPersistentIntegrity) {
			return nil, false, status.Error(codes.FailedPrecondition, fmt.Sprintf("load recovered worker artifact: %v", err))
		}
		return nil, false, fmt.Errorf("load recovered worker artifact: %w", err)
	}
	if artifact.GetContentHash() == nil || !proto.Equal(artifact.ContentHash, checkpoint.ArtifactHashes[0]) {
		return nil, false, status.Error(codes.FailedPrecondition, "recovered worker artifact differs from checkpoint")
	}
	var dependencies *pb.DependencyManifest
	if job.Stage == pb.JobStage_JOB_STAGE_EXTRACT {
		recoveredBatch, verifyErr := e.verifyExtractionOutput(ctx, job.CorpusID, artifact, nil, checkpoint.Manifest, expectedConfig, checkpoint.TerminalStatus)
		if verifyErr != nil {
			if errors.Is(verifyErr, errInvalidExtractionOutput) {
				return nil, false, status.Error(codes.FailedPrecondition, fmt.Sprintf("verify recovered extraction artifact: %v", verifyErr))
			}
			return nil, false, fmt.Errorf("verify recovered extraction artifact: %w", verifyErr)
		}
		dependencies = recoveredBatch.Dependencies
	} else {
		recoveredBatch, verifyErr := e.verifyDocumentOutput(ctx, job.Stage, job.CorpusID, artifact, checkpoint.Manifest, checkpoint.TerminalStatus)
		if verifyErr != nil {
			if errors.Is(verifyErr, errInvalidDocumentOutput) {
				return nil, false, status.Error(codes.FailedPrecondition, fmt.Sprintf("verify recovered document artifact: %v", verifyErr))
			}
			return nil, false, fmt.Errorf("verify recovered document artifact: %w", verifyErr)
		}
		dependencies = recoveredBatch.DependencyManifest
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
		Checkpoint: recoveredCheckpoint, Status: checkpoint.TerminalStatus,
	}
	if job.Stage == pb.JobStage_JOB_STAGE_EXTRACT {
		response.ExtractionBatch = proto.Clone(artifact).(*pb.ArtifactRef)
	} else {
		response.DocumentBatch = proto.Clone(artifact).(*pb.ArtifactRef)
	}
	if err = domain.ValidateWire(response, domain.DefaultWireLimits); err != nil {
		return nil, false, status.Error(codes.FailedPrecondition, fmt.Sprintf("recovered worker response is invalid: %v", err))
	}
	if err = e.store.ReplaceArtifactDependencyManifest(ctx, job.CorpusID, artifact.ArtifactId, dependencies); err != nil {
		return nil, false, fmt.Errorf("restore recovered worker dependencies: %w", err)
	}
	if err = e.store.SaveCheckpoint(ctx, recoveredCheckpoint, job.LeaseOwner); err != nil {
		return nil, false, fmt.Errorf("save recovered worker checkpoint: %w", err)
	}
	next, err := terminalState(checkpoint.TerminalStatus)
	if err != nil {
		return nil, false, status.Error(codes.FailedPrecondition, err.Error())
	}
	actual, err := e.store.CompleteWorkerAttempt(ctx, job.JobID, job.LeaseOwner, job.LeaseFence, next, 0)
	if err != nil {
		return nil, true, fmt.Errorf("complete recovered worker attempt: %w", err)
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

const extractionBatchMediaType = "application/vnd.regulagraph.extraction-batch+protobuf"

func (e *ParseExecutor) verifyExtractionOutput(
	ctx context.Context,
	corpusID string,
	ref *pb.ArtifactRef,
	sourceRef *pb.ArtifactRef,
	manifest *pb.ProducerManifest,
	expectedConfig *pb.ProducerManifest,
	terminal pb.CompletionStatus,
) (*pb.ExtractionBatch, error) {
	if ref == nil || ref.GetContentHash() == nil || ref.MediaType != extractionBatchMediaType || ref.SchemaVersion != 1 ||
		manifest == nil || expectedConfig == nil || expectedConfig.GetConfigHash() == nil {
		return nil, invalidExtractionOutput("complete ExtractionBatch, source, producer, and expected config manifests are required")
	}
	raw, err := e.store.ReadVerified(ctx, ref, e.config.MaximumBatchBytes)
	if err != nil {
		return nil, err
	}
	batch := &pb.ExtractionBatch{}
	if err = proto.Unmarshal(raw, batch); err != nil {
		return nil, invalidExtractionOutput(fmt.Sprintf("decode ExtractionBatch: %v", err))
	}
	if err = domain.ValidateWire(batch, e.config.WireLimits); err != nil {
		return nil, invalidExtractionOutput(fmt.Sprintf("validate ExtractionBatch: %v", err))
	}
	if sourceRef == nil {
		sourceRef = batch.SourceDocumentBatch
	}
	if batch.GetMeta().GetCorpusId() != corpusID || batch.GetContext().GetCorpusId() != corpusID ||
		sourceRef == nil || sourceRef.GetContentHash() == nil || sourceRef.MediaType != documentBatchMediaType || sourceRef.SchemaVersion != 1 ||
		!proto.Equal(batch.SourceDocumentBatch, sourceRef) || batch.GetDependencies().GetProducerManifest() == nil ||
		!proto.Equal(batch.Dependencies.ProducerManifest, manifest) {
		return nil, invalidExtractionOutput("ExtractionBatch corpus, source, or producer differs from its request/checkpoint")
	}
	if !proto.Equal(batch.Context.ConfigFingerprint, expectedConfig.ConfigHash) ||
		!containsExpectedModel(expectedConfig.Models, batch.ModelManifest) ||
		!containsExpectedHash(expectedConfig.PromptHashes, batch.PromptHash) {
		return nil, invalidExtractionOutput("ExtractionBatch model, prompt, or config differs from the persisted ingestion request")
	}
	if !containsExpectedHash(expectedConfig.InputHashes, e.config.Ontology.ContentHash()) ||
		!containsExpectedHash(manifest.InputHashes, e.config.Ontology.ContentHash()) {
		return nil, invalidExtractionOutput("ExtractionBatch ontology bytes differ from pinned request or producer")
	}
	sourceRaw, err := e.store.ReadVerified(ctx, sourceRef, e.config.MaximumBatchBytes)
	if err != nil {
		return nil, err
	}
	source := &pb.DocumentBatch{}
	if err = proto.Unmarshal(sourceRaw, source); err != nil {
		return nil, invalidExtractionOutput(fmt.Sprintf("decode source DocumentBatch: %v", err))
	}
	if err = domain.ValidateWire(source, e.config.WireLimits); err != nil {
		return nil, invalidExtractionOutput(fmt.Sprintf("validate source DocumentBatch: %v", err))
	}
	if err = domain.ValidateDocumentBatchClosure(source, e.config.WireLimits.MaxItems); err != nil {
		return nil, invalidExtractionOutput(fmt.Sprintf("validate source DocumentBatch closure: %v", err))
	}
	if source.Completeness != pb.Completeness_COMPLETENESS_COMPLETE || len(source.Chunks) == 0 ||
		!proto.Equal(source.GetContext().GetConfigFingerprint(), expectedConfig.ConfigHash) {
		return nil, invalidExtractionOutput("EXTRACT requires a complete non-empty CHUNK DocumentBatch")
	}
	if err = domain.ValidateExtractionBatchClosure(batch, source, e.config.WireLimits.MaxItems); err != nil {
		return nil, invalidExtractionOutput(fmt.Sprintf("validate ExtractionBatch closure: %v", err))
	}
	if err = e.config.Ontology.ValidateExtractionOntology(batch); err != nil {
		return nil, invalidExtractionOutput(fmt.Sprintf("validate ExtractionBatch ontology: %v", err))
	}
	if err = e.verifyExtractionSourceText(ctx, batch, source); err != nil {
		if errors.Is(err, errInvalidMentionSurface) {
			return nil, invalidExtractionOutput(fmt.Sprintf("verify mention source text: %v", err))
		}
		return nil, err
	}
	if terminal == pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED && batch.Completeness != pb.Completeness_COMPLETENESS_COMPLETE {
		return nil, invalidExtractionOutput("successful extraction output is not complete")
	}
	if terminal == pb.CompletionStatus_COMPLETION_STATUS_FAILED && batch.Completeness == pb.Completeness_COMPLETENESS_COMPLETE {
		return nil, invalidExtractionOutput("failed extraction output is marked complete")
	}
	return batch, nil
}

// verifyExtractionSourceText hydrates only normalized text artifacts used by extracted evidence.
// It proves UTF-8 boundaries for all evidence and exact source bytes for mention surface forms.
// The aggregate byte budget prevents a valid-looking batch from amplifying coordinator memory/IO.
func (e *ParseExecutor) verifyExtractionSourceText(ctx context.Context, batch *pb.ExtractionBatch, source *pb.DocumentBatch) error {
	textRefs := make(map[string]*pb.ArtifactRef, len(source.TextArtifacts))
	for _, artifact := range source.TextArtifacts {
		if artifact.GetMeta() != nil && artifact.GetNormalizedTextRef() != nil {
			textRefs[artifact.Meta.RecordId] = artifact.NormalizedTextRef
		}
	}
	required := make(map[string]*pb.ArtifactRef)
	var total uint64
	addRequired := func(span *pb.TextSpan, ownerID string) error {
		ref := textRefs[span.GetTextArtifactId()]
		if ref == nil || ref.GetContentHash() == nil || !isUTF8TextMediaType(ref.MediaType) || ref.SchemaVersion != 1 {
			return invalidMentionSurface(fmt.Sprintf("extraction record %q has no verified normalized text artifact", ownerID))
		}
		if existing, exists := required[ref.ArtifactId]; exists && !proto.Equal(existing, ref) {
			return invalidMentionSurface(fmt.Sprintf("normalized artifact %q has conflicting descriptors", ref.ArtifactId))
		} else if !exists {
			if ref.ByteSize == 0 || ref.ByteSize > e.config.MaximumBatchBytes || total > e.config.MaximumBatchBytes-ref.ByteSize {
				return invalidMentionSurface("normalized evidence text exceeds extraction verification byte budget")
			}
			total += ref.ByteSize
			required[ref.ArtifactId] = ref
		}
		return nil
	}
	for _, mention := range batch.Mentions {
		if err := addRequired(mention.GetTextSpan(), mention.GetMeta().GetRecordId()); err != nil {
			return err
		}
	}
	for _, support := range batch.Supports {
		for _, span := range support.EvidenceSpans {
			if err := addRequired(span, support.GetMeta().GetRecordId()); err != nil {
				return err
			}
		}
	}
	loaded := make(map[string][]byte, len(required))
	for artifactID, ref := range required {
		raw, err := e.store.ReadVerified(ctx, ref, e.config.MaximumBatchBytes)
		if err != nil {
			return fmt.Errorf("read normalized artifact %q: %w", artifactID, err)
		}
		if uint64(len(raw)) != ref.ByteSize {
			return invalidMentionSurface(fmt.Sprintf("normalized artifact %q byte size differs from its descriptor", artifactID))
		}
		if !utf8.Valid(raw) {
			return invalidMentionSurface(fmt.Sprintf("normalized artifact %q is not valid UTF-8", artifactID))
		}
		loaded[artifactID] = raw
	}
	validateBoundary := func(span *pb.TextSpan, ownerID string) ([]byte, error) {
		ref := textRefs[span.TextArtifactId]
		raw := loaded[ref.ArtifactId]
		if span.StartByte > uint64(len(raw)) || span.EndByte > uint64(len(raw)) ||
			span.StartByte == uint64(len(raw)) || !utf8.RuneStart(raw[span.StartByte]) ||
			(span.EndByte < uint64(len(raw)) && !utf8.RuneStart(raw[span.EndByte])) {
			return nil, invalidMentionSurface(fmt.Sprintf("extraction record %q uses a non-UTF-8 source boundary", ownerID))
		}
		return raw, nil
	}
	for _, mention := range batch.Mentions {
		span := mention.TextSpan
		raw, err := validateBoundary(span, mention.Meta.RecordId)
		if err != nil {
			return err
		}
		if mention.SurfaceForm != string(raw[span.StartByte:span.EndByte]) {
			return invalidMentionSurface(fmt.Sprintf("mention %q surface form differs from its exact UTF-8 source bytes", mention.Meta.RecordId))
		}
	}
	for _, support := range batch.Supports {
		for _, span := range support.EvidenceSpans {
			if _, err := validateBoundary(span, support.Meta.RecordId); err != nil {
				return err
			}
		}
	}
	return nil
}

func isUTF8TextMediaType(value string) bool {
	mediaType, parameters, err := mime.ParseMediaType(value)
	if err != nil || mediaType != "text/plain" {
		return false
	}
	charset := parameters["charset"]
	return charset == "" || strings.EqualFold(charset, "utf-8")
}

var errInvalidMentionSurface = errors.New("invalid mention source text")

func invalidMentionSurface(message string) error {
	return errors.Join(errors.New(message), errInvalidMentionSurface)
}

func containsExpectedModel(values []*pb.ModelManifest, expected *pb.ModelManifest) bool {
	for _, value := range values {
		if proto.Equal(value, expected) {
			return true
		}
	}
	return false
}

func containsExpectedHash(values []*pb.ContentHash, expected *pb.ContentHash) bool {
	for _, value := range values {
		if proto.Equal(value, expected) {
			return true
		}
	}
	return false
}

var errInvalidDocumentOutput = errors.New("invalid immutable document output")
var errInvalidExtractionOutput = errors.New("invalid immutable extraction output")

func invalidDocumentOutput(message string) error {
	return errors.Join(errors.New(message), errInvalidDocumentOutput)
}

func invalidExtractionOutput(message string) error {
	return errors.Join(errors.New(message), errInvalidExtractionOutput)
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
		(job.Stage != pb.JobStage_JOB_STAGE_PARSE && job.Stage != pb.JobStage_JOB_STAGE_STRUCTURE && job.Stage != pb.JobStage_JOB_STAGE_CHUNK && job.Stage != pb.JobStage_JOB_STAGE_EXTRACT) ||
		job.Attempt == 0 || job.LeaseFence == 0 || job.LeaseOwner != e.config.OwnerID || job.CorpusID != request.GetCorpusId() {
		return nil, status.Error(codes.FailedPrecondition, "claimed job and ingestion request are inconsistent")
	}
	if job.Stage == pb.JobStage_JOB_STAGE_EXTRACT && !containsExpectedHash(request.ConfigManifest.InputHashes, e.config.Ontology.ContentHash()) {
		return nil, status.Error(codes.FailedPrecondition, "persisted EXTRACT request did not pin ontology bytes")
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
		} else if job.Stage == pb.JobStage_JOB_STAGE_EXTRACT {
			expectedInputStage = pb.JobStage_JOB_STAGE_CHUNK
			inputLabel = "CHUNK"
			outputLabel = "EXTRACT"
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
		return nil, status.Error(codes.FailedPrecondition, fmt.Sprintf("constructed worker request is invalid: %v", err))
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
	if errors.Is(cause, errInvalidExtractionOutput) {
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
