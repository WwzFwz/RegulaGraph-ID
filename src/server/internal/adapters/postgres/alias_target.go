// Loads and authenticates the original provisional creation behind an existing
// canonical target. A new alias review must retain that source occurrence and
// historical identity/profile/alias rows; labels alone do not establish identity.
// Admission runs in the alias CAS transaction with no model/file IO. Reads are
// bounded by a unique origin index; measure lock/read p95 and false links against
// benchmark-targets.yaml. Successful fixtures do not establish semantic quality.
package postgres

import (
	"context"
	"errors"
	"math"

	"github.com/jackc/pgx/v5"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func (r *Repository) LoadProvisionalAliasOrigin(ctx context.Context, corpus, canonical, auth string, revision uint64) (domain.ProvisionalAliasOriginRef, error) {
	var out domain.ProvisionalAliasOriginRef
	if !storageIDPattern.MatchString(corpus) || !storageIDPattern.MatchString(canonical) || auth == "" || revision == 0 || revision > math.MaxInt64 {
		return out, errors.New("bounded authorized target selection required")
	}
	var created int64
	err := r.pool.QueryRow(ctx, `SELECT r.operation_key,r.source_artifact_id,r.mention_id,r.registry_revision,r.policy_hash,r.plan_hash
 FROM sourced_alias_reviews r JOIN extraction_evidence_sources s ON s.corpus_id=r.corpus_id
 AND s.artifact_id=r.source_artifact_id AND s.mention_id=r.mention_id
 WHERE r.corpus_id=$1 AND r.canonical_id=$2 AND r.create_provisional AND r.registry_revision<=$3 AND s.auth_scope_ref=$4`, corpus, canonical, int64(revision), auth).Scan(&out.Operation, &out.SourceArtifactID, &out.MentionID, &created, &out.PolicyHash, &out.PlanHash)
	if err != nil {
		return out, err
	}
	if created < 2 {
		return out, domain.ErrPersistentIntegrity
	}
	out.CreatedRevision = uint64(created)
	return out, nil
}

func verifyProvisionalAliasOrigin(ctx context.Context, tx pgx.Tx, in domain.SourcedAliasInput, p *domain.SourcedAliasPreview) error {
	o := in.TargetOrigin
	if o == nil {
		return nil
	}
	origin := p.TargetOrigin
	if origin == nil {
		return domain.ErrPersistentIntegrity
	}
	checked, payloadHash, err := checkAliasRegistrations(in.Corpus, o.Ref.Operation, o.Ref.CreatedRevision, []AliasRegistration{origin.Registration})
	if err != nil {
		return err
	}
	if err := verifyAliasSourcePolicy(ctx, tx, in.Corpus, in.AuthScope, o.Source.Ref.ArtifactId, o.Ref.MentionID, origin.SourceContext, &pb.ContentHash{Sha256: o.Ref.PolicyHash}); err != nil {
		return err
	}
	var matches bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sourced_alias_reviews r
 JOIN registry_alias_operations a ON a.corpus_id=r.corpus_id AND a.operation_key=r.operation_key AND a.registry_revision=r.registry_revision
 JOIN extraction_evidence_sources s ON s.corpus_id=r.corpus_id AND s.artifact_id=r.source_artifact_id AND s.mention_id=r.mention_id
 JOIN job_checkpoints c ON c.checkpoint_id=s.checkpoint_id
 WHERE r.corpus_id=$1 AND r.operation_key=$2 AND r.canonical_id=$3 AND r.create_provisional
 AND r.registry_revision=$4 AND r.expected_revision=$4-1 AND r.registry_revision<=$5
 AND r.source_artifact_id=$6 AND r.mention_id=$7 AND r.target_artifact_id=$8 AND r.alias_id=$9
 AND a.payload_hash=$10 AND s.auth_scope_ref=$11 AND s.snapshot_key=$12
 AND c.stage=$13 AND c.terminal_status=$14 AND r.policy_hash=$15 AND r.plan_hash=$16)`, in.Corpus, o.Ref.Operation, in.CanonicalID, int64(o.Ref.CreatedRevision), int64(in.ExpectedRevision),
		o.Source.Ref.ArtifactId, o.Ref.MentionID, o.Document.Ref.ArtifactId, origin.Registration.Alias.Meta.RecordId, payloadHash, in.AuthScope, extractionSnapshotKey(origin.SourceContext.SnapshotRef), int16(pb.JobStage_JOB_STAGE_EXTRACT), int16(pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED), o.Ref.PolicyHash, o.Ref.PlanHash).Scan(&matches)
	if err != nil {
		return err
	}
	if !matches {
		return domain.ErrPersistentIntegrity
	}
	return verifyAliasOperationReplay(ctx, tx, in.Corpus, int64(o.Ref.CreatedRevision), checked)
}
