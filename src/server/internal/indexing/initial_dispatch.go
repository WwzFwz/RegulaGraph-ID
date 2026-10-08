// Dispatches one admitted INDEX plan through the existing C01 worker boundary.
// The scheduler supplies an authorized, live INDEX job lease per plan; this code
// neither allocates jobs nor advances their state. It rechecks registered plans,
// source checkpoints and lexical authority before inference, verifies returned
// bytes and exact output context, and returns immutable output for fenced commit.
// Context cancellation/deadlines cover RPC and output admission. Record I/O,
// queue/RPC latency and memory under configs/benchmark-targets.yaml; synthetic
// worker tests are not evidence of model quality or required performance.
package indexing

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type IndexBatchWorker interface {
	ProcessBatch(context.Context, *pb.ProcessBatchRequest) (*pb.ProcessBatchResponse, error)
}

// WorkerRequest consumes a scheduler-owned claim, not an untrusted API payload.
// One durable child job must own one plan: the Rust worker caches one request per
// job/attempt/fence and cannot run different plans under the same active claim.
func (p *InitialIndexPlans) WorkerRequest(position int, job domain.JobRecord, call *pb.RequestContext) (*pb.ProcessBatchRequest, error) {
	if p == nil || position < 0 || position >= len(p.plans) || job.State != pb.JobState_JOB_STATE_RUNNING || job.Stage != pb.JobStage_JOB_STAGE_INDEX || job.CancellationRequested ||
		job.CorpusID != p.binding.Generation.Meta.CorpusId || !job.LeaseExpiresAt.After(time.Now()) {
		return nil, errors.New("admitted plan and live scheduler-owned INDEX claim required")
	}
	if err := domain.ValidateWire(call, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	batch := p.plans[position]
	owned := proto.Clone(call).(*pb.RequestContext)
	if job.LeaseExpiresAt.Before(owned.Deadline.AsTime()) {
		owned.Deadline = timestamppb.New(job.LeaseExpiresAt)
	}
	request := &pb.ProcessBatchRequest{Context: owned, JobId: job.JobID, Attempt: job.Attempt,
		Lease:   &pb.Lease{OwnerId: job.LeaseOwner, Fence: job.LeaseFence, ExpiresAt: timestamppb.New(job.LeaseExpiresAt)},
		Sources: []*pb.ArtifactRef{proto.Clone(batch.Plan.DocumentBatch).(*pb.ArtifactRef)}, Manifest: proto.Clone(batch.Plan.Producer).(*pb.ProducerManifest),
		Stages: []pb.JobStage{pb.JobStage_JOB_STAGE_INDEX}, IndexBuildPlan: proto.Clone(batch.Reference).(*pb.ArtifactRef)}
	if err := p.checkWorkerRequest(position, request); err != nil {
		return nil, err
	}
	return request, nil
}

func (p *InitialIndexPlans) checkWorkerRequest(position int, req *pb.ProcessBatchRequest) error {
	if p == nil || position < 0 || position >= len(p.plans) {
		return errors.New("admitted plan required")
	}
	if err := domain.ValidateWire(req, domain.DefaultWireLimits); err != nil {
		return err
	}
	batch := p.plans[position]
	if len(req.Stages) != 1 || req.Stages[0] != pb.JobStage_JOB_STAGE_INDEX || len(req.Sources) != 1 || req.Registry != nil || req.Checkpoint != nil || len(req.Observations) != 0 ||
		!proto.Equal(req.Sources[0], batch.Plan.DocumentBatch) || !proto.Equal(req.IndexBuildPlan, batch.Reference) || !proto.Equal(req.Manifest, batch.Plan.Producer) ||
		req.Context.CorpusId != batch.Plan.Meta.CorpusId || req.Context.AuthScopeRef != p.authScope || !proto.Equal(req.Context.SnapshotRef, batch.Plan.TargetSnapshot) ||
		!proto.Equal(req.Context.ConfigFingerprint, batch.Plan.Producer.ConfigHash) || req.Context.Deadline.AsTime().After(req.Lease.ExpiresAt.AsTime()) {
		return errors.New("worker request differs from admitted INDEX plan/scope")
	}
	return nil
}

// VerifiedIndexOutput exposes copies for the scheduler's fenced checkpoint
// transaction. Registration is deliberately separate from job completion and
// publication. No output is returned when any selected record fails admission.
type VerifiedIndexOutput struct {
	response *pb.ProcessBatchResponse
	batch    *pb.IndexBatch
}

func (o *VerifiedIndexOutput) Response() *pb.ProcessBatchResponse {
	if o == nil || o.response == nil {
		return nil
	}
	return proto.Clone(o.response).(*pb.ProcessBatchResponse)
}
func (o *VerifiedIndexOutput) Register(ctx context.Context, registry IndexPlanRegistry) error {
	if ctx == nil || o == nil || o.batch == nil || o.response == nil || registry == nil {
		return errors.New("verified INDEX output and registry required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	corpus := o.batch.Meta.CorpusId
	ref := proto.Clone(o.response.IndexBatch).(*pb.ArtifactRef)
	if err := registry.RegisterArtifact(ctx, corpus, ref); err != nil {
		return err
	}
	return registry.ReplaceArtifactDependencyManifest(ctx, corpus, ref.ArtifactId, proto.Clone(o.batch.Dependencies).(*pb.DependencyManifest))
}

// ExecuteBatch expects plans persisted and the scheduler's lease/plan association
// committed before entry. Caller owns cancellation polling, durable retry and
// fenced checkpoint commit. The normal gRPC worker client propagates cancellation.
func (p *InitialIndexPlans) ExecuteBatch(ctx context.Context, position int, request *pb.ProcessBatchRequest, worker IndexBatchWorker, authority IndexAuthority, reader IndexArtifactReader) (*VerifiedIndexOutput, error) {
	if ctx == nil || worker == nil || authority == nil || reader == nil {
		return nil, errors.New("INDEX worker, authority and reader required")
	}
	if err := p.checkWorkerRequest(position, request); err != nil {
		return nil, err
	}
	request = proto.Clone(request).(*pb.ProcessBatchRequest)
	bounded, cancel := context.WithDeadline(ctx, request.Context.Deadline.AsTime())
	defer cancel()
	if err := bounded.Err(); err != nil {
		return nil, err
	}
	planned := p.plans[position]
	loader := &initialArtifactLoader{authority: authority, reader: reader, corpus: p.binding.Generation.Meta.CorpusId, remaining: 64 << 20, cache: map[string]initialArtifact{}}
	plan, source := new(pb.IndexBuildPlan), new(pb.DocumentBatch)
	if err := loader.read(bounded, planned.Reference, plan); err != nil {
		return nil, err
	}
	if !proto.Equal(plan, planned.Plan) {
		return nil, errors.New("persisted INDEX plan drift")
	}
	if err := loader.read(bounded, plan.DocumentBatch, source); err != nil {
		return nil, err
	}
	if err := authority.VerifyIndexSourceCheckpoint(bounded, plan.Meta.CorpusId, planned.SourceJobID, plan.DocumentBatch); err != nil {
		return nil, err
	}
	_, dictionary, err := loadInitialLexical(bounded, loader, plan)
	if err != nil {
		return nil, err
	}
	response, err := worker.ProcessBatch(bounded, proto.Clone(request).(*pb.ProcessBatchRequest))
	if err != nil {
		return nil, err
	}
	if err = bounded.Err(); err != nil {
		return nil, err
	}
	if err = domain.VerifyWorkerResponse(request, response); err != nil {
		return nil, err
	}
	if response.Status != pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED || response.Checkpoint == nil || response.IndexBatch == nil || response.DocumentBatch != nil || response.GraphDelta != nil || response.ExtractionBatch != nil || response.ResolutionBatch != nil ||
		!proto.Equal(response.Checkpoint.Manifest, plan.Producer) {
		return nil, errors.New("INDEX requires one successful output and matching checkpoint producer")
	}
	ref := response.IndexBatch
	if ref.MediaType != domain.IndexBatchMediaType || ref.ByteSize == 0 || ref.ByteSize > 16<<20 || ref.ByteSize > loader.remaining {
		return nil, errors.New("INDEX worker output type or byte budget mismatch")
	}
	raw, err := reader.ReadVerified(bounded, ref, ref.ByteSize)
	if err != nil {
		return nil, err
	}
	if uint64(len(raw)) != ref.ByteSize || fmt.Sprintf("%x", sha256.Sum256(raw)) != ref.ContentHash.Sha256 {
		return nil, errors.New("INDEX worker output hash/size mismatch")
	}
	batch := new(pb.IndexBatch)
	limits := domain.DefaultWireLimits
	limits.MaxItems = 1_000_000
	if err = domain.DecodeWire(raw, batch, limits); err != nil {
		return nil, err
	}
	if !proto.Equal(batch.Context, request.Context) {
		return nil, errors.New("INDEX worker output request context mismatch")
	}
	if err = domain.ValidatePlannedIndexBatch(batch, plan, planned.Reference, source); err != nil {
		return nil, err
	}
	if err = verifyIndexSparseTerms(dictionary, batch.Records); err != nil {
		return nil, err
	}
	if err = bounded.Err(); err != nil {
		return nil, err
	}
	return &VerifiedIndexOutput{proto.Clone(response).(*pb.ProcessBatchResponse), batch}, nil
}
