// Commits immutable original-to-bound graph source receipts after recomputing the
// pure envelope transform. Snapshot/corpus and source-job locks prevent publication
// takeover or cancellation from crossing admission; live base pin, published index
// membership, source checkpoint and registered refs are checked in the transaction.
// No missing artifact, model decision, registry freshness proof or graph job is created.
// Hash/decode work precedes locks; receipt payload is capped at 64KiB. Measure lock,
// pool, hash p95/p99 and replay behavior under benchmark-targets.yaml (unmeasured).
package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func (r *Repository) RegisterGraphSourceBinding(ctx context.Context, pin domain.SnapshotPin,
	binding domain.GraphSourceBinding, input domain.GraphSourceBindingInputs, maximumEdges int) error {
	if err := domain.ValidateGraphSourceBinding(binding); err != nil {
		return err
	}
	if err := validateIndexPin(pin); err != nil {
		return err
	}
	if pin.CorpusID != binding.Source.Snapshot.CorpusId || pin.SnapshotID != binding.Source.Snapshot.SnapshotId || pin.Sequence != binding.Source.Snapshot.Sequence {
		return ErrConflict
	}
	bounded, cancel := context.WithDeadline(ctx, pin.ExpiresAt)
	defer cancel()
	if err := bounded.Err(); err != nil {
		return err
	}
	extract, resolve, err := domain.BindGraphSourceEnvelopes(binding.Source.SourceJobID,
		domain.GraphSourceArtifact{Reference: binding.Source.Original, Bytes: input.OriginalDocument},
		domain.GraphSourceArtifact{Reference: binding.Source.Bound, Bytes: input.SnapshotDocument},
		domain.GraphSourceArtifact{Reference: binding.OriginalExtraction, Bytes: input.Extraction},
		domain.GraphSourceArtifact{Reference: binding.OriginalResolution, Bytes: input.Resolution}, maximumEdges)
	if err != nil {
		return err
	}
	if !proto.Equal(extract.Reference, binding.BoundExtraction) || !proto.Equal(resolve.Reference, binding.BoundResolution) {
		return fmt.Errorf("graph source receipt differs from exact transform: %w", ErrConflict)
	}
	resolved := new(pb.ResolutionBatch)
	if err = domain.DecodeWire(resolve.Bytes, resolved, domain.DefaultWireLimits); err != nil {
		return err
	}
	if resolved.RegistryRevision > binding.RegistryRevision {
		return ErrConflict
	}
	raw, err := json.Marshal(binding)
	if err != nil {
		return err
	}
	if len(raw) > 65536 {
		return ErrResultLimit
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(raw))
	tx, err := r.pool.Begin(bounded)
	if err != nil {
		return err
	}
	defer tx.Rollback(bounded)
	corpus := binding.Source.Snapshot.CorpusId
	// Match the established snapshot-then-corpus publication lock order.
	var id string
	err = tx.QueryRow(bounded, `SELECT publication_id FROM snapshots WHERE publication_id=$1 AND corpus_id=$2 FOR UPDATE`, binding.PublicationID, corpus).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	err = tx.QueryRow(bounded, `SELECT corpus_id FROM corpus_state WHERE corpus_id=$1 FOR UPDATE`, corpus).Scan(&id)
	if err != nil {
		return err
	}
	if err = verifyGraphPublicationBinding(bounded, tx, binding.PublicationID, corpus, binding.Fence, binding.TargetSequence, binding.RegistryRevision, binding.Source.Snapshot); err != nil {
		return err
	}
	err = tx.QueryRow(bounded, `SELECT job_id FROM jobs WHERE job_id=$1 AND corpus_id=$2 AND NOT cancellation_requested FOR SHARE`, binding.Source.SourceJobID, corpus).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if err = verifyGraphAssemblySourceCheckpoint(bounded, tx, corpus, binding.Source.SourceJobID, binding.SourceCheckpointID, binding.OriginalResolution); err != nil {
		return err
	}
	if err = verifyPublishedGraphSourceBinding(bounded, tx, pin, binding.Source); err != nil {
		return err
	}
	for _, ref := range []*pb.ArtifactRef{binding.OriginalExtraction, binding.OriginalResolution, binding.BoundExtraction, binding.BoundResolution} {
		registered, e := loadArtifact(bounded, tx, corpus, ref.ArtifactId)
		if e != nil {
			return e
		}
		if !proto.Equal(registered, ref) {
			return domain.ErrPersistentIntegrity
		}
	}
	_, err = tx.Exec(bounded, `INSERT INTO graph_source_bindings
 (publication_id,source_job_id,corpus_id,fence,source_checkpoint_id,base_publication_id,
 original_extraction_id,original_resolution_id,bound_extraction_id,bound_resolution_id,payload,payload_hash)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT(publication_id,source_job_id) DO NOTHING`,
		binding.PublicationID, binding.Source.SourceJobID, corpus, int64(binding.Fence), binding.SourceCheckpointID, binding.Source.PublicationID,
		binding.OriginalExtraction.ArtifactId, binding.OriginalResolution.ArtifactId, binding.BoundExtraction.ArtifactId, binding.BoundResolution.ArtifactId, raw, hash)
	if err != nil {
		return err
	}
	stored, err := loadGraphSourceBinding(bounded, tx, corpus, binding.PublicationID, binding.Source.SourceJobID)
	if err != nil {
		return err
	}
	storedRaw, err := json.Marshal(stored)
	if err != nil {
		return err
	}
	if !bytes.Equal(storedRaw, raw) {
		return ErrConflict
	}
	if err = checkIndexLease(bounded, tx, pin); err != nil {
		return err
	}
	return tx.Commit(bounded)
}

