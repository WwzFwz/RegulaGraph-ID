// Boundary tests enforce exact batch correlation, explicit partial errors and publication readiness.
// Synthetic manifests test invariants only; they do not substitute for real storage recovery/load tests.
package domain

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"strings"
	"testing"
	"time"
)

func hashFixture() *pb.ContentHash { return &pb.ContentHash{Sha256: strings.Repeat("a", 64)} }
func modelFixture() *pb.ModelManifest {
	return &pb.ModelManifest{ModelId: "model-test", Version: "v1", WeightsHash: hashFixture(), TokenizerHash: hashFixture(), Task: pb.ModelTask_MODEL_TASK_EMBED, Dimensions: proto.Uint32(2), MaxTokens: 128, Precision: "fp32", Backend: "test"}
}
func TestEmbeddingCorrelation(t *testing.T) {
	req := &pb.EmbedBatchRequest{Context: &pb.RequestContext{SchemaVersion: 1, RequestId: "req-1", TraceId: "trace-1", CorpusId: "corpus-1", Deadline: timestamppb.New(time.Now().Add(time.Minute)), ConfigFingerprint: hashFixture(), AuthScopeRef: "scope-test"}, Model: modelFixture(), Purpose: pb.EmbeddingPurpose_EMBEDDING_PURPOSE_QUERY, OperationKey: "op-1", Items: []*pb.TextItem{{ItemId: "a", Text: "a"}, {ItemId: "b", Text: "b"}}}
	result := func(id string) *pb.EmbeddingResult {
		return &pb.EmbeddingResult{ItemId: id, Result: &pb.EmbeddingResult_Embedding{Embedding: &pb.Embedding{Values: []float32{0.1, 0.2}, Truncation: &pb.TruncationInfo{}}}}
	}
	res := &pb.EmbedBatchResponse{RequestId: "req-1", Model: proto.Clone(req.Model).(*pb.ModelManifest), Results: []*pb.EmbeddingResult{result("b"), result("a")}}
	if e := VerifyEmbeddingResults(req, res); e != nil {
		t.Fatal(e)
	}
	res.Results[1] = result("b")
	if VerifyEmbeddingResults(req, res) == nil {
		t.Fatal("duplicate response accepted")
	}
	res.Results = []*pb.EmbeddingResult{result("a")}
	if VerifyEmbeddingResults(req, res) == nil {
		t.Fatal("missing response accepted")
	}
	res.Results = []*pb.EmbeddingResult{result("a"), {ItemId: "b", Result: &pb.EmbeddingResult_Error{Error: &pb.OperationError{Code: pb.ErrorCode_ERROR_CODE_UNAVAILABLE, SafeMessage: "failed"}}}}
	if e := VerifyEmbeddingResults(req, res); e != nil {
		t.Fatal("explicit per-item error must preserve correlation", e)
	}
	res.Model.Version = "different"
	if VerifyEmbeddingResults(req, res) == nil {
		t.Fatal("model drift accepted")
	}
}

func TestWorkerCheckpointBindsRequestedStageAndOutputs(t *testing.T) {
	req := &pb.ProcessBatchRequest{
		Context: &pb.RequestContext{SchemaVersion: 1, RequestId: "request-1", TraceId: "trace-1", CorpusId: "corpus-1", Deadline: timestamppb.New(time.Now().Add(time.Minute)), ConfigFingerprint: hashFixture(), AuthScopeRef: "scope-test"},
		JobId:   "job-1", Attempt: 1,
		Lease:    &pb.Lease{OwnerId: "worker-1", Fence: 7, ExpiresAt: timestamppb.New(time.Now().Add(2 * time.Minute))},
		Sources:  []*pb.ArtifactRef{{ArtifactId: "source-1", ContentHash: hashFixture(), StorageKey: "inputs/source.pdf", MediaType: "application/pdf", ByteSize: 1, SchemaVersion: 1}},
		Manifest: &pb.ProducerManifest{Software: "test", Build: "test", SchemaVersion: 1, ConfigHash: hashFixture()},
		Stages:   []pb.JobStage{pb.JobStage_JOB_STAGE_PARSE},
	}
	output := &pb.ArtifactRef{ArtifactId: "batch-1", ContentHash: hashFixture(), StorageKey: "batches/batch.pb", MediaType: "application/x-protobuf", ByteSize: 1, SchemaVersion: 1}
	res := &pb.ProcessBatchResponse{
		RequestId: req.Context.RequestId, JobId: req.JobId, Attempt: req.Attempt, Fence: req.Lease.Fence,
		Checkpoint: &pb.Checkpoint{
			Meta:  &pb.RecordMeta{SchemaVersion: 1, CorpusId: req.Context.CorpusId, RecordId: "checkpoint-1"},
			JobId: req.JobId, Stage: pb.JobStage_JOB_STAGE_PARSE, CompletedBatchKeys: []string{output.ArtifactId},
			ArtifactHashes: []*pb.ContentHash{proto.Clone(output.ContentHash).(*pb.ContentHash)}, Manifest: proto.Clone(req.Manifest).(*pb.ProducerManifest), Fence: req.Lease.Fence,
		},
		DocumentBatch: output, Status: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED,
	}
	if err := VerifyWorkerResponse(req, res); err != nil {
		t.Fatal(err)
	}
	res.Checkpoint.CompletedBatchKeys[0] = "batch-forged"
	if VerifyWorkerResponse(req, res) == nil {
		t.Fatal("checkpoint referencing a different output was accepted")
	}
	res.Checkpoint.CompletedBatchKeys[0] = output.ArtifactId
	res.Checkpoint.Stage = pb.JobStage_JOB_STAGE_INDEX
	if VerifyWorkerResponse(req, res) == nil {
		t.Fatal("checkpoint for an unrequested stage was accepted")
	}
	res.Checkpoint.Stage = pb.JobStage_JOB_STAGE_STRUCTURE
	req.Stages = []pb.JobStage{pb.JobStage_JOB_STAGE_STRUCTURE}
	if err := VerifyWorkerResponse(req, res); err != nil {
		t.Fatal("STRUCTURE document batch should be accepted", err)
	}
}
func TestPublicationRequiresAcknowledgedMatchingBackend(t *testing.T) {
	m := &pb.PublicationManifest{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: "c", RecordId: "publication-1"}, SnapshotRef: &pb.SnapshotRef{CorpusId: "c", SnapshotId: "snapshot-1", Sequence: 1, ManifestHash: hashFixture(), RepresentationGeneration: "generation-1"}, Fence: 1, ValidationReport: &pb.ValidationReport{Valid: true}, BackendGenerations: []*pb.BackendGeneration{{Backend: pb.BackendKind_BACKEND_KIND_NEO4J, Generation: "generation-1", ExpectedCounts: &pb.Counts{Expected: 2, Accepted: 2}, OperationsChecksum: hashFixture()}}}
	if VerifyPublicationReady(m, nil, 1) == nil {
		t.Fatal("missing receipt accepted")
	}
	r := &pb.BackendReceipt{PublicationId: "publication-1", Backend: pb.BackendKind_BACKEND_KIND_NEO4J, Generation: "generation-1", OperationsChecksum: hashFixture(), Counts: &pb.Counts{Expected: 2, Accepted: 2}, DurableAck: true, SearchReady: true, Fence: 1}
	m.Acknowledgements = []*pb.BackendReceipt{r}
	if e := VerifyPublicationReady(m, nil, 1); e != nil {
		t.Fatal(e)
	}
	r.SearchReady = false
	if VerifyPublicationReady(m, nil, 1) == nil {
		t.Fatal("enqueue treated as ready")
	}
	r.SearchReady = true
	r.Fence = 2
	if VerifyPublicationReady(m, nil, 1) == nil {
		t.Fatal("stale fence accepted")
	}
}

