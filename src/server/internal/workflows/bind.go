// Executes the durable Go-owned BIND stage between Rust STRUCTURE and CHUNK.
//
// The executor loads one checkpoint-bound DocumentBatch through verified artifact storage, resolves
// issuer/regulation/provision exact claims with idempotent registry operations, materializes sourced
// legal records, and persists one immutable output plus a fenced terminal checkpoint. It preserves
// partial/review outcomes and never treats portal metadata as verified legal truth. Registry and
// artifact calls are batch operations. Measure queue time, binding p95/p99, registry contention,
// throughput, peak RSS, false merge/split, and completeness against configs/benchmark-targets.yaml;
// required numeric targets remain REQUIRED_UNMEASURED.
package workflows

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

const documentBatchMediaType = "application/vnd.regulagraph.document-batch+protobuf"
const maximumRegistryBatchClaims = 10_000

type BindingExecutionStore interface {
	ClaimBindJob(context.Context, string, time.Duration) (domain.JobRecord, error)
	LoadLatestCheckpoint(context.Context, string) (*pb.Checkpoint, error)
	LoadArtifact(context.Context, string, string) (*pb.ArtifactRef, error)
	RegisterArtifact(context.Context, string, *pb.ArtifactRef) error
	ReplaceArtifactDependencyManifest(context.Context, string, string, *pb.DependencyManifest) error
	ResolveCanonicalIdentities(context.Context, string, string, uint64, []domain.CanonicalIdentityClaim) ([]domain.CanonicalIdentityAssignment, uint64, error)
	CancellationRequested(context.Context, string, string, uint64) (bool, error)
	SaveCheckpoint(context.Context, *pb.Checkpoint, string) error
	CompleteWorkerAttempt(context.Context, string, string, uint64, pb.JobState, time.Duration) (pb.JobState, error)
}

type BindingArtifactStore interface {
	ReadVerified(context.Context, *pb.ArtifactRef, uint64) ([]byte, error)
	Put(context.Context, *pb.ArtifactRef, io.Reader) (bool, error)
}

type BindingExecutorConfig struct {
	OwnerID           string
	Jurisdiction      string
	Software          string
	Build             string
	Language          string
	DocumentKind      pb.DocumentKind
	Lease             time.Duration
	RetryBase         time.Duration
	RetryMax          time.Duration
	MaximumBytes      uint64
	MaximumRecords    int
	RegistryBatchSize int
	WireLimits        domain.WireLimits
}

type BindingResult struct {
	DocumentBatch *pb.ArtifactRef
	Checkpoint    *pb.Checkpoint
	Completeness  pb.Completeness
}

type BindingExecutor struct {
	store     BindingExecutionStore
	artifacts BindingArtifactStore
	config    BindingExecutorConfig
}

func NewBindingExecutor(store BindingExecutionStore, artifacts BindingArtifactStore, config BindingExecutorConfig) (*BindingExecutor, error) {
	if store == nil || artifacts == nil || strings.TrimSpace(config.OwnerID) == "" || strings.TrimSpace(config.Jurisdiction) == "" ||
		strings.TrimSpace(config.Software) == "" || strings.TrimSpace(config.Build) == "" || strings.TrimSpace(config.Language) == "" ||
		config.DocumentKind == pb.DocumentKind_DOCUMENT_KIND_UNSPECIFIED || config.Lease <= 0 || config.MaximumBytes == 0 ||
		config.MaximumRecords <= 0 || config.WireLimits.MaxBytes <= 0 || config.WireLimits.MaxDepth <= 0 || config.WireLimits.MaxItems <= 0 {
		return nil, errors.New("binding executor requires complete bounded configuration")
	}
	if config.RetryBase <= 0 {
		config.RetryBase = time.Second
	}
	if config.RetryMax <= 0 {
		config.RetryMax = time.Minute
	}
	if config.RetryMax < config.RetryBase {
		return nil, errors.New("binding retry maximum must not be shorter than retry base")
	}
	if config.RegistryBatchSize <= 0 {
		config.RegistryBatchSize = maximumRegistryBatchClaims
	}
	if config.RegistryBatchSize > maximumRegistryBatchClaims {
		return nil, errors.New("binding registry batch size exceeds registry contract")
	}
	return &BindingExecutor{store: store, artifacts: artifacts, config: config}, nil
}

