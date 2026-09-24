// Exercises semantic LINK/DEFER transactions against disposable PostgreSQL, including revision
// CAS, replay after later writes, candidate forgery, and stored-decision corruption. The test
// skips without REGULAGRAPH_TEST_POSTGRES_DSN and never proves legal identity accuracy or
// required latency/throughput; those need human gold and the reference workload.
package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/storage"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/workflows"
)

type semanticTransitionReader struct {
	delegate    *storage.FileStore
	afterSecond func(context.Context) error
	reads       int
}

type semanticFailingOutputStore struct{ delegate *storage.FileStore }

func (s *semanticFailingOutputStore) ReadVerified(ctx context.Context,
	ref *pb.ArtifactRef, maximum uint64) ([]byte, error) {
	return s.delegate.ReadVerified(ctx, ref, maximum)
}

func (s *semanticFailingOutputStore) Put(context.Context, *pb.ArtifactRef,
	io.Reader) (bool, error) {
	return false, errors.New("injected output write failure after registry CAS")
}

func (reader *semanticTransitionReader) ReadVerified(ctx context.Context,
	ref *pb.ArtifactRef, maximum uint64) ([]byte, error) {
	raw, err := reader.delegate.ReadVerified(ctx, ref, maximum)
	if err != nil {
		return nil, err
	}
	reader.reads++
	if reader.reads == 2 && reader.afterSecond != nil {
		if err = reader.afterSecond(ctx); err != nil {
			return nil, err
		}
	}
	return raw, nil
}

