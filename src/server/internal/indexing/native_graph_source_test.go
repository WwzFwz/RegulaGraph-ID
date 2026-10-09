// Supplies nonempty, explicitly synthetic EXTRACT evidence to the native graph
// integration. Identity allocation, alias/candidate lookup, resolution intent,
// registry CAS and RESOLVE checkpoint use production code. EXTRACT checkpoint
// and human-review rows are fixture seeds, never claims of live LLM or legal
// review. Source spans must match registered bytes; two identities sharing an
// alias remain distinct. No quality/performance benchmark is inferred here.
package indexing

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/adapters/storage"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/workflows"
)

func populateNativeGraphExtraction(t *testing.T, docs *pb.DocumentBatch, extract *pb.ExtractionBatch, artifacts indexMemoryArtifacts) {
	t.Helper()
	extract.Meta.RecordId = "extract:native-graph-fixture"
	chunk := docs.Chunks[0]
	var text *pb.TextArtifact
	for _, candidate := range docs.TextArtifacts {
		if candidate.Meta.RecordId == chunk.TextSpan.TextArtifactId {
			text = candidate
		}
	}
	if text == nil || chunk.TextSpan.EndByte-chunk.TextSpan.StartByte < 2 {
		t.Fatal("fixture requires two bytes inside a source chunk")
	}
	versionID := chunk.ProvisionVersionRefs[0]
	var regulationID string
	for _, v := range docs.Versions {
		if v.Meta.RecordId == versionID {
			for _, p := range docs.Provisions {
				if p.Meta.RecordId == v.ProvisionId {
					regulationID = p.RegulationId
				}
			}
		}
	}
	ref := &pb.SourceVersionRef{SourceBlobId: text.SourceBlobId, ProvisionVersionId: versionID, RegulationId: regulationID}
	meta := func(id string) *pb.RecordMeta {
		return &pb.RecordMeta{SchemaVersion: 1, CorpusId: docs.Meta.CorpusId, RecordId: id}
	}
	for i := range 2 {
		span := proto.Clone(chunk.TextSpan).(*pb.TextSpan)
		span.StartByte += uint64(i)
		span.EndByte = span.StartByte + 1
		surface := string(artifacts[text.NormalizedTextRef.ArtifactId][span.StartByte:span.EndByte])
		extract.Mentions = append(extract.Mentions, &pb.Mention{Meta: meta(fmt.Sprintf("mention:native:%d", i)), TextSpan: span,
			SourceRefs: []*pb.SourceVersionRef{ref}, SurfaceForm: surface, CandidateType: "organization", ExtractionManifest: extract.Dependencies.ProducerManifest})
	}
	extract.Assertions = []*pb.RelationAssertion{{Meta: meta("assertion:native:references"), SubjectId: extract.Mentions[0].Meta.RecordId,
		ObjectId: extract.Mentions[1].Meta.RecordId, PredicateId: "references", OntologyVersion: extract.OntologyVersion,
		Origin: pb.AssertionOrigin_ASSERTION_ORIGIN_EXPLICIT, TemporalScope: &pb.TemporalScope{Mode: pb.TemporalMode_TEMPORAL_MODE_CURRENT, UnresolvedPolicy: pb.UnresolvedPolicy_UNRESOLVED_POLICY_REPORT}}}
	extract.Supports = []*pb.SupportRecord{{Meta: meta("support:native:references"), AssertionId: extract.Assertions[0].Meta.RecordId,
		EvidenceSpans: []*pb.TextSpan{extract.Mentions[0].TextSpan, extract.Mentions[1].TextSpan}, SourceRefs: []*pb.SourceVersionRef{ref},
		ExtractionManifest: extract.Dependencies.ProducerManifest, IndependentSourceGroup: "source-group:native-fixture", ReviewState: pb.ReviewState_REVIEW_STATE_UNREVIEWED}}
	if err := domain.ValidateExtractionBatchClosure(extract, docs, 4096); err != nil {
		t.Fatal("nonempty fixture source closure", err)
	}
}

