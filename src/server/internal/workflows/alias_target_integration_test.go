// Tests additional aliases against actual provisional-origin receipts and source
// catalogs in isolated PostgreSQL schemas. Faults probe atomicity, replay, target
// evidence and identity preservation. Mentions are synthetic, not legal gold.
package workflows

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/storage"
	"regulagraph.local/server/internal/domain"
	"strings"
	"testing"
	"time"
)

func TestExistingProvisionalAliasAgainstPostgres(t *testing.T) {
	for _, mode := range []string{"commit-replay", "rollback", "missing-origin", "changed-origin-payload", "changed-origin-plan", "changed-origin-policy", "stale", "foreign-target", "forged-target-source"} {
		t.Run(mode, func(t *testing.T) {
			repo, db := reviewDatabase(t)
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			in, source, request := provisionalAliasFixture(t, repo)
			variant := proto.Clone(source.Mentions[0]).(*pb.Mention)
			variant.Meta.RecordId = "mention:variant"
			variant.SurfaceForm = "Contoh"
			variant.TextSpan.StartByte = uint64(strings.Index(source.Mentions[0].SurfaceForm, "Contoh"))
			source.Mentions = append(source.Mentions, variant)
			in.Source = sourcedAliasProto(t, domain.ExtractionBatchMediaType, source)
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

			create := SourcedAliasOptions{CreateProvisional: true, Corpus: in.Corpus, AuthScope: in.AuthScope, SourceArtifactID: in.Source.Ref.ArtifactId, MentionID: in.MentionID, Scope: in.Scope, PreferredLabel: in.PreferredLabel, Policy: in.Policy}
			origin, err := InspectSourcedAlias(ctx, repo, files, create)
			if err != nil {
				t.Fatal(err)
			}
			first, err := origin.Preview()
			if err != nil {
				t.Fatal(err)
			}
			initial, err := origin.Accept(ctx, repo, "origin:create", first.PlanHash, "operator:test", "reviewed source occurrence")
			if err != nil {
				t.Fatal(err)
			}
			canonical := first.Registration.Entity.Meta.RecordId
			opts := create
			opts.CreateProvisional = false
			opts.TargetProvisional = true
			opts.CanonicalID = canonical
			opts.MentionID = variant.Meta.RecordId
			view, err := InspectSourcedAlias(ctx, repo, files, opts)
			if err != nil {
				t.Fatal("inspect existing provisional", err)
			}
			p, err := view.Preview()
			if err != nil {
				t.Fatal(err)
			}
			if p.TargetOrigin == nil || p.TargetOriginOperation != "origin:create" || p.TargetOrigin.Mention.Meta.RecordId != in.MentionID || p.Mention.Meta.RecordId != variant.Meta.RecordId || len(p.TargetOrigin.Contexts) == 0 {
				t.Fatal("independent target proof lost")
			}
			if p.Registration.Entity.Meta.RecordId != canonical || p.Registration.Entity.PreferredLabel != in.PreferredLabel {
				t.Fatal("profile changed")
			}
			switch mode {
			case "rollback":
				_, err = db.Exec(ctx, `ALTER TABLE sourced_alias_reviews ADD CONSTRAINT reject_added_alias CHECK(create_provisional) NOT VALID`)
			case "missing-origin":
				_, err = db.Exec(ctx, `ALTER TABLE sourced_alias_reviews DISABLE TRIGGER sourced_alias_reviews_immutable;DELETE FROM sourced_alias_reviews WHERE operation_key='origin:create';ALTER TABLE sourced_alias_reviews ENABLE TRIGGER sourced_alias_reviews_immutable`)
			case "changed-origin-payload":
				_, err = db.Exec(ctx, `UPDATE registry_alias_operations SET payload_hash=$1 WHERE operation_key='origin:create'`, strings.Repeat("0", 64))
			case "changed-origin-plan", "changed-origin-policy":
				column := "plan_hash"
				if mode == "changed-origin-policy" {
					column = "policy_hash"
				}
				_, err = db.Exec(ctx, `ALTER TABLE sourced_alias_reviews DISABLE TRIGGER sourced_alias_reviews_immutable`)
				if err == nil {
					_, err = db.Exec(ctx, "UPDATE sourced_alias_reviews SET "+column+"=$1 WHERE operation_key='origin:create'", strings.Repeat("0", 64))
				}
				if err == nil {
					_, err = db.Exec(ctx, `ALTER TABLE sourced_alias_reviews ENABLE TRIGGER sourced_alias_reviews_immutable`)
				}
			case "stale":
				_, err = db.Exec(ctx, `UPDATE corpus_state SET registry_revision=registry_revision+1 WHERE corpus_id=$1`, in.Corpus)
			case "foreign-target":
				forged := view.input
				forged.CanonicalID = p.TargetDocument.Regulations[0].IssuerId
				if _, e := domain.BuildSourcedAliasPreview(forged); e == nil {
					t.Fatal("target profile mismatch accepted")
				}
				return
			case "forged-target-source":
				forged := view.input
				o := *forged.TargetOrigin
				forged.TargetOrigin = &o
				o.Ref.MentionID = variant.Meta.RecordId
				if _, e := domain.BuildSourcedAliasPreview(forged); e == nil {
					t.Fatal("different origin occurrence accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			revision, err := view.Accept(ctx, repo, "alias:variant", p.PlanHash, "operator:test", "reviewed both occurrences")
			if mode != "commit-replay" {
				if err == nil {
					t.Fatal("invalid origin accepted")
				}
				if strings.HasPrefix(mode, "changed-origin-") && !errors.Is(err, domain.ErrPersistentIntegrity) {
					t.Fatal("original receipt corruption gate not reached", err)
				}
				if mode == "rollback" && !strings.Contains(err.Error(), "reject_added_alias") {
					t.Fatal("failed before final insert", err)
				}
				var count int
				if e := db.QueryRow(ctx, `SELECT count(*) FROM registry_alias_versions WHERE alias_id=$1`, p.Registration.Alias.Meta.RecordId).Scan(&count); e != nil || count != 0 {
					t.Fatal("partial alias", count, e)
				}
				return
			}
			if err != nil || revision != initial+1 {
				t.Fatal("accept", revision, err)
			}
			current, _, err := repo.LoadAliasProfile(ctx, in.Corpus, canonical, revision)
			if err != nil || current.RegistryRevision != initial || current.PreferredLabel != in.PreferredLabel {
				t.Fatal("profile rewritten", err)
			}
			var origins, identities int
			if err = db.QueryRow(ctx, `SELECT (SELECT count(*) FROM sourced_alias_reviews WHERE create_provisional),(SELECT count(*) FROM canonical_identities WHERE canonical_id=$1)`, canonical).Scan(&origins, &identities); err != nil || origins != 1 || identities != 1 {
				t.Fatal("allocated again", origins, identities, err)
			}
			opts.Revision = p.ExpectedRevision
			restored, err := InspectSourcedAlias(ctx, repo, files, opts)
			if err != nil {
				t.Fatal("restart", err)
			}
			if r, e := restored.Accept(ctx, repo, "alias:variant", p.PlanHash, "operator:test", "reviewed both occurrences"); e != nil || r != revision {
				t.Fatal("replay", r, e)
			}
			plans, err := domain.PlanRegistryCandidates(source, in.Policy)
			if err != nil {
				t.Fatal(err)
			}
			candidates, err := repo.PrepareRegistryCandidateBatch(ctx, source, in.Source.Ref, bindingProducer(), "candidate:variant", plans, 100000, 100, 100, 100)
			if err != nil || len(candidates.GetCandidates()) != 1 || len(candidates.GetAliases()) != 2 {
				t.Fatal("variant candidate", err)
			}
			h := &SemanticResolutionHandoff{store: repo, maximumReferences: 100000}
			remaining := uint64(32 << 20)
			req := &pb.SemanticResolveRequest{Batch: &pb.SemanticBatchContext{Context: source.Context, OntologyVersion: source.OntologyVersion}, Items: []*pb.AmbiguousMention{{ItemId: variant.Meta.RecordId, Mention: variant, Candidates: candidates.Candidates, ExpectedRegistryRevision: revision}}}
			if _, err = h.hydrateCandidateEvidence(ctx, req, candidates, files, &remaining, newResolutionHydrationBudget(16<<20, 100000)); err != nil {
				t.Fatal("consumer hydration", err)
			}
			if len(req.Items[0].CandidateContexts) != 2 {
				t.Fatal("both alias supports not hydrated", len(req.Items[0].CandidateContexts))
			}
		})
	}
}