func TestEmptySemanticResolutionAgainstPostgres(t *testing.T) {
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
	migrations, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "..", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.ApplyMigrations(ctx, os.DirFS(migrations)); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.pool.Exec(ctx, `TRUNCATE TABLE corpus_state CASCADE`); err != nil {
		t.Fatal(err)
	}
	corpusID, jobID := "corpus:empty-semantic", "job:empty-semantic"
	extractModel := &pb.ModelManifest{ModelId: "extractor", Version: "v1",
		Task:          pb.ModelTask_MODEL_TASK_EXTRACT,
		WeightsHash:   &pb.ContentHash{Sha256: strings.Repeat("1", 64)},
		TokenizerHash: &pb.ContentHash{Sha256: strings.Repeat("2", 64)},
		PromptHash:    &pb.ContentHash{Sha256: strings.Repeat("6", 64)},
		MaxTokens:     128, Precision: "fp32", Backend: "test"}
	extractProducer := &pb.ProducerManifest{Software: "extract-worker", Build: "test",
		SchemaVersion: 1, ConfigHash: &pb.ContentHash{Sha256: strings.Repeat("3", 64)},
		Models:       []*pb.ModelManifest{extractModel},
		PromptHashes: []*pb.ContentHash{{Sha256: strings.Repeat("6", 64)}}}
	source := &pb.ExtractionBatch{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpusID,
		RecordId: "extraction:empty"}, Context: &pb.RequestContext{SchemaVersion: 1,
		RequestId: "request:empty", TraceId: "trace:empty", CorpusId: corpusID,
		AuthScopeRef: "scope:empty", ConfigFingerprint: &pb.ContentHash{Sha256: strings.Repeat("4", 64)},
		Deadline: timestamppb.New(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))},
		SourceDocumentBatch: &pb.ArtifactRef{ArtifactId: "artifact:document-empty",
			ContentHash: &pb.ContentHash{Sha256: strings.Repeat("5", 64)},
			StorageKey:  "objects/document-empty", MediaType: "application/x-protobuf", SchemaVersion: 1},
		Dependencies: &pb.DependencyManifest{ArtifactId: "dependencies:extract-empty",
			ProducerManifest: extractProducer, Dependencies: []*pb.Dependency{{
				DependencyId: "artifact:document-empty",
				Fingerprint:  &pb.ContentHash{Sha256: strings.Repeat("5", 64)}}}},
		Completeness:    pb.Completeness_COMPLETENESS_COMPLETE,
		OntologyVersion: "ontology-empty-v1", ModelManifest: extractModel,
		PromptHash: &pb.ContentHash{Sha256: strings.Repeat("6", 64)},
		ItemCounts: &pb.Counts{Expected: 1, Accepted: 1},
		TokenUsage: &pb.TokenUsage{TokenizerId: "extract-tokenizer"}}
	sourceBytes, err := (proto.MarshalOptions{Deterministic: true}).Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(sourceBytes)
	sourceRef := &pb.ArtifactRef{ArtifactId: "artifact:extract-empty",
		ContentHash: &pb.ContentHash{Sha256: hex.EncodeToString(digest[:])},
		StorageKey:  "objects/extract-empty", MediaType: "application/x-protobuf",
		ByteSize: uint64(len(sourceBytes)), SchemaVersion: 1}
	fileStore, err := storage.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer fileStore.Close()
	if _, err = fileStore.Put(ctx, sourceRef, bytes.NewReader(sourceBytes)); err != nil {
		t.Fatal(err)
	}
	if err = repo.RegisterArtifact(ctx, corpusID, sourceRef); err != nil {
		t.Fatal(err)
	}
	checkpoint := &pb.Checkpoint{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpusID,
		RecordId: "checkpoint:extract-empty"}, JobId: jobID,
		Stage: pb.JobStage_JOB_STAGE_EXTRACT, Fence: 1,
		TerminalStatus:     pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED,
		CompletedBatchKeys: []string{sourceRef.ArtifactId},
		ArtifactHashes:     []*pb.ContentHash{proto.Clone(sourceRef.ContentHash).(*pb.ContentHash)},
		Manifest:           extractProducer}
	checkpointBytes, err := (proto.MarshalOptions{Deterministic: true}).Marshal(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	checkpointDigest := sha256.Sum256(checkpointBytes)
	jobPayload := []byte("empty-semantic-job")
	jobDigest := sha256.Sum256(jobPayload)
	if _, err = repo.pool.Exec(ctx, `INSERT INTO jobs
		(job_id,corpus_id,operation,state,stage,input_fingerprint,idempotency_key,
		request_hash,request_payload,attempt,stage_attempt,lease_fence)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,1,1,1)`, jobID, corpusID,
		int16(pb.JobOperation_JOB_OPERATION_INGEST), int16(pb.JobState_JOB_STATE_STAGED),
		int16(pb.JobStage_JOB_STAGE_EXTRACT), strings.Repeat("f", 64),
		"empty-semantic-job", hex.EncodeToString(jobDigest[:]), jobPayload); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.pool.Exec(ctx, `INSERT INTO job_checkpoints
		(checkpoint_id,job_id,stage,fence,payload,payload_hash,terminal_status)
		VALUES ($1,$2,$3,1,$4,$5,$6)`, checkpoint.Meta.RecordId, jobID,
		int16(pb.JobStage_JOB_STAGE_EXTRACT), checkpointBytes,
		hex.EncodeToString(checkpointDigest[:]),
		int16(pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED)); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.pool.Exec(ctx, `UPDATE jobs SET latest_checkpoint_id=$2 WHERE job_id=$1`,
		jobID, checkpoint.Meta.RecordId); err != nil {
		t.Fatal(err)
	}
	var before int64
	if err = repo.pool.QueryRow(ctx, `SELECT registry_revision FROM corpus_state WHERE corpus_id=$1`,
		corpusID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	claimed, err := repo.ClaimResolveJob(ctx, "owner:empty", time.Minute)
	if err != nil || claimed.JobID != jobID {
		t.Fatalf("claim empty RESOLVE: job=%+v err=%v", claimed, err)
	}
	model := proto.Clone(extractModel).(*pb.ModelManifest)
	model.Task = pb.ModelTask_MODEL_TASK_RESOLVE
	producer := &pb.ProducerManifest{Software: "resolve-worker", Build: "test", SchemaVersion: 1,
		ConfigHash: &pb.ContentHash{Sha256: strings.Repeat("7", 64)}, Models: []*pb.ModelManifest{model}}
	handoff, err := workflows.NewSemanticResolutionHandoff(repo, fileStore, uint64(len(sourceBytes)), 64, 8)
	if err != nil {
		t.Fatal(err)
	}
	output, err := handoff.CompleteEmptyResolution(ctx, claimed, model, producer,
		&pb.TokenUsage{TokenizerId: "resolver-tokenizer"}, "resolution:empty")
	if err != nil || output == nil || len(output.Batch.Proposals) != 0 ||
		len(output.Batch.Decisions) != 0 || output.Batch.ItemCounts.Expected != 0 {
		t.Fatalf("empty RESOLVE did not checkpoint: output=%v err=%v", output, err)
	}
	var after int64
	if err = repo.pool.QueryRow(ctx, `SELECT registry_revision FROM corpus_state WHERE corpus_id=$1`,
		corpusID).Scan(&after); err != nil || after != before {
		t.Fatalf("empty RESOLVE changed registry revision: before=%d after=%d err=%v", before, after, err)
	}
	if _, err = repo.pool.Exec(ctx, `UPDATE jobs SET state=$2,lease_owner='owner:crashed',
		lease_expires_at=clock_timestamp()-interval '1 second' WHERE job_id=$1`,
		jobID, int16(pb.JobState_JOB_STATE_RUNNING)); err != nil {
		t.Fatal(err)
	}
	reclaim, err := repo.ClaimResolveJob(ctx, "owner:empty-recovery", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = handoff.CompleteEmptyResolution(ctx, reclaim, model, producer,
		&pb.TokenUsage{TokenizerId: "resolver-tokenizer"}, "resolution:changed"); !errors.Is(err, domain.ErrPersistentIntegrity) {
		t.Fatalf("changed retry identity must not recover the completed output: %v", err)
	}
	recovered, err := handoff.CompleteEmptyResolution(ctx, reclaim, model, producer,
		&pb.TokenUsage{TokenizerId: "resolver-tokenizer"}, "resolution:empty")
	if err != nil || recovered == nil || recovered.Artifact.ArtifactId != output.Artifact.ArtifactId ||
		recovered.Checkpoint.Fence != reclaim.LeaseFence {
		t.Fatalf("empty RESOLVE recovery failed: output=%v err=%v", recovered, err)
	}
}

func TestSemanticRegistryAgainstPostgres(t *testing.T) {
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
	migrations, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "..", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.ApplyMigrations(ctx, os.DirFS(migrations)); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.pool.Exec(ctx, `TRUNCATE TABLE corpus_state CASCADE`); err != nil {
		t.Fatal(err)
	}
	corpusID := "corpus-semantic-test"
	claim := domain.CanonicalIdentityClaim{ProposalKey: "issuer:semantic", EntityType: domain.CanonicalEntityTypeOrganization,
		IdentityScope: domain.IssuerIdentityKeyNamespace, IdentityKey: strings.Repeat("a", 64),
		PayloadHash: strings.Repeat("b", 64)}
	assignments, revision, err := repo.ResolveCanonicalIdentities(ctx, corpusID, "identity:semantic-seed", 1,
		[]domain.CanonicalIdentityClaim{claim})
	if err != nil || len(assignments) != 1 || revision != 2 {
		t.Fatalf("seed identity: assignments=%v revision=%d err=%v", assignments, revision, err)
	}
	canonicalID := assignments[0].CanonicalID
	entity := &pb.CanonicalEntity{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpusID, RecordId: canonicalID},
		EntityType: "organization", Scope: "ID:national", PreferredLabel: "Kementerian X",
		ReviewState: pb.ReviewState_REVIEW_STATE_UNREVIEWED, RegistryRevision: revision,
		IdentityKeys: []*pb.IdentityKey{{Namespace: claim.IdentityScope, Value: claim.IdentityKey}}}
	alias := &pb.Alias{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpusID, RecordId: "alias:semantic"},
		CanonicalId: canonicalID, Surface: "Kementerian X", NormalizedLookup: "kementerian x",
		Scope: "ID:national", Language: "id", SupportRefs: []string{"support:reviewed"}}
	revision, err = repo.RegisterCanonicalAliases(ctx, corpusID, "alias:semantic-seed", revision,
		[]AliasRegistration{{Entity: entity, Alias: alias}})
	if err != nil || revision != 3 {
		t.Fatalf("seed sourced alias: revision=%d err=%v", revision, err)
	}
	ref := &pb.SourceVersionRef{SourceBlobId: "source:semantic", ProvisionVersionId: "version:semantic", RegulationId: "regulation:semantic"}
	extractModel := &pb.ModelManifest{ModelId: "extractor", Version: "v1", Task: pb.ModelTask_MODEL_TASK_EXTRACT,
		WeightsHash:   &pb.ContentHash{Sha256: strings.Repeat("5", 64)},
		TokenizerHash: &pb.ContentHash{Sha256: strings.Repeat("6", 64)},
		MaxTokens:     128, Precision: "fp32", Backend: "test"}
	extractProducer := &pb.ProducerManifest{Software: "extract-worker", Build: "test", SchemaVersion: 1,
		ConfigHash: &pb.ContentHash{Sha256: strings.Repeat("7", 64)}, Models: []*pb.ModelManifest{extractModel}}
	source := &pb.ExtractionBatch{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpusID, RecordId: "extraction:semantic"},
		Context: &pb.RequestContext{SchemaVersion: 1, RequestId: "request:extract-semantic", TraceId: "trace:semantic",
			CorpusId: corpusID, AuthScopeRef: "scope:semantic", ConfigFingerprint: &pb.ContentHash{Sha256: strings.Repeat("c", 64)},
			Deadline: timestamppb.New(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))},
		Completeness: pb.Completeness_COMPLETENESS_COMPLETE, OntologyVersion: "ontology-semantic-v1",
		SourceDocumentBatch: &pb.ArtifactRef{ArtifactId: "artifact:document-semantic",
			ContentHash: &pb.ContentHash{Sha256: strings.Repeat("8", 64)}, StorageKey: "objects/document-semantic",
			MediaType: "application/x-protobuf", SchemaVersion: 1},
		Dependencies:  &pb.DependencyManifest{ArtifactId: "dependencies:extract-semantic", ProducerManifest: extractProducer},
		ModelManifest: extractModel, PromptHash: &pb.ContentHash{Sha256: strings.Repeat("9", 64)},
		ItemCounts: &pb.Counts{Expected: 2, Accepted: 2}, TokenUsage: &pb.TokenUsage{TokenizerId: "extract-tokenizer"},
		Mentions: []*pb.Mention{
			{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpusID, RecordId: "mention:linked"},
				CandidateType: "organization", SurfaceForm: "Kementerian X",
				TextSpan: &pb.TextSpan{TextArtifactId: "text:semantic", StartByte: 0, EndByte: 12}, SourceRefs: []*pb.SourceVersionRef{ref},
				ExtractionManifest: extractProducer},
			{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpusID, RecordId: "mention:deferred"},
				CandidateType: "organization", SurfaceForm: "Instansi tidak diketahui",
				TextSpan: &pb.TextSpan{TextArtifactId: "text:semantic", StartByte: 13, EndByte: 35}, SourceRefs: []*pb.SourceVersionRef{ref},
				ExtractionManifest: extractProducer},
		},
	}
	sourceBytes, err := (proto.MarshalOptions{Deterministic: true}).Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	sourceDigest := sha256.Sum256(sourceBytes)
	sourceRef := &pb.ArtifactRef{ArtifactId: "artifact:extract-semantic",
		ContentHash: &pb.ContentHash{Sha256: hex.EncodeToString(sourceDigest[:])},
		StorageKey:  "objects/extract-semantic", MediaType: "application/x-protobuf",
		ByteSize: uint64(len(sourceBytes)), SchemaVersion: 1}
	if err = repo.RegisterArtifact(ctx, corpusID, sourceRef); err != nil {
		t.Fatal(err)
	}
	producer := &pb.ProducerManifest{Software: "registry-reader", Build: "test", SchemaVersion: 1,
		ConfigHash: &pb.ContentHash{Sha256: strings.Repeat("e", 64)}}
	plans := []RegistryCandidatePlan{
		{MentionID: "mention:linked", Scopes: []RegistryLookupScope{{EntityType: "organization", CanonicalScope: "ID:national", NormalizedLookup: "kementerian x"}}},
		{MentionID: "mention:deferred", Scopes: []RegistryLookupScope{{EntityType: "organization", CanonicalScope: "ID:national", NormalizedLookup: "unknown"}}},
	}
	candidates, err := repo.PrepareRegistryCandidateBatch(ctx, source, sourceRef, producer,
		"candidates:semantic", plans, 8, 8, 64, 8)
	if err != nil || candidates.RegistryRevision != revision {
		t.Fatalf("prepare candidate artifact: revision=%d err=%v", candidates.GetRegistryRevision(), err)
	}
	candidateBytes, err := (proto.MarshalOptions{Deterministic: true}).Marshal(candidates)
	if err != nil {
		t.Fatal(err)
	}
	candidateDigest := sha256.Sum256(candidateBytes)
	candidateRef := &pb.ArtifactRef{ArtifactId: "artifact:candidates-semantic",
		ContentHash: &pb.ContentHash{Sha256: hex.EncodeToString(candidateDigest[:])},
		StorageKey:  "objects/candidates-semantic", MediaType: "application/x-protobuf",
		ByteSize: uint64(len(candidateBytes)), SchemaVersion: 1}
	if err = repo.RegisterArtifact(ctx, corpusID, candidateRef); err != nil {
		t.Fatal(err)
	}
	input := SemanticRegistryInputs{SourceRef: sourceRef, SourceBytes: sourceBytes,
		CandidateRef: candidateRef, CandidateBytes: candidateBytes}
	proposal := func(index int, action pb.ResolutionAction, candidateIDs []string) *pb.ResolutionProposal {
		mention := source.Mentions[index]
		return &pb.ResolutionProposal{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpusID,
			RecordId: "proposal:" + mention.Meta.RecordId},
			MentionIds: []string{mention.Meta.RecordId}, CandidateIds: candidateIDs, Action: action,
			Evidence: &pb.Provenance{Sources: []*pb.SourceVersionRef{proto.Clone(ref).(*pb.SourceVersionRef)},
				Spans: []*pb.TextSpan{proto.Clone(mention.TextSpan).(*pb.TextSpan)}},
			Method: "reviewed_or_deferred", ExpectedRegistryRevision: revision,
			LocalCorrelationId: "correlation:" + mention.Meta.RecordId}
	}
	request := &pb.RegistryResolveRequest{Context: proto.Clone(source.Context).(*pb.RequestContext),
		OperationKey: "semantic:commit", ExpectedRevision: revision,
		Proposals: []*pb.ResolutionProposal{
			proposal(0, pb.ResolutionAction_RESOLUTION_ACTION_LINK, []string{canonicalID}),
			proposal(1, pb.ResolutionAction_RESOLUTION_ACTION_DEFER, nil),
		}}
	request.Context.RequestId = "request:resolve-semantic"
	approval := []ReviewedLink{{ProposalID: request.Proposals[0].Meta.RecordId, CanonicalID: canonicalID,
		Actor: "reviewer:one", Reason: "verified source and legal scope", ReviewID: "review:one"}}
	if _, err = repo.commitSemanticResolutions(ctx, nil, input, request, nil, 64, 8); err == nil {
		t.Fatal("unapproved LINK was committed")
	}
	if _, err = repo.commitSemanticResolutions(ctx, nil, input, request, approval, 64, 8); !errors.Is(err, ErrConflict) || errors.Is(err, domain.ErrResolutionReplan) {
		t.Fatalf("unpersisted review authorized LINK: %v", err)
	}
	proposalBytes, err := (proto.MarshalOptions{Deterministic: true}).Marshal(request.Proposals[0])
	if err != nil {
		t.Fatal(err)
	}
	proposalDigest := sha256.Sum256(proposalBytes)
	if _, err = repo.pool.Exec(ctx, `INSERT INTO registry_semantic_reviews
		(corpus_id,review_id,source_artifact_id,candidate_artifact_id,candidate_hash,
		proposal_id,proposal_hash,canonical_id,actor,reason)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, corpusID, approval[0].ReviewID,
		sourceRef.ArtifactId, candidateRef.ArtifactId, candidateRef.ContentHash.Sha256,
		approval[0].ProposalID, hex.EncodeToString(proposalDigest[:]), canonicalID,
		approval[0].Actor, approval[0].Reason); err != nil {
		t.Fatal(err)
	}
	revokedApproval := append([]ReviewedLink(nil), approval...)
	revokedApproval[0].ReviewID = "review:revoked"
	if _, err = repo.pool.Exec(ctx, `INSERT INTO registry_semantic_reviews
		(corpus_id,review_id,source_artifact_id,candidate_artifact_id,candidate_hash,
		proposal_id,proposal_hash,canonical_id,actor,reason)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, corpusID,
		revokedApproval[0].ReviewID, sourceRef.ArtifactId, candidateRef.ArtifactId,
		candidateRef.ContentHash.Sha256, approval[0].ProposalID,
		hex.EncodeToString(proposalDigest[:]), canonicalID, approval[0].Actor,
		approval[0].Reason); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.pool.Exec(ctx, `UPDATE registry_semantic_reviews SET revoked_at=clock_timestamp()
		WHERE corpus_id=$1 AND review_id=$2`, corpusID, revokedApproval[0].ReviewID); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.pool.Exec(ctx, `UPDATE registry_semantic_reviews SET revoked_at=clock_timestamp()
		WHERE corpus_id=$1 AND review_id=$2`, corpusID, revokedApproval[0].ReviewID); err == nil {
		t.Fatal("revoked review could be rewritten again")
	}
	if _, err = repo.commitSemanticResolutions(ctx, nil, input, request, revokedApproval, 64, 8); !errors.Is(err, ErrConflict) || !errors.Is(err, domain.ErrResolutionReplan) {
		t.Fatalf("revoked review authorized LINK: %v", err)
	}
	changedProposal := proto.Clone(request).(*pb.RegistryResolveRequest)
	changedProposal.Proposals[0].Method = "changed_after_review"
	if _, err = repo.commitSemanticResolutions(ctx, nil, input, changedProposal, approval, 64, 8); !errors.Is(err, ErrConflict) || !errors.Is(err, domain.ErrResolutionReplan) {
		t.Fatalf("review authorized changed proposal content: %v", err)
	}
	otherCandidateBatch := proto.Clone(candidates).(*pb.RegistryCandidateBatch)
	otherCandidateBatch.Meta.RecordId = "candidates:alternate-review-view"
	otherCandidateBytes, err := (proto.MarshalOptions{Deterministic: true}).Marshal(otherCandidateBatch)
	if err != nil {
		t.Fatal(err)
	}
	otherDigest := sha256.Sum256(otherCandidateBytes)
	otherCandidateRef := &pb.ArtifactRef{ArtifactId: "artifact:candidates-alternate-view",
		ContentHash: &pb.ContentHash{Sha256: hex.EncodeToString(otherDigest[:])},
		StorageKey:  "objects/candidates-alternate-view", MediaType: "application/x-protobuf",
		ByteSize: uint64(len(otherCandidateBytes)), SchemaVersion: 1}
	if err = repo.RegisterArtifact(ctx, corpusID, otherCandidateRef); err != nil {
		t.Fatal(err)
	}
	otherCandidateInput := input
	otherCandidateInput.CandidateRef = otherCandidateRef
	otherCandidateInput.CandidateBytes = otherCandidateBytes
	if _, err = repo.commitSemanticResolutions(ctx, nil, otherCandidateInput, request, approval, 64, 8); !errors.Is(err, ErrConflict) {
		t.Fatalf("review authorized a different candidate artifact: %v", err)
	}
	forgedArtifact := proto.Clone(candidates).(*pb.RegistryCandidateBatch)
	forgedArtifact.Aliases[0].Surface = "Nama palsu"
	forgedBytes, err := (proto.MarshalOptions{Deterministic: true}).Marshal(forgedArtifact)
	if err != nil {
		t.Fatal(err)
	}
	forgedDigest := sha256.Sum256(forgedBytes)
	forgedRef := &pb.ArtifactRef{ArtifactId: "artifact:candidates-forged",
		ContentHash: &pb.ContentHash{Sha256: hex.EncodeToString(forgedDigest[:])},
		StorageKey:  "objects/candidates-forged", MediaType: "application/x-protobuf",
		ByteSize: uint64(len(forgedBytes)), SchemaVersion: 1}
	if err = repo.RegisterArtifact(ctx, corpusID, forgedRef); err != nil {
		t.Fatal(err)
	}
	forgedRegistered := SemanticRegistryInputs{SourceRef: sourceRef, SourceBytes: sourceBytes,
		CandidateRef: forgedRef, CandidateBytes: forgedBytes}
	if _, err = repo.commitSemanticResolutions(ctx, nil, forgedRegistered, request, approval, 64, 8); !errors.Is(err, ErrConflict) {
		t.Fatalf("registered but forged candidate view was accepted: %v", err)
	}
	jobID := "job:semantic-resolve"
	checkpoint := &pb.Checkpoint{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpusID,
		RecordId: "checkpoint:semantic-extract"}, JobId: jobID,
		Stage: pb.JobStage_JOB_STAGE_EXTRACT, Fence: 1,
		TerminalStatus:     pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED,
		CompletedBatchKeys: []string{sourceRef.ArtifactId},
		ArtifactHashes:     []*pb.ContentHash{proto.Clone(sourceRef.ContentHash).(*pb.ContentHash)},
		Manifest:           proto.Clone(extractProducer).(*pb.ProducerManifest)}
	checkpointBytes, err := (proto.MarshalOptions{Deterministic: true}).Marshal(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	checkpointDigest := sha256.Sum256(checkpointBytes)
	jobPayload := []byte("semantic-job-fixture")
	jobDigest := sha256.Sum256(jobPayload)
	if _, err = repo.pool.Exec(ctx, `INSERT INTO jobs
		(job_id,corpus_id,operation,state,stage,input_fingerprint,idempotency_key,
		request_hash,request_payload,attempt,stage_attempt,lease_fence)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,1,1,1)`, jobID, corpusID,
		int16(pb.JobOperation_JOB_OPERATION_INGEST), int16(pb.JobState_JOB_STATE_STAGED),
		int16(pb.JobStage_JOB_STAGE_EXTRACT), strings.Repeat("f", 64),
		"semantic-job-fixture", hex.EncodeToString(jobDigest[:]), jobPayload); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.pool.Exec(ctx, `INSERT INTO job_checkpoints
		(checkpoint_id,job_id,stage,fence,payload,payload_hash,terminal_status)
		VALUES ($1,$2,$3,1,$4,$5,$6)`, checkpoint.Meta.RecordId, jobID,
		int16(pb.JobStage_JOB_STAGE_EXTRACT), checkpointBytes,
		hex.EncodeToString(checkpointDigest[:]),
		int16(pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED)); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.pool.Exec(ctx, `UPDATE jobs SET latest_checkpoint_id=$2 WHERE job_id=$1`,
		jobID, checkpoint.Meta.RecordId); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.pool.Exec(ctx, `UPDATE job_checkpoints SET terminal_status=NULL
		WHERE checkpoint_id=$1`, checkpoint.Meta.RecordId); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.ClaimResolveJob(ctx, "owner:semantic", time.Minute); !errors.Is(err, ErrLeaseUnavailable) {
		t.Fatalf("RESOLVE claimed an EXTRACT checkpoint without a successful terminal outcome: %v", err)
	}
	if _, err = repo.pool.Exec(ctx, `UPDATE job_checkpoints SET terminal_status=$2
		WHERE checkpoint_id=$1`, checkpoint.Meta.RecordId,
		int16(pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED)); err != nil {
		t.Fatal(err)
	}
	claimed, err := repo.ClaimResolveJob(ctx, "owner:semantic", time.Minute)
	if err != nil || claimed.JobID != jobID || claimed.Stage != pb.JobStage_JOB_STAGE_RESOLVE ||
		claimed.LeaseFence != 2 {
		t.Fatalf("EXTRACT-to-RESOLVE handoff did not claim expected lease: job=%+v err=%v", claimed, err)
	}
	proof := SemanticJobFence{JobID: jobID, OwnerID: claimed.LeaseOwner,
		SourceCheckpointID: checkpoint.Meta.RecordId, Fence: claimed.LeaseFence}
	staleProof := proof
	staleProof.Fence++
	if _, err = repo.CommitSemanticResolutions(ctx, staleProof, input, request,
		approval, 64, 8); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("stale RESOLVE fence authorized registry write: %v", err)
	}
	if _, err = repo.pool.Exec(ctx, `UPDATE job_checkpoints SET payload_hash=$2
		WHERE checkpoint_id=$1`, checkpoint.Meta.RecordId, strings.Repeat("0", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.CommitSemanticResolutions(ctx, proof, input, request,
		approval, 64, 8); !errors.Is(err, domain.ErrPersistentIntegrity) {
		t.Fatalf("corrupt EXTRACT checkpoint authorized registry write: %v", err)
	}
	if _, err = repo.pool.Exec(ctx, `UPDATE job_checkpoints SET payload_hash=$2
		WHERE checkpoint_id=$1`, checkpoint.Meta.RecordId,
		hex.EncodeToString(checkpointDigest[:])); err != nil {
		t.Fatal(err)
	}
	wrongManifest := proto.Clone(checkpoint).(*pb.Checkpoint)
	wrongManifest.Manifest.Software = "different-extractor"
	wrongManifestBytes, err := (proto.MarshalOptions{Deterministic: true}).Marshal(wrongManifest)
	if err != nil {
		t.Fatal(err)
	}
	wrongManifestDigest := sha256.Sum256(wrongManifestBytes)
	if _, err = repo.pool.Exec(ctx, `UPDATE job_checkpoints SET payload=$2,payload_hash=$3
		WHERE checkpoint_id=$1`, checkpoint.Meta.RecordId, wrongManifestBytes,
		hex.EncodeToString(wrongManifestDigest[:])); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.CommitSemanticResolutions(ctx, proof, input, request,
		approval, 64, 8); !errors.Is(err, domain.ErrPersistentIntegrity) {
		t.Fatalf("different EXTRACT producer authorized registry write: %v", err)
	}
	if _, err = repo.pool.Exec(ctx, `UPDATE job_checkpoints SET payload=$2,payload_hash=$3
		WHERE checkpoint_id=$1`, checkpoint.Meta.RecordId, checkpointBytes,
		hex.EncodeToString(checkpointDigest[:])); err != nil {
		t.Fatal(err)
	}
	fileStore, err := storage.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer fileStore.Close()
	for _, artifact := range []struct {
		ref *pb.ArtifactRef
		raw []byte
	}{{sourceRef, sourceBytes}, {candidateRef, candidateBytes}} {
		if _, err = fileStore.Put(ctx, artifact.ref, bytes.NewReader(artifact.raw)); err != nil {
			t.Fatal(err)
		}
	}
	for _, transition := range []struct {
		name    string
		change  string
		restore string
	}{
		{"cancelled after artifact read", "UPDATE jobs SET cancellation_requested=true WHERE job_id=$1",
			"UPDATE jobs SET cancellation_requested=false WHERE job_id=$1"},
		{"expired after artifact read", "UPDATE jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE job_id=$1",
			"UPDATE jobs SET lease_expires_at=clock_timestamp()+interval '1 minute' WHERE job_id=$1"},
	} {
		t.Run(transition.name, func(t *testing.T) {
			reader := &semanticTransitionReader{delegate: fileStore,
				afterSecond: func(ctx context.Context) error {
					_, changeErr := repo.pool.Exec(ctx, transition.change, jobID)
					return changeErr
				}}
			transitionHandoff, buildErr := workflows.NewSemanticResolutionHandoff(repo,
				reader, uint64(len(sourceBytes)+len(candidateBytes)), 64, 8)
			if buildErr != nil {
				t.Fatal(buildErr)
			}
			if _, commitErr := transitionHandoff.Commit(ctx, claimed, candidateRef, request,
				approval); !errors.Is(commitErr, ErrStaleFence) || reader.reads != 2 {
				t.Fatalf("lease transition after verified reads authorized LINK: err=%v reads=%d",
					commitErr, reader.reads)
			}
			var operations int
			if scanErr := repo.pool.QueryRow(ctx, `SELECT count(*) FROM registry_semantic_operations
				WHERE corpus_id=$1 AND operation_key=$2`, corpusID, request.OperationKey).
				Scan(&operations); scanErr != nil || operations != 0 {
				t.Fatalf("failed fenced write persisted an operation: count=%d err=%v", operations, scanErr)
			}
			if _, restoreErr := repo.pool.Exec(ctx, transition.restore, jobID); restoreErr != nil {
				t.Fatal(restoreErr)
			}
		})
	}
	if _, err = repo.pool.Exec(ctx, `UPDATE jobs SET lease_expires_at=clock_timestamp()+interval '2 seconds'
		WHERE job_id=$1`, jobID); err != nil {
		t.Fatal(err)
	}
	reviewLock, err := repo.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer reviewLock.Rollback(ctx)
	if _, err = reviewLock.Exec(ctx, `SELECT review_id FROM registry_semantic_reviews
		WHERE corpus_id=$1 AND review_id=$2 FOR UPDATE`, corpusID, approval[0].ReviewID); err != nil {
		t.Fatal(err)
	}
	expiredResult := make(chan error, 1)
	go func() {
		_, commitErr := repo.CommitSemanticResolutions(ctx, proof, input, request, approval, 64, 8)
		expiredResult <- commitErr
	}()
	waitDeadline := time.Now().Add(time.Second)
	blocked := false
	for !blocked && time.Now().Before(waitDeadline) {
		if err = repo.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
			WHERE datname=current_database() AND wait_event_type='Lock'
			AND query LIKE '%FROM registry_semantic_reviews%' AND query LIKE '%FOR SHARE%')`).
			Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if !blocked {
			select {
			case commitErr := <-expiredResult:
				t.Fatalf("writer exited before review lock wait: %v", commitErr)
			default:
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	if !blocked {
		t.Fatal("writer never reached the held review lock before lease expiry")
	}
	expiryDeadline := time.Now().Add(3 * time.Second)
	expired := false
	for !expired && time.Now().Before(expiryDeadline) {
		if err = repo.pool.QueryRow(ctx, `SELECT lease_expires_at <= clock_timestamp()
			FROM jobs WHERE job_id=$1`, jobID).Scan(&expired); err != nil {
			t.Fatal(err)
		}
		if !expired {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if !expired {
		t.Fatal("test lease did not expire while writer was blocked")
	}
	if err = reviewLock.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if commitErr := <-expiredResult; !errors.Is(commitErr, ErrStaleFence) {
		t.Fatalf("lease expired while registry transaction waited: %v", commitErr)
	}
	var expiredOperations int
	if err = repo.pool.QueryRow(ctx, `SELECT count(*) FROM registry_semantic_operations
		WHERE corpus_id=$1 AND operation_key=$2`, corpusID, request.OperationKey).
		Scan(&expiredOperations); err != nil || expiredOperations != 0 {
		t.Fatalf("expired transaction persisted an operation: count=%d err=%v", expiredOperations, err)
	}
	if _, err = repo.pool.Exec(ctx, `UPDATE jobs SET lease_expires_at=clock_timestamp()+interval '1 minute'
		WHERE job_id=$1`, jobID); err != nil {
		t.Fatal(err)
	}
	handoff, err := workflows.NewSemanticResolutionHandoff(repo, fileStore,
		uint64(len(sourceBytes)+len(candidateBytes)), 64, 8)
	if err != nil {
		t.Fatal(err)
	}
	resolveModel := &pb.ModelManifest{ModelId: "resolver", Version: "v1", Task: pb.ModelTask_MODEL_TASK_RESOLVE,
		WeightsHash:   &pb.ContentHash{Sha256: strings.Repeat("1", 64)},
		TokenizerHash: &pb.ContentHash{Sha256: strings.Repeat("2", 64)},
		MaxTokens:     128, Precision: "fp32", Backend: "test"}
	resolveProducer := &pb.ProducerManifest{Software: "resolve-worker", Build: "test", SchemaVersion: 1,
		ConfigHash: &pb.ContentHash{Sha256: strings.Repeat("3", 64)}, Models: []*pb.ModelManifest{resolveModel}}
	if _, err = handoff.CommitAndCheckpoint(ctx, claimed, candidateRef, request, approval,
		resolveModel, resolveProducer, &pb.TokenUsage{TokenizerId: "resolver-tokenizer"},
		source.Meta.RecordId); err == nil {
		t.Fatal("colliding RESOLVE output ID passed preflight")
	}
	var preflightOperations int
	if err = repo.pool.QueryRow(ctx, `SELECT count(*) FROM registry_semantic_operations
		WHERE corpus_id=$1 AND operation_key=$2`, corpusID, request.OperationKey).
		Scan(&preflightOperations); err != nil || preflightOperations != 0 {
		t.Fatalf("invalid output intent committed a registry operation: count=%d err=%v", preflightOperations, err)
	}
	failingHandoff, err := workflows.NewSemanticResolutionHandoff(repo,
		&semanticFailingOutputStore{delegate: fileStore},
		uint64(len(sourceBytes)+len(candidateBytes)), 64, 8)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = failingHandoff.CommitAndCheckpoint(ctx, claimed, candidateRef, request,
		approval, resolveModel, resolveProducer,
		&pb.TokenUsage{TokenizerId: "resolver-tokenizer"}, "resolution:semantic"); err == nil {
		t.Fatal("injected output failure was ignored")
	}
	storedIntent, err := repo.LoadSemanticResolutionIntent(ctx, corpusID, jobID)
	if err != nil || storedIntent.Request.OperationKey != request.OperationKey ||
		storedIntent.Preview.GetMeta().GetRecordId() != "resolution:semantic" {
		t.Fatalf("pre-CAS RESOLVE intent was not durable: intent=%v err=%v", storedIntent, err)
	}
	changedIntentRequest := proto.Clone(request).(*pb.RegistryResolveRequest)
	changedIntentRequest.Context.RequestId = "request:changed-after-intent"
	if _, err = handoff.CommitAndCheckpoint(ctx, claimed, candidateRef,
		changedIntentRequest, nil, nil, nil, nil, ""); err == nil {
		t.Fatal("changed retry replaced immutable RESOLVE intent")
	}
	priorCheckpoint, err := repo.LoadLatestCheckpoint(ctx, jobID)
	if err != nil || priorCheckpoint.Stage != pb.JobStage_JOB_STAGE_EXTRACT {
		t.Fatalf("failed output falsely advanced checkpoint: checkpoint=%v err=%v", priorCheckpoint, err)
	}
	response, err := repo.CommitSemanticResolutions(ctx, proof, input, request, approval, 64, 8)
	if err != nil || response.RegistryRevision != revision+1 || len(response.Assignments) != 2 ||
		response.Assignments[0].GetDecision().AssignedCanonicalIds[0] != canonicalID ||
		len(response.Assignments[1].GetDecision().AssignedCanonicalIds) != 0 {
		t.Fatalf("commit semantic LINK/DEFER: response=%v err=%v", response, err)
	}
	resolvedBatch, err := domain.AssembleResolutionBatchFromReceipt(source, sourceRef, candidates, candidateRef,
		request, response, resolveModel, resolveProducer, &pb.TokenUsage{TokenizerId: "resolver-tokenizer"},
		"resolution:semantic", 64, 8)
	if err != nil || resolvedBatch.RegistryRevision != response.RegistryRevision ||
		len(resolvedBatch.Decisions) != 2 || len(resolvedBatch.Dependencies.Dependencies) != 2 ||
		len(resolvedBatch.Dependencies.LookupScopeRevisions) != 2 {
		t.Fatalf("registry receipt did not build a complete resolution artifact: batch=%v err=%v", resolvedBatch, err)
	}
	retry := proto.Clone(request).(*pb.RegistryResolveRequest)
	retry.Context.RequestId = "request:retry-semantic"
	replayed, err := repo.CommitSemanticResolutions(ctx, proof, input, retry, approval, 64, 8)
	if err != nil || replayed.RegistryRevision != response.RegistryRevision ||
		replayed.RequestId != retry.Context.RequestId ||
		replayed.Assignments[0].GetDecision().Meta.RecordId != response.Assignments[0].GetDecision().Meta.RecordId {
		t.Fatalf("semantic replay changed decision: replay=%v err=%v", replayed, err)
	}
	output, err := handoff.CommitAndCheckpoint(ctx, claimed, nil,
		nil, nil, nil, nil, nil, "")
	if err != nil || output == nil || output.Batch == nil ||
		output.Batch.RegistryRevision != response.RegistryRevision ||
		len(output.Batch.Decisions) != 2 || output.Checkpoint.Stage != pb.JobStage_JOB_STAGE_RESOLVE {
		t.Fatalf("RESOLVE receipt was not checkpointed: output=%v err=%v", output, err)
	}
	if _, err = fileStore.ReadVerified(ctx, output.Artifact, uint64(domain.DefaultWireLimits.MaxBytes)); err != nil {
		t.Fatalf("checkpointed RESOLVE bytes are not readable: %v", err)
	}
	// Recreate the crash window after SaveCheckpoint and before completion: the latest
	// RESOLVE output is durable, but the old RUNNING lease has expired.
	if _, err = repo.pool.Exec(ctx, `UPDATE jobs SET state=$2,lease_owner='owner:crashed',
		lease_expires_at=clock_timestamp()-interval '1 second' WHERE job_id=$1`,
		jobID, int16(pb.JobState_JOB_STATE_RUNNING)); err != nil {
		t.Fatal(err)
	}
	recoveryClaim, err := repo.ClaimResolveJob(ctx, "owner:recovery", time.Minute)
	if err != nil || recoveryClaim.JobID != claimed.JobID || recoveryClaim.LeaseFence <= claimed.LeaseFence {
		t.Fatalf("terminal RESOLVE checkpoint could not be reclaimed: job=%+v err=%v", recoveryClaim, err)
	}
	recovered, err := handoff.CommitAndCheckpoint(ctx, recoveryClaim, nil,
		nil, nil, nil, nil, nil, "")
	if err != nil || recovered == nil || recovered.Artifact.ArtifactId != output.Artifact.ArtifactId ||
		recovered.Checkpoint.Fence != recoveryClaim.LeaseFence {
		t.Fatalf("RESOLVE recovery reran or lost output: output=%v err=%v", recovered, err)
	}
	if _, err = repo.pool.Exec(ctx, `UPDATE jobs SET lease_owner='owner:successor',
		lease_fence=lease_fence+1 WHERE job_id=$1`, jobID); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.CommitSemanticResolutions(ctx, proof, input, retry,
		approval, 64, 8); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("superseded RESOLVE owner replayed a registry receipt: %v", err)
	}
	if _, err = repo.pool.Exec(ctx, `UPDATE registry_semantic_reviews SET reason='rewritten'
		WHERE corpus_id=$1 AND review_id=$2`, corpusID, approval[0].ReviewID); err == nil {
		t.Fatal("committed review reason was mutable")
	}
	if _, err = repo.pool.Exec(ctx, `DELETE FROM registry_semantic_reviews
		WHERE corpus_id=$1 AND review_id=$2`, corpusID, approval[0].ReviewID); err == nil {
		t.Fatal("committed review record was deletable")
	}
	if _, err = repo.pool.Exec(ctx, `UPDATE registry_semantic_operations SET request_hash=$3
		WHERE corpus_id=$1 AND operation_key=$2`, corpusID, request.OperationKey,
		strings.Repeat("0", 64)); err == nil {
		t.Fatal("committed operation was mutable")
	}
	if _, err = repo.pool.Exec(ctx, `UPDATE registry_semantic_decisions SET decision_hash=$3
		WHERE corpus_id=$1 AND proposal_id=$2`, corpusID, request.Proposals[0].Meta.RecordId,
		strings.Repeat("0", 64)); err == nil {
		t.Fatal("committed decision was mutable")
	}
	if _, err = repo.pool.Exec(ctx, `DELETE FROM registry_semantic_operations
		WHERE corpus_id=$1 AND operation_key=$2`, corpusID, request.OperationKey); err == nil {
		t.Fatal("committed operation was deletable")
	}
	if _, err = repo.pool.Exec(ctx, `DELETE FROM registry_semantic_decisions
		WHERE corpus_id=$1 AND proposal_id=$2`, corpusID, request.Proposals[0].Meta.RecordId); err == nil {
		t.Fatal("committed decision was deletable")
	}
	changedApproval := append([]ReviewedLink(nil), approval...)
	changedApproval[0].Reason = "different review"
	if _, err = repo.commitSemanticResolutions(ctx, nil, input, request,
		changedApproval, 64, 8); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed review reused operation key: %v", err)
	}
	stale := proto.Clone(request).(*pb.RegistryResolveRequest)
	stale.OperationKey = "semantic:stale"
	if _, err = repo.commitSemanticResolutions(ctx, nil, input, stale,
		approval, 64, 8); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale candidate revision committed: %v", err)
	}
	freshCandidates, err := repo.PrepareRegistryCandidateBatch(ctx, source, sourceRef, producer,
		"candidates:semantic-fresh", plans, 8, 8, 64, 8)
	if err != nil {
		t.Fatal(err)
	}
	redecision := proto.Clone(request).(*pb.RegistryResolveRequest)
	redecision.OperationKey = "semantic:redecision"
	redecision.ExpectedRevision = freshCandidates.RegistryRevision
	for _, item := range redecision.Proposals {
		item.ExpectedRegistryRevision = freshCandidates.RegistryRevision
	}
	freshBytes, err := (proto.MarshalOptions{Deterministic: true}).Marshal(freshCandidates)
	if err != nil {
		t.Fatal(err)
	}
	freshDigest := sha256.Sum256(freshBytes)
	freshRef := &pb.ArtifactRef{ArtifactId: "artifact:candidates-semantic-fresh",
		ContentHash: &pb.ContentHash{Sha256: hex.EncodeToString(freshDigest[:])},
		StorageKey:  "objects/candidates-semantic-fresh", MediaType: "application/x-protobuf",
		ByteSize: uint64(len(freshBytes)), SchemaVersion: 1}
	if err = repo.RegisterArtifact(ctx, corpusID, freshRef); err != nil {
		t.Fatal(err)
	}
	freshInput := SemanticRegistryInputs{SourceRef: sourceRef, SourceBytes: sourceBytes,
		CandidateRef: freshRef, CandidateBytes: freshBytes}
	if _, err = repo.commitSemanticResolutions(ctx, nil, freshInput,
		redecision, approval, 64, 8); !errors.Is(err, ErrConflict) {
		t.Fatalf("same proposal received a second decision: %v", err)
	}
	raceBase := proto.Clone(redecision).(*pb.RegistryResolveRequest)
	candidateIDsByMention := make(map[string][]string, len(freshCandidates.Lookups))
	for _, lookup := range freshCandidates.Lookups {
		for _, scope := range lookup.Scopes {
			candidateIDsByMention[lookup.MentionId] = append(candidateIDsByMention[lookup.MentionId], scope.CandidateIds...)
		}
	}
	for index, item := range raceBase.Proposals {
		item.Meta.RecordId = "proposal:race:" + string(rune('a'+index))
		item.Action = pb.ResolutionAction_RESOLUTION_ACTION_DEFER
		item.CandidateIds = append([]string(nil), candidateIDsByMention[item.MentionIds[0]]...)
	}
	raceResults := make([]error, 2)
	var raceWG sync.WaitGroup
	for index := range raceResults {
		index := index
		raceWG.Add(1)
		go func() {
			defer raceWG.Done()
			raceRequest := proto.Clone(raceBase).(*pb.RegistryResolveRequest)
			raceRequest.OperationKey = "semantic:race:" + string(rune('a'+index))
			_, raceResults[index] = repo.commitSemanticResolutions(ctx, nil, freshInput, raceRequest, nil, 64, 8)
		}()
	}
	raceWG.Wait()
	succeeded := 0
	for _, raceErr := range raceResults {
		if raceErr == nil {
			succeeded++
		}
	}
	var raceOperations int
	var currentRevision int64
	if err = repo.pool.QueryRow(ctx, `SELECT count(*) FROM registry_semantic_operations
		WHERE corpus_id=$1 AND operation_key IN ('semantic:race:a','semantic:race:b')`, corpusID).Scan(&raceOperations); err != nil {
		t.Fatal(err)
	}
	if err = repo.pool.QueryRow(ctx, `SELECT registry_revision FROM corpus_state WHERE corpus_id=$1`,
		corpusID).Scan(&currentRevision); err != nil {
		t.Fatal(err)
	}
	if succeeded != 1 || raceOperations != 1 || currentRevision != int64(freshCandidates.RegistryRevision)+1 {
		t.Fatalf("concurrent CAS created inconsistent operations: errors=%v operations=%d revision=%d",
			raceResults, raceOperations, currentRevision)
	}
	// A prior writer can invalidate a pinned candidate revision after the intent is
	// saved. That job must fail once, rather than retry an immutable losing plan.
	if _, err = fileStore.Put(ctx, freshRef, bytes.NewReader(freshBytes)); err != nil {
		t.Fatal(err)
	}
	replanJobID := "job:semantic-replan"
	replanCheckpoint := proto.Clone(checkpoint).(*pb.Checkpoint)
	replanCheckpoint.JobId = replanJobID
	replanCheckpoint.Meta.RecordId = "checkpoint:semantic-replan-extract"
	replanBytes, err := (proto.MarshalOptions{Deterministic: true}).Marshal(replanCheckpoint)
	if err != nil {
		t.Fatal(err)
	}
	replanDigest := sha256.Sum256(replanBytes)
	replanPayload := []byte("semantic-replan-fixture")
	replanPayloadDigest := sha256.Sum256(replanPayload)
	if _, err = repo.pool.Exec(ctx, `INSERT INTO jobs
		(job_id,corpus_id,operation,state,stage,input_fingerprint,idempotency_key,
		request_hash,request_payload,attempt,stage_attempt,lease_fence)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,1,1,1)`, replanJobID, corpusID,
		int16(pb.JobOperation_JOB_OPERATION_INGEST), int16(pb.JobState_JOB_STATE_STAGED),
		int16(pb.JobStage_JOB_STAGE_EXTRACT), strings.Repeat("e", 64),
		"semantic-replan-fixture", hex.EncodeToString(replanPayloadDigest[:]), replanPayload); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.pool.Exec(ctx, `INSERT INTO job_checkpoints
		(checkpoint_id,job_id,stage,fence,payload,payload_hash,terminal_status)
		VALUES ($1,$2,$3,1,$4,$5,$6)`, replanCheckpoint.Meta.RecordId, replanJobID,
		int16(pb.JobStage_JOB_STAGE_EXTRACT), replanBytes,
		hex.EncodeToString(replanDigest[:]), int16(pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED)); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.pool.Exec(ctx, `UPDATE jobs SET latest_checkpoint_id=$2 WHERE job_id=$1`,
		replanJobID, replanCheckpoint.Meta.RecordId); err != nil {
		t.Fatal(err)
	}
	replanClaim, err := repo.ClaimResolveJob(ctx, "owner:replan", time.Minute)
	if err != nil || replanClaim.JobID != replanJobID {
		t.Fatalf("claim stale candidate job: job=%+v err=%v", replanClaim, err)
	}
	replanRequest := proto.Clone(raceBase).(*pb.RegistryResolveRequest)
	replanRequest.OperationKey = "semantic:terminal-replan"
	replanHandoff, err := workflows.NewSemanticResolutionHandoff(repo, fileStore,
		uint64(len(sourceBytes)+len(freshBytes)), 64, 8)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = replanHandoff.CommitAndCheckpoint(ctx, replanClaim, freshRef, replanRequest,
		nil, resolveModel, resolveProducer,
		&pb.TokenUsage{TokenizerId: "resolver-tokenizer"}, "resolution:terminal-replan"); !errors.Is(err, domain.ErrResolutionReplan) {
		t.Fatalf("stale candidate intent did not request replan: %v", err)
	}
	var replanState int16
	if err = repo.pool.QueryRow(ctx, `SELECT state FROM jobs WHERE job_id=$1`, replanJobID).
		Scan(&replanState); err != nil || replanState != int16(pb.JobState_JOB_STATE_FAILED) {
		t.Fatalf("stale immutable intent remained retryable: state=%d err=%v", replanState, err)
	}
	forged := input
	forged.CandidateBytes = append([]byte(nil), candidateBytes...)
	forged.CandidateBytes[len(forged.CandidateBytes)-1] ^= 1
	if _, err = repo.commitSemanticResolutions(ctx, nil, forged, request,
		approval, 64, 8); err == nil {
		t.Fatal("forged candidate bytes committed")
	}
	missingSource := input
	missingSource.SourceRef = proto.Clone(sourceRef).(*pb.ArtifactRef)
	missingSource.SourceRef.ArtifactId = "artifact:missing-source"
	if _, err = repo.commitSemanticResolutions(ctx, nil, missingSource, request,
		approval, 64, 8); err == nil {
		t.Fatal("unregistered EXTRACT artifact committed")
	}
	corruptTx, err := repo.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer corruptTx.Rollback(ctx)
	if _, err = corruptTx.Exec(ctx, `ALTER TABLE registry_semantic_decisions
		DISABLE TRIGGER registry_semantic_decisions_append_only`); err != nil {
		t.Fatal(err)
	}
	if _, err = corruptTx.Exec(ctx, `UPDATE registry_semantic_decisions SET decision_hash=$3
		WHERE corpus_id=$1 AND proposal_id=$2`, corpusID, request.Proposals[0].Meta.RecordId,
		strings.Repeat("0", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err = corruptTx.Exec(ctx, `ALTER TABLE registry_semantic_decisions
		ENABLE TRIGGER registry_semantic_decisions_append_only`); err != nil {
		t.Fatal(err)
	}
	if err = corruptTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.commitSemanticResolutions(ctx, nil, input, request,
		approval, 64, 8); !errors.Is(err, domain.ErrPersistentIntegrity) {
		t.Fatalf("corrupt semantic decision replay accepted: %v", err)
	}
}