func TestCitationTrustedURLAndSourceLocator(t *testing.T) {
	meta := func(id string) *pb.RecordMeta { return &pb.RecordMeta{SchemaVersion: 1, CorpusId: "c", RecordId: id} }
	snapshot := &pb.SnapshotRef{CorpusId: "c", SnapshotId: "s", Sequence: 1, ManifestHash: hashFixture(), RepresentationGeneration: "g"}
	producer := &pb.ProducerManifest{Software: "test", Build: "test", SchemaVersion: 1, ConfigHash: hashFixture()}
	locator := &pb.PageLocator{SourceBlobId: "source-a", PageNumber: 1}
	other := &pb.PageLocator{SourceBlobId: "source-b", PageNumber: 2}
	evidence := &pb.Evidence{Meta: meta("e"), SourceRefs: []*pb.SourceVersionRef{{SourceBlobId: "source-a", ProvisionVersionId: "v", RegulationId: "r"}}, Text: "Pasal", SourceSpans: []*pb.TextSpan{{TextArtifactId: "t", EndByte: 5}}, Locators: []*pb.PageLocator{locator, other}, SnapshotRef: snapshot, LegalStatus: pb.LegalStatus_LEGAL_STATUS_ACTIVE}
	bundle := &pb.EvidenceBundle{Meta: meta("bundle"), Items: []*pb.Evidence{evidence}, Completeness: pb.Completeness_COMPLETENESS_COMPLETE, RetrievalManifest: producer, Snapshot: snapshot, CompletionStatus: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED}
	citation := &pb.Citation{CitationId: "citation", ClaimIds: []string{"claim"}, EvidenceId: "e", ProvisionVersionId: "v", SourceUrl: "https://example.org/a.pdf", PageLocator: locator}
	answer := &pb.Answer{Meta: meta("answer"), RequestId: "request", Text: "Pasal", Claims: []*pb.Claim{{ClaimId: "claim", AnswerTextSpan: &pb.AnswerTextSpan{EndByte: 5}, EvidenceIds: []string{"e"}, SupportStatus: pb.SupportStatus_SUPPORT_STATUS_SUPPORTED}}, Citations: []*pb.Citation{citation}, SemanticStatus: pb.SemanticStatus_SEMANTIC_STATUS_COMPLETE, CompletionStatus: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED, Snapshot: snapshot, EffectiveDates: []*pb.CalendarDate{{Year: 2026, Month: 1, Day: 1}}, RunManifest: producer}
	lookup := func(blob, version string) ([]string, error) {
		if blob != "source-a" || version != "v" {
			t.Fatalf("wrong source lookup: %s/%s", blob, version)
		}
		return []string{"https://example.org/a.pdf"}, nil
	}
	if err := VerifyCitationEvidence(answer, bundle, lookup); err != nil {
		t.Fatal(err)
	}
	if VerifyCitationEvidence(answer, bundle, nil) == nil {
		t.Fatal("missing trusted metadata accepted")
	}
	citation.SourceUrl = "https://example.org/forged.pdf"
	if VerifyCitationEvidence(answer, bundle, lookup) == nil {
		t.Fatal("invented URL accepted")
	}
	citation.SourceUrl = "https://example.org/a.pdf"
	citation.PageLocator = other
	if VerifyCitationEvidence(answer, bundle, lookup) == nil {
		t.Fatal("URL and locator from different blobs accepted")
	}
}
