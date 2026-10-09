// Operator entry point for a complete, already-scheduled ASSEMBLE publication.
// Inputs select exact corpus/publication/target/generation and trusted local
// routes. Library code authenticates source bytes, writes graph, verifies reused
// index and activates both through existing receipts/CAS. Credentials are never
// printed. Published replay confirms durable identity, not current backend health.
// Measure cold restore/write/readback/CAS separately; benchmark-targets.yaml stays
// REQUIRED_UNMEASURED. This command neither approves resolution nor deploys.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/neo4j"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/adapters/qdrant"
	"regulagraph.local/server/internal/adapters/storage"
	"regulagraph.local/server/internal/config"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/indexing"
	"regulagraph.local/server/internal/workflows"
)

type publishGraphOptions struct {
	corpus, publication, snapshot, generation, scope                                string
	dsn, root, graphEndpoint, database, username, password, indexEndpoint, indexKey string
	ontology                                                                        *domain.Ontology
	timeout                                                                         time.Duration
}

func runPublishGraph(ctx context.Context, args []string, out, errOut io.Writer) int {
	return runPublishGraphWith(ctx, args, out, errOut, os.Getenv, publishGraph)
}
func runPublishGraphWith(ctx context.Context, args []string, out, errOut io.Writer, env func(string) string, execute func(context.Context, publishGraphOptions) (*pb.SnapshotRef, error)) int {
	fs := flag.NewFlagSet("publish-graph", flag.ContinueOnError)
	fs.SetOutput(errOut)
	var o publishGraphOptions
	fs.StringVar(&o.corpus, "corpus", "", "Authorized corpus ID")
	fs.StringVar(&o.publication, "publication", "", "Existing complete ASSEMBLE publication ID")
	fs.StringVar(&o.snapshot, "snapshot", "", "Exact reserved target snapshot ID")
	fs.StringVar(&o.generation, "generation", "", "Immutable physical graph generation ID")
	fs.StringVar(&o.scope, "auth-scope", "", "Same scope as admitted source jobs")
	fs.DurationVar(&o.timeout, "timeout", 5*time.Minute, "Total publication deadline, 1s..30m")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	o.dsn, o.root = env("REGULAGRAPH_POSTGRES_DSN"), env("REGULAGRAPH_ARTIFACTS_DIR")
	o.graphEndpoint, o.database = env("REGULAGRAPH_NEO4J_URI"), env("REGULAGRAPH_NEO4J_DATABASE")
	o.username, o.password = env("REGULAGRAPH_NEO4J_USERNAME"), env("REGULAGRAPH_NEO4J_PASSWORD")
	o.indexEndpoint, o.indexKey = env("REGULAGRAPH_QDRANT_URL"), env("REGULAGRAPH_QDRANT_API_KEY")
	valid := fs.NArg() == 0 && o.dsn != "" && o.root != "" && o.database != "" && o.username != "" && o.password != "" && o.indexEndpoint != "" && o.timeout >= time.Second && o.timeout <= 30*time.Minute && domain.ValidateGraphEndpoint(o.graphEndpoint) == nil
	for _, id := range []string{o.publication, o.snapshot, o.generation, o.scope} {
		valid = valid && domain.ValidateWire(&pb.RecordMeta{SchemaVersion: 1, CorpusId: o.corpus, RecordId: id}, domain.DefaultWireLimits) == nil
	}
	var err error
	if valid {
		o.ontology, err = config.LoadOntology(env("REGULAGRAPH_ONTOLOGY_PATH"), env("REGULAGRAPH_ONTOLOGY_SHA256"))
		valid = err == nil
	}
	if !valid {
		fmt.Fprintln(errOut, "Invalid graph publication arguments/configuration; see doc/graph-publication.md")
		return 2
	}
	bounded, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	snapshot, err := execute(bounded, o)
	if err != nil || bounded.Err() != nil {
		fmt.Fprintln(errOut, "Graph publication failed; inspect source jobs and receipts, then retry identical arguments")
		return 1
	}
	if domain.ValidateWire(snapshot, domain.DefaultWireLimits) != nil || snapshot.CorpusId != o.corpus || snapshot.SnapshotId != o.snapshot {
		fmt.Fprintln(errOut, "Invalid graph publication output")
		return 1
	}
	raw, err := protojson.Marshal(snapshot)
	if err != nil {
		return 1
	}
	if err = json.NewEncoder(out).Encode(struct {
		Status   string          `json:"status"`
		Snapshot json.RawMessage `json:"snapshot"`
	}{"published", raw}); err != nil {
		return 1
	}
	return 0
}

