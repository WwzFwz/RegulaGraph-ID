// Verifies provisional source-occurrence allocation in isolated PostgreSQL schemas.
// Real BIND/FileStore/catalog/candidate hydration protect atomicity and provenance;
// EXTRACT is synthetic and makes no claim about semantic quality or model accuracy.
package workflows

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/storage"
	"regulagraph.local/server/internal/domain"
)

func provisionalAliasFixture(t *testing.T, store BindingExecutionStore) (domain.SourcedAliasInput, *pb.ExtractionBatch, *pb.IngestionRequest) {
	t.Helper()
	in, source, request := sourcedAliasFixture(t, store)
	in.CreateProvisional, in.CanonicalID = true, ""
	source.Mentions[0].CandidateType = "defined_term"
	in.Policy.ScopesByType = map[string][]string{"defined_term": {"ID:national", "ID:secondary"}}
	in.Source = sourcedAliasProto(t, domain.ExtractionBatchMediaType, source)
	pin, _ := in.Policy.Fingerprint()
	request.ConfigManifest.InputHashes = []*pb.ContentHash{pin}
	return in, source, request
}

func TestProvisionalAliasAgainstPostgres(t *testing.T) {
	for _, mode := range []string{"commit-replay-hydration", "rollback", "stale", "concurrent", "other-scope", "incomplete-lookup"} {
		t.Run(mode, func(t *testing.T) {
			repo, db := reviewDatabase(t)
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			in, source, request := provisionalAliasFixture(t, repo)
			files, err := storage.NewFileStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer files.Close()
			for _, a := range []domain.AliasSourceArtifact{in.Source, in.Document, in.Text} {
				if _, err = files.Put(ctx, a.Ref, bytes.NewReader(a.Bytes)); err != nil {
					t.Fatal(err)
				}
				if a.Ref.ArtifactId != in.Text.Ref.ArtifactId {
					if err = repo.RegisterArtifact(ctx, in.Corpus, a.Ref); err != nil {
						t.Fatal(err)
					}
				}
			}
			raw, _ := (proto.MarshalOptions{Deterministic: true}).Marshal(request)
			hash := fmt.Sprintf("%x", sha256.Sum256(raw))
			_, err = db.Exec(ctx, `INSERT INTO jobs(job_id,corpus_id,operation,state,stage,input_fingerprint,idempotency_key,request_hash,request_payload,
attempt,stage_attempt,lease_owner,lease_fence,lease_expires_at) VALUES('job:alias-extract',$1,$2,$3,$4,$5,'job:alias-extract',$5,$6,1,1,'owner:alias',1,clock_timestamp()+interval '1 minute')`,
				in.Corpus, int16(pb.JobOperation_JOB_OPERATION_INGEST), int16(pb.JobState_JOB_STATE_RUNNING), int16(pb.JobStage_JOB_STAGE_EXTRACT), hash, raw)
			if err != nil {
				t.Fatal(err)
			}
			cp := &pb.Checkpoint{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: in.Corpus, RecordId: "checkpoint:alias"}, JobId: "job:alias-extract", Stage: pb.JobStage_JOB_STAGE_EXTRACT, Fence: 1,
				TerminalStatus: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED, CompletedBatchKeys: []string{in.Source.Ref.ArtifactId}, ArtifactHashes: []*pb.ContentHash{in.Source.Ref.ContentHash}, Manifest: source.Dependencies.ProducerManifest}
			if err = repo.SaveExtractionCheckpoint(ctx, cp, "owner:alias", in.Source.Ref, source); err != nil {
				t.Fatal(err)
			}

			opts := SourcedAliasOptions{CreateProvisional: true, Corpus: in.Corpus, AuthScope: in.AuthScope, SourceArtifactID: in.Source.Ref.ArtifactId,
				MentionID: in.MentionID, Scope: in.Scope, PreferredLabel: in.PreferredLabel, Policy: in.Policy}
			view, err := InspectSourcedAlias(ctx, repo, files, opts)
			if err != nil {
				t.Fatal("inspect", err)
			}
			p, err := view.Preview()
			if err != nil {
				t.Fatal(err)
			}
			canonical := p.Registration.Entity.Meta.RecordId
			if !p.CreateProvisional || p.Registration.Entity.RegistryRevision != in.ExpectedRevision+1 {
				t.Fatal("provisional profile lost")
			}
			var identityCount int
			if err = db.QueryRow(ctx, `SELECT count(*) FROM canonical_identities`).Scan(&identityCount); err != nil {
				t.Fatal(err)
			}
			reason := "source occurrence provisional; not global legal equivalence"
			switch mode {
			case "rollback":
				_, err = db.Exec(ctx, `ALTER TABLE sourced_alias_reviews ADD CONSTRAINT reject_provisional_review CHECK(false) NOT VALID`)
			case "stale":
				_, err = db.Exec(ctx, `UPDATE corpus_state SET registry_revision=registry_revision+1 WHERE corpus_id=$1`, in.Corpus)
			case "other-scope":
				// Seed an occupied secondary scope after inspection without advancing the
				// revision, deliberately probing the in-transaction negative recheck.
				other := proto.Clone(p.Registration.Alias).(*pb.Alias)
				other.Meta.RecordId = "alias:other"
				other.Scope = "ID:secondary"
				rawAlias, _ := proto.Marshal(other)
				_, err = db.Exec(ctx, `INSERT INTO registry_alias_versions(corpus_id,alias_id,from_revision,canonical_id,entity_type,canonical_scope,surface,normalized_lookup,language,support_refs,alias_payload,record_hash)
 VALUES($1,$2,$3,$4,'defined_term',$5,$6,$7,'und',$8,$9,$10)`, in.Corpus, other.Meta.RecordId, int64(in.ExpectedRevision), p.TargetDocument.Regulations[0].IssuerId, other.Scope, other.Surface, other.NormalizedLookup, other.SupportRefs, rawAlias, fmt.Sprintf("%x", sha256.Sum256(rawAlias)))
			case "incomplete-lookup":
				s := p.LookupScopes[1]
				_, err = db.Exec(ctx, `INSERT INTO lookup_scope_revisions(corpus_id,scope_key,revision,alias_result_count) VALUES($1,$2,$3,NULL)`, in.Corpus, domain.RegistryLookupScopeID(s.EntityType, s.CanonicalScope, s.NormalizedLookup), int64(in.ExpectedRevision))
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "concurrent" {
				results := make(chan error, 2)
				for i := 0; i < 2; i++ {
					go func(i int) {
						_, e := view.Accept(ctx, repo, fmt.Sprintf("provisional:%d", i), p.PlanHash, "operator:test", reason)
						results <- e
					}(i)
				}
				x, y := <-results, <-results
				if (x == nil) == (y == nil) {
					t.Fatal("expected one CAS winner", x, y)
				}
				var count int
				if e := db.QueryRow(ctx, `SELECT count(*) FROM sourced_alias_reviews WHERE create_provisional`).Scan(&count); e != nil || count != 1 {
					t.Fatal(count, e)
				}
				return
			}
			revision, err := view.Accept(ctx, repo, "provisional:test", p.PlanHash, "operator:test", reason)
			if mode != "commit-replay-hydration" {
				if err == nil {
					t.Fatal("invalid creation succeeded")
				}
				if mode == "rollback" && !strings.Contains(err.Error(), "reject_provisional_review") {
					t.Fatal("failure before final insert", err)
				}
				if (mode == "other-scope" || mode == "incomplete-lookup") && !strings.Contains(err.Error(), "every policy scope") {
					t.Fatal("negative recheck not reached", err)
				}
				var count int
				if e := db.QueryRow(ctx, `SELECT count(*) FROM canonical_identities`).Scan(&count); e != nil || count != identityCount {
					t.Fatal("identity leaked", count, e)
				}
				if e := db.QueryRow(ctx, `SELECT (SELECT count(*) FROM registry_entity_profiles)+(SELECT count(*) FROM registry_alias_operations)+(SELECT count(*) FROM sourced_alias_reviews)`).Scan(&count); e != nil || count != 0 {
					t.Fatal("partial profile/review", count, e)
				}
				var current int64
				if e := db.QueryRow(ctx, `SELECT registry_revision FROM corpus_state WHERE corpus_id=$1`, in.Corpus).Scan(&current); e != nil {
					t.Fatal(e)
				}
				want := in.ExpectedRevision
				if mode == "stale" {
					want++
				}
				if uint64(current) != want {
					t.Fatal("revision leaked", current, want)
				}
				return
			}
			if err != nil || revision != in.ExpectedRevision+1 {
				t.Fatal("accept", revision, err)
			}
			entity, _, err := repo.LoadAliasProfile(ctx, in.Corpus, canonical, revision)
			if err != nil || entity == nil || entity.ReviewState != pb.ReviewState_REVIEW_STATE_UNREVIEWED {
				t.Fatal("profile", entity, err)
			}
			opts.Revision = in.ExpectedRevision
			replay, err := InspectSourcedAlias(ctx, repo, files, opts)
			if err != nil {
				t.Fatal("historical restart", err)
			}
			if r, e := replay.Accept(ctx, repo, "provisional:test", p.PlanHash, "operator:test", reason); e != nil || r != revision {
				t.Fatal("replay", r, e)
			}
			if _, e := replay.Accept(ctx, repo, "provisional:test", p.PlanHash, "operator:changed", reason); e == nil {
				t.Fatal("changed actor replay")
			}
			opts.Revision = 0
			if _, e := InspectSourcedAlias(ctx, repo, files, opts); e == nil {
				t.Fatal("own alias allowed second allocation")
			}
			plans, err := domain.PlanRegistryCandidates(source, in.Policy)
			if err != nil {
				t.Fatal(err)
			}
			candidates, err := repo.PrepareRegistryCandidateBatch(ctx, source, in.Source.Ref, bindingProducer(), "candidate:provisional", plans, 100000, 100, 100, 100)
			if err != nil || len(candidates.GetCandidates()) != 1 {
				t.Fatal("candidate", err)
			}
			if candidates.Candidates[0].Meta.RecordId != canonical {
				t.Fatal("canonical drift")
			}
			h := &SemanticResolutionHandoff{store: repo, maximumReferences: 100000}
			remaining := uint64(32 << 20)
			req := &pb.SemanticResolveRequest{Batch: &pb.SemanticBatchContext{Context: source.Context, OntologyVersion: source.OntologyVersion}, Items: []*pb.AmbiguousMention{{ItemId: in.MentionID, Mention: source.Mentions[0], Candidates: candidates.Candidates, ExpectedRegistryRevision: revision}}}
			if _, err = h.hydrateCandidateEvidence(ctx, req, candidates, files, &remaining, newResolutionHydrationBudget(16<<20, 100000)); err != nil {
				t.Fatal("consumer hydration", err)
			}
			if len(req.Items[0].CandidateContexts) != 1 {
				t.Fatal("missing provisional source context")
			}
			reservation, e := repo.ReservePublication(ctx, "publication:provisional", "", in.Corpus, "snapshot:provisional", "")
			if e != nil {
				t.Fatal(e)
			}
			if e = repo.BindPublicationRegistry(ctx, reservation.PublicationID, in.Corpus, reservation.Fence, revision); e != nil {
				t.Fatal(e)
			}
			exported, e := repo.ExportRegistryEntityView(ctx, domain.RegistryEntityExport{ViewID: "view:provisional", CorpusID: in.Corpus, PublicationID: reservation.PublicationID,
				Fence: reservation.Fence, Revision: revision, EntityIDs: []string{canonical}, Producer: bindingProducer(), MaximumEntities: 10, MaximumBytes: 1 << 20})
			if e != nil || len(exported.GetEntities()) != 1 {
				t.Fatal("ASSEMBLE canonical export", e)
			}
			if exported.Entities[0].Meta.RecordId != canonical || exported.Entities[0].IdentityKeys[0].Namespace != domain.ProvisionalSourceIdentityNamespace || exported.Entities[0].ReviewState != pb.ReviewState_REVIEW_STATE_UNREVIEWED {
				t.Fatal("export upgraded or changed provisional identity")
			}

		})
	}
}
