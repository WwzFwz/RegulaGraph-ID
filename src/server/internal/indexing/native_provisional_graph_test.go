// Exercises source-reviewed provisional identities through durable LINK and the
// real Rust ASSEMBLE/publication pipeline. EXTRACT text labels, BIND metadata,
// vectors and reviewer approvals remain synthetic fixtures; identity creation,
// source catalog, candidate planning and registry writes use production APIs.
// Opt-in backend/worker prerequisites match native_graph_test.go. This proves
// integration and provenance, not semantic accuracy or required performance.
// Required numeric gates remain in configs/benchmark-targets.yaml (unmeasured).
package indexing

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/adapters/storage"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/workflows"
)

func TestNativeProvisionalGraphPipeline(t *testing.T) {
	requireNativeGraph(t)
	t.Setenv("REGULAGRAPH_TEST_PROVISIONAL_GRAPH", "1")
	runInitialIndexPublication(t, false, true, "graph-rpc")
}

func nativeProvisionalPolicy() domain.CandidatePlanningPolicy {
	return domain.CandidatePlanningPolicy{ScopesByType: map[string][]string{"defined_term": {"ID:national", "ID:secondary"}},
		MaximumMentions: 32, MaximumScopesPerMention: 2, MaximumTotalScopes: 64}
}

func checkNativeProvisionalOutput(t *testing.T, ctx context.Context, repo *postgres.Repository, db *pgx.Conn, delta *pb.GraphDelta, revision, targetSequence uint64) {
	t.Helper()
	for _, entity := range delta.Entities {
		id := entity.Meta.RecordId
		if !strings.HasPrefix(id, "canonical:provisional:") || entity.EntityType != "defined_term" ||
			entity.ReviewState != pb.ReviewState_REVIEW_STATE_UNREVIEWED || len(entity.IdentityKeys) != 1 ||
			entity.IdentityKeys[0].Namespace != domain.ProvisionalSourceIdentityNamespace {
			t.Fatal("ASSEMBLE changed provisional identity, type or review status")
		}
		profile, _, err := repo.LoadAliasProfile(ctx, delta.Meta.CorpusId, id, revision)
		if err != nil || profile == nil || profile.Meta.Visibility != nil {
			t.Fatal("invalid source registry profile", err)
		}
		// ASSEMBLE adds only the new generation's visibility to registry records.
		// Compare every other field against the stored source review profile.
		profile.Meta.Visibility = &pb.Visibility{FromSeq: targetSequence}
		if !proto.Equal(profile, entity) {
			t.Fatal("Rust canonical entity differs from reviewed registry profile")
		}
		var reviewed bool
		if err = db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sourced_alias_reviews WHERE corpus_id=$1 AND canonical_id=$2 AND create_provisional)`, delta.Meta.CorpusId, id).Scan(&reviewed); err != nil || !reviewed {
			t.Fatal("graph identity has no source creation receipt", err)
		}
	}
	t.Log("Rust output preserves both reviewed source identities and UNREVIEWED profiles")
}

func registerNativeProvisionalIdentities(t *testing.T, ctx context.Context, repo *postgres.Repository, db *pgx.Conn,
	files *storage.FileStore, jobID string, documents *pb.DocumentBatch, artifacts indexMemoryArtifacts,
	source *pb.ExtractionBatch, extract domain.GraphSourceArtifact) ([]string, []domain.RegistryCandidatePlan, uint64) {
	t.Helper()
	// Persist the already registered original bytes before source inspection. The
	// worker and operator hydrate the same content-addressed artifacts.
	refs := []*pb.ArtifactRef{source.SourceDocumentBatch}
	for _, text := range documents.TextArtifacts {
		refs = append(refs, text.NormalizedTextRef)
	}
	for _, ref := range refs {
		if _, err := files.Put(ctx, ref, bytes.NewReader(artifacts[ref.ArtifactId])); err != nil {
			t.Fatal("persist provisional source", err)
		}
	}
	// EXTRACT itself is a fixture. Its successful catalog/checkpoint is committed
	// by production storage, under an explicit fixture owner/fence.
	_, err := db.Exec(ctx, `UPDATE jobs SET state=$2,stage=$3,attempt=1,stage_attempt=1,
lease_owner='owner:native-extract',lease_fence=1,lease_expires_at=clock_timestamp()+interval '1 minute' WHERE job_id=$1`,
		jobID, int16(pb.JobState_JOB_STATE_RUNNING), int16(pb.JobStage_JOB_STAGE_EXTRACT))
	if err != nil {
		t.Fatal(err)
	}
	cp := &pb.Checkpoint{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: source.Meta.CorpusId, RecordId: "checkpoint:native-extract"},
		JobId: jobID, Stage: pb.JobStage_JOB_STAGE_EXTRACT, Fence: 1, TerminalStatus: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED,
		CompletedBatchKeys: []string{extract.Reference.ArtifactId}, ArtifactHashes: []*pb.ContentHash{extract.Reference.ContentHash}, Manifest: source.Dependencies.ProducerManifest}
	if err = repo.SaveExtractionCheckpoint(ctx, cp, "owner:native-extract", extract.Reference, source); err != nil {
		t.Fatal("catalog provisional EXTRACT", err)
	}
	if _, err = db.Exec(ctx, `UPDATE jobs SET state=$2,lease_owner=NULL,lease_expires_at=NULL WHERE job_id=$1`, jobID, int16(pb.JobState_JOB_STATE_STAGED)); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, len(source.Mentions))
	var revision uint64
	for i, mention := range source.Mentions {
		options := workflows.SourcedAliasOptions{CreateProvisional: true, Corpus: source.Meta.CorpusId, AuthScope: source.Context.AuthScopeRef,
			SourceArtifactID: extract.Reference.ArtifactId, MentionID: mention.Meta.RecordId, Scope: "ID:national",
			PreferredLabel: fmt.Sprintf("Synthetic defined term %d", i), Policy: nativeProvisionalPolicy()}
		inspection, e := workflows.InspectSourcedAlias(ctx, repo, files, options)
		if e != nil {
			t.Fatal("inspect provisional graph identity", e)
		}
		preview, e := inspection.Preview()
		if e != nil {
			t.Fatal(e)
		}
		ids[i] = preview.Registration.Entity.Meta.RecordId
		if !strings.HasPrefix(ids[i], "canonical:provisional:") || !preview.CreateProvisional || len(preview.LookupScopes) != 2 {
			t.Fatal("creation lost source identity or complete policy scopes")
		}
		revision, e = inspection.Accept(ctx, repo, fmt.Sprintf("provisional:native:%d", i), preview.PlanHash, "reviewer:synthetic",
			"synthetic source occurrence for integration, not global legal equivalence")
		if e != nil || revision != preview.ExpectedRevision+1 {
			t.Fatal("commit provisional graph identity", revision, e)
		}
		stored, _, e := repo.LoadAliasProfile(ctx, source.Meta.CorpusId, ids[i], revision)
		if e != nil || !proto.Equal(stored, preview.Registration.Entity) {
			t.Fatal("provisional profile changed after registration", e)
		}
	}
	if len(ids) != 2 || ids[0] == ids[1] {
		t.Fatal("distinct source occurrences collapsed")
	}
	plans, err := domain.PlanRegistryCandidates(source, nativeProvisionalPolicy())
	if err != nil {
		t.Fatal(err)
	}
	t.Log("created two source-reviewed provisional identities via production registry; both policy scopes retained")
	return ids, plans, revision
}
