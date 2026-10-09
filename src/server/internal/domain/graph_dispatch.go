// Builds one ASSEMBLE worker request from a storage-authorized assignment and
// scheduler claim. Pure binding checks preserve the ordered four artifact roles,
// exact plan/snapshot/config and immutable producer; only request correlation and
// deadline may change between attempts. This is not database admission or output
// verification. Bound RPC time by the lease; measure queue/RPC p95/p99 with the
// required benchmark suite before claiming production latency.
package domain

import (
	"errors"
	"math"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func BuildGraphAssemblyRequest(a GraphJobAssignment, job JobRecord, call *pb.RequestContext) (*pb.ProcessBatchRequest, error) {
	if err := ValidateGraphJobInventory(GraphJobInventory{Assignments: []GraphJobAssignment{a}}); err != nil {
		return nil, err
	}
	if err := ValidateWire(call, DefaultWireLimits); err != nil {
		return nil, err
	}
	p := a.Plan
	if !graphAssemblyKnownFields(call.ProtoReflect()) || call.SchemaVersion != 1 ||
		job.JobID != a.JobID || job.CorpusID != p.Meta.CorpusId || job.State != pb.JobState_JOB_STATE_RUNNING ||
		job.Stage != pb.JobStage_JOB_STAGE_ASSEMBLE || job.CancellationRequested || job.Attempt == 0 ||
		job.LeaseFence == 0 || job.LeaseFence > math.MaxInt64 || !validDocumentID(job.LeaseOwner) ||
		!job.LeaseExpiresAt.After(time.Now()) || !call.Deadline.AsTime().After(time.Now()) ||
		call.CorpusId != p.Meta.CorpusId || call.AuthScopeRef != p.Context.AuthScopeRef ||
		!proto.Equal(call.SnapshotRef, p.Context.SnapshotRef) || !proto.Equal(call.ConfigFingerprint, p.Context.ConfigFingerprint) {
		return nil, errors.New("ASSEMBLE request requires matching plan context and live claim")
	}
	owned := proto.Clone(call).(*pb.RequestContext)
	if job.LeaseExpiresAt.Before(owned.Deadline.AsTime()) {
		owned.Deadline = timestamppb.New(job.LeaseExpiresAt)
	}
	request := &pb.ProcessBatchRequest{
		Context: owned, JobId: job.JobID, Attempt: job.Attempt,
		Lease:  &pb.Lease{OwnerId: job.LeaseOwner, Fence: job.LeaseFence, ExpiresAt: timestamppb.New(job.LeaseExpiresAt)},
		Stages: []pb.JobStage{pb.JobStage_JOB_STAGE_ASSEMBLE}, Manifest: proto.Clone(p.ProducerManifest).(*pb.ProducerManifest),
		GraphAssemblyPlan: proto.Clone(a.Reference).(*pb.ArtifactRef),
	}
	for _, ref := range []*pb.ArtifactRef{p.DocumentBatch, p.ExtractionBatch, p.ResolutionBatch, p.RegistryView} {
		request.Sources = append(request.Sources, proto.Clone(ref).(*pb.ArtifactRef))
	}
	if err := ValidateWire(request, DefaultWireLimits); err != nil {
		return nil, err
	}
	return request, nil
}