func (executor *BindingExecutor) RunOnce(ctx context.Context) (domain.JobRecord, *BindingResult, error) {
	job, err := executor.store.ClaimBindJob(ctx, executor.config.OwnerID, executor.config.Lease)
	if err != nil {
		return domain.JobRecord{}, nil, err
	}
	result, err := executor.executeClaimed(ctx, job)
	return job, result, err
}

func (executor *BindingExecutor) executeClaimed(ctx context.Context, job domain.JobRecord) (*BindingResult, error) {
	attemptCtx, cancel := context.WithDeadline(ctx, job.LeaseExpiresAt)
	defer cancel()
	if job.State != pb.JobState_JOB_STATE_RUNNING || job.Stage != pb.JobStage_JOB_STAGE_BIND || job.Attempt == 0 ||
		job.LeaseFence == 0 || job.LeaseOwner != executor.config.OwnerID {
		return nil, executor.finish(attemptCtx, job, permanentBindingError("claimed BIND job is inconsistent"))
	}
	recovered, result, recoveryErr := executor.recover(attemptCtx, job)
	if recovered {
		return result, recoveryErr
	}
	if recoveryErr != nil {
		return nil, executor.finish(attemptCtx, job, recoveryErr)
	}
	checkpoint, inputRef, err := executor.structureInput(attemptCtx, job)
	if err != nil {
		return nil, executor.finish(attemptCtx, job, err)
	}
	raw, err := executor.artifacts.ReadVerified(attemptCtx, inputRef, executor.config.MaximumBytes)
	if err != nil {
		return nil, executor.finish(attemptCtx, job, fmt.Errorf("read STRUCTURE batch: %w", err))
	}
	batch := &pb.DocumentBatch{}
	if err = proto.Unmarshal(raw, batch); err != nil {
		return nil, executor.finish(attemptCtx, job, permanentBindingError("decode STRUCTURE batch: %v", err))
	}
	if err = domain.ValidateWire(batch, executor.config.WireLimits); err != nil {
		return nil, executor.finish(attemptCtx, job, permanentBindingError("validate STRUCTURE batch: %v", err))
	}
	if batch.GetMeta().GetCorpusId() != job.CorpusID || inputRef.MediaType != documentBatchMediaType ||
		inputRef.GetContentHash() == nil || !proto.Equal(inputRef.ContentHash, checkpoint.ArtifactHashes[0]) {
		return nil, executor.finish(attemptCtx, job, permanentBindingError("STRUCTURE artifact identity differs from its checkpoint"))
	}
	if requested, cancellationErr := executor.store.CancellationRequested(attemptCtx, job.JobID, job.LeaseOwner, job.LeaseFence); cancellationErr != nil {
		return nil, executor.finish(attemptCtx, job, fmt.Errorf("check BIND cancellation: %w", cancellationErr))
	} else if requested {
		return nil, executor.finish(attemptCtx, job, errJobCancellationRequested)
	}

	bound, registryRevision, err := executor.bind(attemptCtx, job, inputRef.ContentHash.Sha256, batch)
	if err != nil {
		return nil, executor.finish(attemptCtx, job, err)
	}
	if requested, cancellationErr := executor.store.CancellationRequested(attemptCtx, job.JobID, job.LeaseOwner, job.LeaseFence); cancellationErr != nil {
		return nil, executor.finish(attemptCtx, job, fmt.Errorf("confirm BIND cancellation: %w", cancellationErr))
	} else if requested {
		return nil, executor.finish(attemptCtx, job, errJobCancellationRequested)
	}
	outputRaw, err := proto.MarshalOptions{Deterministic: true}.Marshal(bound)
	if err != nil {
		return nil, executor.finish(attemptCtx, job, permanentBindingError("encode bound DocumentBatch: %v", err))
	}
	outputRef := bindingArtifactReference(outputRaw)
	if _, err = executor.artifacts.Put(attemptCtx, outputRef, bytes.NewReader(outputRaw)); err != nil {
		return nil, executor.finish(attemptCtx, job, fmt.Errorf("persist bound DocumentBatch: %w", err))
	}
	if err = executor.store.RegisterArtifact(attemptCtx, job.CorpusID, outputRef); err != nil {
		return nil, executor.finish(attemptCtx, job, fmt.Errorf("register bound DocumentBatch: %w", err))
	}
	if err = executor.store.ReplaceArtifactDependencyManifest(attemptCtx, job.CorpusID, outputRef.ArtifactId, bound.DependencyManifest); err != nil {
		return nil, executor.finish(attemptCtx, job, fmt.Errorf("register bound dependencies: %w", err))
	}
	terminal := pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED
	desired := pb.JobState_JOB_STATE_STAGED
	if bound.Completeness != pb.Completeness_COMPLETENESS_COMPLETE {
		terminal = pb.CompletionStatus_COMPLETION_STATUS_FAILED
		desired = pb.JobState_JOB_STATE_WAITING_REVIEW
	}
	outputCheckpoint := bindingCheckpoint(job, outputRef, bound.GetDependencyManifest().GetProducerManifest(), terminal, registryRevision)
	if err = executor.store.SaveCheckpoint(attemptCtx, outputCheckpoint, job.LeaseOwner); err != nil {
		return nil, executor.finish(attemptCtx, job, fmt.Errorf("save BIND checkpoint: %w", err))
	}
	actual, err := executor.store.CompleteWorkerAttempt(attemptCtx, job.JobID, job.LeaseOwner, job.LeaseFence, desired, 0)
	if err != nil {
		return nil, fmt.Errorf("complete BIND attempt: %w", err)
	}
	if actual == pb.JobState_JOB_STATE_CANCELLED && desired != pb.JobState_JOB_STATE_CANCELLED {
		return nil, errJobCancellationRequested
	}
	return &BindingResult{DocumentBatch: outputRef, Checkpoint: outputCheckpoint, Completeness: bound.Completeness}, nil
}

