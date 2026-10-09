// Runs the actual Rust ASSEMBLE RPC across FileStore and PostgreSQL authority,
// then restores its committed output with a new processor and no further RPC.
// Initial vectors, EXTRACT and review approvals are explicit fixtures; RESOLVE
// commits two LINKs through the real registry before assembly of a supported edge.
// This establishes transport, artifact admission and durable recovery, not
// required model quality or performance. With Neo4j configured the fixture now
// publishes graph plus reused Qdrant, then verifies source hydration/cited draft.
// Use isolated PostgreSQL/Qdrant and a dedicated shared worker artifact root.
package indexing

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/adapters/storage"
	"regulagraph.local/server/internal/adapters/worker"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/workflows"
)

func TestNativeGraphAssemblyPipeline(t *testing.T) {
	if os.Getenv("REGULAGRAPH_TEST_NATIVE_GRAPH") != "1" {
		t.Skip("explicit native graph integration opt-in required")
	}
	for _, key := range []string{"REGULAGRAPH_TEST_POSTGRES_DSN", "REGULAGRAPH_TEST_QDRANT_ENDPOINT", "REGULAGRAPH_TEST_WORKER_ENDPOINT", "REGULAGRAPH_TEST_GRAPH_ARTIFACT_ROOT"} {
		if os.Getenv(key) == "" {
			t.Fatal("missing native graph prerequisite", key)
		}
	}
	host, _, err := net.SplitHostPort(os.Getenv("REGULAGRAPH_TEST_WORKER_ENDPOINT"))
	if err != nil || !net.ParseIP(host).IsLoopback() {
		t.Fatal("native graph test worker must use a loopback address")
	}
	runInitialIndexPublication(t, false, true, "graph-rpc")
}

type countedGraphRPC struct {
	workflows.GraphBatchWorker
	calls int
}

func (w *countedGraphRPC) ProcessBatch(ctx context.Context, r *pb.ProcessBatchRequest) (*pb.ProcessBatchResponse, error) {
	w.calls++
	return w.GraphBatchWorker.ProcessBatch(ctx, r)
}

