// Drives one durable RESOLVE claim through pinned candidate planning and contextual proposals.
// Nonempty output is advisory and parks atomically in WAITING_REVIEW; no model confidence
// grants registry authority. Empty EXTRACT and previously committed intents use the existing
// checkpoint/recovery paths. A fixed attempt deadline, cancellation polling and bounded retry
// protect resources. Measure queue/model/storage p95/p99, RSS and candidate quality against
// configs/benchmark-targets.yaml; REQUIRED_UNMEASURED is not acceptance.
package workflows

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"maps"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
	"time"
)

type SemanticExecutorStore interface {
	SemanticResolutionOutputStore
	SemanticCandidateStore
	SemanticCandidatePolicyStore
	SemanticEmptyResolutionStore
	ClaimResolveJob(context.Context, string, time.Duration) (domain.JobRecord, error)
	CancellationRequested(context.Context, string, string, uint64) (bool, error)
	ParkSemanticProposal(context.Context, domain.SemanticJobFence, *pb.ArtifactRef, *pb.ExtractionBatch,
		*pb.ArtifactRef, *pb.ArtifactRef, *pb.ArtifactRef) (pb.JobState, error)
}

type SemanticExecutorConfig struct {
	OwnerID, AuthScope                                        string
	Lease, CallTimeout, CancellationPoll, RetryBase, RetryMax time.Duration
	Ontology                                                  *domain.Ontology
	Policies                                                  map[string]domain.CandidatePlanningPolicy
	Producer                                                  *pb.ProducerManifest
	// ProducerPin is the exact-byte SHA-256 of the operator's producer file. Submitters
	// must include it in durable input_hashes to prevent model drift across retries.
	ProducerPin                                                                           *pb.ContentHash
	OutputSchema                                                                          *pb.ArtifactRef
	MaximumBytes                                                                          uint64
	MaximumReferences, MaximumCandidatesPerMention, MaximumScopes, MaximumAliasesPerScope int
}

type SemanticExecutor struct {
	store     SemanticExecutorStore
	artifacts SemanticResolutionOutputArtifacts
	model     SemanticResolutionModel
	handoff   *SemanticResolutionHandoff
	config    SemanticExecutorConfig
}

type SemanticExecutorResult struct {
	State    pb.JobState
	Artifact *pb.ArtifactRef
}

func NewSemanticExecutor(store SemanticExecutorStore, artifacts SemanticResolutionOutputArtifacts,
	model SemanticResolutionModel, config SemanticExecutorConfig) (*SemanticExecutor, error) {
	if store == nil || artifacts == nil || model == nil || config.Ontology == nil ||
		!schedulerCorpusIDPattern.MatchString(config.OwnerID) || config.AuthScope == "" || config.Lease <= 0 ||
		config.CallTimeout <= 0 || config.CallTimeout >= config.Lease || config.CancellationPoll <= 0 ||
		config.RetryBase <= 0 || config.RetryMax < config.RetryBase || config.MaximumScopes <= 0 || config.MaximumAliasesPerScope <= 0 {
		return nil, errors.New("bounded RESOLVE dependencies, identity and timing required")
	}
	for _, value := range []proto.Message{config.Producer, config.ProducerPin, config.OutputSchema} {
		if err := domain.ValidateWire(value, domain.DefaultWireLimits); err != nil {
			return nil, fmt.Errorf("resolution configuration: %w", err)
		}
	}
	if len(config.Producer.Models) != 1 || config.Producer.Models[0].Task != pb.ModelTask_MODEL_TASK_RESOLVE ||
		config.Producer.Models[0].PromptHash == nil ||
		!modelProducerMatches(config.Producer, &pb.SemanticBatchContext{Model: config.Producer.Models[0], OutputSchema: config.OutputSchema}) ||
		!containsExpectedHash(config.Producer.InputHashes, config.Ontology.ContentHash()) {
		return nil, errors.New("RESOLVE producer must pin exactly one resolver, prompt, schema and ontology")
	}
	handoff, err := NewPlannedSemanticResolutionHandoff(store, artifacts, config.MaximumBytes, config.MaximumReferences,
		config.MaximumCandidatesPerMention, config.Policies)
	if err != nil {
		return nil, err
	}
	policies := make(map[string]domain.CandidatePlanningPolicy, len(config.Policies))
	for corpus, policy := range config.Policies {
		if policy.MaximumTotalScopes > config.MaximumScopes || 1+policy.MaximumMentions+2*policy.MaximumTotalScopes > config.MaximumReferences ||
			policy.MaximumTotalScopes > domain.MaximumRegistryLookupAliases/config.MaximumAliasesPerScope {
			return nil, errors.New("RESOLVE runtime cannot accommodate candidate policy")
		}
		copied := policy
		copied.ScopesByType = make(map[string][]string, len(policy.ScopesByType))
		for kind, scopes := range policy.ScopesByType {
			copied.ScopesByType[kind] = append([]string(nil), scopes...)
		}
		copied.IncludeSourceRegulationType = maps.Clone(policy.IncludeSourceRegulationType)
		policies[corpus] = copied
	}
	config.Policies = policies
	config.Producer = proto.Clone(config.Producer).(*pb.ProducerManifest)
	config.ProducerPin = proto.Clone(config.ProducerPin).(*pb.ContentHash)
	config.OutputSchema = proto.Clone(config.OutputSchema).(*pb.ArtifactRef)
	return &SemanticExecutor{store: store, artifacts: artifacts, model: model, handoff: handoff, config: config}, nil
}

