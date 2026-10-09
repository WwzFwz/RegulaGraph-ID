// Prepares the whole committed graph write set without calling Rust or a model.
// Storage authenticates checkpoint/source authority; this workflow repeats exact
// source, byte and graph projection validation before returning an owned result.
// A final authority read rejects changes during artifact I/O. That read is not a
// lock across subsequent backend writes: publisher must recheck under its commit
// locks. Input and output inventories are each capped at 64MiB, per-source at 16MiB.
// Measure read/hash/admission p95/RSS; benchmark-targets.yaml remains unmeasured.
package workflows

import (
	"context"
	"errors"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type CompletedGraphAuthority interface {
	ReadCompletedGraph(context.Context, domain.SnapshotPin) (domain.CompletedGraphInventory, error)
}

type PreparedGraphOutputs struct {
	completed domain.CompletedGraphInventory
	deltas    []*pb.GraphDelta
}

func (p *PreparedGraphOutputs) Deltas() []*pb.GraphDelta {
	if p == nil {
		return nil
	}
	result := make([]*pb.GraphDelta, len(p.deltas))
	for i, delta := range p.deltas {
		result[i] = proto.Clone(delta).(*pb.GraphDelta)
	}
	return result
}

func (p *PreparedGraphOutputs) Completed() domain.CompletedGraphInventory {
	if p == nil {
		return domain.CompletedGraphInventory{}
	}
	return cloneCompletedGraph(p.completed)
}

func PrepareCompletedGraph(ctx context.Context, authority CompletedGraphAuthority, reader DocumentArtifactReader,
	pin domain.SnapshotPin, ontology *domain.Ontology) (*PreparedGraphOutputs, error) {
	if ctx == nil || authority == nil || reader == nil || ontology == nil {
		return nil, errors.New("graph preparation dependencies required")
	}
	bounded, cancel := context.WithDeadline(ctx, pin.ExpiresAt)
	defer cancel()
	completed, err := authority.ReadCompletedGraph(bounded, pin)
	if err != nil {
		return nil, err
	}
	if err = domain.ValidateCompletedGraphInventory(completed); err != nil {
		return nil, err
	}
	completed = cloneCompletedGraph(completed)
	base := completed.Inventory.Assignments[0].Plan.Context.SnapshotRef
	if pin.CorpusID != base.CorpusId || pin.SnapshotID != base.SnapshotId || pin.Sequence != base.Sequence {
		return nil, domain.ErrPersistentIntegrity
	}
	result := &PreparedGraphOutputs{completed: completed}
	inputs := &graphInventoryReader{reader: reader, remaining: 64 << 20}
	outputRemaining := uint64(64 << 20)
	for i, a := range completed.Inventory.Assignments {
		sources, e := readGraphSourceProjection(bounded, inputs, a, ontology)
		if e != nil {
			return nil, e
		}
		raw, e := readGraphBytes(bounded, reader, completed.Outputs[i], &outputRemaining)
		if e != nil {
			return nil, e
		}
		delta := new(pb.GraphDelta)
		if e = domain.DecodeWire(raw, delta, domain.DefaultWireLimits); e != nil {
			return nil, errors.Join(domain.ErrPersistentIntegrity, e)
		}
		if e = domain.ValidatePlannedGraphDelta(delta, a.Plan, sources, ontology); e != nil {
			return nil, errors.Join(domain.ErrPersistentIntegrity, e)
		}
		result.deltas = append(result.deltas, delta)
	}
	if err = result.Revalidate(bounded, authority, pin); err != nil {
		return nil, err
	}
	return result, nil
}

// Revalidate confirms exact checkpoint/plan/output ownership at this instant.
// It grants no durable backend receipt or publication authority on its own.
func (p *PreparedGraphOutputs) Revalidate(ctx context.Context, authority CompletedGraphAuthority, pin domain.SnapshotPin) error {
	if ctx == nil || p == nil || len(p.deltas) == 0 || authority == nil {
		return errors.New("prepared graph and authority required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	again, err := authority.ReadCompletedGraph(ctx, pin)
	if err != nil {
		return err
	}
	if err = domain.ValidateCompletedGraphInventory(again); err != nil {
		return err
	}
	if len(again.Outputs) != len(p.completed.Outputs) {
		return domain.ErrPersistentIntegrity
	}
	for i, a := range again.Inventory.Assignments {
		old := p.completed.Inventory.Assignments[i]
		if a.JobID != old.JobID || a.SourceJobID != old.SourceJobID || !proto.Equal(a.Plan, old.Plan) || !proto.Equal(a.Reference, old.Reference) ||
			!proto.Equal(again.Outputs[i], p.completed.Outputs[i]) || !proto.Equal(again.Checkpoints[i], p.completed.Checkpoints[i]) {
			return domain.ErrPersistentIntegrity
		}
	}
	return ctx.Err()
}

func cloneCompletedGraph(in domain.CompletedGraphInventory) domain.CompletedGraphInventory {
	var out domain.CompletedGraphInventory
	for i, a := range in.Inventory.Assignments {
		out.Inventory.Assignments = append(out.Inventory.Assignments, domain.GraphJobAssignment{JobID: a.JobID, SourceJobID: a.SourceJobID, Plan: proto.Clone(a.Plan).(*pb.GraphAssemblyPlan), Reference: proto.Clone(a.Reference).(*pb.ArtifactRef)})
		out.Checkpoints = append(out.Checkpoints, proto.Clone(in.Checkpoints[i]).(*pb.Checkpoint))
		out.Outputs = append(out.Outputs, proto.Clone(in.Outputs[i]).(*pb.ArtifactRef))
	}
	return out
}

type graphInventoryReader struct {
	reader    DocumentArtifactReader
	remaining uint64
}

func (r *graphInventoryReader) ReadVerified(ctx context.Context, ref *pb.ArtifactRef, limit uint64) ([]byte, error) {
	if ref.ByteSize > r.remaining {
		return nil, errors.Join(domain.ErrPersistentIntegrity, errors.New("graph source inventory exceeds byte budget"))
	}
	r.remaining -= ref.ByteSize
	return r.reader.ReadVerified(ctx, ref, limit)
}