func publishGraph(ctx context.Context, o publishGraphOptions) (*pb.SnapshotRef, error) {
	repo, err := postgres.Open(ctx, postgres.Config{DSN: o.dsn, MaxConnections: 4, HealthTimeout: 5 * time.Second})
	if err != nil {
		return nil, err
	}
	defer repo.Close()
	in, err := repo.LoadGraphJobInventory(ctx, o.corpus, o.publication)
	if err != nil {
		return nil, err
	}
	if err = domain.ValidateGraphJobInventory(in); err != nil {
		return nil, err
	}
	plan := in.Assignments[0].Plan
	if plan.Meta.CorpusId != o.corpus || plan.PublicationId != o.publication || plan.Context.AuthScopeRef != o.scope || !proto.Equal(plan.OntologyHash, o.ontology.ContentHash()) {
		return nil, errors.New("operator scope or ontology differs")
	}
	reservation, err := repo.ReservePublication(ctx, o.publication, "", o.corpus, o.snapshot, plan.Context.SnapshotRef.SnapshotId)
	if err != nil {
		return nil, err
	}
	if reservation.Fence != plan.PublicationFence || reservation.Sequence != plan.TargetSequence {
		return nil, errors.New("publication reservation differs")
	}
	if reservation.State == pb.SnapshotState_SNAPSHOT_STATE_PUBLISHED {
		return publishedGraphReplay(ctx, repo, o, in)
	}
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	id := "graph-publish:" + hex.EncodeToString(nonce[:])
	deadline, ok := ctx.Deadline()
	if !ok {
		return nil, errors.New("bounded publication context required")
	}
	pin, err := repo.PinActiveSnapshot(ctx, o.corpus, "pin:"+id, id, time.Until(deadline))
	if err != nil {
		return nil, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = repo.ReleaseSnapshotPin(cleanup, pin.LeaseID, pin.OwnerID)
	}()
	base, err := repo.LoadPinnedIndex(ctx, pin)
	if err != nil {
		return nil, err
	}
	if !proto.Equal(base.Snapshot, plan.Context.SnapshotRef) || base.Binding.Endpoint != o.indexEndpoint {
		return nil, errors.New("parent index or configured route differs")
	}
	files, err := storage.NewFileStore(o.root)
	if err != nil {
		return nil, err
	}
	defer files.Close()
	inputs, err := workflows.ReadGraphInventoryInputs(ctx, repo, files, in)
	if err != nil {
		return nil, err
	}
	authority, err := repo.PrepareGraphJobAdmission(ctx, in, inputs, domain.DefaultWireLimits.MaxItems, domain.DefaultWireLimits.MaxItems)
	if err != nil {
		return nil, err
	}
	prepared, err := workflows.PrepareCompletedGraph(ctx, authority, files, pin, o.ontology)
	if err != nil {
		return nil, err
	}
	graph, err := neo4j.New(neo4j.Config{URI: o.graphEndpoint, Database: o.database, Username: o.username, Password: o.password, PoolSize: 4, Timeout: 30 * time.Second}, neo4j.Binding{CorpusID: o.corpus, PublicationID: o.publication, Generation: o.generation, Fence: plan.PublicationFence, Sequence: plan.TargetSequence, RegistryRevision: plan.RegistryRevision, BaseSnapshot: plan.Context.SnapshotRef})
	if err != nil {
		return nil, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = graph.Close(cleanup)
	}()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	index, err := qdrant.New(o.indexEndpoint, o.indexKey, &http.Client{Transport: transport, Timeout: 30 * time.Second}, qdrant.Binding{CorpusID: o.corpus, Collection: base.Binding.Collection, Generation: base.Binding.Generation})
	if err != nil {
		return nil, err
	}
	return indexing.PublishPreparedGraph(ctx, repo, authority, graph, index, prepared, pin, o.snapshot)
}

func publishedGraphReplay(ctx context.Context, repo *postgres.Repository, o publishGraphOptions, in domain.GraphJobInventory) (*pb.SnapshotRef, error) {
	m, err := repo.LoadPublicationManifest(ctx, o.publication)
	if err != nil {
		return nil, err
	}
	if err = domain.ValidateWire(m, domain.DefaultWireLimits); err != nil {
		return nil, errors.Join(domain.ErrPersistentIntegrity, err)
	}
	catalog, err := repo.LoadGraphGeneration(ctx, o.corpus, o.publication)
	if err != nil {
		return nil, err
	}
	p := in.Assignments[0].Plan
	if catalog.Endpoint != o.graphEndpoint || catalog.Database != o.database || catalog.Binding.Generation != o.generation || catalog.Binding.Fence != p.PublicationFence || catalog.Binding.Sequence != p.TargetSequence || catalog.Binding.RegistryRevision != p.RegistryRevision || !proto.Equal(catalog.Binding.BaseSnapshot, p.Context.SnapshotRef) || m.SnapshotRef.SnapshotId != o.snapshot || m.Meta.CorpusId != o.corpus || len(m.BackendGenerations) != 2 {
		return nil, errors.New("published graph configuration differs")
	}
	matched := false
	for _, g := range m.BackendGenerations {
		if proto.Equal(g, catalog.ExpectedBackend()) {
			matched = true
		}
	}
	if !matched {
		return nil, errors.New("published graph receipt requirements differ")
	}
	source, err := repo.LoadGraphSourceBinding(ctx, o.corpus, o.publication, in.Assignments[0].SourceJobID)
	if err != nil {
		return nil, err
	}
	origin, err := repo.LoadIndexJobInventory(ctx, source.Source.PublicationID)
	if err != nil {
		return nil, err
	}
	if origin.Binding.Endpoint != o.indexEndpoint {
		return nil, errors.New("published index route differs")
	}
	if err = domain.VerifyPublicationReady(m, p.Context.SnapshotRef, p.PublicationFence); err != nil {
		return nil, err
	}
	snapshot, err := repo.LoadSnapshotRef(ctx, o.snapshot)
	if err != nil {
		return nil, err
	}
	if !proto.Equal(snapshot, m.SnapshotRef) {
		return nil, domain.ErrPersistentIntegrity
	}
	return snapshot, nil
}
