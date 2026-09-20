// Exercises the durable BIND workflow from a checkpoint-bound STRUCTURE artifact through exact
// registry batches, immutable output, fenced checkpoint, partial review, and crash recovery.
// Fixtures prove orchestration invariants only; registry quality and performance remain unmeasured.
package workflows

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type bindingStoreFake struct {
	job             domain.JobRecord
	checkpoint      *pb.Checkpoint
	input           *pb.ArtifactRef
	registered      *pb.ArtifactRef
	completed       pb.JobState
	registryCalls   int
	registryPhases  []string
	dependencies    *pb.DependencyManifest
	cancelRequested bool
	checkpointErr   error
	saveErr         error
}

func (store *bindingStoreFake) ClaimBindJob(context.Context, string, time.Duration) (domain.JobRecord, error) {
	return store.job, nil
}
func (store *bindingStoreFake) LoadLatestCheckpoint(context.Context, string) (*pb.Checkpoint, error) {
	if store.checkpointErr != nil {
		return nil, store.checkpointErr
	}
	if store.checkpoint == nil {
		return nil, domain.ErrNotFound
	}
	return proto.Clone(store.checkpoint).(*pb.Checkpoint), nil
}
func (store *bindingStoreFake) LoadArtifact(context.Context, string, string) (*pb.ArtifactRef, error) {
	if store.input == nil && store.registered == nil {
		return nil, domain.ErrNotFound
	}
	if store.checkpoint.Stage == pb.JobStage_JOB_STAGE_BIND {
		return proto.Clone(store.registered).(*pb.ArtifactRef), nil
	}
	return proto.Clone(store.input).(*pb.ArtifactRef), nil
}
func (store *bindingStoreFake) RegisterArtifact(_ context.Context, _ string, artifact *pb.ArtifactRef) error {
	store.registered = proto.Clone(artifact).(*pb.ArtifactRef)
	return nil
}
func (store *bindingStoreFake) ReplaceArtifactDependencyManifest(_ context.Context, _, _ string, manifest *pb.DependencyManifest) error {
	store.dependencies = proto.Clone(manifest).(*pb.DependencyManifest)
	return nil
}
func (store *bindingStoreFake) ResolveCanonicalIdentities(
	_ context.Context,
	_ string,
	operation string,
	_ uint64,
	claims []domain.CanonicalIdentityClaim,
) ([]domain.CanonicalIdentityAssignment, uint64, error) {
	store.registryCalls++
	store.registryPhases = append(store.registryPhases, operation)
	revision := uint64(store.registryCalls)
	assignments := make([]domain.CanonicalIdentityAssignment, len(claims))
	for index, claim := range claims {
		prefix := "canonical:entity:"
		switch claim.EntityType {
		case domain.CanonicalEntityTypeOrganization:
			prefix = "canonical:organization:"
		case domain.CanonicalEntityTypeRegulation:
			prefix = "canonical:regulation:"
		case domain.CanonicalEntityTypeProvision:
			prefix = "canonical:provision:"
		}
		assignments[index] = domain.CanonicalIdentityAssignment{
			ProposalKey: claim.ProposalKey, CanonicalID: prefix + strconv.Itoa(index+1), Revision: revision, Created: true,
		}
	}
	return assignments, revision, nil
}
func (store *bindingStoreFake) CancellationRequested(context.Context, string, string, uint64) (bool, error) {
	return store.cancelRequested, nil
}
func (store *bindingStoreFake) SaveCheckpoint(_ context.Context, checkpoint *pb.Checkpoint, _ string) error {
	if store.saveErr != nil {
		return store.saveErr
	}
	store.checkpoint = proto.Clone(checkpoint).(*pb.Checkpoint)
	return nil
}
func (store *bindingStoreFake) CompleteWorkerAttempt(_ context.Context, _, _ string, _ uint64, desired pb.JobState, _ time.Duration) (pb.JobState, error) {
	store.completed = desired
	return desired, nil
}

type bindingArtifactsFake struct {
	inputRef  *pb.ArtifactRef
	input     []byte
	outputRef *pb.ArtifactRef
	output    []byte
}