// LoadGraphSourceBinding is an audit/replay read, never a live admission capability.
// It remains readable after publication abort; callers authorize corpus access.
func (r *Repository) LoadGraphSourceBinding(ctx context.Context, corpus, publication, sourceJob string) (domain.GraphSourceBinding, error) {
	return loadGraphSourceBinding(ctx, r.pool, corpus, publication, sourceJob)
}

func loadGraphSourceBinding(ctx context.Context, query indexQuerier, corpus, publication, sourceJob string) (domain.GraphSourceBinding, error) {
	var result domain.GraphSourceBinding
	for _, id := range []string{corpus, publication, sourceJob} {
		if !storageIDPattern.MatchString(id) {
			return result, errors.New("complete graph binding key required")
		}
	}
	var raw []byte
	var hash, checkpoint, base, originalE, originalR, boundE, boundR string
	var fence int64
	err := query.QueryRow(ctx, `SELECT CASE WHEN octet_length(payload)<=65536 THEN payload ELSE NULL END,payload_hash,fence,
 source_checkpoint_id,base_publication_id,original_extraction_id,original_resolution_id,bound_extraction_id,bound_resolution_id
 FROM graph_source_bindings WHERE corpus_id=$1 AND publication_id=$2 AND source_job_id=$3`, corpus, publication, sourceJob).
		Scan(&raw, &hash, &fence, &checkpoint, &base, &originalE, &originalR, &boundE, &boundR)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, ErrNotFound
	}
	if err != nil {
		return result, err
	}
	if len(raw) == 0 || hash != fmt.Sprintf("%x", sha256.Sum256(raw)) || json.Unmarshal(raw, &result) != nil {
		return domain.GraphSourceBinding{}, domain.ErrPersistentIntegrity
	}
	if err = domain.ValidateGraphSourceBinding(result); err != nil {
		return domain.GraphSourceBinding{}, domain.ErrPersistentIntegrity
	}
	canonical, e := json.Marshal(result)
	if e != nil || !bytes.Equal(raw, canonical) || result.PublicationID != publication || result.Source.SourceJobID != sourceJob ||
		result.Source.Snapshot.CorpusId != corpus || result.Fence != uint64(fence) || result.SourceCheckpointID != checkpoint || result.Source.PublicationID != base ||
		result.OriginalExtraction.ArtifactId != originalE || result.OriginalResolution.ArtifactId != originalR || result.BoundExtraction.ArtifactId != boundE || result.BoundResolution.ArtifactId != boundR {
		return domain.GraphSourceBinding{}, domain.ErrPersistentIntegrity
	}
	return result, nil
}
