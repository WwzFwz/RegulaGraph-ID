// Exposes operator abort of an admitted INDEX publication through the existing
// publication coordinator. Corpus identity is checked against the durable
// inventory before mutation; PostgreSQL rejects published snapshots and pending
// backend compensation. No job counters, source artifacts or backend data are
// reset/deleted. Deadlines bound database waits; errors must not leak credentials.
// This recovery command does not prove benchmark acceptance; targets remain in
// configs/benchmark-targets.yaml. An aborted inventory must be replanned with new IDs.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/indexing"
)

type abortIndexOptions struct {
	corpus, publication, dsn string
	timeout                  time.Duration
}

func runAbortIndex(ctx context.Context, args []string, out, errOut io.Writer) int {
	return runAbortIndexWith(ctx, args, out, errOut, os.Getenv, abortIndex)
}

func runAbortIndexWith(ctx context.Context, args []string, out, errOut io.Writer, env func(string) string, execute func(context.Context, abortIndexOptions) error) int {
	fs := flag.NewFlagSet("abort-index", flag.ContinueOnError)
	fs.SetOutput(errOut)
	var opts abortIndexOptions
	fs.StringVar(&opts.corpus, "corpus", "", "Operator corpus owning the INDEX inventory")
	fs.StringVar(&opts.publication, "publication", "", "Unpublished INDEX publication to abandon; preserves failed jobs")
	fs.DurationVar(&opts.timeout, "timeout", time.Minute, "Database deadline, up to 5m")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	opts.dsn = env("REGULAGRAPH_POSTGRES_DSN")
	if fs.NArg() != 0 || opts.dsn == "" || opts.timeout <= 0 || opts.timeout > 5*time.Minute || domain.ValidateWire(&pb.RecordMeta{SchemaVersion: 1, CorpusId: opts.corpus, RecordId: opts.publication}, domain.DefaultWireLimits) != nil {
		fmt.Fprintln(errOut, "Invalid abort arguments/configuration; require corpus, publication and PostgreSQL settings")
		return 2
	}
	bounded, cancel := context.WithTimeout(ctx, opts.timeout)
	defer cancel()
	if bounded.Err() != nil {
		fmt.Fprintln(errOut, "Index abort cancelled")
		return 1
	}
	if err := execute(bounded, opts); err != nil {
		fmt.Fprintln(errOut, "Index abort failed; inspect inventory ownership, snapshot state and uncompensated backend operations")
		return 1
	}
	if err := json.NewEncoder(out).Encode(map[string]string{"status": "aborted", "corpus": opts.corpus, "publication": opts.publication}); err != nil {
		fmt.Fprintln(errOut, "Abort output failed; inspect durable snapshot state before further recovery")
		return 1
	}
	return 0
}

type indexAbortStore interface {
	indexing.PublicationStore
	LoadIndexJobInventory(context.Context, string) (domain.IndexJobInventory, error)
}

func abortOwnedIndex(ctx context.Context, store indexAbortStore, opts abortIndexOptions) error {
	inventory, err := store.LoadIndexJobInventory(ctx, opts.publication)
	if err != nil {
		return err
	}
	if inventory.Snapshot == nil || inventory.Snapshot.CorpusId != opts.corpus {
		return errors.New("inventory corpus mismatch")
	}
	coordinator, err := indexing.NewPublicationCoordinator(store)
	if err != nil {
		return err
	}
	return coordinator.Abort(ctx, opts.publication)
}

func abortIndex(ctx context.Context, opts abortIndexOptions) error {
	repo, err := postgres.Open(ctx, postgres.Config{DSN: opts.dsn, MaxConnections: 2, HealthTimeout: 5 * time.Second})
	if err != nil {
		return err
	}
	defer repo.Close()
	return abortOwnedIndex(ctx, repo, opts)
}
