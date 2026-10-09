// Verifies sourced alias review against an isolated PostgreSQL schema and real
// immutable file storage. Exercises BIND authority, CAS/replay/rollback and actual
// downstream candidate hydration. Synthetic EXTRACT mentions are explicit fixtures;
// no model quality or legal correctness is claimed. Opt-in DSN; SKIP is not PASS.
package workflows

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/adapters/storage"
	"regulagraph.local/server/internal/domain"
)

func TestSourcedAliasAgainstPostgres(t *testing.T) {
	for _, mode := range []string{"commit-replay-hydration", "rollback", "stale", "missing-support", "wrong-policy", "changed-target-key", "missing-review-replay", "concurrent"} {
		t.Run(mode, func(t *testing.T) {
			repo, db := reviewDatabase(t)
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			in, source, request := sourcedAliasFixture(t, repo)
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
			opts := SourcedAliasOptions{Corpus: in.Corpus, AuthScope: in.AuthScope, SourceArtifactID: in.Source.Ref.ArtifactId, TargetDocumentID: in.TargetDocument.Ref.ArtifactId,
				MentionID: in.MentionID, CanonicalID: in.CanonicalID, Scope: in.Scope, PreferredLabel: in.PreferredLabel, Policy: in.Policy}
			view, err := InspectSourcedAlias(ctx, repo, files, opts)
			if err != nil {
				t.Fatal("inspect", err)
			}
			p, err := view.Preview()
			if err != nil {
				t.Fatal(err)
			}
			if p.ExpectedRevision != in.ExpectedRevision || p.TargetDocument.Regulations[0].IssuerId != in.CanonicalID {
				t.Fatal("inspection lost independent target")
			}
			var before int
			if err = db.QueryRow(ctx, `SELECT count(*) FROM registry_alias_versions`).Scan(&before); err != nil || before != 0 {
				t.Fatal("inspection mutated aliases", before, err)
			}
			if _, e := view.Accept(ctx, repo, "alias:test", strings.Repeat("0", 64), "operator:test", "reviewed"); e == nil {
				t.Fatal("wrong approval hash accepted")
			}
			switch mode {
			case "rollback":
				_, err = db.Exec(ctx, `ALTER TABLE sourced_alias_reviews ADD CONSTRAINT reject_alias_review CHECK(false) NOT VALID`)
			case "stale":
				_, err = db.Exec(ctx, `UPDATE corpus_state SET registry_revision=registry_revision+1 WHERE corpus_id=$1`, in.Corpus)
			case "missing-support":
				_, err = db.Exec(ctx, `ALTER TABLE extraction_evidence_sources DISABLE TRIGGER extraction_evidence_sources_append_only; DELETE FROM extraction_evidence_sources; ALTER TABLE extraction_evidence_sources ENABLE TRIGGER extraction_evidence_sources_append_only`)
			case "wrong-policy":
				request.ConfigManifest.InputHashes = []*pb.ContentHash{bindingHash("a")}
				raw, _ = proto.Marshal(request)
				hash = fmt.Sprintf("%x", sha256.Sum256(raw))
				_, err = db.Exec(ctx, `UPDATE jobs SET request_payload=$2,request_hash=$3 WHERE job_id=$1`, "job:alias-extract", raw, hash)
			case "changed-target-key":
				_, err = db.Exec(ctx, `UPDATE canonical_identities SET identity_key=$3 WHERE corpus_id=$1 AND canonical_id=$2`, in.Corpus, in.CanonicalID, strings.Repeat("f", 64))
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "concurrent" {
				type outcome struct {
					revision uint64
					err      error
				}
				results := make(chan outcome, 2)
				for i := 0; i < 2; i++ {
					go func(i int) {
						r, e := view.Accept(ctx, repo, fmt.Sprintf("alias:concurrent:%d", i), p.PlanHash, "operator:test", "reviewed")
						results <- outcome{r, e}
					}(i)
				}
				x, y := <-results, <-results
				if (x.err == nil) == (y.err == nil) {
					t.Fatal("expected one CAS winner", x, y)
				}
				var count int
				if e := db.QueryRow(ctx, `SELECT count(*) FROM sourced_alias_reviews`).Scan(&count); e != nil || count != 1 {
					t.Fatal("competing approval duplicated", count, e)
				}
				return
			}
			revision, err := view.Accept(ctx, repo, "alias:test", p.PlanHash, "operator:test", "reviewed exact source against BIND identity")
			if mode != "commit-replay-hydration" && mode != "missing-review-replay" {
				if err == nil {
					t.Fatal("invalid registration committed")
				}
				if (mode == "wrong-policy" || mode == "missing-support") && !errors.Is(err, domain.ErrPersistentIntegrity) {
					t.Fatal("wrong failure boundary", err)
				}
				if mode == "stale" && !errors.Is(err, postgres.ErrConflict) {
					t.Fatal("stale CAS was not tested", err)
				}
				if mode == "rollback" && !strings.Contains(err.Error(), "reject_alias_review") {
					t.Fatal("did not reach final review insert", err)
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
					t.Fatal("failed registration changed registry revision", current, want)
				}
				var count int
				if e := db.QueryRow(ctx, `SELECT (SELECT count(*) FROM registry_alias_versions)+(SELECT count(*) FROM registry_entity_profiles)+(SELECT count(*) FROM registry_alias_operations)+(SELECT count(*) FROM sourced_alias_reviews)`).Scan(&count); e != nil || count != 0 {
					t.Fatal("partial alias state", count, e)
				}
				return
			}
			if err != nil || revision != in.ExpectedRevision+1 {
				t.Fatal("accept", revision, err)
			}
			if mode == "missing-review-replay" {
				_, err = db.Exec(ctx, `ALTER TABLE sourced_alias_reviews DISABLE TRIGGER sourced_alias_reviews_immutable; DELETE FROM sourced_alias_reviews; ALTER TABLE sourced_alias_reviews ENABLE TRIGGER sourced_alias_reviews_immutable`)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = view.Accept(ctx, repo, "alias:test", p.PlanHash, "operator:test", "reviewed exact source against BIND identity"); !errors.Is(err, postgres.ErrConflict) {
					t.Fatal("missing review accepted on replay", err)
				}
				return
			}
			// Reconstruct a fresh inspection at the old revision, as CLI accept does
			// after a lost acknowledgement/restart; don't reuse an in-memory proof.
			opts.Revision = p.ExpectedRevision
			replayed, err := InspectSourcedAlias(ctx, repo, files, opts)
			if err != nil {
				t.Fatal("restore", err)
			}
			if r, e := replayed.Accept(ctx, repo, "alias:test", p.PlanHash, "operator:test", "reviewed exact source against BIND identity"); e != nil || r != revision {
				t.Fatal("replay", r, e)
			}
			if _, e := replayed.Accept(ctx, repo, "alias:test", p.PlanHash, "operator:other", "reviewed exact source against BIND identity"); e == nil {
				t.Fatal("changed actor replay accepted")
			}
			if _, e := replayed.Accept(ctx, repo, "alias:stale-new", p.PlanHash, "operator:test", "reviewed exact source against BIND identity"); e == nil {
				t.Fatal("stale new operation accepted")
			}
			opts.Revision = 0
			current, err := InspectSourcedAlias(ctx, repo, files, opts)
			if err != nil {
				t.Fatal("existing profile inspect", err)
			}
			currentPreview, _ := current.Preview()
			if r, e := current.Accept(ctx, repo, "alias:repeat-existing", currentPreview.PlanHash, "operator:test", "reviewed same alias"); e != nil || r != revision {
				t.Fatal("identical registration changed revision", r, e)
			}
			forged := current.input
			forged.ExistingProfile = proto.Clone(current.input.ExistingProfile).(*pb.CanonicalEntity)
			forged.ExistingProfile.IdentityKeys = append(forged.ExistingProfile.IdentityKeys, &pb.IdentityKey{Namespace: "invented", Value: "not-a-registered-key"})
			forgedPreview, e := domain.BuildSourcedAliasPreview(forged)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = repo.RegisterSourcedAlias(ctx, forged, "alias:forged", forgedPreview.PlanHash, "operator:test", "reviewed"); e == nil {
				t.Fatal("forged historical profile accepted")
			}
			plans, err := domain.PlanRegistryCandidates(source, in.Policy)
			if err != nil {
				t.Fatal(err)
			}
			candidates, err := repo.PrepareRegistryCandidateBatch(ctx, source, in.Source.Ref, bindingProducer(), "candidate:alias", plans, 100000, 100, 100, 100)
			if err != nil {
				t.Fatal("candidate batch", err)
			}
			if len(candidates.Candidates) != 1 || len(candidates.Aliases) != 1 || candidates.Aliases[0].SupportRefs[0] != in.MentionID {
				t.Fatal("registered alias not usable")
			}
			// The real consumer must hydrate this alias's support; successful alias SQL
			// alone would miss a broken EXTRACT evidence-reference contract.
			h := &SemanticResolutionHandoff{store: repo, maximumReferences: 100000}
			batch := &pb.SemanticBatchContext{Context: source.Context, OntologyVersion: source.OntologyVersion}
			remaining := uint64(32 << 20)
			req := &pb.SemanticResolveRequest{Batch: batch, Items: []*pb.AmbiguousMention{{ItemId: in.MentionID, Mention: source.Mentions[0], Candidates: candidates.Candidates, ExpectedRegistryRevision: revision}}}
			if _, err = h.hydrateCandidateEvidence(ctx, req, candidates, files, &remaining, newResolutionHydrationBudget(16<<20, 100000)); err != nil {
				t.Fatal("actual candidate evidence hydration", err)
			}
			if len(req.Items[0].CandidateContexts) != 1 || req.Items[0].CandidateContexts[0].SupportMention.SurfaceForm != "Kementerian Contoh" {
				t.Fatal("hydrated candidate context lost source")
			}
		})
	}
}
