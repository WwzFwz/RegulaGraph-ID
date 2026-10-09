// Exercises warm ASSEMBLE processing and checkpoint recovery with real Rust
// artifact bytes and synthetic metadata/authority ports. Cold inventory restore
// has PostgreSQL integration coverage; these tests do not assert a real registry
// decision or model quality. Verify pin cleanup, no repeated RPC on recovery,
// checkpoint corruption rejection and cancellable cache acquisition.
package workflows

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type graphProcessorStoreFixture struct {
	GraphJobProcessorStore
	publication string
	pin         domain.SnapshotPin
	cp          *pb.Checkpoint
	ref         *pb.ArtifactRef
	releases    int
}

func TestGraphArtifactFailureClassification(t *testing.T) {
	for _, err := range []error{domain.ErrNotFound, os.ErrNotExist, domain.ErrPersistentIntegrity} {
		if !errors.Is(graphArtifactReadError(err), domain.ErrPersistentIntegrity) {
			t.Fatal("immutable loss remained retryable", err)
		}
	}
	for _, err := range []error{context.DeadlineExceeded, context.Canceled, errors.New("backend unavailable")} {
		actual := graphArtifactReadError(err)
		if errors.Is(actual, domain.ErrPersistentIntegrity) || !errors.Is(actual, err) {
			t.Fatal("transient transport classified as corruption", actual)
		}
	}
}

func (f *graphProcessorStoreFixture) GraphJobPublication(context.Context, domain.JobRecord) (string, error) {
	return f.publication, nil
}
func (f *graphProcessorStoreFixture) PinActiveSnapshot(_ context.Context, corpus, lease, owner string, ttl time.Duration) (domain.SnapshotPin, error) {
	p := f.pin
	p.CorpusID, p.LeaseID, p.OwnerID = corpus, lease, owner
	p.ExpiresAt = time.Now().Add(ttl)
	return p, nil
}
func (f *graphProcessorStoreFixture) ReleaseSnapshotPin(context.Context, string, string) error {
	f.releases++
	return nil
}
func (f *graphProcessorStoreFixture) LoadLatestCheckpoint(context.Context, string) (*pb.Checkpoint, error) {
	return proto.Clone(f.cp).(*pb.Checkpoint), nil
}
func (f *graphProcessorStoreFixture) LoadArtifact(_ context.Context, _, id string) (*pb.ArtifactRef, error) {
	if id != f.ref.ArtifactId {
		return nil, errors.New("unknown artifact")
	}
	return proto.Clone(f.ref).(*pb.ArtifactRef), nil
}

type graphProcessorAuthorityFixture struct {
	graphAuthorityFixture
	commits int
	result  *pb.ProcessBatchResponse
}

func (f *graphProcessorAuthorityFixture) CommitGraphOutput(_ context.Context, _ domain.SnapshotPin, _ domain.JobRecord, req *pb.ProcessBatchRequest, res *pb.ProcessBatchResponse, deps *pb.DependencyManifest) error {
	if err := domain.ValidateGraphWorkerEnvelope(req, res, f.a.Plan); err != nil {
		return err
	}
	f.commits++
	f.result = proto.Clone(res).(*pb.ProcessBatchResponse)
	return nil
}
func (f *graphProcessorAuthorityFixture) GraphCheckpointCommitted(context.Context, *pb.Checkpoint) (bool, error) {
	return false, errors.New("unexpected reconciliation")
}

func checkGraphProcessorWithRustArtifacts(t *testing.T, artifacts graphArtifactFixture, a domain.GraphJobAssignment, originalJob domain.JobRecord, res *pb.ProcessBatchResponse, ontology *domain.Ontology) {
	t.Helper()
	for _, mode := range []string{"fresh", "recovery", "wrong checkpoint", "changed output", "waiting cancelled"} {
		t.Run("processor_"+mode, func(t *testing.T) {
			job := originalJob
			cp := proto.Clone(res.Checkpoint).(*pb.Checkpoint)
			cp.JobId = job.JobID
			cp.Fence = job.LeaseFence - 1
			if mode == "recovery" || mode == "wrong checkpoint" || mode == "changed output" {
				job.LatestCheckpointID = cp.Meta.RecordId
			}
			if mode == "wrong checkpoint" {
				cp.ArtifactHashes[0].Sha256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			}
			store := &graphProcessorStoreFixture{publication: a.Plan.PublicationId, cp: cp, ref: res.GraphDelta}
			port := &graphProcessorAuthorityFixture{graphAuthorityFixture: graphAuthorityFixture{a: a}}
			reader := graphArtifactFixture{}
			for id, raw := range artifacts {
				reader[id] = append([]byte(nil), raw...)
			}
			if mode == "changed output" {
				reader[res.GraphDelta.ArtifactId][0] ^= 1
			}
			worker := &graphRPCFixture{response: res}
			factory := func(context.Context, domain.GraphJobInventory, map[string]domain.GraphJobSourceInputs) (GraphExecutionAuthority, error) {
				t.Fatal("warm admission restored unexpectedly")
				return nil, nil
			}
			processor, err := NewGraphJobProcessor(store, reader, worker, factory, ontology, a.Plan.Context.AuthScopeRef)
			if err != nil {
				t.Fatal(err)
			}
			processor.authority, processor.publication, processor.corpus = port, a.Plan.PublicationId, job.CorpusID
			ctx := context.Background()
			if mode == "waiting cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 10*time.Millisecond)
				defer cancel()
				processor.gate <- struct{}{}
			}
			response, err := processor.ProcessGraphJob(ctx, job)
			if store.releases != 1 {
				t.Fatal("snapshot pin leaked", store.releases)
			}
			if mode == "fresh" || mode == "recovery" {
				if err != nil || response == nil || port.commits != 1 {
					t.Fatal("processor failed", err, port.commits)
				}
				if mode == "fresh" && worker.calls != 1 || mode == "recovery" && worker.calls != 0 {
					t.Fatal("incorrect worker replay", worker.calls)
				}
				if mode == "recovery" && (response.Checkpoint.Meta.RecordId == cp.Meta.RecordId || response.Checkpoint.Fence != job.LeaseFence || !proto.Equal(response.GraphDelta, res.GraphDelta)) {
					t.Fatal("recovery changed output or reused old fence")
				}
			} else if err == nil || response != nil || port.commits != 0 || worker.calls != 0 {
				t.Fatal("failed recovery/cache wait committed", err, port.commits, worker.calls)
			}
			if (mode == "wrong checkpoint" || mode == "changed output") && !errors.Is(err, domain.ErrPersistentIntegrity) {
				t.Fatal("corrupt recovery must fail permanently", err)
			}
		})
	}
}
