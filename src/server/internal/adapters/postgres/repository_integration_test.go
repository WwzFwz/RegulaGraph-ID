// Menjalankan bukti integrasi S01 terhadap PostgreSQL aktual.
// Peran: memverifikasi migration replay, idempotency conflict, exclusive claim, stale fence,
// receipt readiness, snapshot CAS, read lease historis, recovery replay, dan abort.
// Kontrak: REGULAGRAPH_TEST_POSTGRES_DSN wajib menunjuk database disposable; tanpa DSN test
// dinyatakan skipped dan tidak boleh dilaporkan sebagai PASS database. Mock tidak memenuhi gate.
// Benchmark: test ini menguji correctness, bukan target latency/throughput; raw timing benchmark
// harus direkam terpisah dengan workload dan hardware dari configs/benchmark-targets.yaml.
// Target numerik required: status REQUIRED_UNMEASURED sampai suite performa dijalankan.
// Status: integration suite S01 aktif dan membutuhkan PostgreSQL nyata.
package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func TestStorageFoundationAgainstPostgres(t *testing.T) {
	dsn := os.Getenv("REGULAGRAPH_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("REGULAGRAPH_TEST_POSTGRES_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repo, err := Open(ctx, Config{DSN: dsn, MaxConnections: 12, HealthTimeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	migrations, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "..", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.ApplyMigrations(ctx, os.DirFS(migrations)); err != nil {
		t.Fatal(err)
	}
	if err = repo.ApplyMigrations(ctx, os.DirFS(migrations)); err != nil {
		t.Fatalf("migration replay must be idempotent: %v", err)
	}
	if _, err = repo.pool.Exec(ctx, `TRUNCATE TABLE corpus_state CASCADE`); err != nil {
		t.Fatalf("reset disposable integration database: %v", err)
	}

	suffix := strings.ReplaceAll(time.Now().UTC().Format("20060102T150405.000000000"), ".", "")
	corpusID, jobID := "corpus-"+suffix, "job-"+suffix
	hashA, hashB := strings.Repeat("a", 64), strings.Repeat("b", 64)
	request := ingestionRequestFixture(corpusID, "request-"+suffix, "https://example.test/regulation-a.pdf")
	requestPayload, err := proto.MarshalOptions{Deterministic: true}.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	requestDigest := sha256.Sum256(requestPayload)
	intent := JobIntent{JobID: jobID, CorpusID: corpusID, Operation: pb.JobOperation_JOB_OPERATION_INGEST,
		InitialStage: pb.JobStage_JOB_STAGE_ACQUIRE, InputFingerprint: ingestionFingerprint(t, request),
		IdempotencyKey: request.IdempotencyKey, RequestHash: hex.EncodeToString(requestDigest[:]), RequestPayload: requestPayload}
	created, reused, err := repo.SubmitJob(ctx, intent)
	if err != nil || reused || created.State != pb.JobState_JOB_STATE_QUEUED {
		t.Fatalf("submit created=%+v reused=%v err=%v", created, reused, err)
	}
	_, reused, err = repo.SubmitJob(ctx, intent)
	if err != nil || !reused {
		t.Fatalf("idempotent replay reused=%v err=%v", reused, err)
	}
	loadedRequest, err := repo.LoadIngestionRequest(ctx, jobID)
	if err != nil || !proto.Equal(request, loadedRequest) {
		t.Fatalf("durable request recovery mismatch: request=%v err=%v", loadedRequest, err)
	}
	conflict := intent
	conflict.JobID = "other-" + suffix
	changedRequest := ingestionRequestFixture(corpusID, request.IdempotencyKey, "https://example.test/regulation-b.pdf")
	conflict.RequestPayload, err = proto.MarshalOptions{Deterministic: true}.Marshal(changedRequest)
	if err != nil {
		t.Fatal(err)
	}
	changedDigest := sha256.Sum256(conflict.RequestPayload)
	conflict.RequestHash = hex.EncodeToString(changedDigest[:])
	conflict.InputFingerprint = ingestionFingerprint(t, changedRequest)
	if _, _, err = repo.SubmitJob(ctx, conflict); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected request conflict, got %v", err)
	}
	artifact := &pb.ArtifactRef{ArtifactId: "artifact-" + suffix,
		ContentHash: &pb.ContentHash{Sha256: hashA}, StorageKey: "objects/" + suffix,
		MediaType: "application/octet-stream", ByteSize: 12, SchemaVersion: 1}
	if err = repo.RegisterArtifact(ctx, corpusID, artifact); err != nil {
		t.Fatal(err)
	}
	if err = repo.RegisterArtifact(ctx, corpusID, artifact); err != nil {
		t.Fatalf("artifact replay must be idempotent: %v", err)
	}
	changedArtifact := proto.Clone(artifact).(*pb.ArtifactRef)
	changedArtifact.MediaType = "application/pdf"
	if err = repo.RegisterArtifact(ctx, corpusID, changedArtifact); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected immutable artifact conflict, got %v", err)
	}
	if err = repo.ReplaceArtifactDependencies(ctx, corpusID, artifact.ArtifactId, []ArtifactDependency{{
		Kind: "lookup_scope", Key: "regulation-number:2026", Revision: 1, ProducerHash: hashB,
	}}); err != nil {
		t.Fatal(err)
	}
	if revision, scopeErr := repo.AdvanceLookupScope(ctx, corpusID, "regulation-number:2026", 0); scopeErr != nil || revision != 1 {
		t.Fatalf("first lookup revision=%d err=%v", revision, scopeErr)
	}
	if revision, scopeErr := repo.AdvanceLookupScope(ctx, corpusID, "regulation-number:2026", 1); scopeErr != nil || revision != 2 {
		t.Fatalf("second lookup revision=%d err=%v", revision, scopeErr)
	}
	if _, scopeErr := repo.AdvanceLookupScope(ctx, corpusID, "regulation-number:2026", 1); !errors.Is(scopeErr, ErrConflict) {
		t.Fatalf("expected stale lookup revision conflict, got %v", scopeErr)
	}

	var successes atomic.Int32
	var claimed JobRecord
	var lock sync.Mutex
	start := make(chan struct{})
	var group sync.WaitGroup
	for index := 0; index < 6; index++ {
		group.Add(1)
		go func(worker int) {
			defer group.Done()
			<-start
			record, claimErr := repo.ClaimJob(ctx, "worker-"+string(rune('a'+worker)), 200*time.Millisecond)
			if claimErr == nil {
				successes.Add(1)
				lock.Lock()
				claimed = record
				lock.Unlock()
			} else if !errors.Is(claimErr, ErrLeaseUnavailable) {
				t.Errorf("claim error: %v", claimErr)
			}
		}(index)
	}
	close(start)
	group.Wait()
	if successes.Load() != 1 {
		t.Fatalf("expected exactly one successful claim, got %d", successes.Load())
	}
	if err = repo.TransitionJob(ctx, jobID, claimed.LeaseOwner, claimed.LeaseFence,
		pb.JobState_JOB_STATE_RUNNING, pb.JobState_JOB_STATE_SUCCEEDED); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected direct success transition rejection, got %v", err)
	}
	checkpoint := checkpointFixture(corpusID, jobID, claimed.LeaseFence)
	if err = repo.SaveCheckpoint(ctx, checkpoint, claimed.LeaseOwner); err != nil {
		t.Fatalf("save current checkpoint: %v", err)
	}
	loadedCheckpoint, err := repo.LoadLatestCheckpoint(ctx, jobID)
	if err != nil || !proto.Equal(checkpoint, loadedCheckpoint) {
		t.Fatalf("durable checkpoint recovery mismatch: checkpoint=%v err=%v", loadedCheckpoint, err)
	}
	foreignCheckpoint := proto.Clone(checkpoint).(*pb.Checkpoint)
	foreignCheckpoint.Meta.RecordId = "checkpoint-foreign-" + suffix
	foreignCheckpoint.Meta.CorpusId = "foreign-corpus-" + suffix
	if err = repo.SaveCheckpoint(ctx, foreignCheckpoint, claimed.LeaseOwner); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("expected foreign-corpus checkpoint rejection, got %v", err)
	}
	regressedCheckpoint := checkpointFixture(corpusID, jobID, claimed.LeaseFence)
	regressedCheckpoint.Meta.RecordId = "checkpoint-regressed-" + suffix
	regressedCheckpoint.Stage = pb.JobStage_JOB_STAGE_ACQUIRE
	if err = repo.SaveCheckpoint(ctx, regressedCheckpoint, claimed.LeaseOwner); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected checkpoint stage regression rejection, got %v", err)
	}
	time.Sleep(250 * time.Millisecond)
	reclaimed, err := repo.ClaimJob(ctx, "worker-recovery", time.Minute)
	if err != nil || reclaimed.LeaseFence <= claimed.LeaseFence {
		t.Fatalf("reclaim=%+v err=%v", reclaimed, err)
	}
	if err = repo.TransitionJob(ctx, jobID, claimed.LeaseOwner, claimed.LeaseFence,
		pb.JobState_JOB_STATE_RUNNING, pb.JobState_JOB_STATE_STAGED); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("expected expired attempt transition rejection, got %v", err)
	}
	checkpoint.Meta.RecordId = "checkpoint-stale-" + suffix
	if err = repo.SaveCheckpoint(ctx, checkpoint, claimed.LeaseOwner); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("expected stale checkpoint rejection, got %v", err)
	}
	if err = repo.TransitionJob(ctx, jobID, reclaimed.LeaseOwner, reclaimed.LeaseFence,
		pb.JobState_JOB_STATE_RUNNING, pb.JobState_JOB_STATE_STAGED); err != nil {
		t.Fatalf("stage reclaimed job: %v", err)
	}
	if _, err = repo.RenewLease(ctx, jobID, reclaimed.LeaseOwner, reclaimed.LeaseFence, time.Minute); err != nil {
		t.Fatalf("renew staged job: %v", err)
	}
	if _, err = repo.pool.Exec(ctx, `UPDATE jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE job_id=$1`, jobID); err != nil {
		t.Fatal(err)
	}
	reclaimed, err = repo.ClaimJob(ctx, "worker-staged-recovery", time.Minute)
	if err != nil || reclaimed.State != pb.JobState_JOB_STATE_STAGED {
		t.Fatalf("reclaim staged job=%+v err=%v", reclaimed, err)
	}
	foreignCorpus := "foreign-corpus-" + suffix
	foreignRequest := ingestionRequestFixture(foreignCorpus, "foreign-request-"+suffix, "https://example.test/foreign.pdf")
	foreignPayload, marshalErr := proto.MarshalOptions{Deterministic: true}.Marshal(foreignRequest)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	foreignDigest := sha256.Sum256(foreignPayload)
	foreignJob := "foreign-job-" + suffix
	if _, _, err = repo.SubmitJob(ctx, JobIntent{JobID: foreignJob, CorpusID: foreignCorpus,
		Operation: pb.JobOperation_JOB_OPERATION_INGEST, InitialStage: pb.JobStage_JOB_STAGE_ACQUIRE,
		InputFingerprint: ingestionFingerprint(t, foreignRequest), IdempotencyKey: foreignRequest.IdempotencyKey,
		RequestHash: hex.EncodeToString(foreignDigest[:]), RequestPayload: foreignPayload}); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.ReservePublication(ctx, "publication-foreign-job-"+suffix, foreignJob, corpusID,
		"snapshot-foreign-job-"+suffix, ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected foreign-job corpus rejection, got %v", err)
	}

	publicationID, snapshotID := "publication-"+suffix, "snapshot-"+suffix
	reservation, err := repo.ReservePublication(ctx, publicationID, jobID, corpusID, snapshotID, "")
	if err != nil {
		t.Fatal(err)
	}
	manifest := publicationFixture(corpusID, publicationID, snapshotID, reservation.Sequence, reservation.Fence, nil)
	if err = repo.StagePublication(ctx, manifest); err != nil {
		t.Fatal(err)
	}
	if err = repo.TransitionJob(ctx, jobID, reclaimed.LeaseOwner, reclaimed.LeaseFence,
		pb.JobState_JOB_STATE_STAGED, pb.JobState_JOB_STATE_VALIDATING); err != nil {
		t.Fatalf("validate publication job: %v", err)
	}
	unready := receiptFixture(publicationID, reservation.Fence)
	unready.SearchReady = false
	if err = repo.RecordBackendReceipt(ctx, unready); !errors.Is(err, ErrPublicationNotReady) {
		t.Fatalf("expected unready rejection, got %v", err)
	}
	stale := receiptFixture(publicationID, reservation.Fence+1)
	if err = repo.RecordBackendReceipt(ctx, stale); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("expected stale receipt rejection, got %v", err)
	}
	if err = repo.RecordBackendReceipt(ctx, receiptFixture(publicationID, reservation.Fence)); err != nil {
		t.Fatal(err)
	}
	if err = repo.TransitionJob(ctx, jobID, reclaimed.LeaseOwner, reclaimed.LeaseFence,
		pb.JobState_JOB_STATE_VALIDATING, pb.JobState_JOB_STATE_PUBLISHING); err != nil {
		t.Fatalf("publish job transition: %v", err)
	}
	if _, err = repo.pool.Exec(ctx, `UPDATE jobs SET cancellation_requested=true WHERE job_id=$1`, jobID); err != nil {
		t.Fatal(err)
	}
	if err = repo.CommitPublication(ctx, publicationID); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected cancelled publication job rejection, got %v", err)
	}
	if _, _, activeErr := repo.ActiveSnapshot(ctx, corpusID); !errors.Is(activeErr, ErrNotFound) {
		t.Fatalf("cancelled publication became visible: %v", activeErr)
	}
	if _, err = repo.pool.Exec(ctx, `UPDATE jobs SET cancellation_requested=false WHERE job_id=$1`, jobID); err != nil {
		t.Fatal(err)
	}
	if err = repo.CommitPublication(ctx, publicationID); err != nil {
		t.Fatal(err)
	}
	if err = repo.CommitPublication(ctx, publicationID); err != nil {
		t.Fatalf("publication recovery/replay must be idempotent: %v", err)
	}
	active, sequence, err := repo.ActiveSnapshot(ctx, corpusID)
	if err != nil || active != snapshotID || sequence != reservation.Sequence {
		t.Fatalf("active=%s sequence=%d err=%v", active, sequence, err)
	}
	if _, err = repo.ReservePublication(ctx, "publication-wrong-parent-"+suffix, "", corpusID,
		"snapshot-wrong-parent-"+suffix, ""); !errors.Is(err, ErrSnapshotCASConflict) {
		t.Fatalf("expected parent CAS conflict, got %v", err)
	}
	pin, err := repo.PinActiveSnapshot(ctx, corpusID, "lease-"+suffix, "reader-1", time.Minute)
	if err != nil || pin.SnapshotID != snapshotID {
		t.Fatalf("pin=%+v err=%v", pin, err)
	}
	secondPublication := "publication-second-" + suffix
	secondSnapshot := "snapshot-second-" + suffix
	second, err := repo.ReservePublication(ctx, secondPublication, "", corpusID, secondSnapshot, snapshotID)
	if err != nil || second.Sequence <= reservation.Sequence || second.Fence <= reservation.Fence {
		t.Fatalf("second reservation=%+v err=%v", second, err)
	}
	parentRef := proto.Clone(manifest.SnapshotRef).(*pb.SnapshotRef)
	badParent := proto.Clone(parentRef).(*pb.SnapshotRef)
	badParent.RepresentationGeneration = "forged-generation"
	badParentManifest := publicationFixture(corpusID, secondPublication, secondSnapshot, second.Sequence, second.Fence, badParent)
	if err = repo.StagePublication(ctx, badParentManifest); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected forged parent metadata rejection, got %v", err)
	}
	secondManifest := publicationFixture(corpusID, secondPublication, secondSnapshot, second.Sequence, second.Fence, parentRef)
	if err = repo.StagePublication(ctx, secondManifest); err != nil {
		t.Fatal(err)
	}
	if err = repo.RecordBackendReceipt(ctx, receiptFixture(secondPublication, second.Fence)); err != nil {
		t.Fatal(err)
	}
	if err = repo.CommitPublication(ctx, secondPublication); err != nil {
		t.Fatal(err)
	}
	if err = repo.CommitPublication(ctx, publicationID); err != nil {
		t.Fatalf("old published snapshot replay must remain idempotent: %v", err)
	}
	var pinnedSnapshot string
	if err = repo.pool.QueryRow(ctx, `SELECT snapshot_id FROM snapshot_read_leases WHERE lease_id=$1`, pin.LeaseID).Scan(&pinnedSnapshot); err != nil || pinnedSnapshot != snapshotID {
		t.Fatalf("old reader pin changed across publication: snapshot=%s err=%v", pinnedSnapshot, err)
	}
	if err = repo.ReleaseSnapshotPin(ctx, pin.LeaseID, pin.OwnerID); err != nil {
		t.Fatal(err)
	}

	abortPublication := "publication-abort-" + suffix
	abortSnapshot := "snapshot-abort-" + suffix
	aborted, err := repo.ReservePublication(ctx, abortPublication, "", corpusID, abortSnapshot, secondSnapshot)
	if err != nil || aborted.Sequence <= second.Sequence || aborted.Fence <= second.Fence {
		t.Fatalf("abort reservation=%+v err=%v", aborted, err)
	}
	operationKey := "operation-abort-" + suffix
	if err = repo.RecordPublicationOperation(ctx, abortPublication, pb.BackendKind_BACKEND_KIND_QDRANT,
		operationKey, hashA, "planned", aborted.Fence); err != nil {
		t.Fatal(err)
	}
	if err = repo.AbortPublication(ctx, abortPublication); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected in-flight planned operation to block abort, got %v", err)
	}
	if err = repo.RecordPublicationOperation(ctx, abortPublication, pb.BackendKind_BACKEND_KIND_QDRANT,
		operationKey, hashA, "applied", aborted.Fence); err != nil {
		t.Fatal(err)
	}
	if err = repo.AbortPublication(ctx, abortPublication); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected uncompensated abort rejection, got %v", err)
	}
	if err = repo.RecordPublicationOperation(ctx, abortPublication, pb.BackendKind_BACKEND_KIND_QDRANT,
		operationKey, hashA, "compensated", aborted.Fence); err != nil {
		t.Fatal(err)
	}
	if err = repo.RecordPublicationOperation(ctx, abortPublication, pb.BackendKind_BACKEND_KIND_QDRANT,
		operationKey, hashA, "applied", aborted.Fence); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected operation state regression conflict, got %v", err)
	}
	if err = repo.AbortPublication(ctx, abortPublication); err != nil {
		t.Fatal(err)
	}
	abortedManifest := publicationFixture(corpusID, abortPublication, abortSnapshot, aborted.Sequence, aborted.Fence, secondManifest.SnapshotRef)
	if err = repo.StagePublication(ctx, abortedManifest); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected stage-after-abort rejection, got %v", err)
	}
	if err = repo.RecordPublicationOperation(ctx, abortPublication, pb.BackendKind_BACKEND_KIND_QDRANT,
		"operation-after-abort-"+suffix, hashA, "planned", aborted.Fence); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected operation-after-abort rejection, got %v", err)
	}
	active, _, err = repo.ActiveSnapshot(ctx, corpusID)
	if err != nil || active != secondSnapshot {
		t.Fatalf("abort changed active snapshot: active=%s err=%v", active, err)
	}
}

