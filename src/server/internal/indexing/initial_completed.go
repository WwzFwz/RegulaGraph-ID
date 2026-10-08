// Restores a durable inventory and authenticates all committed child outputs
// before handing a complete write set to the existing initial index writer.
// Storage STAGED status alone cannot substitute for source/plan/vector checks.
// No publication or Qdrant mutation occurs here; every backend receipt remains
// mandatory. Record collection/admission time and RSS under benchmark-targets.yaml.
package indexing

import (
	"context"
	"errors"

	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type CompletedIndexStore interface {
	IndexAuthority
	LoadIndexJobInventory(context.Context, string) (domain.IndexJobInventory, error)
	LoadIndexJobOutputs(context.Context, string) ([]*pb.ArtifactRef, error)
}

func PrepareCompletedInitialIndex(ctx context.Context, store CompletedIndexStore, reader IndexArtifactReader, publication string) (*PreparedInitialIndex, error) {
	if ctx == nil || store == nil || reader == nil {
		return nil, errors.New("completed INDEX dependencies required")
	}
	inventory, err := store.LoadIndexJobInventory(ctx, publication)
	if err != nil {
		return nil, err
	}
	outputs, err := store.LoadIndexJobOutputs(ctx, publication)
	if err != nil {
		return nil, err
	}
	plans, err := RestoreInitialIndexPlans(ctx, store, reader, inventory)
	if err != nil {
		return nil, err
	}
	return plans.PrepareOutputs(ctx, store, reader, outputs)
}