func (e *SemanticExecutor) RunOnce(ctx context.Context) (domain.JobRecord, *SemanticExecutorResult, error) {
	job, err := e.store.ClaimResolveJob(ctx, e.config.OwnerID, e.config.Lease)
	if err != nil {
		return domain.JobRecord{}, nil, err
	}
	if job.State != pb.JobState_JOB_STATE_RUNNING || job.Stage != pb.JobStage_JOB_STAGE_RESOLVE ||
		job.LeaseOwner != e.config.OwnerID || job.LeaseFence == 0 || !job.LeaseExpiresAt.After(time.Now()) {
		return job, nil, domain.ErrPersistentIntegrity
	}
	deadline := time.Now().Add(e.config.CallTimeout)
	if job.LeaseExpiresAt.Before(deadline) {
		deadline = job.LeaseExpiresAt
	}
	timed, stop := context.WithDeadline(ctx, deadline)
	defer stop()
	attempt, cancel := context.WithCancelCause(timed)
	monitor, stopMonitor := context.WithCancel(attempt)
	done := make(chan struct{})
	go func() { defer close(done); e.monitor(monitor, job, cancel) }()
	result, err := e.execute(attempt, job)
	stopMonitor()
	<-done
	cause := context.Cause(attempt)
	cancel(nil)
	if err != nil {
		return job, nil, e.finish(ctx, job, errors.Join(err, cause))
	}
	return job, result, nil
}

