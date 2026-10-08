// Exercises the INDEX RPC/admission boundary with authenticated fixture bytes
// and a controllable worker. Failures cover cross-request output, stale response,
// deadline/cancellation and source authority before inference. Durable scheduling
// and model quality are not simulated as passing acceptance checks.
package indexing

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type indexWorkerFunc func(context.Context, *pb.ProcessBatchRequest) (*pb.ProcessBatchResponse, error)

func (f indexWorkerFunc) ProcessBatch(ctx context.Context, req *pb.ProcessBatchRequest) (*pb.ProcessBatchResponse, error) {
	return f(ctx, req)
}

func dispatchFixture(t *testing.T) (*planningFixture, *InitialIndexPlans, *pb.ProcessBatchRequest, indexWorkerFunc) {
	t.Helper()
	f, p, outputs := plannedOutputFixture(t)
	call := proto.Clone(f.source.Context).(*pb.RequestContext)
	call.Deadline = timestamppb.New(time.Now().Add(time.Minute))
	call.ConfigFingerprint = proto.Clone(f.config.Producer.ConfigHash).(*pb.ContentHash)
	job := domain.JobRecord{JobID: "job:index:fixture", CorpusID: call.CorpusId, Stage: pb.JobStage_JOB_STAGE_INDEX, State: pb.JobState_JOB_STATE_RUNNING,
		Attempt: 1, LeaseOwner: "worker:go", LeaseFence: 7, LeaseExpiresAt: time.Now().Add(30 * time.Second)}
	req, err := p.WorkerRequest(0, job, call)
	if err != nil {
		t.Fatal(err)
	}
	if req.Context.Deadline.AsTime().After(job.LeaseExpiresAt) {
		t.Fatal("request exceeds lease")
	}
	worker := indexWorkerFunc(func(ctx context.Context, r *pb.ProcessBatchRequest) (*pb.ProcessBatchResponse, error) {
		if deadline, ok := ctx.Deadline(); !ok || deadline.After(r.Context.Deadline.AsTime()) {
			t.Fatal("RPC deadline missing")
		}
		batch := new(pb.IndexBatch)
		if err := proto.Unmarshal(f.artifacts[outputs[0].ArtifactId], batch); err != nil {
			t.Fatal(err)
		}
		batch.Context = proto.Clone(r.Context).(*pb.RequestContext)
		ref := f.put(t, batch, "artifact:worker:new")
		delete(f.authority.refs, ref.ArtifactId) // Worker bytes precede registry admission.
		return &pb.ProcessBatchResponse{RequestId: r.Context.RequestId, JobId: r.JobId, Attempt: r.Attempt, Fence: r.Lease.Fence, IndexBatch: ref, Status: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED,
			Checkpoint: &pb.Checkpoint{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: r.Context.CorpusId, RecordId: "checkpoint:index:fixture"}, JobId: r.JobId, Stage: pb.JobStage_JOB_STAGE_INDEX, Fence: r.Lease.Fence,
				Manifest: r.Manifest, CompletedBatchKeys: []string{ref.ArtifactId}, ArtifactHashes: []*pb.ContentHash{ref.ContentHash}, TerminalStatus: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED}}, nil
	})
	return f, p, req, worker
}

func TestInitialIndexDispatchAdmitsAndRegistersOutput(t *testing.T) {
	f, p, req, worker := dispatchFixture(t)
	verified, err := p.ExecuteBatch(context.Background(), 0, req, worker, f.authority, f.artifacts)
	if err != nil {
		t.Fatal(err)
	}
	registry := &planTestRegistry{refs: map[string]*pb.ArtifactRef{}, deps: map[string]*pb.DependencyManifest{}}
	for range 2 {
		if err = verified.Register(context.Background(), registry); err != nil {
			t.Fatal(err)
		}
	}
	response := verified.Response()
	if !proto.Equal(registry.refs[response.IndexBatch.ArtifactId], response.IndexBatch) {
		t.Fatal("registered output drift")
	}
	response.Checkpoint.Manifest.Build = "mutated"
	if verified.Response().Checkpoint.Manifest.Build == "mutated" {
		t.Fatal("mutable output escaped")
	}
	if len(registry.deps[response.IndexBatch.ArtifactId].Dependencies) != 1 {
		t.Fatal("plan dependency missing")
	}
}

