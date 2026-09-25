// Reads EXTRACT and registry candidate artifacts through verified storage before a fenced
// PostgreSQL LINK/DEFER commit. The EXTRACT checkpoint is checked before I/O and again by
// the registry writer inside its CAS transaction, so a stale lease cannot authorize a write.
// This handoff does not authenticate reviewers or generate proposals; callers must supply
// persisted, authenticated review approvals and a candidate artifact from trusted production.
// Measure storage read, lock wait, and commit p95/p99 plus peak bytes under the reference
// workload; targets in configs/benchmark-targets.yaml remain REQUIRED_UNMEASURED.
package workflows

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type SemanticResolutionStore interface {
	LoadLatestCheckpoint(context.Context, string) (*pb.Checkpoint, error)
	LoadArtifact(context.Context, string, string) (*pb.ArtifactRef, error)
	CommitSemanticResolutions(context.Context, domain.SemanticJobFence, domain.SemanticRegistryInputs,
		*pb.RegistryResolveRequest, []domain.ReviewedLink, int, int) (*pb.RegistryResolveResponse, error)
}

type SemanticResolutionArtifactReader interface {
	ReadVerified(context.Context, *pb.ArtifactRef, uint64) ([]byte, error)
}

type SemanticResolutionHandoff struct {
	store                       SemanticResolutionStore
	artifacts                   SemanticResolutionArtifactReader
	maximumBytes                uint64
	maximumReferences           int
	maximumCandidatesPerMention int
	expectedCandidatePolicies   map[string]*pb.ContentHash
}

func NewSemanticResolutionHandoff(store SemanticResolutionStore,
	artifacts SemanticResolutionArtifactReader, maximumBytes uint64,
	maximumReferences, maximumCandidatesPerMention int) (*SemanticResolutionHandoff, error) {
	if store == nil || artifacts == nil || maximumBytes == 0 ||
		maximumReferences <= 0 || maximumCandidatesPerMention <= 0 {
		return nil, errors.New("bounded semantic resolution handoff dependencies are required")
	}
	return &SemanticResolutionHandoff{store: store, artifacts: artifacts,
		maximumBytes: maximumBytes, maximumReferences: maximumReferences,
		maximumCandidatesPerMention: maximumCandidatesPerMention}, nil
}

// Commit requires a claimed RESOLVE job and the exact registered candidate reference.
// Authentication of ReviewedLink actor is a separate review-workflow boundary.
func (handoff *SemanticResolutionHandoff) Commit(ctx context.Context, job domain.JobRecord,
	candidateRef *pb.ArtifactRef, request *pb.RegistryResolveRequest,
	approvals []domain.ReviewedLink) (*pb.RegistryResolveResponse, error) {
	result, err := handoff.commitVerified(ctx, job, candidateRef, request, approvals, nil)
	if err != nil {
		return nil, err
	}
	return result.response, nil
}

type semanticCommitResult struct {
	response  *pb.RegistryResolveResponse
	sourceRef *pb.ArtifactRef
	input     domain.SemanticRegistryInputs
}

