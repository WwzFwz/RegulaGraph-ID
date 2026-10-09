// Wires ASSEMBLE execution explicitly into the existing coordinator. PostgreSQL
// owns admission and commit; workflow restores plans/source evidence and invokes
// the reusable Rust client. Migration0020 and scheduled graph inventories are
// prerequisites. Enabling this stage creates no plans, decisions or publications.
// Measure restore/queue/attempt latency and RSS under benchmark-targets.yaml.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"

	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/workflows"
)

func newGraphExecutor(cfg runtimeConfig, repo *postgres.Repository, reader workflows.DocumentArtifactReader, worker workflows.GraphBatchWorker, ontology *domain.Ontology) (*workflows.GraphExecutor, error) {
	raw := os.Getenv("REGULAGRAPH_ASSEMBLE_ENABLED")
	if raw == "" {
		return nil, nil
	}
	enabled, err := strconv.ParseBool(raw)
	if err != nil {
		return nil, fmt.Errorf("REGULAGRAPH_ASSEMBLE_ENABLED must be boolean")
	}
	if !enabled {
		return nil, nil
	}
	if repo == nil {
		return nil, errors.New("ASSEMBLE repository required")
	}
	factory := func(ctx context.Context, in domain.GraphJobInventory, inputs map[string]domain.GraphJobSourceInputs) (workflows.GraphExecutionAuthority, error) {
		return repo.PrepareGraphJobAdmission(ctx, in, inputs, domain.DefaultWireLimits.MaxItems, domain.DefaultWireLimits.MaxItems)
	}
	processor, err := workflows.NewGraphJobProcessor(repo, reader, worker, factory, ontology, cfg.authScope)
	if err != nil {
		return nil, err
	}
	return workflows.NewGraphExecutor(repo, processor, workflows.GraphExecutorConfig{AuthScope: cfg.authScope, OwnerID: cfg.ownerID, Lease: cfg.lease, CallTimeout: cfg.callTimeout, CancellationPoll: cfg.cancellationPoll, RetryBase: cfg.retryBase, RetryMax: cfg.retryMax})
}