func (store *bindingArtifactsFake) ReadVerified(_ context.Context, ref *pb.ArtifactRef, maximum uint64) ([]byte, error) {
	if store.outputRef != nil && ref.ArtifactId == store.outputRef.ArtifactId {
		if uint64(len(store.output)) > maximum {
			return nil, errors.New("unexpected binding output size")
		}
		return append([]byte(nil), store.output...), nil
	}
	if ref.ArtifactId != store.inputRef.ArtifactId || uint64(len(store.input)) > maximum {
		return nil, errors.New("unexpected binding input")
	}
	return append([]byte(nil), store.input...), nil
}
func (store *bindingArtifactsFake) Put(_ context.Context, ref *pb.ArtifactRef, reader io.Reader) (bool, error) {
	raw, err := io.ReadAll(reader)
	if err != nil {
		return false, err
	}
	digest := sha256.Sum256(raw)
	if ref.GetContentHash().GetSha256() != hex.EncodeToString(digest[:]) || ref.ByteSize != uint64(len(raw)) {
		return false, errors.New("output descriptor mismatch")
	}
	store.output = raw
	store.outputRef = proto.Clone(ref).(*pb.ArtifactRef)
	return false, nil
}

func TestBindingExecutorPersistsCanonicalDocumentAndStagesChunkInput(t *testing.T) {
	batch := bindingWorkflowBatch(true)
	store, artifacts := bindingWorkflowFakes(t, batch)
	executor, err := NewBindingExecutor(store, artifacts, bindingWorkflowConfig())
	if err != nil {
		t.Fatal(err)
	}
	job, result, err := executor.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if job.Stage != pb.JobStage_JOB_STAGE_BIND || result.Completeness != pb.Completeness_COMPLETENESS_COMPLETE ||
		store.completed != pb.JobState_JOB_STATE_STAGED || store.registryCalls != 3 || store.registered == nil ||
		store.dependencies == nil ||
		result.Checkpoint.Stage != pb.JobStage_JOB_STAGE_BIND || result.Checkpoint.TerminalStatus != pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED {
		t.Fatalf("unexpected BIND outcome: job=%+v result=%+v completed=%s calls=%d", job, result, store.completed, store.registryCalls)
	}
	bound := &pb.DocumentBatch{}
	if err = proto.Unmarshal(artifacts.output, bound); err != nil {
		t.Fatal(err)
	}
	if len(bound.Regulations) != 1 || len(bound.Editions) != 1 || len(bound.Provisions) != 2 || len(bound.Versions) != 2 {
		t.Fatalf("bound output record counts are incomplete: %#v", bound)
	}
	if bound.DependencyManifest.GetLookupScopeRevisions()[0].Revision != 3 {
		t.Fatal("final registry revision was not retained")
	}
}

func TestBindingExecutorRoutesUnresolvedRegulationToReviewWithoutRegistryWrites(t *testing.T) {
	batch := bindingWorkflowBatch(false)
	store, artifacts := bindingWorkflowFakes(t, batch)
	executor, err := NewBindingExecutor(store, artifacts, bindingWorkflowConfig())
	if err != nil {
		t.Fatal(err)
	}
	_, result, err := executor.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Completeness != pb.Completeness_COMPLETENESS_PARTIAL || store.completed != pb.JobState_JOB_STATE_WAITING_REVIEW ||
		store.registryCalls != 0 || result.Checkpoint.TerminalStatus != pb.CompletionStatus_COMPLETION_STATUS_FAILED {
		t.Fatalf("unresolved regulation was not routed to review: result=%+v state=%s calls=%d", result, store.completed, store.registryCalls)
	}
	bound := &pb.DocumentBatch{}
	if err = proto.Unmarshal(artifacts.output, bound); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, issue := range bound.Issues {
		found = found || issue.Code == "REGULATION_IDENTITY_REVIEW_REQUIRED"
	}
	if !found || !bound.GetDependencyManifest().GetLookupScopeRevisions()[0].EmptyResult {
		t.Fatal("review output lost unresolved identity or empty registry revision")
	}
}