func checkpointFixture(corpusID, jobID string, fence uint64) *pb.Checkpoint {
	hash := &pb.ContentHash{Sha256: strings.Repeat("c", 64)}
	return &pb.Checkpoint{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpusID, RecordId: "checkpoint-current-" + jobID},
		JobId: jobID, Stage: pb.JobStage_JOB_STAGE_PARSE, Fence: fence,
		Manifest: &pb.ProducerManifest{Software: "s01-test", Build: "test", SchemaVersion: 1, ConfigHash: hash}}
}

func publicationFixture(corpusID, publicationID, snapshotID string, sequence, fence uint64, parent *pb.SnapshotRef) *pb.PublicationManifest {
	hash := &pb.ContentHash{Sha256: strings.Repeat("d", 64)}
	return &pb.PublicationManifest{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpusID, RecordId: publicationID},
		SnapshotRef: &pb.SnapshotRef{CorpusId: corpusID, SnapshotId: snapshotID, Sequence: sequence,
			ManifestHash: hash, RepresentationGeneration: "representation-1"}, ParentRef: parent, Fence: fence,
		ValidationReport: &pb.ValidationReport{Valid: true, CheckedRecords: 1},
		BackendGenerations: []*pb.BackendGeneration{{Backend: pb.BackendKind_BACKEND_KIND_POSTGRES,
			Generation: "postgres-generation-1", ExpectedCounts: &pb.Counts{Expected: 1, Accepted: 1}, OperationsChecksum: hash}}}
}