func (executor *BindingExecutor) structureInput(ctx context.Context, job domain.JobRecord) (*pb.Checkpoint, *pb.ArtifactRef, error) {
	checkpoint, err := executor.store.LoadLatestCheckpoint(ctx, job.JobID)
	if err != nil {
		return nil, nil, bindingPrerequisiteError("load STRUCTURE checkpoint", err)
	}
	if checkpoint.Stage != pb.JobStage_JOB_STAGE_STRUCTURE || checkpoint.TerminalStatus != pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED ||
		checkpoint.GetMeta().GetCorpusId() != job.CorpusID || checkpoint.JobId != job.JobID || checkpoint.Fence == 0 ||
		checkpoint.Fence >= job.LeaseFence || len(checkpoint.CompletedBatchKeys) != 1 || len(checkpoint.ArtifactHashes) != 1 {
		return nil, nil, permanentBindingError("BIND requires one successful STRUCTURE checkpoint")
	}
	artifact, err := executor.store.LoadArtifact(ctx, job.CorpusID, checkpoint.CompletedBatchKeys[0])
	if err != nil {
		return nil, nil, bindingPrerequisiteError("load STRUCTURE artifact", err)
	}
	return checkpoint, artifact, nil
}

func (executor *BindingExecutor) bind(
	ctx context.Context,
	job domain.JobRecord,
	inputHash string,
	batch *pb.DocumentBatch,
) (*pb.DocumentBatch, uint64, error) {
	plan, err := domain.PlanRegulationIdentities(batch, domain.RegulationIdentityPolicy{
		Jurisdiction: executor.config.Jurisdiction, MaximumItems: executor.config.MaximumRecords,
	})
	if err != nil {
		return nil, 0, permanentBindingError("plan regulation identities: %v", err)
	}
	bindings := []domain.RegulationDocumentBinding{}
	registryRevision := uint64(0)
	if len(plan.Candidates) != 0 {
		issuerClaims := make([]domain.CanonicalIdentityClaim, 0, len(plan.Candidates))
		for _, candidate := range plan.Candidates {
			claim, claimErr := candidate.CanonicalIssuerClaim()
			if claimErr != nil {
				return nil, 0, permanentBindingError("build issuer claim: %v", claimErr)
			}
			issuerClaims = append(issuerClaims, claim)
		}
		issuerAssignments, revision, registryErr := executor.resolveClaims(ctx, job, inputHash, "issuer", issuerClaims)
		if registryErr != nil {
			return nil, 0, fmt.Errorf("resolve canonical issuers: %w", registryErr)
		}
		registryRevision = revision
		canonical, planErr := domain.PlanCanonicalRegulationClaims(plan.Candidates, issuerAssignments)
		if planErr != nil {
			return nil, 0, permanentBindingError("plan canonical regulations: %v", planErr)
		}
		regulationClaims := make([]domain.CanonicalIdentityClaim, len(canonical))
		for index := range canonical {
			regulationClaims[index] = canonical[index].Claim
		}
		regulationAssignments, revision, registryErr := executor.resolveClaims(ctx, job, inputHash, "regulation", regulationClaims)
		if registryErr != nil {
			return nil, 0, fmt.Errorf("resolve canonical regulations: %w", registryErr)
		}
		if revision > registryRevision {
			registryRevision = revision
		}
		bindings, err = domain.BuildRegulationDocumentBindings(canonical, issuerAssignments, regulationAssignments)
		if err != nil {
			return nil, 0, permanentBindingError("build regulation bindings: %v", err)
		}
	}
	provisions, err := domain.PlanProvisionIdentities(batch, bindings, executor.config.MaximumRecords)
	if err != nil {
		return nil, 0, permanentBindingError("plan provision identities: %v", err)
	}
	provisionAssignments := []domain.CanonicalIdentityAssignment{}
	if len(provisions) != 0 {
		claims := make([]domain.CanonicalIdentityClaim, 0, len(provisions))
		for _, candidate := range provisions {
			claim, claimErr := candidate.CanonicalClaim()
			if claimErr != nil {
				return nil, 0, permanentBindingError("build provision claim: %v", claimErr)
			}
			claims = append(claims, claim)
		}
		var revision uint64
		provisionAssignments, revision, err = executor.resolveClaims(ctx, job, inputHash, "provision", claims)
		if err != nil {
			return nil, 0, fmt.Errorf("resolve canonical provisions: %w", err)
		}
		if revision > registryRevision {
			registryRevision = revision
		}
	}
	bound, err := domain.BindDocumentBatch(batch, bindings, provisions, provisionAssignments, domain.DocumentBindingConfig{
		Software: executor.config.Software, Build: executor.config.Build, Language: executor.config.Language,
		Jurisdiction: executor.config.Jurisdiction, RegistryBatchSize: executor.config.RegistryBatchSize,
		DocumentKind: executor.config.DocumentKind, RegistryRevision: registryRevision,
		MaximumRecords: executor.config.MaximumRecords, WireLimits: executor.config.WireLimits,
	})
	if err != nil {
		return nil, 0, permanentBindingError("materialize bound DocumentBatch: %v", err)
	}
	return bound, registryRevision, nil
}

