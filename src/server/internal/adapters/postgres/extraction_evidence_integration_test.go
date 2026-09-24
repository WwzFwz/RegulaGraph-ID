// Verifies the EXTRACT evidence catalog on disposable PostgreSQL: atomic checkpoint/catalog
// commit, replay/recovery, cancellation/fencing, source metadata integrity, isolated lookup,
// missing support and bounded results. Requires REGULAGRAPH_TEST_POSTGRES_DSN; skipped runs
// are not database evidence. Synthetic bytes do not establish legal/model benchmark quality.
package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func TestExtractionEvidenceCatalogAgainstPostgres(t *testing.T) {
	dsn := os.Getenv("REGULAGRAPH_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("REGULAGRAPH_TEST_POSTGRES_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repo, err := Open(ctx, Config{DSN: dsn, MaxConnections: 4, HealthTimeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	migrations, _ := filepath.Abs(filepath.Join("..", "..", "..", "..", "..", "migrations"))
	if err = repo.ApplyMigrations(ctx, os.DirFS(migrations)); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.pool.Exec(ctx, `TRUNCATE TABLE corpus_state CASCADE`); err != nil {
		t.Fatal(err)
	}
	source, ref, checkpoint := catalogFixture(t, "one")
	seed := func(ref *pb.ArtifactRef, checkpoint *pb.Checkpoint) {
		t.Helper()
		if err := repo.RegisterArtifact(ctx, source.Meta.CorpusId, ref); err != nil {
			t.Fatal(err)
		}
		_, err := repo.pool.Exec(ctx, `INSERT INTO jobs(job_id,corpus_id,operation,state,stage,
			input_fingerprint,idempotency_key,request_hash,request_payload,attempt,stage_attempt,
			lease_owner,lease_fence,lease_expires_at) VALUES($1,$2,$3,$4,$5,$6,$1,$6,$7,1,1,'owner:catalog',1,clock_timestamp()+interval '1 minute')`,
			checkpoint.JobId, source.Meta.CorpusId, int16(pb.JobOperation_JOB_OPERATION_INGEST), int16(pb.JobState_JOB_STATE_RUNNING),
			int16(pb.JobStage_JOB_STAGE_EXTRACT), strings.Repeat("1", 64), []byte("synthetic catalog job"))
		if err != nil {
			t.Fatal(err)
		}
	}
	seed(ref, checkpoint)
	for i := 0; i < 2; i++ {
		if err = repo.SaveExtractionCheckpoint(ctx, checkpoint, "owner:catalog", ref, source); err != nil {
			t.Fatalf("checkpoint/catalog replay: %v", err)
		}
	}
	actual, err := repo.LoadResolutionEvidenceSources(ctx, source.Context, []string{"mention:catalog"}, 8)
	if err != nil || len(actual) != 1 || !proto.Equal(actual[0], ref) {
		t.Fatalf("catalog source mismatch: %v %v", actual, err)
	}
	for _, dimension := range []string{"corpus", "auth", "snapshot", "missing"} {
		t.Run(dimension, func(t *testing.T) {
			request := proto.Clone(source.Context).(*pb.RequestContext)
			ids := []string{"mention:catalog"}
			switch dimension {
			case "corpus":
				request.CorpusId = "corpus:foreign"
			case "auth":
				request.AuthScopeRef = "scope:foreign"
			case "snapshot":
				request.SnapshotRef = &pb.SnapshotRef{CorpusId: request.CorpusId, SnapshotId: "snapshot:foreign", Sequence: 1, ManifestHash: ref.ContentHash, RepresentationGeneration: "generation:foreign"}
			case "missing":
				ids = append(ids, "mention:missing")
			}
			if _, err := repo.LoadResolutionEvidenceSources(ctx, request, ids, 8); !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("isolation/missing support failed: %v", err)
			}
		})
	}
	assertRolledBack := func(id string) {
		t.Helper()
		var count int
		if err := repo.pool.QueryRow(ctx, `SELECT count(*) FROM job_checkpoints WHERE checkpoint_id=$1`, id).Scan(&count); err != nil || count != 0 {
			t.Fatalf("failed catalog left checkpoint: count=%d err=%v", count, err)
		}
		latest, err := repo.LoadLatestCheckpoint(ctx, checkpoint.JobId)
		if err != nil || latest.Meta.RecordId != checkpoint.Meta.RecordId {
			t.Fatalf("failed catalog moved job pointer: %v %v", latest, err)
		}
	}
	bad := proto.Clone(checkpoint).(*pb.Checkpoint)
	bad.Meta.RecordId = "checkpoint:catalog-invalid"
	wrongRef := proto.Clone(ref).(*pb.ArtifactRef)
	wrongRef.StorageKey = "unregistered/bytes.pb"
	if err = repo.SaveExtractionCheckpoint(ctx, bad, "owner:catalog", wrongRef, source); !errors.Is(err, domain.ErrPersistentIntegrity) {
		t.Fatalf("unregistered source accepted: %v", err)
	}
	assertRolledBack(bad.Meta.RecordId)
	if _, err = repo.pool.Exec(ctx, `UPDATE jobs SET cancellation_requested=true WHERE job_id=$1`, checkpoint.JobId); err != nil {
		t.Fatal(err)
	}
	if err = repo.SaveExtractionCheckpoint(ctx, bad, "owner:catalog", ref, source); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("cancelled job wrote catalog: %v", err)
	}
	assertRolledBack(bad.Meta.RecordId)
	if _, err = repo.pool.Exec(ctx, `UPDATE jobs SET cancellation_requested=false,lease_fence=2,lease_owner='owner:recovered' WHERE job_id=$1`, checkpoint.JobId); err != nil {
		t.Fatal(err)
	}
	if err = repo.SaveExtractionCheckpoint(ctx, bad, "owner:catalog", ref, source); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("stale owner wrote catalog: %v", err)
	}
	assertRolledBack(bad.Meta.RecordId)
	recovered := proto.Clone(checkpoint).(*pb.Checkpoint)
	recovered.Meta.RecordId, recovered.Fence = "checkpoint:catalog-recovered", 2
	if err = repo.SaveExtractionCheckpoint(ctx, recovered, "owner:recovered", ref, source); err != nil {
		t.Fatal(err)
	}
	var original string
	if err = repo.pool.QueryRow(ctx, `SELECT checkpoint_id FROM extraction_evidence_sources WHERE artifact_id=$1`, ref.ArtifactId).Scan(&original); err != nil || original != checkpoint.Meta.RecordId {
		t.Fatal("recovery rewrote immutable source catalog", err)
	}
	if _, err = repo.pool.Exec(ctx, `UPDATE extraction_evidence_sources SET auth_scope_ref='forged' WHERE artifact_id=$1`, ref.ArtifactId); err == nil {
		t.Fatal("catalog rewrite accepted")
	}
	second, secondRef, secondCheckpoint := catalogFixture(t, "two")
	seed(secondRef, secondCheckpoint)
	if err = repo.SaveExtractionCheckpoint(ctx, secondCheckpoint, "owner:catalog", secondRef, second); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.LoadResolutionEvidenceSources(ctx, source.Context, []string{"mention:catalog"}, 1); !errors.Is(err, ErrResultLimit) {
		t.Fatalf("catalog silently truncated results: %v", err)
	}
	actual, err = repo.LoadResolutionEvidenceSources(ctx, source.Context, []string{"mention:catalog"}, 2)
	if err != nil || len(actual) != 2 {
		t.Fatalf("catalog lost an alternative source: %v %v", actual, err)
	}
	partial, partialRef, partialCheckpoint := catalogFixture(t, "partial")
	partial.Mentions[0].Meta.RecordId = "mention:partial"
	partial.Completeness = pb.Completeness_COMPLETENESS_PARTIAL
	partialCheckpoint.TerminalStatus = pb.CompletionStatus_COMPLETION_STATUS_FAILED
	partialRef = catalogRef(t, partialRef.ArtifactId, partial)
	partialCheckpoint.ArtifactHashes = []*pb.ContentHash{partialRef.ContentHash}
	seed(partialRef, partialCheckpoint)
	if err = repo.SaveExtractionCheckpoint(ctx, partialCheckpoint, "owner:catalog", partialRef, partial); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.LoadResolutionEvidenceSources(ctx, partial.Context, []string{"mention:partial"}, 8); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("failed EXTRACT became candidate evidence: %v", err)
	}
}

