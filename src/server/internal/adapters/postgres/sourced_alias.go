// Commits explicitly reviewed aliases using actual EXTRACT supports and existing
// BIND identities or explicit provisional occurrence allocation. Source catalog,
// registered bytes, BIND keys, policy and CAS
// are checked before alias/profile/review writes in one transaction. No model or
// filesystem work occurs while locks are held. Replay verifies the immutable
// approval as well as every alias/profile record; it never samples or reallocates.
// Measure lock/commit p95, conflicts and reference budgets; benchmark targets in
// configs/benchmark-targets.yaml remain REQUIRED_UNMEASURED.
package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

// LoadAliasProfile reads a retained revision, including an absent profile. A zero
// revision chooses current; explicit historical reads permit exact approval replay.
func (r *Repository) LoadAliasProfile(ctx context.Context, corpus, canonical string, revision uint64) (*pb.CanonicalEntity, uint64, error) {
	if !storageIDPattern.MatchString(corpus) || !storageIDPattern.MatchString(canonical) || revision > math.MaxInt64 {
		return nil, 0, errors.New("invalid alias profile selection")
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback(ctx)
	var current, floor int64
	if err = tx.QueryRow(ctx, `SELECT registry_revision,registry_history_floor FROM corpus_state WHERE corpus_id=$1`, corpus).Scan(&current, &floor); err != nil {
		return nil, 0, err
	}
	if revision == 0 {
		revision = uint64(current)
	}
	if floor <= 0 || current < floor || revision < uint64(floor) || revision > uint64(current) {
		return nil, 0, ErrConflict
	}
	identities, err := readAliasCanonicalIdentities(ctx, tx, corpus, []string{canonical})
	if err != nil {
		return nil, 0, err
	}
	identity, ok := identities[canonical]
	if !ok || identity.fromRevision <= 0 || identity.fromRevision > int64(revision) || identity.toRevision != nil && *identity.toRevision <= int64(revision) {
		return nil, 0, ErrNotFound
	}
	var raw []byte
	var digest, kind, scope, label string
	var from int64
	var review int16
	err = tx.QueryRow(ctx, `SELECT CASE WHEN octet_length(profile_payload)<=1048576 THEN profile_payload ELSE NULL END,
 record_hash,entity_type,canonical_scope,preferred_label,review_state,from_revision
 FROM registry_entity_profiles WHERE corpus_id=$1 AND canonical_id=$2 AND from_revision<=$3
 AND (to_revision IS NULL OR to_revision>$3)`, corpus, canonical, int64(revision)).Scan(&raw, &digest, &kind, &scope, &label, &review, &from)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, revision, tx.Commit(ctx)
	}
	if err != nil {
		return nil, 0, err
	}
	entity := new(pb.CanonicalEntity)
	if len(raw) == 0 {
		return nil, 0, ErrResultLimit
	}
	if err = decodeRegistryPayload(raw, digest, entity); err != nil {
		return nil, 0, err
	}
	if entity.GetMeta().GetCorpusId() != corpus || entity.GetMeta().GetRecordId() != canonical || entity.EntityType != kind || entity.Scope != scope || entity.PreferredLabel != label ||
		int16(entity.ReviewState) != review || from < identity.fromRevision || from > int64(revision) || !profileContainsExactIdentity(entity, identity) || aliasEntityType(kind) != identity.entityType {
		return nil, 0, domain.ErrPersistentIntegrity
	}
	entity.RegistryRevision = uint64(from)
	if err = domain.ValidateWire(entity, domain.DefaultWireLimits); err != nil {
		return nil, 0, err
	}
	return entity, revision, tx.Commit(ctx)
}