func (handoff *SemanticResolutionHandoff) commitVerified(ctx context.Context, job domain.JobRecord,
	candidateRef *pb.ArtifactRef, request *pb.RegistryResolveRequest,
	approvals []domain.ReviewedLink,
	preflight func(domain.SemanticRegistryInputs, string) error) (*semanticCommitResult, error) {
	if handoff == nil || handoff.store == nil || handoff.artifacts == nil || ctx == nil ||
		job.JobID == "" || job.CorpusID == "" || job.LeaseOwner == "" || job.LeaseFence == 0 ||
		job.State != pb.JobState_JOB_STATE_RUNNING || job.Stage != pb.JobStage_JOB_STAGE_RESOLVE ||
		job.CancellationRequested || !job.LeaseExpiresAt.After(time.Now()) ||
		candidateRef == nil || request == nil || request.Context == nil ||
		request.Context.CorpusId != job.CorpusID {
		return nil, errors.New("live RESOLVE lease, candidate, and matching request are required")
	}
	attemptCtx, cancel := context.WithDeadline(ctx, job.LeaseExpiresAt)
	defer cancel()
	checkpoint, err := handoff.store.LoadLatestCheckpoint(attemptCtx, job.JobID)
	if err != nil {
		return nil, fmt.Errorf("load RESOLVE source checkpoint: %w", err)
	}
	if checkpoint == nil || checkpoint.GetMeta().GetCorpusId() != job.CorpusID ||
		checkpoint.JobId != job.JobID || checkpoint.Stage != pb.JobStage_JOB_STAGE_EXTRACT ||
		checkpoint.TerminalStatus != pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED ||
		checkpoint.Fence == 0 || checkpoint.Fence >= job.LeaseFence ||
		len(checkpoint.CompletedBatchKeys) != 1 || len(checkpoint.ArtifactHashes) != 1 {
		return nil, errors.New("RESOLVE requires one successful earlier EXTRACT checkpoint")
	}
	sourceRef, err := handoff.store.LoadArtifact(attemptCtx, job.CorpusID,
		checkpoint.CompletedBatchKeys[0])
	if err != nil {
		return nil, fmt.Errorf("load checkpointed EXTRACT artifact: %w", err)
	}
	if sourceRef.GetContentHash() == nil ||
		!proto.Equal(sourceRef.ContentHash, checkpoint.ArtifactHashes[0]) {
		return nil, errors.New("EXTRACT artifact hash differs from checkpoint")
	}
	storedCandidate, err := handoff.store.LoadArtifact(attemptCtx, job.CorpusID,
		candidateRef.ArtifactId)
	if err != nil {
		return nil, fmt.Errorf("load candidate artifact metadata: %w", err)
	}
	if !proto.Equal(storedCandidate, candidateRef) || sourceRef.ArtifactId == candidateRef.ArtifactId ||
		!resolutionProtoMedia(sourceRef.MediaType, new(pb.ExtractionBatch)) || candidateRef.MediaType != "application/x-protobuf" ||
		sourceRef.ByteSize == 0 || candidateRef.ByteSize == 0 ||
		sourceRef.ByteSize > handoff.maximumBytes ||
		candidateRef.ByteSize > handoff.maximumBytes-sourceRef.ByteSize {
		return nil, errors.New("semantic artifact metadata, media, or total byte budget differs")
	}
	sourceBytes, err := handoff.artifacts.ReadVerified(attemptCtx, sourceRef, handoff.maximumBytes)
	if err != nil {
		return nil, fmt.Errorf("read verified EXTRACT artifact: %w", err)
	}
	candidateBytes, err := handoff.artifacts.ReadVerified(attemptCtx, candidateRef,
		handoff.maximumBytes-sourceRef.ByteSize)
	if err != nil {
		return nil, fmt.Errorf("read verified candidate artifact: %w", err)
	}
	proof := domain.SemanticJobFence{JobID: job.JobID, OwnerID: job.LeaseOwner,
		SourceCheckpointID: checkpoint.Meta.RecordId, Fence: job.LeaseFence}
	input := domain.SemanticRegistryInputs{SourceRef: sourceRef, SourceBytes: sourceBytes,
		CandidateRef: candidateRef, CandidateBytes: candidateBytes}
	if preflight != nil {
		if err = preflight(input, checkpoint.Meta.RecordId); err != nil {
			return nil, fmt.Errorf("preflight RESOLVE output before registry CAS: %w", err)
		}
	}
	response, err := handoff.store.CommitSemanticResolutions(attemptCtx, proof, input, request, approvals,
		handoff.maximumReferences, handoff.maximumCandidatesPerMention)
	if err != nil {
		return nil, err
	}
	return &semanticCommitResult{response: response, sourceRef: sourceRef, input: input}, nil
}