func (e *SemanticExecutor) execute(ctx context.Context, job domain.JobRecord) (*SemanticExecutorResult, error) {
	policy, ok := e.config.Policies[job.CorpusID]
	if !ok {
		return nil, fmt.Errorf("corpus has no RESOLVE policy: %w", domain.ErrPersistentIntegrity)
	}
	request, err := e.store.LoadIngestionRequest(ctx, job.JobID)
	if err != nil {
		return nil, err
	}
	policyHash, _ := policy.Fingerprint()
	if request == nil || domain.ValidateWire(request, domain.DefaultWireLimits) != nil || request.CorpusId != job.CorpusID ||
		!containsExpectedHash(request.GetConfigManifest().GetInputHashes(), policyHash) ||
		!containsExpectedHash(request.GetConfigManifest().GetInputHashes(), e.config.ProducerPin) ||
		!containsExpectedHash(request.GetConfigManifest().GetInputHashes(), e.config.Ontology.ContentHash()) {
		return nil, fmt.Errorf("durable request lost corpus/ontology/policy/model pins: %w", domain.ErrPersistentIntegrity)
	}
	intent, intentErr := e.store.LoadSemanticResolutionIntent(ctx, job.CorpusID, job.JobID)
	if intentErr == nil {
		if !proto.Equal(intent.Preview.GetDependencies().GetProducerManifest(), e.config.Producer) {
			return nil, domain.ErrPersistentIntegrity
		}
		if _, err := e.recoverySource(ctx, job, request, intent.Preview.GetSourceExtractionBatch()); err != nil {
			return nil, err
		}
		output, err := e.handoff.CommitAndCheckpoint(ctx, job, nil, nil, nil, nil, nil, nil, "")
		if err != nil {
			return nil, err
		}
		return &SemanticExecutorResult{State: pb.JobState_JOB_STATE_STAGED, Artifact: output.Artifact}, nil
	}
	if !errors.Is(intentErr, domain.ErrNotFound) {
		return nil, intentErr
	}
	checkpoint, err := e.store.LoadLatestCheckpoint(ctx, job.JobID)
	if err != nil {
		return nil, err
	}
	idDigest := sha256.Sum256([]byte("resolve-executor-v1\x00" + job.JobID))
	recordID := "resolution:" + hex.EncodeToString(idDigest[:])
	model := e.config.Producer.Models[0]
	if checkpoint.GetStage() == pb.JobStage_JOB_STAGE_RESOLVE {
		// With no decision intent, only the mention-free path may recover a terminal output.
		if len(checkpoint.CompletedBatchKeys) != 1 {
			return nil, domain.ErrPersistentIntegrity
		}
		ref, err := e.store.LoadArtifact(ctx, job.CorpusID, checkpoint.CompletedBatchKeys[0])
		if err != nil {
			return nil, err
		}
		raw, err := e.artifacts.ReadVerified(ctx, ref, e.config.MaximumBytes)
		if err != nil {
			return nil, err
		}
		batch := new(pb.ResolutionBatch)
		if domain.DecodeWire(raw, batch, domain.DefaultWireLimits) != nil || len(batch.Proposals) != 0 || len(batch.Decisions) != 0 {
			return nil, domain.ErrPersistentIntegrity
		}
		source, err := e.recoverySource(ctx, job, request, batch.SourceExtractionBatch)
		if err != nil {
			return nil, err
		}
		if len(source.Mentions) != 0 || len(source.Assertions) != 0 || len(source.Supports) != 0 {
			return nil, domain.ErrPersistentIntegrity
		}
		output, err := e.handoff.CompleteEmptyResolution(ctx, job, model, e.config.Producer,
			&pb.TokenUsage{TokenizerId: model.ModelId + ":no-inference"}, recordID)
		if err != nil {
			return nil, err
		}
		return &SemanticExecutorResult{State: pb.JobState_JOB_STATE_STAGED, Artifact: output.Artifact}, nil
	}
	if checkpoint == nil || checkpoint.GetMeta().GetCorpusId() != job.CorpusID || checkpoint.JobId != job.JobID ||
		checkpoint.Stage != pb.JobStage_JOB_STAGE_EXTRACT || checkpoint.TerminalStatus != pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED ||
		checkpoint.Fence == 0 || checkpoint.Fence >= job.LeaseFence || len(checkpoint.CompletedBatchKeys) != 1 || len(checkpoint.ArtifactHashes) != 1 {
		return nil, domain.ErrPersistentIntegrity
	}
	sourceRef, err := e.store.LoadArtifact(ctx, job.CorpusID, checkpoint.CompletedBatchKeys[0])
	if err != nil {
		return nil, err
	}
	if !proto.Equal(sourceRef.GetContentHash(), checkpoint.ArtifactHashes[0]) || !resolutionProtoMedia(sourceRef.MediaType, new(pb.ExtractionBatch)) {
		return nil, domain.ErrPersistentIntegrity
	}
	raw, err := e.artifacts.ReadVerified(ctx, sourceRef, e.config.MaximumBytes)
	if err != nil {
		return nil, err
	}
	source := new(pb.ExtractionBatch)
	if domain.DecodeWire(raw, source, domain.DefaultWireLimits) != nil || source.GetMeta().GetCorpusId() != job.CorpusID ||
		source.GetContext().GetCorpusId() != job.CorpusID || source.GetContext().GetAuthScopeRef() != e.config.AuthScope ||
		!proto.Equal(source.GetContext().GetConfigFingerprint(), request.GetConfigManifest().GetConfigHash()) ||
		!proto.Equal(source.GetDependencies().GetProducerManifest(), checkpoint.Manifest) ||
		source.OntologyVersion != e.config.Ontology.Version() || source.Completeness != pb.Completeness_COMPLETENESS_COMPLETE {
		return nil, domain.ErrPersistentIntegrity
	}
	if len(source.Mentions) == 0 {
		output, err := e.handoff.CompleteEmptyResolution(ctx, job, model, e.config.Producer,
			&pb.TokenUsage{TokenizerId: model.ModelId + ":no-inference"}, recordID)
		if err != nil {
			return nil, err
		}
		return &SemanticExecutorResult{State: pb.JobState_JOB_STATE_STAGED, Artifact: output.Artifact}, nil
	}
	candidateProducer := proto.Clone(request.ConfigManifest).(*pb.ProducerManifest)
	_, candidateRef, err := e.handoff.PrepareAndStorePlannedCandidates(ctx, job, candidateProducer,
		"candidates:"+hex.EncodeToString(idDigest[:]), policy, e.config.MaximumScopes, e.config.MaximumAliasesPerScope)
	if err != nil {
		return nil, err
	}
	requestContext := proto.Clone(source.Context).(*pb.RequestContext)
	requestContext.RequestId = recordID
	requestContext.TraceId = recordID
	deadline, _ := ctx.Deadline()
	requestContext.Deadline = timestamppb.New(deadline)
	proposals, err := e.handoff.ProposeWithModel(ctx, job, candidateRef, &pb.SemanticBatchContext{
		Context: requestContext, Model: model, OperationKey: recordID, OntologyVersion: source.OntologyVersion,
		OutputSchema: e.config.OutputSchema}, e.config.Producer, e.model)
	if err != nil {
		return nil, err
	}
	state, err := e.store.ParkSemanticProposal(ctx, domain.SemanticJobFence{JobID: job.JobID, OwnerID: job.LeaseOwner,
		Fence: job.LeaseFence, SourceCheckpointID: checkpoint.Meta.RecordId}, sourceRef, source, candidateRef, proposals.InputArtifact, proposals.OutputArtifact)
	if err != nil {
		return nil, err
	}
	if state == pb.JobState_JOB_STATE_CANCELLED {
		return &SemanticExecutorResult{State: state}, nil
	}
	if state != pb.JobState_JOB_STATE_WAITING_REVIEW {
		return nil, domain.ErrPersistentIntegrity
	}
	return &SemanticExecutorResult{State: state, Artifact: proposals.OutputArtifact}, nil
}

