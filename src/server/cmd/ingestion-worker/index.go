// Wires optional durable INDEX execution into the ingestion coordinator using
// its existing PostgreSQL, FileStore and Rust client. Startup never allocates a
// corpus or schedules plans implicitly. Migration0015 and admitted inventories
// are prerequisites; completion stages outputs without publishing a snapshot.
package main

import (
	"fmt"
	"os"
	"strconv"

	"regulagraph.local/server/internal/indexing"
	"regulagraph.local/server/internal/workflows"
)

type indexRuntimeStore interface {
	indexing.IndexJobProcessorStore
	workflows.IndexAttemptStore
}

func newIndexExecutor(cfg runtimeConfig, store indexRuntimeStore, reader indexing.IndexArtifactReader, worker indexing.IndexBatchWorker) (*workflows.IndexExecutor, error) {
	raw := os.Getenv("REGULAGRAPH_INDEX_ENABLED")
	if raw == "" {
		return nil, nil
	}
	enabled, err := strconv.ParseBool(raw)
	if err != nil {
		return nil, fmt.Errorf("REGULAGRAPH_INDEX_ENABLED must be boolean")
	}
	if !enabled {
		return nil, nil
	}
	processor, err := indexing.NewInitialIndexJobProcessor(store, reader, worker, cfg.authScope)
	if err != nil {
		return nil, err
	}
	return workflows.NewIndexExecutor(store, processor, workflows.IndexExecutorConfig{OwnerID: cfg.ownerID, Lease: cfg.lease, CallTimeout: cfg.callTimeout, CancellationPoll: cfg.cancellationPoll, RetryBase: cfg.retryBase, RetryMax: cfg.retryMax})
}
