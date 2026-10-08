// Exercises real PostgreSQL review transactions through proposal generation and the existing
// RESOLVE executor. Each case owns an isolated disposable schema. Model and source evidence
// are synthetic; the tests prove atomicity, replay, cancellation, and no resampling on resume,
// not resolution accuracy. Opt in with REGULAGRAPH_TEST_POSTGRES_DSN; record logs in artifacts.
package workflows

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/domain"
)

func reviewDatabase(t *testing.T) (*postgres.Repository, *pgx.Conn) {
	t.Helper()
	dsn := os.Getenv("REGULAGRAPH_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("disposable PostgreSQL required")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := pgx.Identifier{fmt.Sprintf("review_%d", time.Now().UnixNano())}
	if _, err = conn.Exec(ctx, "CREATE SCHEMA "+name.Sanitize()); err != nil {
		conn.Close(ctx)
		t.Fatal(err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", name[0])
	u.RawQuery = q.Encode()
	repo, err := postgres.Open(ctx, postgres.Config{DSN: u.String(), MaxConnections: 4, HealthTimeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		repo.Close()
		conn.Exec(context.Background(), "DROP SCHEMA "+name.Sanitize()+" CASCADE")
		conn.Close(context.Background())
	})
	if _, err = conn.Exec(ctx, "SET search_path TO "+name.Sanitize()); err != nil {
		t.Fatal(err)
	}
	path, _ := filepath.Abs("../../../../migrations")
	if err = repo.ApplyMigrations(ctx, os.DirFS(path)); err != nil {
		t.Fatal(err)
	}
	return repo, conn
}

func TestSemanticReviewResumeAgainstPostgres(t *testing.T) {
	for _, name := range []string{"resume", "cancel-before", "stale-before", "stale-after", "rollback", "changed-replay", "cancel-after", "concurrent"} {
		t.Run(name, func(t *testing.T) {
			repo, db := reviewDatabase(t)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			fixture, fake, artifacts, provider, gateway := executorFixture(t)
			job := fake.job
			sourceRef := fake.refs[fake.checkpoint.CompletedBatchKeys[0]]
			if err := repo.RegisterArtifact(ctx, job.CorpusID, sourceRef); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(ctx, `UPDATE corpus_state SET registry_revision=3 WHERE corpus_id=$1`, job.CorpusID); err != nil {
				t.Fatal(err)
			}
			raw, _ := (proto.MarshalOptions{Deterministic: true}).Marshal(fake.request)
			sum := sha256.Sum256(raw)
			hash := hex.EncodeToString(sum[:])
			_, err := db.Exec(ctx, `INSERT INTO jobs(job_id,corpus_id,operation,state,stage,input_fingerprint,idempotency_key,request_hash,request_payload,attempt,stage_attempt,lease_fence) VALUES($1,$2,$3,$4,$5,$6,$1,$6,$7,2,1,2)`, job.JobID, job.CorpusID, int16(pb.JobOperation_JOB_OPERATION_INGEST), int16(pb.JobState_JOB_STATE_STAGED), int16(pb.JobStage_JOB_STAGE_EXTRACT), hash, raw)
			if err != nil {
				t.Fatal(err)
			}
			cp := fake.checkpoint
			raw, _ = (proto.MarshalOptions{Deterministic: true}).Marshal(cp)
			sum = sha256.Sum256(raw)
			_, err = db.Exec(ctx, `INSERT INTO job_checkpoints(checkpoint_id,job_id,stage,fence,payload,payload_hash,terminal_status) VALUES($1,$2,$3,2,$4,$5,$6)`, cp.Meta.RecordId, job.JobID, int16(cp.Stage), raw, hex.EncodeToString(sum[:]), int16(cp.TerminalStatus))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(ctx, `UPDATE jobs SET latest_checkpoint_id=$2 WHERE job_id=$1`, job.JobID, cp.Meta.RecordId); err != nil {
				t.Fatal(err)
			}
			executor, err := NewSemanticExecutor(repo, artifacts, gateway, fixture.config)
			if err != nil {
				t.Fatal(err)
			}
			_, result, err := executor.RunOnce(ctx)
			if err != nil || result.State != pb.JobState_JOB_STATE_WAITING_REVIEW {
				t.Fatal("proposal", result, err)
			}
			reviewer, err := NewSemanticReviewer(repo, artifacts, SemanticReviewConfig{Actor: "operator:test", Corpus: job.CorpusID, AuthScope: fixture.config.AuthScope, Producer: fixture.config.Producer, ProducerPin: fixture.config.ProducerPin, MaximumBytes: 1 << 20, MaximumReferences: 1000, MaximumCandidates: 10})
			if err != nil {
				t.Fatal(err)
			}
			view, err := reviewer.Inspect(ctx, job.JobID)
			if err != nil {
				t.Fatal(err)
			}
			accept := func() (bool, error) {
				return reviewer.Accept(ctx, job.JobID, view.Queue.Output.ContentHash.Sha256, view.Candidates.RegistryRevision, "Reviewed source and unresolved identity")
			}
			switch name {
			case "cancel-before":
				_, err = db.Exec(ctx, `UPDATE jobs SET cancellation_requested=true WHERE job_id=$1`, job.JobID)
			case "stale-before":
				_, err = db.Exec(ctx, `UPDATE corpus_state SET registry_revision=4 WHERE corpus_id=$1`, job.CorpusID)
			case "rollback":
				_, err = db.Exec(ctx, fmt.Sprintf(`ALTER TABLE jobs ADD CONSTRAINT reject_review_resume CHECK(state <> %d) NOT VALID`, pb.JobState_JOB_STATE_RETRY_WAIT))
			}
			if err != nil {
				t.Fatal(err)
			}
			if name == "concurrent" {
				type outcome struct {
					fresh bool
					err   error
				}
				ch := make(chan outcome, 2)
				for range 2 {
					go func() { fresh, e := accept(); ch <- outcome{fresh, e} }()
				}
				x, y := <-ch, <-ch
				if x.err != nil || y.err != nil || x.fresh == y.fresh {
					t.Fatal("double submit", x, y)
				}
			} else {
				fresh, e := accept()
				if name == "cancel-before" || name == "stale-before" || name == "rollback" {
					if e == nil || fresh {
						t.Fatal("invalid approval accepted")
					}
					var count int
					if err = db.QueryRow(ctx, `SELECT (SELECT count(*) FROM semantic_batch_reviews)+(SELECT count(*) FROM semantic_resolution_intents)+(SELECT count(*) FROM registry_semantic_reviews)`).Scan(&count); err != nil || count != 0 {
						t.Fatal("partial approval", count, err)
					}
					return
				}
				if e != nil || !fresh {
					t.Fatal("accept", fresh, e)
				}
			}
			if name == "changed-replay" {
				if _, err = reviewer.Accept(ctx, job.JobID, view.Queue.Output.ContentHash.Sha256, 3, "changed"); !errors.Is(err, postgres.ErrConflict) {
					t.Fatal("changed replay", err)
				}
			}
			if name == "cancel-after" {
				if _, err = db.Exec(ctx, `UPDATE jobs SET cancellation_requested=true,state=$2 WHERE job_id=$1`, job.JobID, int16(pb.JobState_JOB_STATE_CANCELLED)); err != nil {
					t.Fatal(err)
				}
				if fresh, e := accept(); e != nil || fresh {
					t.Fatal("cancelled historical replay", fresh, e)
				}
				var state int16
				db.QueryRow(ctx, `SELECT state FROM jobs WHERE job_id=$1`, job.JobID).Scan(&state)
				if state != int16(pb.JobState_JOB_STATE_CANCELLED) {
					t.Fatal("revived cancelled job")
				}
				return
			}
			if name == "stale-after" {
				if _, err = db.Exec(ctx, `UPDATE corpus_state SET registry_revision=4 WHERE corpus_id=$1`, job.CorpusID); err != nil {
					t.Fatal(err)
				}
			}
			calls := provider.calls
			_, result, err = executor.RunOnce(ctx)
			if name == "stale-after" {
				if !errors.Is(err, domain.ErrResolutionReplan) {
					t.Fatal("stale intent not replanned", err)
				}
				return
			}
			if err != nil || result.State != pb.JobState_JOB_STATE_STAGED || provider.calls != calls {
				t.Fatal("resume resampled or failed", result, err, provider.calls, calls)
			}
			if fresh, e := accept(); e != nil || fresh {
				t.Fatal("historical exact replay", fresh, e)
			}
			var state int16
			var attempt, fence int
			db.QueryRow(ctx, `SELECT state,stage_attempt,lease_fence FROM jobs WHERE job_id=$1`, job.JobID).Scan(&state, &attempt, &fence)
			if state != int16(pb.JobState_JOB_STATE_STAGED) || attempt != 1 || fence != 4 {
				t.Fatal("replay reset job", state, attempt, fence)
			}
		})
	}
}

// LINK row persistence is checked separately from the DEFER executor end-to-end case.
// Registry writer LINK authentication/CAS is covered by its existing PostgreSQL suite.
func TestSemanticReviewLinkTransactionAgainstPostgres(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(fmt.Sprint("rollback=", rollback), func(t *testing.T) {
			repo, db := reviewDatabase(t)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			r, s, a := reviewFixtureWithLink(t, true)
			if _, err := r.Accept(ctx, s.queue.JobID, s.queue.Output.ContentHash.Sha256, 3, "Both contexts inspected"); err != nil {
				t.Fatal(err)
			}
			review := *s.accepted
			q := review.Queue
			for _, ref := range []*pb.ArtifactRef{q.Source, q.Candidates, q.Input, q.Output} {
				if err := repo.RegisterArtifact(ctx, q.CorpusID, ref); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := db.Exec(ctx, `UPDATE corpus_state SET registry_revision=3 WHERE corpus_id=$1`, q.CorpusID); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(ctx, `INSERT INTO canonical_identities(corpus_id,canonical_id,entity_type,identity_scope,identity_key,valid_from_revision) VALUES($1,'canonical:candidate',1,'fixture','fixture',1)`, q.CorpusID); err != nil {
				t.Fatal(err)
			}
			raw := []byte("synthetic request; no worker dispatch in this storage test")
			sum := sha256.Sum256(raw)
			hash := hex.EncodeToString(sum[:])
			if _, err := db.Exec(ctx, `INSERT INTO jobs(job_id,corpus_id,operation,state,stage,input_fingerprint,idempotency_key,request_hash,request_payload,attempt,stage_attempt,lease_fence) VALUES($1,$2,$3,$4,$5,$6,$1,$6,$7,3,3,3)`, q.JobID, q.CorpusID, int16(pb.JobOperation_JOB_OPERATION_INGEST), int16(pb.JobState_JOB_STATE_WAITING_REVIEW), int16(pb.JobStage_JOB_STAGE_RESOLVE), hash, raw); err != nil {
				t.Fatal(err)
			}
			source := new(pb.ExtractionBatch)
			if err := proto.Unmarshal(a.contents[q.Source.ArtifactId], source); err != nil {
				t.Fatal(err)
			}
			cp := &pb.Checkpoint{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: q.CorpusID, RecordId: q.SourceCheckpointID}, JobId: q.JobID, Stage: pb.JobStage_JOB_STAGE_EXTRACT, Fence: 2, CompletedBatchKeys: []string{q.Source.ArtifactId}, ArtifactHashes: []*pb.ContentHash{q.Source.ContentHash}, Manifest: source.Dependencies.ProducerManifest, TerminalStatus: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED}
			raw, _ = proto.Marshal(cp)
			sum = sha256.Sum256(raw)
			if _, err := db.Exec(ctx, `INSERT INTO job_checkpoints(checkpoint_id,job_id,stage,fence,payload,payload_hash,terminal_status) VALUES($1,$2,$3,2,$4,$5,$6)`, q.SourceCheckpointID, q.JobID, int16(cp.Stage), raw, hex.EncodeToString(sum[:]), int16(cp.TerminalStatus)); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(ctx, `UPDATE jobs SET latest_checkpoint_id=$2 WHERE job_id=$1`, q.JobID, q.SourceCheckpointID); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(ctx, `INSERT INTO semantic_proposal_queue(job_id,corpus_id,source_checkpoint_id,source_artifact_id,candidate_artifact_id,input_artifact_id,output_artifact_id) VALUES($1,$2,$3,$4,$5,$6,$7)`, q.JobID, q.CorpusID, q.SourceCheckpointID, q.Source.ArtifactId, q.Candidates.ArtifactId, q.Input.ArtifactId, q.Output.ArtifactId); err != nil {
				t.Fatal(err)
			}
			if rollback {
				if _, err := db.Exec(ctx, fmt.Sprintf(`ALTER TABLE jobs ADD CONSTRAINT reject_resume CHECK(state <> %d) NOT VALID`, pb.JobState_JOB_STATE_RETRY_WAIT)); err != nil {
					t.Fatal(err)
				}
			}
			fresh, err := repo.AcceptSemanticReview(ctx, review)
			if rollback {
				var n int
				if e := db.QueryRow(ctx, `SELECT (SELECT count(*) FROM registry_semantic_reviews)+(SELECT count(*) FROM semantic_resolution_intents)+(SELECT count(*) FROM semantic_batch_reviews)`).Scan(&n); e != nil || n != 0 || err == nil || fresh {
					t.Fatal("LINK partial transaction", n, fresh, err, e)
				}
				return
			}
			if err != nil || !fresh {
				t.Fatal(fresh, err)
			}
			approval := review.Intent.Approvals[0]
			var actor, canonical, proposalHash string
			if err = db.QueryRow(ctx, `SELECT actor,canonical_id,proposal_hash FROM registry_semantic_reviews WHERE corpus_id=$1 AND review_id=$2`, q.CorpusID, approval.ReviewID).Scan(&actor, &canonical, &proposalHash); err != nil {
				t.Fatal(err)
			}
			raw, _ = (proto.MarshalOptions{Deterministic: true}).Marshal(review.Intent.Request.Proposals[0])
			sum = sha256.Sum256(raw)
			if actor != approval.Actor || canonical != approval.CanonicalID || proposalHash != hex.EncodeToString(sum[:]) {
				t.Fatal("LINK review identity drift")
			}
			if _, err = db.Exec(ctx, `UPDATE semantic_batch_reviews SET reason='changed' WHERE job_id=$1`, q.JobID); err == nil {
				t.Fatal("audit rewrite allowed")
			}
			if fresh, err = repo.AcceptSemanticReview(ctx, review); err != nil || fresh {
				t.Fatal("LINK replay", fresh, err)
			}
		})
	}
}