func (executor *BindingExecutor) resolveClaims(
	ctx context.Context,
	job domain.JobRecord,
	inputHash string,
	phase string,
	claims []domain.CanonicalIdentityClaim,
) ([]domain.CanonicalIdentityAssignment, uint64, error) {
	ordered := append([]domain.CanonicalIdentityClaim(nil), claims...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ProposalKey < ordered[j].ProposalKey })
	assignments := make([]domain.CanonicalIdentityAssignment, 0, len(ordered))
	revision := uint64(0)
	for start, batchIndex := 0, 0; start < len(ordered); start, batchIndex = start+executor.config.RegistryBatchSize, batchIndex+1 {
		end := start + executor.config.RegistryBatchSize
		if end > len(ordered) {
			end = len(ordered)
		}
		claimsRaw, err := json.Marshal(ordered[start:end])
		if err != nil {
			return nil, 0, permanentBindingError("encode %s registry batch: %v", phase, err)
		}
		claimsDigest := sha256.Sum256(claimsRaw)
		operationPhase := fmt.Sprintf("%s:batch-size:%06d:index:%06d:claims:%s", phase,
			executor.config.RegistryBatchSize, batchIndex, hex.EncodeToString(claimsDigest[:]))
		resolved, batchRevision, err := executor.store.ResolveCanonicalIdentities(
			ctx, job.CorpusID, bindingOperationKey(job.JobID, inputHash, operationPhase), 0, ordered[start:end])
		if err != nil {
			return nil, 0, err
		}
		assignments = append(assignments, resolved...)
		if batchRevision > revision {
			revision = batchRevision
		}
	}
	return assignments, revision, nil
}

