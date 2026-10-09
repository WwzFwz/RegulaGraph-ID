// Extends real graph inventory admission through daemon processor restoration.
// PostgreSQL owns receipt/registry authority; the worker deliberately returns a
// sentinel after verified source reads. This proves cold restore, cache reuse,
// stamp refresh and pin cleanup, not graph output/RPC/Neo4j completion.
package indexing

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/workflows"
)

type graphRestoreWorker struct {
	calls int
	err   error
}

func (w *graphRestoreWorker) ProcessBatch(context.Context, *pb.ProcessBatchRequest) (*pb.ProcessBatchResponse, error) {
	w.calls++
	return nil, w.err
}

func checkGraphProcessorRestore(t *testing.T, ctx context.Context, repo *postgres.Repository, db *pgx.Conn, in domain.GraphJobInventory, inputs map[string]domain.GraphJobSourceInputs, job domain.JobRecord) *workflows.GraphJobProcessor {
	t.Helper()
	a := in.Assignments[0]
	if id, err := repo.GraphJobPublication(ctx, job); err != nil || id != a.Plan.PublicationId {
		t.Fatal("graph publication locator", id, err)
	}
	wrong := job
	wrong.Attempt++
	if _, err := repo.GraphJobPublication(ctx, wrong); err == nil {
		t.Fatal("locator accepted wrong attempt")
	}
	b, err := repo.LoadGraphSourceBinding(ctx, job.CorpusID, a.Plan.PublicationId, a.SourceJobID)
	if err != nil {
		t.Fatal(err)
	}
	input := inputs[a.SourceJobID]
	boundE, boundR, err := domain.BindGraphSourceEnvelopes(a.SourceJobID,
		domain.GraphSourceArtifact{Reference: b.Source.Original, Bytes: input.Source.OriginalDocument}, domain.GraphSourceArtifact{Reference: b.Source.Bound, Bytes: input.Source.SnapshotDocument},
		domain.GraphSourceArtifact{Reference: b.OriginalExtraction, Bytes: input.Source.Extraction}, domain.GraphSourceArtifact{Reference: b.OriginalResolution, Bytes: input.Source.Resolution}, 4096)
	if err != nil {
		t.Fatal(err)
	}
	planBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(a.Plan)
	if err != nil {
		t.Fatal(err)
	}
	reader := indexMemoryArtifacts{b.Source.Original.ArtifactId: input.Source.OriginalDocument, b.Source.Bound.ArtifactId: input.Source.SnapshotDocument,
		b.OriginalExtraction.ArtifactId: input.Source.Extraction, b.OriginalResolution.ArtifactId: input.Source.Resolution,
		b.BoundExtraction.ArtifactId: boundE.Bytes, b.BoundResolution.ArtifactId: boundR.Bytes, a.Plan.RegistryView.ArtifactId: input.RegistryView, a.Reference.ArtifactId: planBytes}
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "configs", "ontology-v1.jsonc"))
	if err != nil {
		t.Fatal(err)
	}
	ontology, err := domain.ParseOntologyJSONC(raw)
	if err != nil {
		t.Fatal(err)
	}
	worker := &graphRestoreWorker{err: errors.New("stop after verified graph preflight")}
	admissions := 0
	factory := func(ctx context.Context, in domain.GraphJobInventory, input map[string]domain.GraphJobSourceInputs) (workflows.GraphExecutionAuthority, error) {
		admissions++
		return repo.PrepareGraphJobAdmission(ctx, in, input, 4096, 32)
	}
	processor, err := workflows.NewGraphJobProcessor(repo, graphRestoreReader{reader}, worker, factory, ontology, a.Plan.Context.AuthScopeRef)
	if err != nil {
		t.Fatal(err)
	}
	var pinsBefore int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM snapshot_read_leases WHERE corpus_id=$1`, job.CorpusID).Scan(&pinsBefore); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 3; attempt++ {
		if attempt == 2 {
			if _, err = db.Exec(ctx, `UPDATE corpus_state SET registry_revision=registry_revision+1 WHERE corpus_id=$1`, job.CorpusID); err != nil {
				t.Fatal(err)
			}
		}
		if _, err = processor.ProcessGraphJob(ctx, job); !errors.Is(err, worker.err) {
			t.Fatal("restored graph processor did not reach worker", err)
		}
		want := 1
		if attempt == 2 {
			want = 2
		}
		if admissions != want || worker.calls != attempt+1 {
			t.Fatal("cache/stamp refresh mismatch", admissions, worker.calls)
		}
		var pins int
		if err = db.QueryRow(ctx, `SELECT count(*) FROM snapshot_read_leases WHERE corpus_id=$1`, job.CorpusID).Scan(&pins); err != nil || pins != pinsBefore {
			t.Fatal("processor pin leaked", pins, pinsBefore, err)
		}
	}
	return processor
}

// A missing immutable object is distinguishable from a transient transport error.
type graphRestoreReader struct{ indexMemoryArtifacts }

func (r graphRestoreReader) ReadVerified(ctx context.Context, ref *pb.ArtifactRef, max uint64) ([]byte, error) {
	if _, ok := r.indexMemoryArtifacts[ref.ArtifactId]; !ok {
		return nil, domain.ErrNotFound
	}
	return r.indexMemoryArtifacts.ReadVerified(ctx, ref, max)
}

func checkGraphRecoveryFailure(t *testing.T, ctx context.Context, repo *postgres.Repository, db *pgx.Conn, processor *workflows.GraphJobProcessor, in domain.GraphJobInventory) {
	t.Helper()
	a := in.Assignments[0]
	// Simulate a legacy/recovered checkpoint before STAGED finalization. The
	// metadata-only commit fixture deliberately has no output blob in this reader.
	if _, err := db.Exec(ctx, `UPDATE jobs SET state=$2,next_attempt_at=clock_timestamp() WHERE job_id=$1`, a.JobID, int16(pb.JobState_JOB_STATE_RETRY_WAIT)); err != nil {
		t.Fatal(err)
	}
	executor, err := workflows.NewGraphExecutor(repo, processor, workflows.GraphExecutorConfig{OwnerID: "owner:graph-recovery", AuthScope: a.Plan.Context.AuthScopeRef, Lease: time.Minute, CallTimeout: 20 * time.Second, CancellationPoll: time.Second, RetryBase: time.Second, RetryMax: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	_, response, err := executor.RunOnce(ctx)
	if !errors.Is(err, domain.ErrPersistentIntegrity) || response != nil {
		t.Fatal("missing recovered graph output was not permanent", err)
	}
	var state int16
	if err = db.QueryRow(ctx, `SELECT state FROM jobs WHERE job_id=$1`, a.JobID).Scan(&state); err != nil || state != int16(pb.JobState_JOB_STATE_FAILED) {
		t.Fatal("corrupt recovery remained retryable", state, err)
	}
	if _, err = repo.ClaimGraphJobInScope(ctx, "owner:graph-no-loop", time.Minute, a.Plan.Context.AuthScopeRef); !errors.Is(err, domain.ErrLeaseUnavailable) {
		t.Fatal("failed graph recovery reclaimed", err)
	}
}