// Recovery must retain the same authorization/configuration gate as a fresh EXTRACT input.
// A persisted intent or terminal checkpoint is not permission to change its source scope.
func (e *SemanticExecutor) recoverySource(ctx context.Context, job domain.JobRecord, request *pb.IngestionRequest,
	ref *pb.ArtifactRef) (*pb.ExtractionBatch, error) {
	if ref == nil || !resolutionProtoMedia(ref.MediaType, new(pb.ExtractionBatch)) {
		return nil, domain.ErrPersistentIntegrity
	}
	stored, err := e.store.LoadArtifact(ctx, job.CorpusID, ref.ArtifactId)
	if err != nil {
		return nil, err
	}
	if !proto.Equal(stored, ref) {
		return nil, domain.ErrPersistentIntegrity
	}
	raw, err := e.artifacts.ReadVerified(ctx, ref, e.config.MaximumBytes)
	if err != nil {
		return nil, err
	}
	source := new(pb.ExtractionBatch)
	if domain.DecodeWire(raw, source, domain.DefaultWireLimits) != nil || source.GetMeta().GetCorpusId() != job.CorpusID ||
		source.GetContext().GetCorpusId() != job.CorpusID || source.GetContext().GetAuthScopeRef() != e.config.AuthScope ||
		!proto.Equal(source.GetContext().GetConfigFingerprint(), request.GetConfigManifest().GetConfigHash()) ||
		source.OntologyVersion != e.config.Ontology.Version() || source.Completeness != pb.Completeness_COMPLETENESS_COMPLETE {
		return nil, domain.ErrPersistentIntegrity
	}
	return source, nil
}

func (e *SemanticExecutor) monitor(ctx context.Context, job domain.JobRecord, cancel context.CancelCauseFunc) {
	ticker := time.NewTicker(e.config.CancellationPoll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			requested, err := e.store.CancellationRequested(ctx, job.JobID, job.LeaseOwner, job.LeaseFence)
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				cancel(err)
				return
			}
			if requested {
				cancel(errJobCancellationRequested)
				return
			}
		}
	}
}

func (e *SemanticExecutor) finish(ctx context.Context, job domain.JobRecord, cause error) error {
	desired := pb.JobState_JOB_STATE_RETRY_WAIT
	delay := e.config.RetryBase
	for step := uint32(1); step < job.StageAttempt && delay < e.config.RetryMax; step++ {
		if delay > e.config.RetryMax/2 {
			delay = e.config.RetryMax
			break
		}
		delay *= 2
	}
	if delay > e.config.RetryMax {
		delay = e.config.RetryMax
	}
	if errors.Is(cause, domain.ErrPersistentIntegrity) || errors.Is(cause, domain.ErrNotFound) ||
		status.Code(cause) == codes.InvalidArgument || status.Code(cause) == codes.FailedPrecondition {
		desired, delay = pb.JobState_JOB_STATE_FAILED, 0
	}
	if errors.Is(cause, errJobCancellationRequested) {
		desired, delay = pb.JobState_JOB_STATE_CANCELLED, 0
	}
	transition, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	_, err := e.store.CompleteWorkerAttempt(transition, job.JobID, job.LeaseOwner, job.LeaseFence, desired, delay)
	return errors.Join(cause, err)
}