func (executor *BindingExecutor) recover(ctx context.Context, job domain.JobRecord) (bool, *BindingResult, error) {
	checkpoint, err := executor.store.LoadLatestCheckpoint(ctx, job.JobID)
	if errors.Is(err, domain.ErrNotFound) || err == nil && checkpoint.Stage != pb.JobStage_JOB_STAGE_BIND {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, bindingPrerequisiteError("load BIND recovery checkpoint", err)
	}
	if checkpoint.TerminalStatus == pb.CompletionStatus_COMPLETION_STATUS_UNSPECIFIED || checkpoint.Fence >= job.LeaseFence ||
		checkpoint.GetMeta().GetCorpusId() != job.CorpusID || checkpoint.JobId != job.JobID ||
		len(checkpoint.CompletedBatchKeys) != 1 || len(checkpoint.ArtifactHashes) != 1 {
		return false, nil, permanentBindingError("BIND recovery checkpoint is inconsistent")
	}
	artifact, err := executor.store.LoadArtifact(ctx, job.CorpusID, checkpoint.CompletedBatchKeys[0])
	if err != nil {
		return false, nil, bindingPrerequisiteError("load BIND recovery artifact", err)
	}
	if artifact.GetContentHash() == nil || !proto.Equal(artifact.ContentHash, checkpoint.ArtifactHashes[0]) {
		return false, nil, permanentBindingError("BIND recovery artifact differs from checkpoint")
	}
	if requested, cancellationErr := executor.store.CancellationRequested(ctx, job.JobID, job.LeaseOwner, job.LeaseFence); cancellationErr != nil {
		return false, nil, fmt.Errorf("check BIND recovery cancellation: %w", cancellationErr)
	} else if requested {
		return false, nil, errJobCancellationRequested
	}
	raw, err := executor.artifacts.ReadVerified(ctx, artifact, executor.config.MaximumBytes)
	if err != nil {
		return false, nil, bindingPrerequisiteError("verify BIND recovery artifact", err)
	}
	bound := &pb.DocumentBatch{}
	if err = proto.Unmarshal(raw, bound); err != nil {
		return false, nil, permanentBindingError("decode BIND recovery artifact: %v", err)
	}
	if err = domain.ValidateWire(bound, executor.config.WireLimits); err != nil {
		return false, nil, permanentBindingError("validate BIND recovery artifact: %v", err)
	}
	if artifact.MediaType != documentBatchMediaType || artifact.SchemaVersion != 1 ||
		bound.GetMeta().GetCorpusId() != job.CorpusID || bound.GetDependencyManifest().GetProducerManifest() == nil ||
		checkpoint.GetManifest() == nil || !proto.Equal(bound.DependencyManifest.ProducerManifest, checkpoint.Manifest) ||
		checkpoint.TerminalStatus == pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED && bound.Completeness != pb.Completeness_COMPLETENESS_COMPLETE ||
		checkpoint.TerminalStatus == pb.CompletionStatus_COMPLETION_STATUS_FAILED && bound.Completeness == pb.Completeness_COMPLETENESS_COMPLETE {
		return false, nil, permanentBindingError("BIND recovery payload differs from terminal checkpoint")
	}
	recovered := proto.Clone(checkpoint).(*pb.Checkpoint)
	recovered.Fence = job.LeaseFence
	recoveryHash := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%d\x00%s", job.JobID, job.Attempt, job.LeaseFence, checkpoint.Meta.RecordId)))
	recovered.Meta.RecordId = "checkpoint:bind-recovery:" + hex.EncodeToString(recoveryHash[:])
	if err = executor.store.SaveCheckpoint(ctx, recovered, job.LeaseOwner); err != nil {
		return false, nil, fmt.Errorf("save recovered BIND checkpoint: %w", err)
	}
	desired, stateErr := terminalState(checkpoint.TerminalStatus)
	if stateErr != nil {
		return false, nil, permanentBindingError("recover BIND outcome: %v", stateErr)
	}
	actual, err := executor.store.CompleteWorkerAttempt(ctx, job.JobID, job.LeaseOwner, job.LeaseFence, desired, 0)
	if err != nil {
		return false, nil, fmt.Errorf("complete recovered BIND attempt: %w", err)
	}
	if actual == pb.JobState_JOB_STATE_CANCELLED && desired != pb.JobState_JOB_STATE_CANCELLED {
		return true, nil, errJobCancellationRequested
	}
	return true, &BindingResult{DocumentBatch: artifact, Checkpoint: recovered, Completeness: bound.Completeness}, nil
}

