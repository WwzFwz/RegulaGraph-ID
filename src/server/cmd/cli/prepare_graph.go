// Wires trusted operator configuration to full-source graph preparation and
// atomic ASSEMBLE scheduling. The command owns its base pin, bounded deadline
// and artifact store. It neither calls a model nor approves ambiguous decisions.
// Output lists durable child jobs; scheduled is not published/search-ready.
// Record preparation time/bytes/retries under benchmark-targets.yaml; required
// quality/performance remain unmeasured until corpus acceptance runs exist.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/adapters/storage"
	"regulagraph.local/server/internal/config"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/workflows"
)

type prepareGraphOptions struct {
	corpus, publication, snapshot, base, scope, dsn, root string
	ontology                                              *domain.Ontology
	timeout                                               time.Duration
}

func runPrepareGraph(ctx context.Context, args []string, out, errOut io.Writer) int {
	return runPrepareGraphWith(ctx, args, out, errOut, os.Getenv, prepareGraph)
}

func runPrepareGraphWith(ctx context.Context, args []string, out, errOut io.Writer, env func(string) string,
	execute func(context.Context, prepareGraphOptions) (domain.GraphJobInventory, error)) int {
	fs := flag.NewFlagSet("prepare-graph", flag.ContinueOnError)
	fs.SetOutput(errOut)
	var o prepareGraphOptions
	fs.StringVar(&o.corpus, "corpus", "", "Authorized corpus ID")
	fs.StringVar(&o.publication, "publication", "", "New or identically replayed graph publication ID")
	fs.StringVar(&o.snapshot, "snapshot", "", "Reserved target snapshot ID (same for publish-graph)")
	fs.StringVar(&o.base, "base-snapshot", "", "Exact active initial-index snapshot ID")
	fs.StringVar(&o.scope, "auth-scope", "", "Scope of all indexed source jobs")
	fs.DurationVar(&o.timeout, "timeout", 5*time.Minute, "Total preparation deadline, 1s..30m")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	o.dsn, o.root = env("REGULAGRAPH_POSTGRES_DSN"), env("REGULAGRAPH_ARTIFACTS_DIR")
	valid := fs.NArg() == 0 && o.dsn != "" && o.root != "" && o.timeout >= time.Second && o.timeout <= 30*time.Minute && o.snapshot != o.base
	for _, id := range []string{o.publication, o.snapshot, o.base, o.scope} {
		valid = valid && domain.ValidateWire(&pb.RecordMeta{SchemaVersion: 1, CorpusId: o.corpus, RecordId: id}, domain.DefaultWireLimits) == nil
	}
	if valid {
		var err error
		o.ontology, err = config.LoadOntology(env("REGULAGRAPH_ONTOLOGY_PATH"), env("REGULAGRAPH_ONTOLOGY_SHA256"))
		valid = err == nil
	}
	if !valid {
		fmt.Fprintln(errOut, "Invalid graph preparation arguments/configuration; see doc/graph-preparation.md")
		return 2
	}
	bounded, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	in, err := execute(bounded, o)
	if err != nil || bounded.Err() != nil {
		fmt.Fprintln(errOut, "Graph preparation failed; all indexed sources need compatible completed RESOLVE and the exact active base. Retry identical arguments after checking source state.")
		return 1
	}
	if domain.ValidateGraphJobInventory(in) != nil {
		fmt.Fprintln(errOut, "Invalid graph preparation output")
		return 1
	}
	jobs := make([]string, 0, len(in.Assignments))
	for _, a := range in.Assignments {
		p := a.Plan
		if p.Meta.CorpusId != o.corpus || p.PublicationId != o.publication || p.Context.AuthScopeRef != o.scope || p.Context.SnapshotRef.SnapshotId != o.base || p.OntologyHash.Sha256 != o.ontology.ContentHash().Sha256 {
			return 1
		}
		jobs = append(jobs, a.JobID)
	}
	if err = json.NewEncoder(out).Encode(struct {
		Status      string   `json:"status"`
		Publication string   `json:"publication"`
		Snapshot    string   `json:"snapshot"`
		Jobs        []string `json:"jobs"`
	}{"scheduled", o.publication, o.snapshot, jobs}); err != nil {
		return 1
	}
	return 0
}

func prepareGraph(ctx context.Context, o prepareGraphOptions) (domain.GraphJobInventory, error) {
	var empty domain.GraphJobInventory
	repo, err := postgres.Open(ctx, postgres.Config{DSN: o.dsn, MaxConnections: 4, HealthTimeout: 5 * time.Second})
	if err != nil {
		return empty, err
	}
	defer repo.Close()
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return empty, err
	}
	id := "graph-prepare:" + hex.EncodeToString(nonce[:])
	deadline, ok := ctx.Deadline()
	if !ok {
		return empty, errors.New("bounded preparation context required")
	}
	pin, err := repo.PinActiveSnapshot(ctx, o.corpus, "pin:"+id, id, time.Until(deadline))
	if err != nil {
		return empty, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = repo.ReleaseSnapshotPin(cleanup, pin.LeaseID, pin.OwnerID)
	}()
	if pin.SnapshotID != o.base {
		return empty, errors.New("active base differs")
	}
	files, err := storage.NewFileStore(o.root)
	if err != nil {
		return empty, err
	}
	defer files.Close()
	policy := fmt.Sprintf("%x", sha256.Sum256([]byte("regulagraph-graph-preparation-v1")))
	cfg := workflows.GraphPreparationConfig{CorpusID: o.corpus, PublicationID: o.publication, SnapshotID: o.snapshot, BaseSnapshotID: o.base, AuthScope: o.scope,
		Producer:     &pb.ProducerManifest{Software: "regulagraph-graph-preparation", Build: "v1", SchemaVersion: 1, ConfigHash: &pb.ContentHash{Sha256: policy}, InputHashes: []*pb.ContentHash{o.ontology.ContentHash()}},
		OntologyHash: o.ontology.ContentHash(), MaximumReferences: domain.DefaultWireLimits.MaxItems, MaximumCandidates: domain.DefaultWireLimits.MaxItems}
	in, err := workflows.PrepareGraphInventory(ctx, repo, files, files, pin, cfg)
	if err != nil {
		return empty, err
	}
	inputs, err := workflows.ReadGraphInventoryInputs(ctx, repo, files, in)
	if err != nil {
		return empty, err
	}
	admission, err := repo.PrepareGraphJobAdmission(ctx, in, inputs, cfg.MaximumReferences, cfg.MaximumCandidates)
	if err != nil {
		return empty, err
	}
	if err = admission.Schedule(ctx, pin); err != nil {
		return empty, err
	}
	return in, nil
}
