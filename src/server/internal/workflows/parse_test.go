// Tests the durable PARSE dispatch handoff, including state classification and checkpoint persistence.
// Fakes inject protocol, cancellation and lifecycle races without claiming database or worker performance;
// actual cross-process/storage checks and required targets remain separate verification gates.
package workflows

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type parseStoreFake struct {
	job             domain.JobRecord
	request         *pb.IngestionRequest
	registered      *pb.ArtifactRef
	checkpoint      *pb.Checkpoint
	transitions     []pb.JobState
	cancelled       bool
	cancelOnSave    bool
	loadErr         error
	retryDelay      time.Duration
	claimParseErr   error
	priorCheckpoint *pb.Checkpoint
	priorArtifact   *pb.ArtifactRef
}

func (s *parseStoreFake) SubmitJob(context.Context, domain.JobIntent) (domain.JobRecord, bool, error) {
	return domain.JobRecord{}, false, nil
}
func (s *parseStoreFake) ClaimJob(context.Context, string, time.Duration) (domain.JobRecord, error) {
	return s.job, nil
}
func (s *parseStoreFake) ClaimParseJob(context.Context, string, time.Duration) (domain.JobRecord, error) {
	if s.claimParseErr != nil {
		return domain.JobRecord{}, s.claimParseErr
	}
	return s.job, nil
}
func (s *parseStoreFake) ClaimStructureJob(context.Context, string, time.Duration) (domain.JobRecord, error) {
	return s.job, nil
}
func (s *parseStoreFake) RenewLease(context.Context, string, string, uint64, time.Duration) (time.Time, error) {
	return time.Time{}, nil
}
func (s *parseStoreFake) SaveCheckpoint(_ context.Context, checkpoint *pb.Checkpoint, _ string) error {
	s.checkpoint = proto.Clone(checkpoint).(*pb.Checkpoint)
	if s.cancelOnSave {
		s.cancelled = true
	}
	return nil
}
func (s *parseStoreFake) TransitionJob(_ context.Context, _, _ string, _ uint64, _, next pb.JobState) error {
	s.transitions = append(s.transitions, next)
	return nil
}
func (s *parseStoreFake) LoadIngestionRequest(context.Context, string) (*pb.IngestionRequest, error) {
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	return proto.Clone(s.request).(*pb.IngestionRequest), nil
}
func (s *parseStoreFake) LoadLatestCheckpoint(context.Context, string) (*pb.Checkpoint, error) {
	if s.priorCheckpoint == nil {
		return nil, domain.ErrNotFound
	}
	return proto.Clone(s.priorCheckpoint).(*pb.Checkpoint), nil
}
func (s *parseStoreFake) LoadArtifact(context.Context, string, string) (*pb.ArtifactRef, error) {
	if s.priorArtifact == nil {
		return nil, errors.New("artifact missing")
	}
	return proto.Clone(s.priorArtifact).(*pb.ArtifactRef), nil
}
func (s *parseStoreFake) RegisterArtifact(_ context.Context, _ string, artifact *pb.ArtifactRef) error {
	s.registered = proto.Clone(artifact).(*pb.ArtifactRef)
	return nil
}
func (s *parseStoreFake) CancellationRequested(context.Context, string, string, uint64) (bool, error) {
	return s.cancelled, nil
}
func (s *parseStoreFake) CompleteWorkerAttempt(_ context.Context, _, _ string, _ uint64, desired pb.JobState, retryDelay time.Duration) (pb.JobState, error) {
	s.retryDelay = retryDelay
	if s.cancelled {
		desired = pb.JobState_JOB_STATE_CANCELLED
	}
	s.transitions = append(s.transitions, desired)
	return desired, nil
}

type parseWorkerFake struct {
	completion       pb.CompletionStatus
	err              error
	calls            int
	cancelCalls      int
	blockUntilCancel bool
	deadlineObserved bool
	mutateResponse   func(*pb.ProcessBatchResponse)
}