func (executor *BindingExecutor) finish(ctx context.Context, job domain.JobRecord, cause error) error {
	desired := pb.JobState_JOB_STATE_RETRY_WAIT
	delay := executor.retryDelay(job.StageAttempt)
	if errors.Is(cause, domain.ErrPersistentIntegrity) {
		desired, delay = pb.JobState_JOB_STATE_FAILED, 0
	}
	if errors.Is(cause, errJobCancellationRequested) {
		desired, delay = pb.JobState_JOB_STATE_CANCELLED, 0
	}
	transitionCtx := ctx
	cancel := func() {}
	if ctx.Err() != nil {
		transitionCtx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
	}
	defer cancel()
	actual, err := executor.store.CompleteWorkerAttempt(transitionCtx, job.JobID, job.LeaseOwner, job.LeaseFence, desired, delay)
	if err != nil {
		return errors.Join(cause, fmt.Errorf("record BIND failure as %s: %w", desired, err))
	}
	if actual == pb.JobState_JOB_STATE_CANCELLED && desired != pb.JobState_JOB_STATE_CANCELLED {
		return errors.Join(cause, errJobCancellationRequested)
	}
	return cause
}

func (executor *BindingExecutor) retryDelay(attempt uint32) time.Duration {
	delay := executor.config.RetryBase
	for step := uint32(1); step < attempt && delay < executor.config.RetryMax; step++ {
		if delay > executor.config.RetryMax/2 {
			return executor.config.RetryMax
		}
		delay *= 2
	}
	if delay > executor.config.RetryMax {
		return executor.config.RetryMax
	}
	return delay
}

func bindingOperationKey(jobID, inputHash, phase string) string {
	digest := sha256.Sum256([]byte(strings.Join([]string{"bind-v1", jobID, inputHash, phase}, "\x00")))
	return "registry:bind:" + phase + ":" + hex.EncodeToString(digest[:])
}

func bindingArtifactReference(payload []byte) *pb.ArtifactRef {
	digest := sha256.Sum256(payload)
	hexDigest := hex.EncodeToString(digest[:])
	return &pb.ArtifactRef{
		ArtifactId:  "artifact:document-batch:" + hexDigest,
		ContentHash: &pb.ContentHash{Sha256: hexDigest},
		StorageKey:  "sha256/" + hexDigest[:2] + "/" + hexDigest[2:4] + "/" + hexDigest + ".bin",
		MediaType:   documentBatchMediaType, ByteSize: uint64(len(payload)), SchemaVersion: 1,
	}
}

func bindingCheckpoint(
	job domain.JobRecord,
	output *pb.ArtifactRef,
	producer *pb.ProducerManifest,
	terminal pb.CompletionStatus,
	registryRevision uint64,
) *pb.Checkpoint {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%d\x00%s\x00%d", job.JobID, job.Attempt, job.LeaseFence, output.ArtifactId, registryRevision)))
	return &pb.Checkpoint{
		Meta:  &pb.RecordMeta{SchemaVersion: 1, CorpusId: job.CorpusID, RecordId: "checkpoint:bind:" + hex.EncodeToString(digest[:])},
		JobId: job.JobID, Stage: pb.JobStage_JOB_STAGE_BIND,
		CompletedBatchKeys: []string{output.ArtifactId}, ArtifactHashes: []*pb.ContentHash{proto.Clone(output.ContentHash).(*pb.ContentHash)},
		Manifest: proto.Clone(producer).(*pb.ProducerManifest), Fence: job.LeaseFence, TerminalStatus: terminal,
	}
}

func bindingPrerequisiteError(operation string, err error) error {
	if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrPersistentIntegrity) {
		return permanentBindingError("%s: %v", operation, err)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func permanentBindingError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", domain.ErrPersistentIntegrity, fmt.Sprintf(format, arguments...))
}
