// Recovers a legacy successful INDEX checkpoint under a new live claim without
// repeating inference or rewriting its immutable batch/context. Re-admission
// checks original checkpoint identity, registered output bytes, source authority,
// lexical mapping and the exact assigned plan. Only the coordinator checkpoint
// gets a new ID/fence; the old checkpoint and generated output remain unchanged.
// Unsupported/failed checkpoints require replan instead of unlimited model retry.
// Measure recovery I/O/RSS/latency against configs/benchmark-targets.yaml.
package indexing

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func (p *InitialIndexJobProcessor) recoverOutput(ctx context.Context, plans *InitialIndexPlans, position int, request *pb.ProcessBatchRequest, job domain.JobRecord) (*VerifiedIndexOutput, error) {
	cp, err := p.store.LoadLatestCheckpoint(ctx, job.JobID)
	if err != nil {
		return nil, indexArtifactReadError(err)
	}
	if err = domain.ValidateWire(cp, domain.DefaultWireLimits); err != nil {
		return nil, fmt.Errorf("invalid recovery checkpoint: %w", domain.ErrIndexReplan)
	}
	planned := plans.plans[position]
	if cp.Meta.RecordId != job.LatestCheckpointID || cp.Meta.CorpusId != job.CorpusID || cp.JobId != job.JobID || cp.Stage != pb.JobStage_JOB_STAGE_INDEX ||
		cp.Fence >= job.LeaseFence || cp.TerminalStatus != pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED || len(cp.CompletedBatchKeys) != 1 || len(cp.ArtifactHashes) != 1 || !proto.Equal(cp.Manifest, planned.Plan.Producer) {
		return nil, fmt.Errorf("unsupported INDEX recovery checkpoint: %w", domain.ErrIndexReplan)
	}
	ref, err := p.store.LoadArtifact(ctx, job.CorpusID, cp.CompletedBatchKeys[0])
	if err != nil {
		return nil, indexArtifactReadError(err)
	}
	if ref.MediaType != domain.IndexBatchMediaType || !proto.Equal(ref.ContentHash, cp.ArtifactHashes[0]) {
		return nil, domain.ErrPersistentIntegrity
	}
	loader := &initialArtifactLoader{authority: p.store, reader: p.reader, corpus: job.CorpusID, remaining: 64 << 20, cache: map[string]initialArtifact{}}
	plan, source, batch := new(pb.IndexBuildPlan), new(pb.DocumentBatch), new(pb.IndexBatch)
	if err = loader.read(ctx, planned.Reference, plan); err != nil {
		return nil, err
	}
	if !proto.Equal(plan, planned.Plan) {
		return nil, domain.ErrPersistentIntegrity
	}
	if err = loader.read(ctx, plan.DocumentBatch, source); err != nil {
		return nil, err
	}
	if err = p.store.VerifyIndexSourceCheckpoint(ctx, job.CorpusID, planned.SourceJobID, plan.DocumentBatch); err != nil {
		return nil, err
	}
	_, dictionary, err := loadInitialLexical(ctx, loader, plan)
	if err != nil {
		return nil, err
	}
	if err = loader.read(ctx, ref, batch); err != nil {
		return nil, err
	}
	if err = domain.ValidatePlannedIndexBatch(batch, plan, planned.Reference, source); err != nil {
		return nil, fmt.Errorf("invalid recovered INDEX batch: %w", domain.ErrPersistentIntegrity)
	}
	if batch.Context.AuthScopeRef != p.scope || !proto.Equal(batch.Context.SnapshotRef, plan.TargetSnapshot) || !proto.Equal(batch.Context.ConfigFingerprint, plan.Producer.ConfigHash) {
		return nil, domain.ErrPersistentIntegrity
	}
	if err = verifyIndexSparseTerms(dictionary, batch.Records); err != nil {
		return nil, fmt.Errorf("invalid recovered sparse mapping: %w", domain.ErrPersistentIntegrity)
	}
	recovered := proto.Clone(cp).(*pb.Checkpoint)
	recovered.Meta.RecordId = initialPlanID("index-recovery-checkpoint-v1", job.JobID, cp.Meta.RecordId, fmt.Sprint(job.LeaseFence))
	recovered.Fence = job.LeaseFence
	return &VerifiedIndexOutput{batch: batch, response: &pb.ProcessBatchResponse{RequestId: request.Context.RequestId, JobId: job.JobID, Attempt: job.Attempt, Fence: job.LeaseFence, Status: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED, Checkpoint: recovered, IndexBatch: ref}}, ctx.Err()
}
