// Exposes the already admitted complete plan set to a durable scheduler. Child
// identities are stable across replay and each owns exactly one worker plan.
// The storage implementation must commit inventory and all jobs atomically;
// this conversion cannot authorize source membership or publish any snapshot.
// Track scheduling/recovery overhead under configs/benchmark-targets.yaml.
package indexing

import (
	"context"
	"errors"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func (p *InitialIndexPlans) JobInventory() (domain.IndexJobInventory, error) {
	if p == nil || len(p.plans) == 0 {
		return domain.IndexJobInventory{}, errors.New("admitted initial plans required")
	}
	result := domain.IndexJobInventory{Binding: p.binding, Snapshot: proto.Clone(p.plans[0].Plan.TargetSnapshot).(*pb.SnapshotRef), AuthScope: p.authScope}
	result.Binding.Generation = proto.Clone(p.binding.Generation).(*pb.IndexGeneration)
	for _, batch := range p.Batches() {
		result.Assignments = append(result.Assignments, domain.IndexJobAssignment{JobID: initialPlanID("index-job-v1", p.binding.PublicationID, batch.Reference.ArtifactId), SourceJobID: batch.SourceJobID, Plan: batch.Plan, Reference: batch.Reference})
	}
	return result, domain.ValidateIndexJobInventory(result)
}

// RestoreInitialIndexPlans re-runs source, dictionary and population admission.
// A stored inventory is routing state, never a substitute for authenticated
// source membership. Reconstructed plans must match every persisted byte binding.
func RestoreInitialIndexPlans(ctx context.Context, authority IndexAuthority, reader IndexArtifactReader, in domain.IndexJobInventory) (*InitialIndexPlans, error) {
	if err := domain.ValidateIndexJobInventory(in); err != nil {
		return nil, err
	}
	first := in.Assignments[0].Plan
	cfg := InitialIndexPlanConfig{Binding: in.Binding, Snapshot: in.Snapshot, AuthScope: in.AuthScope, Producer: first.Producer, DictionaryChain: first.DictionaryChain}
	seen := map[string]bool{}
	var sources []InitialIndexSource
	for _, a := range in.Assignments {
		if len(a.Plan.Items) > cfg.ChunksPerBatch {
			cfg.ChunksPerBatch = len(a.Plan.Items)
		}
		if !seen[a.SourceJobID] {
			sources = append(sources, InitialIndexSource{SourceJobID: a.SourceJobID, DocumentBatch: a.Plan.DocumentBatch})
			seen[a.SourceJobID] = true
		}
	}
	result, err := PlanInitialIndex(ctx, authority, reader, cfg, sources)
	if err != nil {
		return nil, err
	}
	rebuilt, err := result.JobInventory()
	if err != nil {
		return nil, err
	}
	if len(rebuilt.Assignments) != len(in.Assignments) {
		return nil, errors.New("restored INDEX inventory count changed")
	}
	for i, a := range rebuilt.Assignments {
		old := in.Assignments[i]
		if a.JobID != old.JobID || a.SourceJobID != old.SourceJobID || !proto.Equal(a.Plan, old.Plan) || !proto.Equal(a.Reference, old.Reference) {
			return nil, errors.New("restored INDEX inventory differs from admitted sources")
		}
	}
	return result, nil
}
