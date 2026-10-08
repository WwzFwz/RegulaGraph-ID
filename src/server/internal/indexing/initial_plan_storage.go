// Persists admitted INDEX plans and their transitive artifact dependencies before
// worker dispatch, then ties returned batches to the exact complete planned set.
// Writes are immutable/idempotent; interruption may leave registered plans but
// never a published snapshot. Job leases/checkpoint commits remain scheduler-owned.
// Uses Rust-compatible SHA-256 storage keys and existing C01 schema only. Measure
// registration/I/O p95 and preparation RSS under configs/benchmark-targets.yaml;
// this boundary does not establish production quality or performance acceptance.
package indexing

import (
	"bytes"
	"context"
	"errors"
	"io"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

type IndexPlanArtifactWriter interface {
	Put(context.Context, *pb.ArtifactRef, io.Reader) (bool, error)
}
type IndexPlanRegistry interface {
	RegisterArtifact(context.Context, string, *pb.ArtifactRef) error
	ReplaceArtifactDependencyManifest(context.Context, string, string, *pb.DependencyManifest) error
}

// Persist must succeed for the entire set before the caller schedules work.
// Retry with the same admitted set safely repairs interrupted registrations.
func (p *InitialIndexPlans) Persist(ctx context.Context, writer IndexPlanArtifactWriter, registry IndexPlanRegistry) error {
	if ctx == nil || p == nil || len(p.plans) == 0 || writer == nil || registry == nil {
		return errors.New("admitted plans and persistence dependencies required")
	}
	for _, batch := range p.plans {
		if err := ctx.Err(); err != nil {
			return err
		}
		ref, raw, err := initialPlanArtifact(batch.Plan)
		if err != nil {
			return err
		}
		if !proto.Equal(ref, batch.Reference) {
			return errors.New("private plan identity changed")
		}
		if _, err = writer.Put(ctx, ref, bytes.NewReader(raw)); err != nil {
			return err
		}
		if err = registry.RegisterArtifact(ctx, p.binding.Generation.Meta.CorpusId, ref); err != nil {
			return err
		}
		dep := &pb.DependencyManifest{ArtifactId: ref.ArtifactId, ProducerManifest: proto.Clone(batch.Plan.Producer).(*pb.ProducerManifest)}
		refs := append([]*pb.ArtifactRef{batch.Plan.DocumentBatch, batch.Plan.Generation.LexicalAnalyzer, batch.Plan.Generation.LexicalStatistics}, batch.Plan.DictionaryChain...)
		seen := map[string]bool{}
		for _, r := range refs {
			if seen[r.ArtifactId] {
				continue
			}
			seen[r.ArtifactId] = true
			dep.Dependencies = append(dep.Dependencies, &pb.Dependency{DependencyId: r.ArtifactId, Fingerprint: proto.Clone(r.ContentHash).(*pb.ContentHash)})
		}
		if err = registry.ReplaceArtifactDependencyManifest(ctx, p.binding.Generation.Meta.CorpusId, ref.ArtifactId, dep); err != nil {
			return err
		}
	}
	return nil
}

// PrepareOutputs rejects omissions, duplicate plans and plan substitution even
// when a replacement batch could otherwise pass local source validation. Output
// references must already be registered by a verified worker/checkpoint handoff.
// The result still needs every required backend receipt before publication.
func (p *InitialIndexPlans) PrepareOutputs(ctx context.Context, authority IndexAuthority, reader IndexArtifactReader, outputs []*pb.ArtifactRef) (*PreparedInitialIndex, error) {
	if ctx == nil || p == nil || len(p.plans) == 0 || authority == nil || reader == nil || len(outputs) != len(p.plans) {
		return nil, errors.New("complete planned output set required")
	}
	plans := map[string]PlannedInitialBatch{}
	for _, batch := range p.plans {
		plans[batch.Reference.ArtifactId] = batch
	}
	loader := &initialArtifactLoader{authority: authority, reader: reader, corpus: p.binding.Generation.Meta.CorpusId, remaining: 64 << 20, cache: map[string]initialArtifact{}}
	inputs := make([]InitialIndexInput, 0, len(outputs))
	seen := map[string]bool{}
	for _, ref := range outputs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		batch := new(pb.IndexBatch)
		if err := loader.read(ctx, ref, batch); err != nil {
			return nil, err
		}
		planned, ok := plans[batch.BuildPlan.GetArtifactId()]
		if !ok || seen[batch.BuildPlan.GetArtifactId()] || !proto.Equal(batch.BuildPlan, planned.Reference) {
			return nil, errors.New("unplanned, duplicate or substituted INDEX output")
		}
		seen[batch.BuildPlan.ArtifactId] = true
		inputs = append(inputs, InitialIndexInput{SourceJobID: planned.SourceJobID, BatchRef: proto.Clone(ref).(*pb.ArtifactRef)})
	}
	// Reuse verified output bytes and the same aggregate budget for source/plan
	// admission, avoiding a second backend read for every embedding batch.
	return prepareInitialIndex(ctx, authority, reader, p.binding, inputs, loader)
}