func TestBindingExecutorRecoversCheckpointWithoutRegistryReplay(t *testing.T) {
	batch := bindingWorkflowBatch(true)
	store, artifacts := bindingWorkflowFakes(t, batch)
	executor, err := NewBindingExecutor(store, artifacts, bindingWorkflowConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = executor.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	registryCalls := store.registryCalls
	store.job.Attempt++
	store.job.LeaseFence++
	store.job.LeaseExpiresAt = time.Now().Add(time.Minute)
	store.completed = pb.JobState_JOB_STATE_UNSPECIFIED
	_, result, err := executor.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if store.registryCalls != registryCalls || store.completed != pb.JobState_JOB_STATE_STAGED || result.Checkpoint.Fence != store.job.LeaseFence {
		t.Fatalf("BIND recovery reran registry or lost fence: calls=%d state=%s result=%+v", store.registryCalls, store.completed, result)
	}
}

func TestBindingExecutorRejectsCorruptRecoveryPayload(t *testing.T) {
	store, artifacts := bindingWorkflowFakes(t, bindingWorkflowBatch(true))
	executor, err := NewBindingExecutor(store, artifacts, bindingWorkflowConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = executor.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	store.job.Attempt++
	store.job.LeaseFence++
	store.job.LeaseExpiresAt = time.Now().Add(time.Minute)
	store.completed = pb.JobState_JOB_STATE_UNSPECIFIED
	artifacts.output = []byte("corrupt")
	if _, _, err = executor.RunOnce(context.Background()); !errors.Is(err, domain.ErrPersistentIntegrity) {
		t.Fatalf("corrupt recovery payload was accepted: %v", err)
	}
	if store.completed != pb.JobState_JOB_STATE_FAILED {
		t.Fatalf("corrupt recovery payload did not terminate deterministically: %s", store.completed)
	}
}

func TestBindingExecutorSchedulesRetryWhenRecoveryCheckpointWriteFails(t *testing.T) {
	store, artifacts := bindingWorkflowFakes(t, bindingWorkflowBatch(true))
	executor, err := NewBindingExecutor(store, artifacts, bindingWorkflowConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = executor.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	store.job.Attempt++
	store.job.LeaseFence++
	store.job.LeaseExpiresAt = time.Now().Add(time.Minute)
	store.completed = pb.JobState_JOB_STATE_UNSPECIFIED
	store.saveErr = errors.New("checkpoint database temporarily unavailable")
	if _, _, err = executor.RunOnce(context.Background()); err == nil {
		t.Fatal("recovery checkpoint failure was hidden")
	}
	if store.completed != pb.JobState_JOB_STATE_RETRY_WAIT {
		t.Fatalf("recovery checkpoint failure did not schedule retry: %s", store.completed)
	}
}

func TestBindingExecutorBatchesRegistryClaimsDeterministically(t *testing.T) {
	store, artifacts := bindingWorkflowFakes(t, bindingWorkflowBatch(true))
	config := bindingWorkflowConfig()
	config.RegistryBatchSize = 10_000
	executor, err := NewBindingExecutor(store, artifacts, config)
	if err != nil {
		t.Fatal(err)
	}
	claims := make([]domain.CanonicalIdentityClaim, 10_001)
	for index := range claims {
		claims[index] = domain.CanonicalIdentityClaim{
			ProposalKey: fmt.Sprintf("proposal:%05d", 10_000-index), EntityType: domain.CanonicalEntityTypeProvision,
		}
	}
	assignments, revision, err := executor.resolveClaims(context.Background(), store.job, strings.Repeat("a", 64), "provision", claims)
	if err != nil {
		t.Fatal(err)
	}
	if len(assignments) != len(claims) || store.registryCalls != 2 || revision != 2 ||
		len(store.registryPhases) != 2 || store.registryPhases[0] == store.registryPhases[1] {
		t.Fatalf("registry batching lost deterministic closure: assignments=%d calls=%d revision=%d phases=%v",
			len(assignments), store.registryCalls, revision, store.registryPhases)
	}
}

func TestBindingExecutorHonorsCancellationBeforeRegistryMutation(t *testing.T) {
	store, artifacts := bindingWorkflowFakes(t, bindingWorkflowBatch(true))
	store.cancelRequested = true
	executor, err := NewBindingExecutor(store, artifacts, bindingWorkflowConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = executor.RunOnce(context.Background()); !errors.Is(err, errJobCancellationRequested) {
		t.Fatalf("cancellation was not returned: %v", err)
	}
	if store.completed != pb.JobState_JOB_STATE_CANCELLED || store.registryCalls != 0 || store.registered != nil {
		t.Fatalf("cancelled BIND mutated durable output: state=%s calls=%d artifact=%v", store.completed, store.registryCalls, store.registered)
	}
}

func TestBindingExecutorRetriesTransientCheckpointLoad(t *testing.T) {
	store, artifacts := bindingWorkflowFakes(t, bindingWorkflowBatch(true))
	store.checkpointErr = errors.New("database temporarily unavailable")
	executor, err := NewBindingExecutor(store, artifacts, bindingWorkflowConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = executor.RunOnce(context.Background()); err == nil {
		t.Fatal("transient checkpoint failure was hidden")
	}
	if store.completed != pb.JobState_JOB_STATE_RETRY_WAIT {
		t.Fatalf("transient checkpoint failure became terminal: %s", store.completed)
	}
}

func bindingWorkflowFakes(t *testing.T, batch *pb.DocumentBatch) (*bindingStoreFake, *bindingArtifactsFake) {
	t.Helper()
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	hash := hex.EncodeToString(digest[:])
	input := &pb.ArtifactRef{
		ArtifactId: "artifact:document-batch:" + hash, ContentHash: &pb.ContentHash{Sha256: hash},
		StorageKey: "sha256/aa/bb/" + hash + ".bin", MediaType: documentBatchMediaType,
		ByteSize: uint64(len(raw)), SchemaVersion: 1,
	}
	job := domain.JobRecord{
		JobID: "job:bind-fixture", CorpusID: "corpus:fixture", State: pb.JobState_JOB_STATE_RUNNING,
		Stage: pb.JobStage_JOB_STAGE_BIND, Attempt: 3, StageAttempt: 1, LeaseOwner: "executor:bind",
		LeaseFence: 3, LeaseExpiresAt: time.Now().Add(time.Minute),
	}
	checkpoint := &pb.Checkpoint{
		Meta:  &pb.RecordMeta{SchemaVersion: 1, CorpusId: job.CorpusID, RecordId: "checkpoint:structure"},
		JobId: job.JobID, Stage: pb.JobStage_JOB_STAGE_STRUCTURE, CompletedBatchKeys: []string{input.ArtifactId},
		ArtifactHashes: []*pb.ContentHash{proto.Clone(input.ContentHash).(*pb.ContentHash)}, Manifest: bindingProducer(),
		Fence: 2, TerminalStatus: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED,
	}
	return &bindingStoreFake{job: job, checkpoint: checkpoint, input: input}, &bindingArtifactsFake{inputRef: input, input: raw}
}

func bindingWorkflowConfig() BindingExecutorConfig {
	return BindingExecutorConfig{
		OwnerID: "executor:bind", Jurisdiction: "ID", Software: "regulagraph-server", Build: "test", Language: "id",
		DocumentKind: pb.DocumentKind_DOCUMENT_KIND_REGULATION, Lease: time.Minute,
		RetryBase: time.Millisecond, RetryMax: time.Second, MaximumBytes: 4 << 20, MaximumRecords: 100,
		WireLimits: domain.WireLimits{MaxBytes: 4 << 20, MaxDepth: 64, MaxItems: 1000},
	}
}

func bindingWorkflowBatch(completeIdentity bool) *pb.DocumentBatch {
	sourceID := "source-blob:a"
	fields := map[string]string{
		"regulation_type": "Peraturan", "number": "1", "year": "2026", "page_title": "Peraturan Contoh",
	}
	if completeIdentity {
		fields["issuer"] = "Kementerian Contoh"
	}
	metadata := make([]*pb.NamedValue, 0, len(fields))
	for name, value := range fields {
		metadata = append(metadata, &pb.NamedValue{Name: name, Value: &pb.NamedValue_Text{Text: value}})
	}
	rootID, articleID := "structure:root", "structure:article-1"
	return &pb.DocumentBatch{
		Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: "corpus:fixture", RecordId: "document-batch:structure"},
		Context: &pb.RequestContext{
			SchemaVersion: 1, RequestId: "request:fixture", TraceId: "trace:fixture", CorpusId: "corpus:fixture",
			Deadline: timestamppb.New(time.Now().Add(time.Hour)), ConfigFingerprint: bindingHash("c"), AuthScopeRef: "scope:fixture",
		},
		Sources: []*pb.SourceBlob{{
			Meta:      &pb.RecordMeta{SchemaVersion: 1, CorpusId: "corpus:fixture", RecordId: sourceID},
			RawSha256: bindingHash("a"), MediaType: "application/pdf", ByteSize: 1,
			ArtifactRef: bindingArtifactFixture("artifact:source", "objects/source.pdf", bindingHash("a"), 1),
		}},
		Observations: []*pb.SourceObservation{{
			Meta:     &pb.RecordMeta{SchemaVersion: 1, CorpusId: "corpus:fixture", RecordId: "observation:a"},
			PortalId: "bpk", DetailUrl: "https://peraturan.bpk.go.id/Details/1/test", FetchedAt: timestamppb.Now(),
			Status: pb.ObservationStatus_OBSERVATION_STATUS_COMPLETE, MetadataHash: bindingHash("d"),
			SourceBlobId: &sourceID, PortalMetadata: metadata,
		}},
		TextArtifacts: []*pb.TextArtifact{{
			Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: "corpus:fixture", RecordId: "text:a"}, SourceBlobId: sourceID,
			ParserManifest: bindingProducer(), RawTextRef: bindingArtifactFixture("raw:a", "raw/a.txt", bindingHash("e"), 100),
			NormalizedTextRef: bindingArtifactFixture("normalized:a", "normalized/a.txt", bindingHash("e"), 100),
			MappingRef:        bindingArtifactFixture("mapping:a", "mapping/a.bin", bindingHash("e"), 100),
			PageResults: []*pb.PageResult{{PageNumber: 1, Status: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED,
				Spans: []*pb.TextSpan{{TextArtifactId: "text:a", EndByte: 100}}}}, NormalizerManifest: bindingProducer(),
		}},
		Structures: []*pb.StructureNode{
			{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: "corpus:fixture", RecordId: rootID},
				Kind: pb.StructureKind_STRUCTURE_KIND_DOCUMENT, Label: "document", OrderedChildren: []string{articleID},
				SourceSpans: []*pb.TextSpan{{TextArtifactId: "text:a", EndByte: 100}}},
			{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: "corpus:fixture", RecordId: articleID},
				Kind: pb.StructureKind_STRUCTURE_KIND_ARTICLE, Label: "Pasal 1", ParentId: &rootID,
				SourceSpans: []*pb.TextSpan{{TextArtifactId: "text:a", StartByte: 10, EndByte: 100}}},
		},
		DependencyManifest: &pb.DependencyManifest{ArtifactId: "dependencies:fixture", ProducerManifest: bindingProducer()},
		Completeness:       pb.Completeness_COMPLETENESS_COMPLETE,
	}
}

func bindingProducer() *pb.ProducerManifest {
	return &pb.ProducerManifest{Software: "fixture", Build: "test", SchemaVersion: 1, ConfigHash: bindingHash("b")}
}

func bindingHash(character string) *pb.ContentHash {
	return &pb.ContentHash{Sha256: strings.Repeat(character, 64)}
}

func bindingArtifactFixture(id, key string, hash *pb.ContentHash, size uint64) *pb.ArtifactRef {
	return &pb.ArtifactRef{ArtifactId: id, ContentHash: hash, StorageKey: key, MediaType: "application/octet-stream", ByteSize: size, SchemaVersion: 1}
}

func TestBindingArtifactReferenceIsContentAddressed(t *testing.T) {
	payload := []byte("fixture")
	first := bindingArtifactReference(payload)
	second := bindingArtifactReference(bytes.Clone(payload))
	if !proto.Equal(first, second) || !strings.Contains(first.StorageKey, first.ContentHash.Sha256) {
		t.Fatal("binding artifact descriptor is not deterministic and content-addressed")
	}
}