func receiptFixture(publicationID string, fence uint64) *pb.BackendReceipt {
	return &pb.BackendReceipt{PublicationId: publicationID, Backend: pb.BackendKind_BACKEND_KIND_POSTGRES,
		Generation: "postgres-generation-1", OperationsChecksum: &pb.ContentHash{Sha256: strings.Repeat("d", 64)},
		Counts: &pb.Counts{Expected: 1, Accepted: 1}, DurableAck: true, SearchReady: true, Fence: fence}
}

func ingestionRequestFixture(corpusID, idempotencyKey, sourceURL string) *pb.IngestionRequest {
	return &pb.IngestionRequest{CorpusId: corpusID, Operation: pb.JobOperation_JOB_OPERATION_INGEST,
		IdempotencyKey: idempotencyKey,
		Sources:        []*pb.SourceLocator{{PortalId: "bpk", Locator: &pb.SourceLocator_Url{Url: sourceURL}}},
		ConfigManifest: &pb.ProducerManifest{Software: "s01-test", Build: "test", SchemaVersion: 1,
			ConfigHash: &pb.ContentHash{Sha256: strings.Repeat("e", 64)}}}
}

func ingestionFingerprint(t *testing.T, request *pb.IngestionRequest) string {
	t.Helper()
	copy := proto.Clone(request).(*pb.IngestionRequest)
	copy.IdempotencyKey = ""
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(copy)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}