func catalogRef(t *testing.T, id string, message proto.Message) *pb.ArtifactRef {
	t.Helper()
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	return &pb.ArtifactRef{ArtifactId: id, ContentHash: &pb.ContentHash{Sha256: hex.EncodeToString(digest[:])}, StorageKey: "catalog/" + strings.ReplaceAll(id, ":", "-"),
		MediaType: domain.ExtractionBatchMediaType, ByteSize: uint64(len(raw)), SchemaVersion: 1}
}

func catalogFixture(t *testing.T, suffix string) (*pb.ExtractionBatch, *pb.ArtifactRef, *pb.Checkpoint) {
	t.Helper()
	meta := func(id string) *pb.RecordMeta {
		return &pb.RecordMeta{SchemaVersion: 1, CorpusId: "corpus:catalog", RecordId: id}
	}
	hash := &pb.ContentHash{Sha256: strings.Repeat("a", 64)}
	model := &pb.ModelManifest{ModelId: "model:catalog", Version: "v1", WeightsHash: hash, TokenizerHash: hash, Task: pb.ModelTask_MODEL_TASK_EXTRACT, MaxTokens: 128, Precision: "fp32", Backend: "fixture", PromptHash: hash}
	producer := &pb.ProducerManifest{Software: "catalog-fixture", Build: "test", SchemaVersion: 1, Models: []*pb.ModelManifest{model}, ConfigHash: hash, PromptHashes: []*pb.ContentHash{hash}}
	source := &pb.ExtractionBatch{Meta: meta("extract:catalog:" + suffix), Context: &pb.RequestContext{SchemaVersion: 1, RequestId: "request:catalog", TraceId: "trace:catalog", CorpusId: "corpus:catalog", AuthScopeRef: "scope:catalog", Deadline: timestamppb.New(time.Now().Add(time.Minute)), ConfigFingerprint: hash},
		SourceDocumentBatch: &pb.ArtifactRef{ArtifactId: "document:catalog", ContentHash: hash, StorageKey: "catalog/document", MediaType: "application/x-protobuf", SchemaVersion: 1},
		Mentions:            []*pb.Mention{{Meta: meta("mention:catalog"), SurfaceForm: "Badan", CandidateType: "organization", TextSpan: &pb.TextSpan{TextArtifactId: "text:catalog", EndByte: 5}, SourceRefs: []*pb.SourceVersionRef{{SourceBlobId: "source:catalog", ProvisionVersionId: "version:catalog", RegulationId: "regulation:catalog"}}, ExtractionManifest: producer}},
		Dependencies:        &pb.DependencyManifest{ArtifactId: "dependencies:catalog", ProducerManifest: producer}, Completeness: pb.Completeness_COMPLETENESS_COMPLETE, OntologyVersion: "ontology:catalog", ModelManifest: model, PromptHash: hash, ItemCounts: &pb.Counts{Expected: 1, Accepted: 1}, TokenUsage: &pb.TokenUsage{TokenizerId: "tokenizer:catalog"}}
	ref := catalogRef(t, "artifact:catalog:"+suffix, source)
	checkpoint := &pb.Checkpoint{Meta: meta("checkpoint:catalog:" + suffix), JobId: "job:catalog:" + suffix, Stage: pb.JobStage_JOB_STAGE_EXTRACT, Fence: 1, TerminalStatus: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED, CompletedBatchKeys: []string{ref.ArtifactId}, ArtifactHashes: []*pb.ContentHash{ref.ContentHash}, Manifest: producer}
	return source, ref, checkpoint
}
