// Exercises queue/state atomicity, corpus metadata binding and lease/cancellation fencing
// against disposable PostgreSQL. Registered bytes are synthetic: model/evidence validation
// is tested in workflows, not inferred from these locator tests. Opt in with the existing
// REGULAGRAPH_TEST_POSTGRES_DSN; no production quality or latency claims are made.
package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"google.golang.org/protobuf/proto"
	"os"
	"path/filepath"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
	"strings"
	"testing"
	"time"
)

func TestSemanticProposalQueueAgainstPostgres(t *testing.T) {
	dsn := os.Getenv("REGULAGRAPH_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("disposable PostgreSQL DSN required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repo, err := Open(ctx, Config{DSN: dsn, MaxConnections: 4, HealthTimeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	migrations, err := filepath.Abs("../../../../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.ApplyMigrations(ctx, os.DirFS(migrations)); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.pool.Exec(ctx, `TRUNCATE corpus_state CASCADE`); err != nil {
		t.Fatal(err)
	}
	for _, test := range []string{"park", "cancel", "stale", "foreign", "metadata", "rollback"} {
		t.Run(test, func(t *testing.T) {
			corpus, jobID := "corpus:queue:"+test, "job:queue:"+test
			producer := &pb.ProducerManifest{Software: "fixture", Build: "test", SchemaVersion: 1, ConfigHash: &pb.ContentHash{Sha256: strings.Repeat("a", 64)}}
			source := &pb.ExtractionBatch{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpus, RecordId: "extract:" + test}, Dependencies: &pb.DependencyManifest{ProducerManifest: producer}}
			refs := make([]*pb.ArtifactRef, 4)
			for i, label := range []string{"source", "candidate", "input", "output"} {
				raw := []byte(test + ":" + label)
				digest := sha256.Sum256(raw)
				hash := hex.EncodeToString(digest[:])
				refs[i] = &pb.ArtifactRef{ArtifactId: "artifact:queue:" + test + ":" + label, ContentHash: &pb.ContentHash{Sha256: hash},
					StorageKey: "sha256/" + hash, MediaType: "application/x-protobuf", ByteSize: uint64(len(raw)), SchemaVersion: 1}
				owner := corpus
				if test == "foreign" && i == 3 {
					owner = "corpus:queue:other"
				}
				if err = repo.RegisterArtifact(ctx, owner, refs[i]); err != nil {
					t.Fatal(err)
				}
			}
			payload := []byte("fixture request")
			digest := sha256.Sum256(payload)
			hash := hex.EncodeToString(digest[:])
			_, err = repo.pool.Exec(ctx, `INSERT INTO jobs(job_id,corpus_id,operation,state,stage,input_fingerprint,idempotency_key,
   request_hash,request_payload,attempt,stage_attempt,lease_fence) VALUES($1,$2,$3,$4,$5,$6,$1,$6,$7,1,1,1)`,
				jobID, corpus, int16(pb.JobOperation_JOB_OPERATION_INGEST), int16(pb.JobState_JOB_STATE_STAGED), int16(pb.JobStage_JOB_STAGE_EXTRACT), hash, payload)
			if err != nil {
				t.Fatal(err)
			}
			checkpoint := &pb.Checkpoint{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpus, RecordId: "checkpoint:queue:" + test},
				JobId: jobID, Stage: pb.JobStage_JOB_STAGE_EXTRACT, Fence: 1, CompletedBatchKeys: []string{refs[0].ArtifactId},
				ArtifactHashes: []*pb.ContentHash{refs[0].ContentHash}, Manifest: producer, TerminalStatus: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED}
			raw, err := proto.Marshal(checkpoint)
			if err != nil {
				t.Fatal(err)
			}
			digest = sha256.Sum256(raw)
			_, err = repo.pool.Exec(ctx, `INSERT INTO job_checkpoints(checkpoint_id,job_id,stage,fence,payload,payload_hash,terminal_status)
   VALUES($1,$2,$3,1,$4,$5,$6)`, checkpoint.Meta.RecordId, jobID, int16(pb.JobStage_JOB_STAGE_EXTRACT), raw,
				hex.EncodeToString(digest[:]), int16(pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = repo.pool.Exec(ctx, `UPDATE jobs SET latest_checkpoint_id=$2 WHERE job_id=$1`, jobID, checkpoint.Meta.RecordId); err != nil {
				t.Fatal(err)
			}
			job, err := repo.ClaimResolveJob(ctx, "owner:queue", time.Minute)
			if err != nil || job.JobID != jobID {
				t.Fatalf("claim: %v %v", job, err)
			}
			proof := domain.SemanticJobFence{JobID: jobID, OwnerID: job.LeaseOwner, Fence: job.LeaseFence, SourceCheckpointID: checkpoint.Meta.RecordId}
			switch test {
			case "cancel":
				if _, err = repo.pool.Exec(ctx, `UPDATE jobs SET cancellation_requested=true WHERE job_id=$1`, jobID); err != nil {
					t.Fatal(err)
				}
			case "stale":
				proof.Fence++
			case "metadata":
				refs[3] = proto.Clone(refs[3]).(*pb.ArtifactRef)
				refs[3].StorageKey = "wrong/key"
			case "rollback":
				if _, err = repo.pool.Exec(ctx, `ALTER TABLE jobs ADD CONSTRAINT test_queue_rollback CHECK(state <> 3) NOT VALID`); err != nil {
					t.Fatal(err)
				}
				defer func() {
					if _, err := repo.pool.Exec(ctx, `ALTER TABLE jobs DROP CONSTRAINT test_queue_rollback`); err != nil {
						t.Error(err)
					}
				}()
			}
			state, err := repo.ParkSemanticProposal(ctx, proof, refs[0], source, refs[1], refs[2], refs[3])
			var actual int16
			var queued int
			var leaseReleased bool
			if queryErr := repo.pool.QueryRow(ctx, `SELECT state,lease_owner IS NULL AND lease_expires_at IS NULL FROM jobs WHERE job_id=$1`, jobID).Scan(&actual, &leaseReleased); queryErr != nil {
				t.Fatal(queryErr)
			}
			if queryErr := repo.pool.QueryRow(ctx, `SELECT count(*) FROM semantic_proposal_queue WHERE job_id=$1`, jobID).Scan(&queued); queryErr != nil {
				t.Fatal(queryErr)
			}
			switch test {
			case "park":
				if err != nil || state != pb.JobState_JOB_STATE_WAITING_REVIEW || actual != int16(state) || queued != 1 || !leaseReleased {
					t.Fatalf("park: %s %v rows=%d actual=%d released=%t", state, err, queued, actual, leaseReleased)
				}
				loaded, loadErr := repo.LoadPendingSemanticProposal(ctx, corpus, jobID)
				if loadErr != nil || !proto.Equal(loaded, refs[3]) {
					t.Fatal("queue output was not retained", loadErr)
				}
				if _, loadErr = repo.LoadPendingSemanticProposal(ctx, "corpus:other", jobID); !errors.Is(loadErr, ErrNotFound) {
					t.Fatal("queue leaked corpus", loadErr)
				}
				if _, rewriteErr := repo.pool.Exec(ctx, `DELETE FROM semantic_proposal_queue WHERE job_id=$1`, jobID); rewriteErr == nil {
					t.Fatal("queue deletion accepted")
				}
			case "cancel":
				if err != nil || state != pb.JobState_JOB_STATE_CANCELLED || queued != 0 || !leaseReleased {
					t.Fatalf("cancel: %s %v rows=%d", state, err, queued)
				}
			default:
				if err == nil || queued != 0 || actual != int16(pb.JobState_JOB_STATE_RUNNING) {
					t.Fatalf("invalid/failed park committed: %s %v rows=%d actual=%d", state, err, queued, actual)
				}
			}
		})
	}
}