func (w *parseWorkerFake) ProcessBatch(ctx context.Context, request *pb.ProcessBatchRequest) (*pb.ProcessBatchResponse, error) {
	w.calls++
	deadline, ok := ctx.Deadline()
	w.deadlineObserved = ok && deadline.Equal(request.Context.Deadline.AsTime())
	if w.blockUntilCancel {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if w.err != nil {
		return nil, w.err
	}
	artifact := parseArtifact("document-batch:fixture", "batches/fixture.pb", "b")
	checkpoint := &pb.Checkpoint{
		Meta:  &pb.RecordMeta{SchemaVersion: 1, CorpusId: request.Context.CorpusId, RecordId: "checkpoint:fixture"},
		JobId: request.JobId, Stage: request.Stages[0],
		CompletedBatchKeys: []string{artifact.ArtifactId}, ArtifactHashes: []*pb.ContentHash{proto.Clone(artifact.ContentHash).(*pb.ContentHash)},
		Manifest: parseManifest(), Fence: request.Lease.Fence, TerminalStatus: w.completion,
	}
	response := &pb.ProcessBatchResponse{
		RequestId: request.Context.RequestId, JobId: request.JobId, Attempt: request.Attempt,
		Fence: request.Lease.Fence, Checkpoint: checkpoint, DocumentBatch: artifact, Status: w.completion,
	}
	if w.completion == pb.CompletionStatus_COMPLETION_STATUS_FAILED {
		response.Errors = []*pb.OperationError{{Code: pb.ErrorCode_ERROR_CODE_NOT_IMPLEMENTED, SafeMessage: "requires OCR", Stage: "parse"}}
	}
	if w.mutateResponse != nil {
		w.mutateResponse(response)
	}
	return response, nil
}

func TestStructureExecutorUsesCheckpointBoundDocumentBatch(t *testing.T) {
	store := parseFixtureStore(false)
	store.claimParseErr = domain.ErrLeaseUnavailable
	store.job.Stage = pb.JobStage_JOB_STAGE_STRUCTURE
	store.job.Attempt = 2
	store.job.StageAttempt = 1
	store.job.LeaseFence = 2
	store.priorArtifact = parseArtifact("document-batch:parse", "objects/parse.pb", "d")
	store.priorArtifact.MediaType = "application/vnd.regulagraph.document-batch+protobuf"
	store.priorCheckpoint = &pb.Checkpoint{
		Meta:  &pb.RecordMeta{SchemaVersion: 1, CorpusId: store.job.CorpusID, RecordId: "checkpoint:parse"},
		JobId: store.job.JobID, Stage: pb.JobStage_JOB_STAGE_PARSE,
		CompletedBatchKeys: []string{store.priorArtifact.ArtifactId},
		ArtifactHashes:     []*pb.ContentHash{proto.Clone(store.priorArtifact.ContentHash).(*pb.ContentHash)},
		Manifest:           parseManifest(), Fence: 1, TerminalStatus: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED,
	}
	worker := &parseWorkerFake{completion: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED}
	executor, _ := NewParseExecutor(store, worker, parseExecutorConfig())
	_, response, err := executor.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if response.Checkpoint.Stage != pb.JobStage_JOB_STAGE_STRUCTURE || store.checkpoint.Stage != pb.JobStage_JOB_STAGE_STRUCTURE {
		t.Fatal("STRUCTURE checkpoint was not persisted")
	}
	if got := store.transitions; len(got) != 1 || got[0] != pb.JobState_JOB_STATE_STAGED {
		t.Fatalf("unexpected STRUCTURE transition: %v", got)
	}
}

func TestStructureExecutorRecoversCheckpointWithoutRerunningWorker(t *testing.T) {
	store := parseFixtureStore(false)
	store.claimParseErr = domain.ErrLeaseUnavailable
	store.job.Stage = pb.JobStage_JOB_STAGE_STRUCTURE
	store.job.Attempt = 3
	store.job.StageAttempt = 2
	store.job.LeaseFence = 3
	store.priorArtifact = parseArtifact("document-batch:structure", "objects/structure.pb", "e")
	store.priorArtifact.MediaType = "application/vnd.regulagraph.document-batch+protobuf"
	store.priorCheckpoint = &pb.Checkpoint{
		Meta:  &pb.RecordMeta{SchemaVersion: 1, CorpusId: store.job.CorpusID, RecordId: "checkpoint:structure:prior"},
		JobId: store.job.JobID, Stage: pb.JobStage_JOB_STAGE_STRUCTURE, Fence: 2,
		CompletedBatchKeys: []string{store.priorArtifact.ArtifactId},
		ArtifactHashes:     []*pb.ContentHash{proto.Clone(store.priorArtifact.ContentHash).(*pb.ContentHash)},
		Manifest:           parseManifest(), TerminalStatus: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED,
	}
	worker := &parseWorkerFake{completion: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED}
	executor, _ := NewParseExecutor(store, worker, parseExecutorConfig())
	_, response, err := executor.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if worker.calls != 0 || response.GetDocumentBatch().GetArtifactId() != store.priorArtifact.ArtifactId {
		t.Fatalf("recovery reran worker or changed output: calls=%d response=%v", worker.calls, response)
	}
	if store.checkpoint.GetFence() != store.job.LeaseFence || store.checkpoint.GetMeta().GetRecordId() == store.priorCheckpoint.GetMeta().GetRecordId() {
		t.Fatalf("recovery checkpoint did not bind new fence: %v", store.checkpoint)
	}
	if got := store.transitions; len(got) != 1 || got[0] != pb.JobState_JOB_STATE_STAGED {
		t.Fatalf("unexpected recovery transition: %v", got)
	}
}

func TestParseExecutorRecoversFailedOutcomeWithoutRerunningWorker(t *testing.T) {
	store := parseFixtureStore(false)
	store.job.Attempt = 2
	store.job.StageAttempt = 1
	store.job.LeaseFence = 2
	store.priorArtifact = parseArtifact("document-batch:partial", "objects/partial.pb", "d")
	store.priorArtifact.MediaType = "application/vnd.regulagraph.document-batch+protobuf"
	store.priorCheckpoint = &pb.Checkpoint{
		Meta:  &pb.RecordMeta{SchemaVersion: 1, CorpusId: store.job.CorpusID, RecordId: "checkpoint:parse:partial"},
		JobId: store.job.JobID, Stage: pb.JobStage_JOB_STAGE_PARSE, Fence: 1,
		CompletedBatchKeys: []string{store.priorArtifact.ArtifactId},
		ArtifactHashes:     []*pb.ContentHash{proto.Clone(store.priorArtifact.ContentHash).(*pb.ContentHash)},
		Manifest:           parseManifest(), TerminalStatus: pb.CompletionStatus_COMPLETION_STATUS_FAILED,
	}
	worker := &parseWorkerFake{}
	executor, _ := NewParseExecutor(store, worker, parseExecutorConfig())
	_, response, err := executor.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if worker.calls != 0 || response.Status != pb.CompletionStatus_COMPLETION_STATUS_FAILED {
		t.Fatalf("PARSE recovery reran worker or changed outcome: calls=%d response=%v", worker.calls, response)
	}
	if got := store.transitions; len(got) != 1 || got[0] != pb.JobState_JOB_STATE_WAITING_REVIEW {
		t.Fatalf("partial PARSE recovery was not routed to review: %v", got)
	}
}

func TestParseExecutorRerunsLegacyCheckpointWithoutTerminalOutcome(t *testing.T) {
	store := parseFixtureStore(false)
	store.job.Attempt = 2
	store.job.StageAttempt = 2
	store.job.LeaseFence = 2
	store.priorCheckpoint = &pb.Checkpoint{
		Meta:  &pb.RecordMeta{SchemaVersion: 1, CorpusId: store.job.CorpusID, RecordId: "checkpoint:parse:legacy"},
		JobId: store.job.JobID, Stage: pb.JobStage_JOB_STAGE_PARSE, Fence: 1,
		Manifest: parseManifest(),
	}
	worker := &parseWorkerFake{completion: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED}
	executor, _ := NewParseExecutor(store, worker, parseExecutorConfig())
	_, response, err := executor.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if worker.calls != 1 || response.Status != pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED {
		t.Fatalf("legacy checkpoint was inferred as terminal: calls=%d response=%v", worker.calls, response)
	}
	if got := store.transitions; len(got) != 1 || got[0] != pb.JobState_JOB_STATE_STAGED {
		t.Fatalf("legacy checkpoint rerun did not use the worker outcome: %v", got)
	}
}

func TestStructureExecutorRejectsMismatchedRecoveryArtifact(t *testing.T) {
	store := parseFixtureStore(false)
	store.claimParseErr = domain.ErrLeaseUnavailable
	store.job.Stage = pb.JobStage_JOB_STAGE_STRUCTURE
	store.job.Attempt = 3
	store.job.StageAttempt = 2
	store.job.LeaseFence = 3
	store.priorArtifact = parseArtifact("document-batch:structure", "objects/structure.pb", "e")
	store.priorCheckpoint = &pb.Checkpoint{
		Meta:  &pb.RecordMeta{SchemaVersion: 1, CorpusId: store.job.CorpusID, RecordId: "checkpoint:structure:prior"},
		JobId: store.job.JobID, Stage: pb.JobStage_JOB_STAGE_STRUCTURE, Fence: 2,
		CompletedBatchKeys: []string{store.priorArtifact.ArtifactId}, ArtifactHashes: []*pb.ContentHash{parseHash("f")},
		Manifest: parseManifest(), TerminalStatus: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED,
	}
	worker := &parseWorkerFake{}
	executor, _ := NewParseExecutor(store, worker, parseExecutorConfig())
	_, _, err := executor.RunOnce(context.Background())
	if status.Code(err) != codes.FailedPrecondition || worker.calls != 0 {
		t.Fatalf("mismatched recovery was not rejected: calls=%d err=%v", worker.calls, err)
	}
	if got := store.transitions; len(got) != 1 || got[0] != pb.JobState_JOB_STATE_FAILED {
		t.Fatalf("unexpected mismatch transition: %v", got)
	}
}
func (w *parseWorkerFake) Cancel(context.Context, *pb.WorkerStatusRequest) (*pb.WorkerStatusResponse, error) {
	w.cancelCalls++
	return nil, nil
}

func TestParseExecutorPersistsCheckpointAndStagesCompleteBatch(t *testing.T) {
	store := parseFixtureStore(false)
	worker := &parseWorkerFake{completion: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED}
	executor, err := NewParseExecutor(store, worker, parseExecutorConfig())
	if err != nil {
		t.Fatal(err)
	}
	_, response, err := executor.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if response.GetDocumentBatch() == nil || store.registered == nil || store.checkpoint == nil || !worker.deadlineObserved {
		t.Fatal("artifact and checkpoint handoff were not persisted")
	}
	if got := store.transitions; len(got) != 1 || got[0] != pb.JobState_JOB_STATE_STAGED {
		t.Fatalf("unexpected transitions: %v", got)
	}
}

func TestParseExecutorRoutesIncompleteBatchToReview(t *testing.T) {
	store := parseFixtureStore(false)
	worker := &parseWorkerFake{completion: pb.CompletionStatus_COMPLETION_STATUS_FAILED}
	executor, _ := NewParseExecutor(store, worker, parseExecutorConfig())
	if _, _, err := executor.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := store.transitions; len(got) != 1 || got[0] != pb.JobState_JOB_STATE_WAITING_REVIEW {
		t.Fatalf("unexpected transitions: %v", got)
	}
}

func TestParseExecutorRejectsUnacquiredURLAsPermanentFailure(t *testing.T) {
	store := parseFixtureStore(true)
	worker := &parseWorkerFake{}
	executor, _ := NewParseExecutor(store, worker, parseExecutorConfig())
	_, _, err := executor.RunOnce(context.Background())
	if err == nil || !strings.Contains(err.Error(), "requires acquired blob") || worker.calls != 0 {
		t.Fatalf("unexpected result calls=%d err=%v", worker.calls, err)
	}
	if got := store.transitions; len(got) != 1 || got[0] != pb.JobState_JOB_STATE_FAILED {
		t.Fatalf("unexpected transitions: %v", got)
	}
}

func TestParseExecutorRetriesUnavailableWorker(t *testing.T) {
	store := parseFixtureStore(false)
	worker := &parseWorkerFake{err: status.Error(codes.Unavailable, "offline")}
	executor, _ := NewParseExecutor(store, worker, parseExecutorConfig())
	if _, _, err := executor.RunOnce(context.Background()); status.Code(err) != codes.Unavailable {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := store.transitions; len(got) != 1 || got[0] != pb.JobState_JOB_STATE_RETRY_WAIT {
		t.Fatalf("unexpected transitions: %v", got)
	}
	if store.retryDelay != time.Second {
		t.Fatalf("first retry delay=%s", store.retryDelay)
	}
}

func TestParseRetryDelayIsExponentiallyBounded(t *testing.T) {
	executor, _ := NewParseExecutor(parseFixtureStore(false), &parseWorkerFake{}, ParseExecutorConfig{
		OwnerID: "executor:fixture", AuthScope: "scope:ingestion", Lease: 10 * time.Second,
		CallTimeout: 2 * time.Second, RetryBase: 2 * time.Second, RetryMax: 10 * time.Second,
	})
	for attempt, expected := range map[uint32]time.Duration{1: 2 * time.Second, 2: 4 * time.Second, 3: 8 * time.Second, 4: 10 * time.Second, 32: 10 * time.Second} {
		if got := executor.retryDelay(attempt); got != expected {
			t.Fatalf("attempt %d delay=%s want=%s", attempt, got, expected)
		}
	}
}

func TestParseExecutorDoesNotRetryPersistentStorageCorruption(t *testing.T) {
	store := parseFixtureStore(false)
	store.loadErr = domain.ErrPersistentIntegrity
	executor, _ := NewParseExecutor(store, &parseWorkerFake{}, parseExecutorConfig())
	_, _, err := executor.RunOnce(context.Background())
	if !errors.Is(err, domain.ErrPersistentIntegrity) {
		t.Fatalf("persistent integrity error lost: %v", err)
	}
	if got := store.transitions; len(got) != 1 || got[0] != pb.JobState_JOB_STATE_FAILED {
		t.Fatalf("persistent corruption was retried: %v", got)
	}
}

func TestParseExecutorPropagatesDurableCancellation(t *testing.T) {
	store := parseFixtureStore(false)
	store.cancelled = true
	worker := &parseWorkerFake{blockUntilCancel: true}
	config := parseExecutorConfig()
	config.CancellationPoll = time.Millisecond
	executor, _ := NewParseExecutor(store, worker, config)
	_, _, err := executor.RunOnce(context.Background())
	if !errors.Is(err, errJobCancellationRequested) || worker.cancelCalls != 1 {
		t.Fatalf("durable cancellation not propagated: cancel calls=%d err=%v", worker.cancelCalls, err)
	}
	if got := store.transitions; len(got) != 1 || got[0] != pb.JobState_JOB_STATE_CANCELLED {
		t.Fatalf("unexpected transitions: %v", got)
	}
}

func TestParseExecutorRetriesCoordinatorShutdown(t *testing.T) {
	store := parseFixtureStore(false)
	worker := &parseWorkerFake{blockUntilCancel: true}
	executor, _ := NewParseExecutor(store, worker, parseExecutorConfig())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := executor.RunOnce(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected shutdown error: %v", err)
	}
	if got := store.transitions; len(got) != 1 || got[0] != pb.JobState_JOB_STATE_RETRY_WAIT {
		t.Fatalf("shutdown made job terminal: %v", got)
	}
}

func TestParseExecutorCancellationWinsAfterCheckpoint(t *testing.T) {
	store := parseFixtureStore(false)
	store.cancelOnSave = true
	worker := &parseWorkerFake{completion: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED}
	executor, _ := NewParseExecutor(store, worker, parseExecutorConfig())
	_, _, err := executor.RunOnce(context.Background())
	if !errors.Is(err, errJobCancellationRequested) {
		t.Fatalf("late durable cancellation not reported: %v", err)
	}
	if got := store.transitions; len(got) != 1 || got[0] != pb.JobState_JOB_STATE_CANCELLED {
		t.Fatalf("late cancellation did not win atomically: %v", got)
	}
}

func TestParseExecutorRejectsForgedOrUnexpectedOutputsPermanently(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*pb.ProcessBatchResponse)
	}{
		{name: "forged checkpoint", mutate: func(response *pb.ProcessBatchResponse) {
			response.Checkpoint.CompletedBatchKeys[0] = "document-batch:forged"
		}},
		{name: "unexpected graph output", mutate: func(response *pb.ProcessBatchResponse) {
			graph := parseArtifact("graph-delta:forged", "graphs/forged.pb", "d")
			response.GraphDelta = graph
			response.Checkpoint.CompletedBatchKeys = append(response.Checkpoint.CompletedBatchKeys, graph.ArtifactId)
			response.Checkpoint.ArtifactHashes = append(response.Checkpoint.ArtifactHashes, graph.ContentHash)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := parseFixtureStore(false)
			worker := &parseWorkerFake{completion: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED, mutateResponse: test.mutate}
			executor, _ := NewParseExecutor(store, worker, parseExecutorConfig())
			_, _, err := executor.RunOnce(context.Background())
			if status.Code(err) != codes.FailedPrecondition || store.registered != nil || store.checkpoint != nil {
				t.Fatalf("unsafe output was not rejected before persistence: err=%v", err)
			}
			if got := store.transitions; len(got) != 1 || got[0] != pb.JobState_JOB_STATE_FAILED {
				t.Fatalf("unsafe output was not terminal: %v", got)
			}
		})
	}
}

