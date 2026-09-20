// Mendaftarkan metadata artefak immutable dan dependency manifest di PostgreSQL.
// Peran: mengikat bytes pada artifact store ke corpus/schema serta menyediakan reverse
// dependency dan revision lookup scope untuk incremental closure U01.
// Kontrak: artifact ID boleh direplay hanya jika seluruh metadata identik; dependency
// replacement transaksional; lookup kosong tetap dicatat melalui scope revision agar
// penambahan data di masa depan dapat menginvalidasi hasil lama.
// Benchmark: ukur batch registration, reverse lookup, write amplification, p95/p99, dan
// pertumbuhan index pada volume corpus referensi.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Status: registration/load immutable metadata, dependency replacement, dan lookup-scope CAS S01 aktif.
package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strconv"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type ArtifactDependency struct {
	Kind         string
	Key          string
	Revision     uint64
	EmptyResult  bool
	Fingerprint  string
	ProducerHash string
}

// ReplaceArtifactDependencyManifest persists the wire manifest attached to a derived artifact.
// The adapter computes one deterministic producer hash and retains zero-revision empty lookups so
// later scope advances can invalidate outputs that previously observed no registry candidates.
func (r *Repository) ReplaceArtifactDependencyManifest(ctx context.Context, corpusID, artifactID string, manifest *pb.DependencyManifest) error {
	if manifest == nil || manifest.GetProducerManifest() == nil {
		return errors.New("dependency manifest and producer are required")
	}
	if err := domain.ValidateWire(manifest, domain.DefaultWireLimits); err != nil {
		return fmt.Errorf("validate dependency manifest: %w", err)
	}
	producer, err := proto.MarshalOptions{Deterministic: true}.Marshal(manifest.ProducerManifest)
	if err != nil {
		return fmt.Errorf("marshal dependency producer: %w", err)
	}
	digest := sha256.Sum256(producer)
	producerHash := hex.EncodeToString(digest[:])
	dependencies := make([]ArtifactDependency, 0, len(manifest.Dependencies)+len(manifest.LookupScopeRevisions))
	for _, dependency := range manifest.Dependencies {
		dependencies = append(dependencies, ArtifactDependency{
			Kind: "fingerprint", Key: dependency.DependencyId,
			Fingerprint: dependency.GetFingerprint().GetSha256(), ProducerHash: producerHash,
		})
	}
	for _, lookup := range manifest.LookupScopeRevisions {
		dependencies = append(dependencies, ArtifactDependency{
			Kind: "lookup_scope", Key: lookup.ScopeId, Revision: lookup.Revision,
			EmptyResult: lookup.EmptyResult, ProducerHash: producerHash,
		})
	}
	return r.ReplaceArtifactDependencies(ctx, corpusID, artifactID, dependencies)
}