func checkNativeGraphExecution(t *testing.T, ctx context.Context, repo *postgres.Repository, db *pgx.Conn,
	files *storage.FileStore, artifacts indexMemoryArtifacts, pin domain.SnapshotPin,
	prepared *workflows.PreparedGraphAssembly, binding domain.GraphSourceBinding,
	input domain.GraphSourceBindingInputs, viewBytes []byte, ontology *domain.Ontology) {
	t.Helper()
	// Copy registered source bytes without changing their identity or address.
	// Derived envelopes/plan/view are already persisted by production preparation.
	for _, ref := range []*pb.ArtifactRef{binding.Source.Original, binding.Source.Bound} {
		if _, err := files.Put(ctx, ref, bytes.NewReader(artifacts[ref.ArtifactId])); err != nil {
			t.Fatal("persist original graph source", err)
		}
	}
	docs := new(pb.DocumentBatch)
	if err := domain.DecodeWire(input.SnapshotDocument, docs, domain.DefaultWireLimits); err != nil {
		t.Fatal(err)
	}
	for _, text := range docs.TextArtifacts {
		if _, err := files.Put(ctx, text.NormalizedTextRef, bytes.NewReader(artifacts[text.NormalizedTextRef.ArtifactId])); err != nil {
			t.Fatal("persist graph source text", err)
		}
	}
	in := domain.GraphJobInventory{Assignments: []domain.GraphJobAssignment{{JobID: "job:native-graph:" + pin.CorpusID, SourceJobID: binding.Source.SourceJobID, Plan: prepared.Plan, Reference: prepared.Reference}}}
	intent, err := repo.LoadSemanticResolutionIntent(ctx, pin.CorpusID, binding.Source.SourceJobID)
	if err != nil {
		t.Fatal(err)
	}
	inputs := map[string]domain.GraphJobSourceInputs{binding.Source.SourceJobID: {Source: input, RegistryView: viewBytes, Candidates: artifacts[intent.CandidateRef.ArtifactId]}}
	admitted, err := repo.PrepareGraphJobAdmission(ctx, in, inputs, 4096, 32)
	if err != nil {
		t.Fatal("native graph admission", err)
	}
	if err = admitted.Schedule(ctx, pin); err != nil {
		t.Fatal("native graph scheduling", err)
	}
	client, err := worker.New(os.Getenv("REGULAGRAPH_TEST_WORKER_ENDPOINT"), insecure.NewCredentials(), worker.DefaultMaxMessageBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	rpc := &countedGraphRPC{GraphBatchWorker: client}
	factory := func(ctx context.Context, inv domain.GraphJobInventory, inputs map[string]domain.GraphJobSourceInputs) (workflows.GraphExecutionAuthority, error) {
		return repo.PrepareGraphJobAdmission(ctx, inv, inputs, 4096, 32)
	}
	newExecutor := func(w workflows.GraphBatchWorker) *workflows.GraphExecutor {
		t.Helper()
		p, e := workflows.NewGraphJobProcessor(repo, files, w, factory, ontology, prepared.Plan.Context.AuthScopeRef)
		if e != nil {
			t.Fatal(e)
		}
		executor, e := workflows.NewGraphExecutor(repo, p, workflows.GraphExecutorConfig{OwnerID: "owner:native-graph", AuthScope: prepared.Plan.Context.AuthScopeRef, Lease: time.Minute, CallTimeout: 20 * time.Second, CancellationPoll: time.Second, RetryBase: time.Second, RetryMax: time.Minute})
		if e != nil {
			t.Fatal(e)
		}
		return executor
	}
	var pinsBefore int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM snapshot_read_leases WHERE corpus_id=$1`, pin.CorpusID).Scan(&pinsBefore); err != nil {
		t.Fatal(err)
	}
	job, response, err := newExecutor(rpc).RunOnce(ctx)
	if err != nil || response == nil || rpc.calls != 1 {
		t.Fatal("native graph execution", rpc.calls, err)
	}
	raw, err := files.ReadVerified(ctx, response.GraphDelta, uint64(domain.DefaultWireLimits.MaxBytes))
	if err != nil {
		t.Fatal(err)
	}
	delta := new(pb.GraphDelta)
	if err = domain.DecodeWire(raw, delta, domain.DefaultWireLimits); err != nil || delta.Meta.RecordId != prepared.Plan.OutputArtifactId {
		t.Fatal("actual Rust graph delta", err)
	}
	if len(delta.Entities) != 2 || len(delta.Mentions) != 2 || len(delta.Assertions) != 1 || len(delta.Supports) != 1 || delta.Assertions[0].SubjectId == delta.Assertions[0].ObjectId {
		t.Fatal("native graph lost resolved endpoints or source evidence")
	}
	publicationInputs, err := workflows.PrepareCompletedGraph(ctx, admitted, files, pin, ontology)
	if err != nil || publicationInputs == nil || len(publicationInputs.Deltas()) != 1 || !proto.Equal(publicationInputs.Deltas()[0], delta) {
		t.Fatal("prepare complete native graph for publication", err)
	}
	if committed, e := admitted.GraphCheckpointCommitted(ctx, response.Checkpoint); e != nil || !committed {
		t.Fatal("native graph checkpoint not committed", e)
	}
	registered, err := repo.LoadArtifact(ctx, pin.CorpusID, response.GraphDelta.ArtifactId)
	if err != nil || !proto.Equal(registered, response.GraphDelta) {
		t.Fatal("native graph output registration drift", err)
	}
	// Inject a historical checkpoint needing finalization. A fresh processor must
	// restore authority and bytes from durable state instead of invoking Rust again.
	if _, err = db.Exec(ctx, `UPDATE jobs SET state=$2,next_attempt_at=clock_timestamp() WHERE job_id=$1`, job.JobID, int16(pb.JobState_JOB_STATE_RETRY_WAIT)); err != nil {
		t.Fatal(err)
	}
	noRPC := &graphRestoreWorker{err: errors.New("checkpoint recovery must not call RPC")}
	reclaimed, recovered, err := newExecutor(noRPC).RunOnce(ctx)
	if err != nil || recovered == nil || noRPC.calls != 0 || reclaimed.LeaseFence <= job.LeaseFence || !proto.Equal(response.GraphDelta, recovered.GraphDelta) || proto.Equal(response.Checkpoint, recovered.Checkpoint) {
		t.Fatal("native graph durable recovery", noRPC.calls, err)
	}
	if committed, e := admitted.GraphCheckpointCommitted(ctx, recovered.Checkpoint); e != nil || !committed {
		t.Fatal("recovered checkpoint not durable", e)
	}
	if err = publicationInputs.Revalidate(ctx, admitted, pin); err == nil {
		t.Fatal("prepared publication ignored replaced checkpoint after recovery")
	}
	publicationInputs, err = workflows.PrepareCompletedGraph(ctx, admitted, files, pin, ontology)
	if err != nil {
		t.Fatal("recovered graph cannot be prepared afresh", err)
	}
	var pinsAfter, checkpoints int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM snapshot_read_leases WHERE corpus_id=$1`, pin.CorpusID).Scan(&pinsAfter); err != nil || pinsBefore != pinsAfter {
		t.Fatal("native/recovery leaked reader lease", pinsBefore, pinsAfter, err)
	}
	if err = db.QueryRow(ctx, `SELECT count(*) FROM job_checkpoints WHERE job_id=$1`, job.JobID).Scan(&checkpoints); err != nil || checkpoints != 2 {
		t.Fatal("recovery lost original checkpoint", checkpoints, err)
	}
	if _, err = repo.ClaimGraphJobInScope(ctx, "owner:no-repeat", time.Minute, prepared.Plan.Context.AuthScopeRef); !errors.Is(err, domain.ErrLeaseUnavailable) {
		t.Fatal("completed graph job became claimable", err)
	}
	t.Logf("actual Rust RPC=%d; output=%s; original fence=%d; recovered fence=%d; historical checkpoints=%d", rpc.calls, response.GraphDelta.ArtifactId, job.LeaseFence, reclaimed.LeaseFence, checkpoints)
	t.Run("Neo4j actual Rust output", func(t *testing.T) {
		checkNativeGraphNeo4j(t, ctx, repo, db, admitted, pin, publicationInputs, artifacts)
	})
}