func TestInitialIndexDispatchRejectsDriftBeforeWorker(t *testing.T) {
	for _, name := range []string{"plan", "scope", "snapshot", "manifest", "unregistered plan", "cancel", "deadline", "checkpoint"} {
		t.Run(name, func(t *testing.T) {
			f, p, req, _ := dispatchFixture(t)
			ctx := context.Background()
			switch name {
			case "plan":
				req.IndexBuildPlan = p.Batches()[1].Reference
			case "scope":
				req.Context.AuthScopeRef = "scope:other"
			case "snapshot":
				req.Context.SnapshotRef.Sequence++
			case "manifest":
				req.Manifest.Build = "other"
			case "unregistered plan":
				delete(f.authority.refs, req.IndexBuildPlan.ArtifactId)
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "deadline":
				req.Context.Deadline = timestamppb.New(time.Now().Add(-time.Second))
			case "checkpoint":
				delete(f.authority.sources, "job:source")
			}
			called := false
			worker := indexWorkerFunc(func(context.Context, *pb.ProcessBatchRequest) (*pb.ProcessBatchResponse, error) {
				called = true
				return nil, errors.New("unexpected worker")
			})
			if out, err := p.ExecuteBatch(ctx, 0, req, worker, f.authority, f.artifacts); err == nil || out != nil || called {
				t.Fatal("invalid input reached worker", err)
			}
		})
	}
}

func TestInitialIndexDispatchRejectsWorkerOutput(t *testing.T) {
	for _, name := range []string{"fence", "missing checkpoint", "producer", "hash", "output context", "foreign sparse term", "cancel during RPC"} {
		t.Run(name, func(t *testing.T) {
			f, p, req, valid := dispatchFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			worker := indexWorkerFunc(func(c context.Context, r *pb.ProcessBatchRequest) (*pb.ProcessBatchResponse, error) {
				response, err := valid(c, r)
				if err != nil {
					return nil, err
				}
				switch name {
				case "fence":
					response.Fence++
				case "missing checkpoint":
					response.Checkpoint = nil
				case "producer":
					response.Checkpoint.Manifest = proto.Clone(response.Checkpoint.Manifest).(*pb.ProducerManifest)
					response.Checkpoint.Manifest.Build = "foreign"
				case "hash":
					f.artifacts[response.IndexBatch.ArtifactId] = []byte("bad")
				case "output context", "foreign sparse term":
					batch := new(pb.IndexBatch)
					if err := proto.Unmarshal(f.artifacts[response.IndexBatch.ArtifactId], batch); err != nil {
						t.Fatal(err)
					}
					if name == "output context" {
						batch.Context.AuthScopeRef = "scope:other"
					} else {
						batch.Records[0].SparseVector = &pb.SparseVector{Indices: []uint32{999999}, Values: []float32{1}}
						batch.OperationsChecksum.Sha256, err = domain.IndexPlanOperationsChecksum(batch)
						if err != nil {
							t.Fatal(err)
						}
					}
					response.IndexBatch = f.put(t, batch, response.IndexBatch.ArtifactId)
					response.Checkpoint.ArtifactHashes[0] = response.IndexBatch.ContentHash
				case "cancel during RPC":
					cancel()
				}
				return response, nil
			})
			if out, err := p.ExecuteBatch(ctx, 0, req, worker, f.authority, f.artifacts); err == nil || out != nil {
				t.Fatal("invalid worker output admitted")
			}
		})
	}
}