func commitNativeGraphResolution(t *testing.T, ctx context.Context, repo *postgres.Repository, db *pgx.Conn, files *storage.FileStore,
	jobID string, source *pb.ExtractionBatch, extract domain.GraphSourceArtifact, model *pb.ModelManifest, producer *pb.ProducerManifest,
	put func(proto.Message, string, string) domain.GraphSourceArtifact) *workflows.SemanticResolutionOutput {
	t.Helper()
	corpus := source.Meta.CorpusId
	var revision uint64
	if err := db.QueryRow(ctx, `SELECT registry_revision FROM corpus_state WHERE corpus_id=$1`, corpus).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	claims := make([]domain.CanonicalIdentityClaim, 2)
	for i := range claims {
		claims[i] = domain.CanonicalIdentityClaim{ProposalKey: fmt.Sprintf("organization:native:%d", i), EntityType: domain.CanonicalEntityTypeOrganization,
			IdentityScope: domain.IssuerIdentityKeyNamespace, IdentityKey: fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("native-organization-%d", i)))), PayloadHash: strings.Repeat("a", 64)}
	}
	assigned, revision, err := repo.ResolveCanonicalIdentities(ctx, corpus, "allocate:native-graph", revision, claims)
	if err != nil || len(assigned) != 2 {
		t.Fatal("allocate native graph identities", err)
	}
	registrations := make([]postgres.AliasRegistration, 2)
	plans := make([]postgres.RegistryCandidatePlan, 2)
	for i, mention := range source.Mentions {
		canonical := assigned[i].CanonicalID
		registrations[i] = postgres.AliasRegistration{Entity: &pb.CanonicalEntity{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpus, RecordId: canonical},
			EntityType: "organization", PreferredLabel: fmt.Sprintf("Synthetic organization %d", i), Scope: "ID:national", RegistryRevision: revision, ReviewState: pb.ReviewState_REVIEW_STATE_UNREVIEWED,
			IdentityKeys: []*pb.IdentityKey{{Namespace: claims[i].IdentityScope, Value: claims[i].IdentityKey}}},
			Alias: &pb.Alias{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpus, RecordId: fmt.Sprintf("alias:native:%d", i)}, CanonicalId: canonical,
				Surface: mention.SurfaceForm, NormalizedLookup: strings.ToLower(mention.SurfaceForm), Scope: "ID:national", Language: "id", SupportRefs: []string{source.Supports[0].Meta.RecordId}}}
		plans[i] = postgres.RegistryCandidatePlan{MentionID: mention.Meta.RecordId, Scopes: []postgres.RegistryLookupScope{{EntityType: "organization", CanonicalScope: "ID:national", NormalizedLookup: strings.ToLower(mention.SurfaceForm)}}}
	}
	revision, err = repo.RegisterCanonicalAliases(ctx, corpus, "aliases:native-graph", revision, registrations)
	if err != nil {
		t.Fatal("native graph aliases", err)
	}
	candidates, err := repo.PrepareRegistryCandidateBatch(ctx, source, extract.Reference, producer, "candidates:native-graph", plans, 32, 32, 4096, 32)
	if err != nil {
		t.Fatal("native graph candidates", err)
	}
	candidate := put(candidates, "native-candidates", "application/x-protobuf")
	request := &pb.RegistryResolveRequest{Context: proto.Clone(source.Context).(*pb.RequestContext), OperationKey: "resolve:native-graph", ExpectedRevision: revision}
	approvals := make([]domain.ReviewedLink, 2)
	for i, mention := range source.Mentions {
		proposal := &pb.ResolutionProposal{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpus, RecordId: fmt.Sprintf("proposal:native:%d", i)},
			MentionIds: []string{mention.Meta.RecordId}, CandidateIds: []string{assigned[i].CanonicalID}, Action: pb.ResolutionAction_RESOLUTION_ACTION_LINK,
			Evidence: &pb.Provenance{Sources: mention.SourceRefs, Spans: []*pb.TextSpan{mention.TextSpan}}, Method: "synthetic-review-fixture", ExpectedRegistryRevision: revision, LocalCorrelationId: fmt.Sprintf("correlation:native:%d", i)}
		request.Proposals = append(request.Proposals, proposal)
		approvals[i] = domain.ReviewedLink{ProposalID: proposal.Meta.RecordId, CanonicalID: assigned[i].CanonicalID, Actor: "reviewer:synthetic", Reason: "fixture identity only, not legal review", ReviewID: fmt.Sprintf("review:native:%d", i)}
		raw, e := proto.MarshalOptions{Deterministic: true}.Marshal(proposal)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(ctx, `INSERT INTO registry_semantic_reviews(corpus_id,review_id,source_artifact_id,candidate_artifact_id,candidate_hash,proposal_id,proposal_hash,canonical_id,actor,reason) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, corpus, approvals[i].ReviewID, extract.Reference.ArtifactId, candidate.Reference.ArtifactId, candidate.Reference.ContentHash.Sha256, proposal.Meta.RecordId, fmt.Sprintf("%x", sha256.Sum256(raw)), approvals[i].CanonicalID, approvals[i].Actor, approvals[i].Reason); e != nil {
			t.Fatal(e)
		}
	}
	// Only EXTRACT is seeded; RESOLVE must produce its own intent/ledger/checkpoint.
	cp := &pb.Checkpoint{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpus, RecordId: "checkpoint:native-extract"}, JobId: jobID, Stage: pb.JobStage_JOB_STAGE_EXTRACT, Fence: 1,
		CompletedBatchKeys: []string{extract.Reference.ArtifactId}, ArtifactHashes: []*pb.ContentHash{extract.Reference.ContentHash}, Manifest: source.Dependencies.ProducerManifest, TerminalStatus: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED}
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(cp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO job_checkpoints(checkpoint_id,job_id,stage,fence,payload,payload_hash,terminal_status) VALUES($1,$2,$3,1,$4,$5,$6)`, cp.Meta.RecordId, jobID, int16(cp.Stage), raw, fmt.Sprintf("%x", sha256.Sum256(raw)), int16(cp.TerminalStatus)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `UPDATE jobs SET latest_checkpoint_id=$2,stage=$3,state=$4,lease_fence=1 WHERE job_id=$1`, jobID, cp.Meta.RecordId, int16(cp.Stage), int16(pb.JobState_JOB_STATE_STAGED)); err != nil {
		t.Fatal(err)
	}
	job, err := repo.ClaimResolveJob(ctx, "owner:native-resolve", time.Minute)
	if err != nil || job.JobID != jobID {
		t.Fatal("claim native RESOLVE", err)
	}
	handoff, err := workflows.NewSemanticResolutionHandoff(repo, files, uint64(domain.DefaultWireLimits.MaxBytes), 4096, 32)
	if err != nil {
		t.Fatal(err)
	}
	output, err := handoff.CommitAndCheckpoint(ctx, job, candidate.Reference, request, approvals, model, producer, &pb.TokenUsage{TokenizerId: "fixture"}, "resolve:native-graph")
	if err != nil {
		t.Fatal("actual RESOLVE commit/checkpoint", err)
	}
	return output
}
