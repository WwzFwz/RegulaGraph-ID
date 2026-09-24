// Exercises semantic LINK/DEFER transactions against disposable PostgreSQL, including revision
// CAS, replay after later writes, candidate forgery, and stored-decision corruption. The test
// skips without REGULAGRAPH_TEST_POSTGRES_DSN and never proves legal identity accuracy or
// required latency/throughput; those need human gold and the reference workload.
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
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

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
	if _, err = repo.CommitSemanticResolutions(ctx, input, request, nil, 64, 8); err == nil {
		t.Fatal("unapproved LINK was committed")
	}
	if _, err = repo.CommitSemanticResolutions(ctx, input, request, approval, 64, 8); !errors.Is(err, ErrConflict) {
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
	if _, err = repo.CommitSemanticResolutions(ctx, input, request, revokedApproval, 64, 8); !errors.Is(err, ErrConflict) {
		t.Fatalf("revoked review authorized LINK: %v", err)
	}
	changedProposal := proto.Clone(request).(*pb.RegistryResolveRequest)
	changedProposal.Proposals[0].Method = "changed_after_review"
	if _, err = repo.CommitSemanticResolutions(ctx, input, changedProposal, approval, 64, 8); !errors.Is(err, ErrConflict) {
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
	if _, err = repo.CommitSemanticResolutions(ctx, otherCandidateInput, request, approval, 64, 8); !errors.Is(err, ErrConflict) {
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
	if _, err = repo.CommitSemanticResolutions(ctx, forgedRegistered, request, approval, 64, 8); !errors.Is(err, ErrConflict) {
		t.Fatalf("registered but forged candidate view was accepted: %v", err)
	}
	response, err := repo.CommitSemanticResolutions(ctx, input, request, approval, 64, 8)
	if err != nil || response.RegistryRevision != revision+1 || len(response.Assignments) != 2 ||
		response.Assignments[0].GetDecision().AssignedCanonicalIds[0] != canonicalID ||
		len(response.Assignments[1].GetDecision().AssignedCanonicalIds) != 0 {
		t.Fatalf("commit semantic LINK/DEFER: response=%v err=%v", response, err)
	}
	resolveModel := &pb.ModelManifest{ModelId: "resolver", Version: "v1", Task: pb.ModelTask_MODEL_TASK_RESOLVE,
		WeightsHash:   &pb.ContentHash{Sha256: strings.Repeat("1", 64)},
		TokenizerHash: &pb.ContentHash{Sha256: strings.Repeat("2", 64)},
		MaxTokens:     128, Precision: "fp32", Backend: "test"}
	resolveProducer := &pb.ProducerManifest{Software: "resolve-worker", Build: "test", SchemaVersion: 1,
		ConfigHash: &pb.ContentHash{Sha256: strings.Repeat("3", 64)}, Models: []*pb.ModelManifest{resolveModel}}
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
	replayed, err := repo.CommitSemanticResolutions(ctx, input, retry, approval, 64, 8)
	if err != nil || replayed.RegistryRevision != response.RegistryRevision ||
		replayed.RequestId != retry.Context.RequestId ||
		replayed.Assignments[0].GetDecision().Meta.RecordId != response.Assignments[0].GetDecision().Meta.RecordId {
		t.Fatalf("semantic replay changed decision: replay=%v err=%v", replayed, err)
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
	if _, err = repo.CommitSemanticResolutions(ctx, input, request,
		changedApproval, 64, 8); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed review reused operation key: %v", err)
	}
	stale := proto.Clone(request).(*pb.RegistryResolveRequest)
	stale.OperationKey = "semantic:stale"
	if _, err = repo.CommitSemanticResolutions(ctx, input, stale,
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
	if _, err = repo.CommitSemanticResolutions(ctx, freshInput,
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
			_, raceResults[index] = repo.CommitSemanticResolutions(ctx, freshInput, raceRequest, nil, 64, 8)
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
	forged := input
	forged.CandidateBytes = append([]byte(nil), candidateBytes...)
	forged.CandidateBytes[len(forged.CandidateBytes)-1] ^= 1
	if _, err = repo.CommitSemanticResolutions(ctx, forged, request,
		approval, 64, 8); err == nil {
		t.Fatal("forged candidate bytes committed")
	}
	missingSource := input
	missingSource.SourceRef = proto.Clone(sourceRef).(*pb.ArtifactRef)
	missingSource.SourceRef.ArtifactId = "artifact:missing-source"
	if _, err = repo.CommitSemanticResolutions(ctx, missingSource, request,
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
	if _, err = repo.CommitSemanticResolutions(ctx, input, request,
		approval, 64, 8); !errors.Is(err, domain.ErrPersistentIntegrity) {
		t.Fatalf("corrupt semantic decision replay accepted: %v", err)
	}
}
