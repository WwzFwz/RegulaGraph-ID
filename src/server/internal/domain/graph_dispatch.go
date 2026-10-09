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

// ValidateGraphWorkerEnvelope authenticates response correlation/roles before
// reading output bytes. Physical output locator IDs may differ from the logical
// delta ID; the latter is bound to the plan by ValidatePlannedGraphDelta.
func ValidateGraphWorkerEnvelope(request *pb.ProcessBatchRequest, response *pb.ProcessBatchResponse, plan *pb.GraphAssemblyPlan) error {
	if err := ValidateGraphAssemblyPlan(plan); err != nil {
		return err
	}
	if err := VerifyWorkerResponse(request, response); err != nil {
		return err
	}
	if !graphAssemblyKnownFields(response.ProtoReflect()) || len(request.Stages) != 1 || request.Stages[0] != pb.JobStage_JOB_STAGE_ASSEMBLE ||
		response.Status != pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED || response.GraphDelta == nil || response.Checkpoint == nil ||
		response.GraphDelta.SchemaVersion != 1 || response.GraphDelta.MediaType != GraphDeltaMediaType || response.GraphDelta.ByteSize == 0 || response.GraphDelta.ByteSize > uint64(DefaultWireLimits.MaxBytes) ||
		response.Checkpoint.Meta.SchemaVersion != 1 || response.Checkpoint.Meta.Visibility != nil || !proto.Equal(response.Checkpoint.Manifest, plan.ProducerManifest) {
		return errors.New("ASSEMBLE requires one successful known-schema delta and bound checkpoint")
	}
	for _, ref := range request.Sources {
		if ref.ArtifactId == response.GraphDelta.ArtifactId {
			return errors.New("graph output aliases source role")
		}
	}
	if request.GraphAssemblyPlan == nil || request.GraphAssemblyPlan.ArtifactId == response.GraphDelta.ArtifactId {
		return errors.New("graph output aliases plan")
	}
	return nil
}
