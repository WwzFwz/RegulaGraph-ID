// Exercises publication preparation with actual exported Rust bytes and a fake
// storage authority. Hash-valid but semantically wrong output, missing coverage,
// late authority loss and mutation must not expose a prepared graph. Real storage
// locking is covered by PostgreSQL integration; these fixtures are not model gold.
package workflows

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type completedGraphFixture struct {
	value            domain.CompletedGraphInventory
	calls, failAt    int
	changeCheckpoint bool
}

func (f *completedGraphFixture) ReadCompletedGraph(ctx context.Context, _ domain.SnapshotPin) (domain.CompletedGraphInventory, error) {
	f.calls++
	if err := ctx.Err(); err != nil {
		return domain.CompletedGraphInventory{}, err
	}
	if f.calls == f.failAt {
		return domain.CompletedGraphInventory{}, errors.New("lost graph authority")
	}
	result := cloneCompletedGraph(f.value)
	if f.changeCheckpoint && f.calls > 1 {
		result.Checkpoints[0].Meta.RecordId += ":new"
	}
	return result, nil
}

func checkCompletedGraphWithRustArtifacts(t *testing.T, artifacts graphArtifactFixture, a domain.GraphJobAssignment,
	response *pb.ProcessBatchResponse, ontology *domain.Ontology) {
	t.Helper()
	base := a.Plan.Context.SnapshotRef
	pin := domain.SnapshotPin{CorpusID: base.CorpusId, SnapshotID: base.SnapshotId, Sequence: base.Sequence, ExpiresAt: time.Now().Add(time.Minute)}
	fixture := domain.CompletedGraphInventory{Inventory: domain.GraphJobInventory{Assignments: []domain.GraphJobAssignment{a}}, Checkpoints: []*pb.Checkpoint{response.Checkpoint}, Outputs: []*pb.ArtifactRef{response.GraphDelta}}
	if err := domain.ValidateCompletedGraphInventory(fixture); err != nil {
		t.Fatal("invalid completed Rust fixture", err)
	}
	for _, c := range []struct {
		name   string
		mutate func(*completedGraphFixture, graphArtifactFixture)
		pass   bool
	}{
		{"valid completed", nil, true},
		{"late authority loss", func(f *completedGraphFixture, _ graphArtifactFixture) { f.failAt = 2 }, false},
		{"checkpoint replacement", func(f *completedGraphFixture, _ graphArtifactFixture) { f.changeCheckpoint = true }, false},
		{"unknown checkpoint", func(f *completedGraphFixture, _ graphArtifactFixture) {
			f.value.Checkpoints[0].ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 1})
		}, false},
		{"wrong producer", func(f *completedGraphFixture, _ graphArtifactFixture) {
			f.value.Checkpoints[0].Manifest.Build += "-bad"
		}, false},
		{"wrong source bytes", func(_ *completedGraphFixture, r graphArtifactFixture) { r[a.Plan.DocumentBatch.ArtifactId][0] ^= 1 }, false},
		{"hash-valid wrong graph", func(f *completedGraphFixture, r graphArtifactFixture) {
			delta := new(pb.GraphDelta)
			if err := proto.Unmarshal(r[response.GraphDelta.ArtifactId], delta); err != nil {
				t.Fatal(err)
			}
			delta.Assertions[0].SubjectId = delta.Assertions[0].ObjectId
			raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(delta)
			if err != nil {
				t.Fatal(err)
			}
			ref := f.value.Outputs[0]
			ref.ContentHash = &pb.ContentHash{Sha256: fmt.Sprintf("%x", sha256.Sum256(raw))}
			ref.ByteSize = uint64(len(raw))
			f.value.Checkpoints[0].ArtifactHashes[0] = proto.Clone(ref.ContentHash).(*pb.ContentHash)
			r[ref.ArtifactId] = raw
		}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := &completedGraphFixture{value: cloneCompletedGraph(fixture)}
			reader := graphArtifactFixture{}
			for key, raw := range artifacts {
				reader[key] = append([]byte(nil), raw...)
			}
			if c.mutate != nil {
				c.mutate(f, reader)
			}
			prepared, err := PrepareCompletedGraph(context.Background(), f, reader, pin, ontology)
			if !c.pass {
				if err == nil || prepared != nil {
					t.Fatal("invalid completed graph exposed prepared results")
				}
				return
			}
			if err != nil || prepared == nil || f.calls != 2 || len(prepared.Deltas()) != 1 {
				t.Fatal("complete graph rejected", err)
			}
			owned := prepared.Completed()
			owned.Inventory.Assignments[0].Plan.OutputArtifactId = "delta:changed"
			owned.Outputs[0].ArtifactId = "artifact:changed"
			prepared.Deltas()[0].Assertions[0].PredicateId = "changed"
			if prepared.Completed().Outputs[0].ArtifactId != response.GraphDelta.ArtifactId || prepared.Deltas()[0].Assertions[0].PredicateId == "changed" {
				t.Fatal("prepared output aliases caller data")
			}
			if err = prepared.Revalidate(context.Background(), f, pin); err != nil {
				t.Fatal(err)
			}
		})
	}
	// Coverage failures are rejected before any artifact I/O.
	for _, mutate := range []func(*domain.CompletedGraphInventory){
		func(in *domain.CompletedGraphInventory) { in.Outputs = nil },
		func(in *domain.CompletedGraphInventory) { in.Checkpoints = nil },
		func(in *domain.CompletedGraphInventory) { in.Outputs[0].ByteSize = 17 << 20 },
		func(in *domain.CompletedGraphInventory) { in.Checkpoints[0].JobId = "job:other" },
		func(in *domain.CompletedGraphInventory) {
			in.Checkpoints[0].Meta.Visibility = &pb.Visibility{FromSeq: 2}
		},
	} {
		in := cloneCompletedGraph(fixture)
		mutate(&in)
		if err := domain.ValidateCompletedGraphInventory(in); err == nil {
			t.Fatal("invalid completed inventory admitted")
		}
	}
	// Distinct assignments may share canonical entities later, but they must not
	// share an owned source, checkpoint or physical output artifact.
	var many domain.CompletedGraphInventory
	for i := 0; i < 5; i++ {
		part := cloneCompletedGraph(fixture)
		suffix := fmt.Sprintf(":copy%d", i)
		a := part.Inventory.Assignments[0]
		a.JobID += suffix
		a.SourceJobID += suffix
		a.Plan.Meta.RecordId += suffix
		a.Plan.OutputArtifactId += suffix
		for _, ref := range []*pb.ArtifactRef{a.Plan.DocumentBatch, a.Plan.ExtractionBatch, a.Plan.ResolutionBatch, a.Plan.RegistryView} {
			ref.ArtifactId += suffix
		}
		raw, e := proto.MarshalOptions{Deterministic: true}.Marshal(a.Plan)
		if e != nil {
			t.Fatal(e)
		}
		a.Reference.ArtifactId = a.Plan.Meta.RecordId
		a.Reference.ByteSize = uint64(len(raw))
		a.Reference.ContentHash = &pb.ContentHash{Sha256: fmt.Sprintf("%x", sha256.Sum256(raw))}
		cp, ref := part.Checkpoints[0], part.Outputs[0]
		cp.JobId = a.JobID
		cp.Meta.RecordId += suffix
		ref.ArtifactId += suffix
		cp.CompletedBatchKeys[0] = ref.ArtifactId
		many.Inventory.Assignments = append(many.Inventory.Assignments, a)
		many.Checkpoints = append(many.Checkpoints, cp)
		many.Outputs = append(many.Outputs, ref)
	}
	if err := domain.ValidateCompletedGraphInventory(many); err != nil {
		t.Fatal("valid multi-source metadata rejected", err)
	}
	duplicate := cloneCompletedGraph(many)
	duplicate.Outputs[1] = proto.Clone(duplicate.Outputs[0]).(*pb.ArtifactRef)
	duplicate.Checkpoints[1].CompletedBatchKeys[0] = duplicate.Outputs[1].ArtifactId
	duplicate.Checkpoints[1].ArtifactHashes[0] = duplicate.Outputs[1].ContentHash
	if err := domain.ValidateCompletedGraphInventory(duplicate); err == nil {
		t.Fatal("shared physical output accepted")
	}
	for _, ref := range many.Outputs {
		ref.ByteSize = 16 << 20
	}
	if err := domain.ValidateCompletedGraphInventory(many); err == nil {
		t.Fatal("aggregate graph output budget ignored")
	}
}
