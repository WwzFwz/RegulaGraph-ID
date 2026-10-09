// Exercises the Go ASSEMBLE execution boundary with real exported Rust bytes and
// synthetic authority/RPC ports. Tests cover source corruption before RPC, response
// drift, late authority loss and cancellation; no mock approval counts as a real
// registry receipt or durable output commit. Real network wiring remains separate.
package workflows

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type graphAuthorityFixture struct {
	a      domain.GraphJobAssignment
	calls  int
	failAt int
}

func (f *graphAuthorityFixture) AuthorizeGraphDispatch(context.Context, domain.SnapshotPin, domain.JobRecord) (domain.GraphJobAssignment, error) {
	f.calls++
	if f.calls == f.failAt {
		return domain.GraphJobAssignment{}, errors.New("authority lost")
	}
	return f.a, nil
}

type graphArtifactFixture map[string][]byte

func (f graphArtifactFixture) ReadVerified(_ context.Context, r *pb.ArtifactRef, _ uint64) ([]byte, error) {
	v, ok := f[r.ArtifactId]
	if !ok {
		return nil, errors.New("missing fixture")
	}
	return append([]byte(nil), v...), nil
}

type graphRPCFixture struct {
	response *pb.ProcessBatchResponse
	calls    int
	mutate   func(*pb.ProcessBatchResponse)
	onCall   func(context.Context) error
}

func (f *graphRPCFixture) ProcessBatch(ctx context.Context, req *pb.ProcessBatchRequest) (*pb.ProcessBatchResponse, error) {
	f.calls++
	if f.onCall != nil {
		if err := f.onCall(ctx); err != nil {
			return nil, err
		}
	}
	r := proto.Clone(f.response).(*pb.ProcessBatchResponse)
	r.RequestId, r.JobId, r.Attempt, r.Fence = req.Context.RequestId, req.JobId, req.Attempt, req.Lease.Fence
	r.Checkpoint.JobId, r.Checkpoint.Fence = req.JobId, req.Lease.Fence
	if f.mutate != nil {
		f.mutate(r)
	}
	return r, nil
}

