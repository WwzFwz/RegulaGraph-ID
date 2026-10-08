// Operator entry point for publishing a complete durable dense/BM25 inventory.
// Inputs are an explicit profile/publication and configured corpus/backend;
// indexing owns admission, writes, receipts and pointer commit. No graph profile
// or deployment is inferred. Credentials/backend errors are redacted, deadlines
// propagate, and output is a C01 SnapshotRef in a small CLI JSON envelope.
// Benchmark targets remain configs/benchmark-targets.yaml; successful publication
// does not establish retrieval quality or measured latency acceptance.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/adapters/qdrant"
	"regulagraph.local/server/internal/adapters/storage"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/indexing"
)

type publishIndexOptions struct {
	publication, corpus, profile, dsn, artifacts, endpoint, key string
	timeout                                                     time.Duration
}

func runPublishIndex(ctx context.Context, args []string, out, errOut io.Writer) int {
	return runPublishIndexWith(ctx, args, out, errOut, os.Getenv, publishIndex)
}

func runPublishIndexWith(ctx context.Context, args []string, out, errOut io.Writer, env func(string) string, execute func(context.Context, publishIndexOptions) (*pb.SnapshotRef, error)) int {
	fs := flag.NewFlagSet("publish-index", flag.ContinueOnError)
	fs.SetOutput(errOut)
	var opts publishIndexOptions
	fs.StringVar(&opts.publication, "publication", "", "Existing complete initial INDEX publication ID")
	fs.StringVar(&opts.corpus, "corpus", "", "Authorized operator corpus ID")
	fs.StringVar(&opts.profile, "profile", "", "Required: hybrid (dense plus BM25; excludes graph)")
	fs.DurationVar(&opts.timeout, "timeout", 5*time.Minute, "Total admission/write/readiness deadline, up to 30m")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	opts.dsn, opts.artifacts, opts.endpoint, opts.key = env("REGULAGRAPH_POSTGRES_DSN"), env("REGULAGRAPH_ARTIFACTS_DIR"), env("REGULAGRAPH_QDRANT_URL"), env("REGULAGRAPH_QDRANT_API_KEY")
	if fs.NArg() != 0 || opts.profile != "hybrid" || opts.dsn == "" || opts.artifacts == "" || opts.endpoint == "" || opts.timeout <= 0 || opts.timeout > 30*time.Minute ||
		domain.ValidateWire(&pb.RecordMeta{SchemaVersion: 1, CorpusId: opts.corpus, RecordId: opts.publication}, domain.DefaultWireLimits) != nil {
		fmt.Fprintln(errOut, "Invalid publication arguments/configuration; require corpus, publication, explicit hybrid profile and storage settings")
		return 2
	}
	bounded, cancel := context.WithTimeout(ctx, opts.timeout)
	defer cancel()
	snapshot, err := execute(bounded, opts)
	if err != nil {
		fmt.Fprintln(errOut, "Index publication failed; inspect child jobs, source authority and backend readiness, then retry the same publication")
		return 1
	}
	if bounded.Err() != nil || domain.ValidateWire(snapshot, domain.DefaultWireLimits) != nil || snapshot.CorpusId != opts.corpus {
		fmt.Fprintln(errOut, "Invalid publication result")
		return 1
	}
	raw, err := protojson.Marshal(snapshot)
	if err != nil {
		return 1
	}
	if err = json.NewEncoder(out).Encode(struct {
		Status   string          `json:"status"`
		Profile  string          `json:"profile"`
		Snapshot json.RawMessage `json:"snapshot"`
	}{"published", opts.profile, raw}); err != nil {
		return 1
	}
	return 0
}

func publishIndex(ctx context.Context, opts publishIndexOptions) (*pb.SnapshotRef, error) {
	repo, err := postgres.Open(ctx, postgres.Config{DSN: opts.dsn, MaxConnections: 4, HealthTimeout: 5 * time.Second})
	if err != nil {
		return nil, err
	}
	defer repo.Close()
	inventory, err := repo.LoadIndexJobInventory(ctx, opts.publication)
	if err != nil {
		return nil, err
	}
	if inventory.Snapshot.CorpusId != opts.corpus || inventory.Binding.Endpoint != opts.endpoint {
		return nil, errors.New("publication corpus/backend differs from configured operator scope")
	}
	files, err := storage.NewFileStore(opts.artifacts)
	if err != nil {
		return nil, err
	}
	defer files.Close()
	backend, err := qdrant.New(opts.endpoint, opts.key, &http.Client{Timeout: 30 * time.Second}, qdrant.Binding{Collection: inventory.Binding.Collection, CorpusID: opts.corpus, Generation: inventory.Binding.Generation})
	if err != nil {
		return nil, err
	}
	return indexing.PublishCompletedVectorIndex(ctx, repo, files, backend, opts.publication)
}
