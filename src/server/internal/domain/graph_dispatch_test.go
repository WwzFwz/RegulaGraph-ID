// Verifies scheduler-to-worker ASSEMBLE bindings, deadline clipping and owned
// references. Synthetic claims test pure request semantics; PostgreSQL integration
// separately proves live authority. Neither proves GraphDelta correctness.
package domain

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func TestGraphAssemblyWorkerRequest(t *testing.T) {
	a := graphJobFixture(t).Assignments[0]
	job := JobRecord{JobID: a.JobID, CorpusID: a.Plan.Meta.CorpusId, State: pb.JobState_JOB_STATE_RUNNING,
		Stage: pb.JobStage_JOB_STAGE_ASSEMBLE, Attempt: 2, LeaseOwner: "owner:worker", LeaseFence: 3, LeaseExpiresAt: time.Now().Add(time.Minute)}
	call := proto.Clone(a.Plan.Context).(*pb.RequestContext)
	call.RequestId, call.TraceId = "request:dispatch", "trace:dispatch"
	call.Deadline = timestamppb.New(time.Now().Add(2 * time.Minute))
	request, err := BuildGraphAssemblyRequest(a, job, call)
	if err != nil {
		t.Fatal(err)
	}
	if request.JobId != job.JobID || request.Attempt != job.Attempt || request.Lease.Fence != job.LeaseFence ||
		request.Context.RequestId != call.RequestId || !request.Context.Deadline.AsTime().Equal(job.LeaseExpiresAt) ||
		request.IndexBuildPlan != nil || request.Registry != nil || request.Checkpoint != nil || len(request.Observations) != 0 ||
		len(request.Stages) != 1 || request.Stages[0] != pb.JobStage_JOB_STAGE_ASSEMBLE || len(request.Sources) != 4 ||
		!proto.Equal(request.GraphAssemblyPlan, a.Reference) || !proto.Equal(request.Manifest, a.Plan.ProducerManifest) {
		t.Fatal("worker request lost assignment/lease/stage binding")
	}
	for i, ref := range []*pb.ArtifactRef{a.Plan.DocumentBatch, a.Plan.ExtractionBatch, a.Plan.ResolutionBatch, a.Plan.RegistryView} {
		if !proto.Equal(request.Sources[i], ref) {
			t.Fatal("worker artifact role order changed", i)
		}
	}
	request.Sources[0].ArtifactId = "mutated"
	request.Manifest.Build = "mutated"
	request.GraphAssemblyPlan.StorageKey = "mutated"
	request.Context.SnapshotRef.SnapshotId = "mutated"
	if a.Plan.DocumentBatch.ArtifactId == "mutated" || a.Plan.ProducerManifest.Build == "mutated" || a.Reference.StorageKey == "mutated" || call.SnapshotRef.SnapshotId == "mutated" {
		t.Fatal("request exposes mutable caller plan/context")
	}
	for name, change := range map[string]func(*JobRecord, *pb.RequestContext){
		"foreign job":    func(j *JobRecord, _ *pb.RequestContext) { j.JobID = "job:foreign" },
		"foreign corpus": func(j *JobRecord, _ *pb.RequestContext) { j.CorpusID = "corpus:foreign" },
		"queued":         func(j *JobRecord, _ *pb.RequestContext) { j.State = pb.JobState_JOB_STATE_QUEUED },
		"wrong stage":    func(j *JobRecord, _ *pb.RequestContext) { j.Stage = pb.JobStage_JOB_STAGE_INDEX },
		"cancelled":      func(j *JobRecord, _ *pb.RequestContext) { j.CancellationRequested = true },
		"expired lease":  func(j *JobRecord, _ *pb.RequestContext) { j.LeaseExpiresAt = time.Now().Add(-time.Second) },
		"zero attempt":   func(j *JobRecord, _ *pb.RequestContext) { j.Attempt = 0 },
		"zero fence":     func(j *JobRecord, _ *pb.RequestContext) { j.LeaseFence = 0 },
		"overflow fence": func(j *JobRecord, _ *pb.RequestContext) { j.LeaseFence = ^uint64(0) },
		"missing owner":  func(j *JobRecord, _ *pb.RequestContext) { j.LeaseOwner = "" },
		"scope drift":    func(_ *JobRecord, c *pb.RequestContext) { c.AuthScopeRef = "scope:foreign" },
		"snapshot drift": func(_ *JobRecord, c *pb.RequestContext) { c.SnapshotRef.Sequence++ },
		"config drift": func(_ *JobRecord, c *pb.RequestContext) {
			c.ConfigFingerprint.Sha256 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		},
		"expired request": func(_ *JobRecord, c *pb.RequestContext) { c.Deadline = timestamppb.New(time.Now().Add(-time.Second)) },
		"unknown context": func(_ *JobRecord, c *pb.RequestContext) { c.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01}) },
	} {
		t.Run(name, func(t *testing.T) {
			j, c := job, proto.Clone(call).(*pb.RequestContext)
			change(&j, c)
			if _, err := BuildGraphAssemblyRequest(a, j, c); err == nil {
				t.Fatal("invalid dispatch context accepted")
			}
		})
	}
	call.Deadline = timestamppb.New(time.Now().Add(10 * time.Second))
	request, err = BuildGraphAssemblyRequest(a, job, call)
	if err != nil || !proto.Equal(request.Context.Deadline, call.Deadline) {
		t.Fatal("earlier caller deadline extended", err)
	}
}