func (r *Repository) RegisterSourcedAlias(ctx context.Context, in domain.SourcedAliasInput, operation, planHash, actor, reason string) (uint64, error) {
	if !storageIDPattern.MatchString(operation) || !storageIDPattern.MatchString(actor) || len(actor) > 256 || strings.TrimSpace(reason) == "" ||
		len(reason) > 1024 || !utf8.ValidString(reason) || strings.ContainsRune(reason, 0) {
		return 0, errors.New("explicit bounded operator approval required")
	}
	if !in.CreateProvisional {
		actual, _, err := r.LoadAliasProfile(ctx, in.Corpus, in.CanonicalID, in.ExpectedRevision)
		if err != nil {
			return 0, err
		}
		if !proto.Equal(actual, in.ExistingProfile) {
			return 0, errors.New("reviewed profile differs from historical registry")
		}
	}
	preview, err := domain.BuildSourcedAliasPreview(in)
	if err != nil {
		return 0, err
	}
	if preview.PlanHash != planHash {
		return 0, errors.New("alias approval differs from inspected plan")
	}
	canonicalID := preview.Registration.Entity.Meta.RecordId
	var creation *domain.DocumentRegistryIdentity
	if in.CreateProvisional {
		e := preview.Registration.Entity
		creation = &domain.DocumentRegistryIdentity{CanonicalID: canonicalID, EntityType: domain.CanonicalEntityTypeCode(e.EntityType),
			IdentityScope: e.IdentityKeys[0].Namespace, IdentityKey: e.IdentityKeys[0].Value}
	}
	// The source's persisted ingest request must contain this policy fingerprint.
	policyHash, err := in.Policy.Fingerprint()
	if err != nil {
		return 0, err
	}
	admit := func(ctx context.Context, tx pgx.Tx, replay bool, revision int64) error {
		for _, a := range []domain.AliasSourceArtifact{in.Source, in.Document, in.TargetDocument} {
			var registered bool
			e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM artifacts WHERE corpus_id=$1 AND artifact_id=$2
 AND digest=$3 AND storage_key=$4 AND media_type=$5 AND byte_size=$6 AND schema_version=$7)`, in.Corpus, a.Ref.ArtifactId,
				a.Ref.ContentHash.Sha256, a.Ref.StorageKey, a.Ref.MediaType, int64(a.Ref.ByteSize), fmt.Sprint(a.Ref.SchemaVersion)).Scan(&registered)
			if e != nil {
				return e
			}
			if !registered {
				return domain.ErrPersistentIntegrity
			}
		}
		var requestRaw []byte
		var requestHash string
		e := tx.QueryRow(ctx, `SELECT CASE WHEN octet_length(j.request_payload)<=16777216 THEN j.request_payload ELSE NULL END,j.request_hash FROM extraction_evidence_sources s
 JOIN job_checkpoints c ON c.checkpoint_id=s.checkpoint_id
 JOIN jobs j ON j.job_id=c.job_id AND j.corpus_id=s.corpus_id
 WHERE s.corpus_id=$1 AND s.artifact_id=$2 AND s.mention_id=$3 AND s.auth_scope_ref=$4 AND s.snapshot_key=$5
 AND c.stage=$6 AND c.terminal_status=$7`, in.Corpus, in.Source.Ref.ArtifactId, in.MentionID, in.AuthScope,
			extractionSnapshotKey(preview.SourceContext.SnapshotRef), int16(pb.JobStage_JOB_STAGE_EXTRACT), int16(pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED)).Scan(&requestRaw, &requestHash)
		if errors.Is(e, pgx.ErrNoRows) {
			return domain.ErrPersistentIntegrity
		}
		if e != nil {
			return e
		}
		if len(requestRaw) == 0 || fmt.Sprintf("%x", sha256.Sum256(requestRaw)) != requestHash {
			return domain.ErrPersistentIntegrity
		}
		request := new(pb.IngestionRequest)
		if e = domain.DecodeWire(requestRaw, request, domain.DefaultWireLimits); e != nil {
			return e
		}
		pinned := false
		for _, h := range request.GetConfigManifest().GetInputHashes() {
			if proto.Equal(h, policyHash) {
				pinned = true
			}
		}
		if !pinned || request.CorpusId != in.Corpus || !proto.Equal(request.GetConfigManifest().GetConfigHash(), preview.SourceContext.ConfigFingerprint) {
			return domain.ErrPersistentIntegrity
		}
		if e = verifyDocumentRegistryDependencies(ctx, tx, in.Corpus, preview.TargetDependencies, in.ExpectedRevision); e != nil {
			return e
		}
		if !replay && in.CreateProvisional {
			if e = verifyProvisionalEmptyScopes(ctx, tx, in.Corpus, preview.LookupScopes); e != nil {
				return e
			}
		}
		if replay {
			var matches bool
			e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sourced_alias_reviews WHERE corpus_id=$1 AND operation_key=$2
 AND plan_hash=$3 AND expected_revision=$4 AND registry_revision=$5 AND actor=$6 AND reason=$7
 AND source_artifact_id=$8 AND mention_id=$9 AND canonical_id=$10 AND alias_id=$11 AND target_artifact_id=$12 AND policy_hash=$13 AND create_provisional=$14)`, in.Corpus, operation, planHash,
				int64(in.ExpectedRevision), revision, actor, reason, in.Source.Ref.ArtifactId, in.MentionID, canonicalID, preview.Registration.Alias.Meta.RecordId, in.TargetDocument.Ref.ArtifactId, policyHash.Sha256, in.CreateProvisional).Scan(&matches)
			if e != nil {
				return e
			}
			if !matches {
				return ErrConflict
			}
		}
		return nil
	}
	record := func(ctx context.Context, tx pgx.Tx, revision int64) error {
		_, e := tx.Exec(ctx, `INSERT INTO sourced_alias_reviews(corpus_id,operation_key,plan_hash,expected_revision,registry_revision,
 actor,reason,source_artifact_id,mention_id,canonical_id,alias_id,target_artifact_id,policy_hash,create_provisional) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
			in.Corpus, operation, planHash, int64(in.ExpectedRevision), revision, actor, reason, in.Source.Ref.ArtifactId, in.MentionID, canonicalID, preview.Registration.Alias.Meta.RecordId, in.TargetDocument.Ref.ArtifactId, policyHash.Sha256, in.CreateProvisional)
		return e
	}
	return r.registerCanonicalAliases(ctx, in.Corpus, operation, in.ExpectedRevision, []AliasRegistration{preview.Registration}, creation, admit, record)
}