func (r *Repository) RegisterArtifact(ctx context.Context, corpusID string, ref *pb.ArtifactRef) error {
	if ref == nil || ref.ContentHash == nil || ref.ByteSize > math.MaxInt64 || !sha256Pattern.MatchString(ref.ContentHash.Sha256) {
		return errors.New("valid artifact reference required")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO corpus_state(corpus_id) VALUES ($1)
      ON CONFLICT (corpus_id) DO NOTHING`, corpusID); err != nil {
		return fmt.Errorf("ensure artifact corpus: %w", err)
	}
	tag, err := tx.Exec(ctx, `INSERT INTO artifacts(artifact_id,corpus_id,algorithm,digest,storage_key,
      media_type,byte_size,schema_version) VALUES ($1,$2,'sha256',$3,$4,$5,$6,$7)
      ON CONFLICT (artifact_id) DO NOTHING`, ref.ArtifactId, corpusID, ref.ContentHash.Sha256,
		ref.StorageKey, ref.MediaType, int64(ref.ByteSize), fmt.Sprint(ref.SchemaVersion))
	if err != nil {
		return fmt.Errorf("register artifact: %w", err)
	}
	if tag.RowsAffected() == 0 {
		var storedCorpus, digest, key, mediaType, schemaVersion string
		var size int64
		err = tx.QueryRow(ctx, `SELECT corpus_id,digest,storage_key,media_type,byte_size,schema_version
          FROM artifacts WHERE artifact_id=$1`, ref.ArtifactId).Scan(&storedCorpus, &digest, &key, &mediaType, &size, &schemaVersion)
		if err != nil {
			return fmt.Errorf("inspect artifact conflict: %w", err)
		}
		if storedCorpus != corpusID || digest != ref.ContentHash.Sha256 || key != ref.StorageKey ||
			mediaType != ref.MediaType || uint64(size) != ref.ByteSize || schemaVersion != fmt.Sprint(ref.SchemaVersion) {
			return fmt.Errorf("artifact id reused with different immutable metadata: %w", ErrConflict)
		}
	}
	return tx.Commit(ctx)
}

// LoadArtifact reconstructs immutable metadata for a checkpoint-bound artifact.
func (r *Repository) LoadArtifact(ctx context.Context, corpusID, artifactID string) (*pb.ArtifactRef, error) {
	if !storageIDPattern.MatchString(corpusID) || !storageIDPattern.MatchString(artifactID) {
		return nil, errors.New("valid corpus and artifact IDs required")
	}
	var digest, key, mediaType, schemaVersion string
	var byteSize int64
	err := r.pool.QueryRow(ctx, `SELECT digest,storage_key,media_type,byte_size,schema_version
		FROM artifacts WHERE corpus_id=$1 AND artifact_id=$2`, corpusID, artifactID).
		Scan(&digest, &key, &mediaType, &byteSize, &schemaVersion)
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load artifact: %w", err)
	}
	if byteSize < 0 || !sha256Pattern.MatchString(digest) {
		return nil, fmt.Errorf("stored artifact metadata is invalid: %w", domain.ErrPersistentIntegrity)
	}
	schemaValue, parseErr := strconv.ParseUint(schemaVersion, 10, 32)
	if parseErr != nil || schemaValue == 0 {
		return nil, fmt.Errorf("stored artifact schema is invalid: %w", domain.ErrPersistentIntegrity)
	}
	schema := uint32(schemaValue)
	ref := &pb.ArtifactRef{ArtifactId: artifactID, ContentHash: &pb.ContentHash{Sha256: digest},
		StorageKey: key, MediaType: mediaType, ByteSize: uint64(byteSize), SchemaVersion: schema}
	if err = domain.ValidateWire(ref, domain.DefaultWireLimits); err != nil {
		return nil, fmt.Errorf("stored artifact metadata failed validation: %w", errors.Join(err, domain.ErrPersistentIntegrity))
	}
	return ref, nil
}

func (r *Repository) ReplaceArtifactDependencies(ctx context.Context, corpusID, artifactID string, dependencies []ArtifactDependency) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var lockedArtifact string
	if err = tx.QueryRow(ctx, `SELECT artifact_id FROM artifacts WHERE artifact_id=$1 AND corpus_id=$2 FOR UPDATE`, artifactID, corpusID).Scan(&lockedArtifact); err == pgx.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("lock artifact dependency owner: %w", err)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM artifact_dependencies WHERE artifact_id=$1`, artifactID); err != nil {
		return fmt.Errorf("clear artifact dependencies: %w", err)
	}
	for _, dependency := range dependencies {
		if dependency.Kind != "artifact" && dependency.Kind != "fingerprint" && dependency.Kind != "lookup_scope" {
			return errors.New("invalid dependency kind")
		}
		if dependency.Key == "" || !sha256Pattern.MatchString(dependency.ProducerHash) {
			return errors.New("dependency key and producer hash required")
		}
		if dependency.EmptyResult && dependency.Kind != "lookup_scope" {
			return errors.New("only lookup-scope dependencies may mark an empty result")
		}
		if (dependency.Kind == "fingerprint" && !sha256Pattern.MatchString(dependency.Fingerprint)) ||
			(dependency.Kind != "fingerprint" && dependency.Fingerprint != "") {
			return errors.New("fingerprint dependency requires exactly one SHA-256 value")
		}
		var revision any
		if dependency.Revision > 0 {
			if dependency.Revision > math.MaxInt64 {
				return errors.New("dependency revision exceeds PostgreSQL bigint")
			}
			revision = int64(dependency.Revision)
		}
		var fingerprint any
		if dependency.Fingerprint != "" {
			fingerprint = dependency.Fingerprint
		}
		if _, err = tx.Exec(ctx, `INSERT INTO artifact_dependencies(corpus_id,artifact_id,dependency_kind,
		  dependency_key,dependency_revision,producer_manifest_hash,empty_result,dependency_fingerprint)
		  VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, corpusID, artifactID, dependency.Kind, dependency.Key,
			revision, dependency.ProducerHash, dependency.EmptyResult, fingerprint); err != nil {
			return fmt.Errorf("insert artifact dependency: %w", err)
		}
	}
	return tx.Commit(ctx)
}

func (r *Repository) AdvanceLookupScope(ctx context.Context, corpusID, scopeKey string, expectedRevision uint64) (uint64, error) {
	if expectedRevision > math.MaxInt64-1 || scopeKey == "" {
		return 0, errors.New("valid scope and revision required")
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO corpus_state(corpus_id) VALUES ($1) ON CONFLICT DO NOTHING`, corpusID); err != nil {
		return 0, err
	}
	var current int64
	err = tx.QueryRow(ctx, `SELECT revision FROM lookup_scope_revisions WHERE corpus_id=$1 AND scope_key=$2 FOR UPDATE`, corpusID, scopeKey).Scan(&current)
	if err == pgx.ErrNoRows {
		if expectedRevision != 0 {
			return 0, ErrConflict
		}
		current = 1
		_, err = tx.Exec(ctx, `INSERT INTO lookup_scope_revisions(corpus_id,scope_key,revision) VALUES ($1,$2,$3)`, corpusID, scopeKey, current)
	} else if err == nil {
		if uint64(current) != expectedRevision {
			return 0, ErrConflict
		}
		current++
		_, err = tx.Exec(ctx, `UPDATE lookup_scope_revisions SET revision=$3,updated_at=clock_timestamp()
          WHERE corpus_id=$1 AND scope_key=$2`, corpusID, scopeKey, current)
	}
	if err != nil {
		return 0, fmt.Errorf("advance lookup scope: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	return uint64(current), nil
}