func TestParseExecutorBoundsCorrelationIDForMaximumJobID(t *testing.T) {
	store := parseFixtureStore(false)
	store.job.JobID = strings.Repeat("j", 256)
	worker := &parseWorkerFake{completion: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED}
	executor, _ := NewParseExecutor(store, worker, parseExecutorConfig())
	_, _, err := executor.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
}

func parseFixtureStore(url bool) *parseStoreFake {
	now := time.Now()
	locator := &pb.SourceLocator{PortalId: "bpk"}
	if url {
		locator.Locator = &pb.SourceLocator_Url{Url: "https://example.invalid/source.pdf"}
	} else {
		locator.Locator = &pb.SourceLocator_Blob{Blob: parseArtifact("source:fixture", "inputs/source.pdf", "a")}
	}
	return &parseStoreFake{
		job: domain.JobRecord{
			JobID: "job:fixture", CorpusID: "corpus:fixture", State: pb.JobState_JOB_STATE_RUNNING,
			Stage: pb.JobStage_JOB_STAGE_PARSE, Attempt: 1, StageAttempt: 1, LeaseOwner: "executor:fixture", LeaseFence: 1,
			LeaseExpiresAt: now.Add(10 * time.Second),
		},
		request: &pb.IngestionRequest{
			CorpusId: "corpus:fixture", Sources: []*pb.SourceLocator{locator}, Operation: pb.JobOperation_JOB_OPERATION_INGEST,
			IdempotencyKey: "ingest:fixture", ConfigManifest: parseManifest(),
		},
	}
}

func parseExecutorConfig() ParseExecutorConfig {
	return ParseExecutorConfig{OwnerID: "executor:fixture", AuthScope: "scope:ingestion", Lease: 10 * time.Second, CallTimeout: 2 * time.Second}
}

func parseManifest() *pb.ProducerManifest {
	return &pb.ProducerManifest{Software: "regulagraph-server", Build: "test", SchemaVersion: 1, ConfigHash: parseHash("c")}
}

func parseArtifact(id, key, hash string) *pb.ArtifactRef {
	return &pb.ArtifactRef{ArtifactId: id, ContentHash: parseHash(hash), StorageKey: key, MediaType: "application/pdf", ByteSize: 1, SchemaVersion: 1}
}

func parseHash(value string) *pb.ContentHash {
	return &pb.ContentHash{Sha256: strings.Repeat(value, 64)}
}