func TestExecuteGraphAssemblyWithRustArtifacts(t *testing.T) {
	dir := os.Getenv("REGULAGRAPH_GRAPH_FIXTURE_DIR")
	if dir == "" {
		t.Skip("requires exported Rust worker fixture")
	}
	read := func(name string, m proto.Message) []byte {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err = domain.DecodeWire(raw, m, domain.DefaultWireLimits); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	req, res, plan, doc := new(pb.ProcessBatchRequest), new(pb.ProcessBatchResponse), new(pb.GraphAssemblyPlan), new(pb.DocumentBatch)
	read("request.pb", req)
	read("response.pb", res)
	artifacts := graphArtifactFixture{}
	artifacts[req.GraphAssemblyPlan.ArtifactId] = read("plan.pb", plan)
	artifacts[req.Sources[0].ArtifactId] = read("document.pb", doc)
	for _, item := range []struct {
		name   string
		ref    *pb.ArtifactRef
		target proto.Message
	}{
		{"extraction.pb", req.Sources[1], new(pb.ExtractionBatch)}, {"resolution.pb", req.Sources[2], new(pb.ResolutionBatch)},
		{"registry.pb", req.Sources[3], new(pb.RegistryEntityView)}, {"delta.pb", res.GraphDelta, new(pb.GraphDelta)},
	} {
		artifacts[item.ref.ArtifactId] = read(item.name, item.target)
	}
	for i, text := range doc.TextArtifacts {
		raw, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("text-%d.bin", i)))
		if err != nil {
			t.Fatal(err)
		}
		artifacts[text.NormalizedTextRef.ArtifactId] = raw
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "configs", "ontology-v1.jsonc"))
	if err != nil {
		t.Fatal(err)
	}
	ontology, err := domain.ParseOntologyJSONC(raw)
	if err != nil {
		t.Fatal(err)
	}
	job := domain.JobRecord{JobID: req.JobId, CorpusID: req.Context.CorpusId, Stage: pb.JobStage_JOB_STAGE_ASSEMBLE, State: pb.JobState_JOB_STATE_RUNNING, Attempt: 2, LeaseOwner: "owner:fixture", LeaseFence: 3, LeaseExpiresAt: time.Now().Add(time.Minute)}
	pin := domain.SnapshotPin{ExpiresAt: time.Now().Add(30 * time.Second)}
	call := proto.Clone(req.Context).(*pb.RequestContext)
	call.Deadline = timestamppb.New(time.Now().Add(2 * time.Minute))
	a := domain.GraphJobAssignment{JobID: job.JobID, SourceJobID: "job:source", Plan: plan, Reference: req.GraphAssemblyPlan}
	for _, test := range []struct {
		name          string
		mutate        func(*pb.ProcessBatchResponse)
		failAuthority int
		corruptSource bool
		cancel        bool
		wantOK        bool
	}{
		{name: "valid", wantOK: true},
		{name: "stale response", mutate: func(r *pb.ProcessBatchResponse) { r.Fence++ }},
		{name: "wrong checkpoint producer", mutate: func(r *pb.ProcessBatchResponse) { r.Checkpoint.Manifest.Build = "wrong" }},
		{name: "checkpoint visibility", mutate: func(r *pb.ProcessBatchResponse) { r.Checkpoint.Meta.Visibility = &pb.Visibility{FromSeq: 2} }},
		{name: "unknown checkpoint", mutate: func(r *pb.ProcessBatchResponse) { r.Checkpoint.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 1}) }},
		{name: "unknown response", mutate: func(r *pb.ProcessBatchResponse) { r.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 1}) }},
		{name: "wrong output media", mutate: func(r *pb.ProcessBatchResponse) { r.GraphDelta.MediaType = "application/pdf" }},
		{name: "missing checkpoint", mutate: func(r *pb.ProcessBatchResponse) { r.Checkpoint = nil }},
		{name: "missing delta", mutate: func(r *pb.ProcessBatchResponse) { r.GraphDelta = nil }},
		{name: "preflight rejected", failAuthority: 1},
		{name: "late authority loss", failAuthority: 2},
		{name: "source corruption", corruptSource: true},
		{name: "cancel during RPC", cancel: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			authority := &graphAuthorityFixture{a: a, failAt: test.failAuthority}
			worker := &graphRPCFixture{response: res, mutate: test.mutate}
			worker.onCall = func(ctx context.Context) error {
				deadline, ok := ctx.Deadline()
				if !ok || deadline.After(pin.ExpiresAt) || deadline.After(job.LeaseExpiresAt) {
					return errors.New("RPC deadline escapes pin/claim")
				}
				return nil
			}
			if test.cancel {
				worker.onCall = func(ctx context.Context) error { cancel(); return nil }
			}
			reader := graphArtifactFixture{}
			for id, bytes := range artifacts {
				reader[id] = append([]byte(nil), bytes...)
			}
			if test.corruptSource {
				reader[req.Sources[0].ArtifactId][0] ^= 1
			}
			out, err := ExecuteGraphAssembly(ctx, authority, reader, worker, pin, job, call, ontology)
			if test.wantOK {
				if err != nil || out == nil || authority.calls != 2 || worker.calls != 1 {
					t.Fatal("valid worker dispatch rejected", err)
				}
				if out.Response().GraphDelta.ArtifactId == out.Delta().Meta.RecordId {
					t.Fatal("fixture must cover physical vs logical graph IDs")
				}
				copy := out.Delta()
				copy.Assertions[0].PredicateId = "mutated"
				if out.Delta().Assertions[0].PredicateId == "mutated" {
					t.Fatal("verified output is mutable")
				}
			} else if err == nil || out != nil {
				t.Fatal("failed execution exposed verified output")
			}
			if (test.failAuthority == 1 || test.corruptSource) && worker.calls != 0 {
				t.Fatal("RPC ran before source admission")
			}
		})
	}
}
