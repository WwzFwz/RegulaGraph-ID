// Catalogs verified EXTRACT mention locations atomically with their fenced checkpoint.
// Resolution reads all support locations in one bounded SQL call scoped to corpus, snapshot
// and authorization. The workflow must re-read hashes and verify mention/alias/document closure;
// this catalog is a locator, never proof of identity or legal validity. Replay preserves the
// first successful checkpoint and rejects conflicting immutable metadata. Measure index growth,
// lock/lookup p95/p99 and hydration bytes; benchmark targets remain REQUIRED_UNMEASURED.
package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

// SaveExtractionCheckpoint is called only after the coordinator verifies the original
// EXTRACT/document/text bytes. A partial or failed batch remains checkpointed without indexing.
func (r *Repository) SaveExtractionCheckpoint(ctx context.Context, checkpoint *pb.Checkpoint, owner string,
	ref *pb.ArtifactRef, source *pb.ExtractionBatch) error {
	for _, message := range []proto.Message{checkpoint, ref, source} {
		if err := domain.ValidateWire(message, domain.DefaultWireLimits); err != nil {
			return err
		}
	}
	if checkpoint.Stage != pb.JobStage_JOB_STAGE_EXTRACT || source.Meta.CorpusId != checkpoint.Meta.CorpusId ||
		source.Context.CorpusId != checkpoint.Meta.CorpusId || len(checkpoint.CompletedBatchKeys) != 1 ||
		len(checkpoint.ArtifactHashes) != 1 || checkpoint.CompletedBatchKeys[0] != ref.ArtifactId ||
		!proto.Equal(checkpoint.ArtifactHashes[0], ref.ContentHash) {
		return errors.New("extraction catalog differs from checkpoint identity")
	}
	if checkpoint.TerminalStatus != pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED {
		return r.SaveCheckpoint(ctx, checkpoint, owner)
	}
	if source.Completeness != pb.Completeness_COMPLETENESS_COMPLETE {
		return errors.New("successful extraction catalog requires complete output")
	}
	ids := make([]string, 0, len(source.Mentions))
	seen := map[string]bool{}
	for _, mention := range source.Mentions {
		id := mention.GetMeta().GetRecordId()
		if !storageIDPattern.MatchString(id) || mention.Meta.CorpusId != source.Meta.CorpusId || seen[id] {
			return errors.New("invalid or duplicate extraction catalog mention")
		}
		seen[id] = true
		ids = append(ids, id)
	}
	snapshot := extractionSnapshotKey(source.Context.SnapshotRef)
	return r.saveCheckpoint(ctx, checkpoint, owner, func(ctx context.Context, tx pgx.Tx) error {
		var registered bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM artifacts
			WHERE corpus_id=$1 AND artifact_id=$2 AND digest=$3 AND storage_key=$4
			AND media_type=$5 AND byte_size=$6 AND schema_version=$7)`, source.Meta.CorpusId,
			ref.ArtifactId, ref.ContentHash.Sha256, ref.StorageKey, ref.MediaType, int64(ref.ByteSize),
			fmt.Sprint(ref.SchemaVersion)).Scan(&registered); err != nil {
			return err
		}
		if !registered {
			return domain.ErrPersistentIntegrity
		}
		var cancelled bool
		if err := tx.QueryRow(ctx, `SELECT cancellation_requested FROM jobs WHERE job_id=$1`, checkpoint.JobId).Scan(&cancelled); err != nil {
			return err
		}
		if cancelled {
			return ErrStaleFence
		}
		_, err := tx.Exec(ctx, `INSERT INTO extraction_evidence_sources
			(corpus_id,mention_id,artifact_id,checkpoint_id,snapshot_key,auth_scope_ref)
			SELECT $1,mention_id,$2,$3,$4,$5 FROM unnest($6::text[]) AS input(mention_id)
			ON CONFLICT DO NOTHING`, source.Meta.CorpusId, ref.ArtifactId, checkpoint.Meta.RecordId,
			snapshot, source.Context.AuthScopeRef, ids)
		if err != nil {
			return fmt.Errorf("index extraction support locations: %w", err)
		}
		// Preserve idempotent replay, including recovery with a new checkpoint/fence. The
		// immutable extraction payload fixes the complete set; missing/extra rows are corruption.
		var matched int
		err = tx.QueryRow(ctx, `SELECT count(*) FROM extraction_evidence_sources
			WHERE corpus_id=$1 AND artifact_id=$2 AND snapshot_key=$3 AND auth_scope_ref=$4
			AND mention_id=ANY($5::text[])`, source.Meta.CorpusId, ref.ArtifactId, snapshot,
			source.Context.AuthScopeRef, ids).Scan(&matched)
		if err != nil {
			return err
		}
		var total int
		err = tx.QueryRow(ctx, `SELECT count(*) FROM extraction_evidence_sources WHERE corpus_id=$1 AND artifact_id=$2`,
			source.Meta.CorpusId, ref.ArtifactId).Scan(&total)
		if err != nil {
			return err
		}
		if matched != len(ids) || total != len(ids) {
			return domain.ErrPersistentIntegrity
		}
		return nil
	})
}

// LoadResolutionEvidenceSources returns every catalogued location for every requested support.
// It never silently truncates or converts a missing/cross-snapshot support into model evidence.
func (r *Repository) LoadResolutionEvidenceSources(ctx context.Context, request *pb.RequestContext,
	mentionIDs []string, maximumReferences int) ([]*pb.ArtifactRef, error) {
	if err := domain.ValidateWire(request, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	if maximumReferences <= 0 || maximumReferences > domain.DefaultWireLimits.MaxItems || len(mentionIDs) == 0 || len(mentionIDs) > maximumReferences {
		return nil, errors.New("bounded non-empty support IDs required")
	}
	wanted := map[string]bool{}
	for _, id := range mentionIDs {
		if !storageIDPattern.MatchString(id) || wanted[id] {
			return nil, errors.New("invalid or repeated support ID")
		}
		wanted[id] = true
	}
	rows, err := r.pool.Query(ctx, `SELECT s.mention_id,a.artifact_id,a.digest,a.storage_key,a.media_type,a.byte_size,a.schema_version
		FROM extraction_evidence_sources s JOIN artifacts a ON a.artifact_id=s.artifact_id AND a.corpus_id=s.corpus_id
		JOIN job_checkpoints c ON c.checkpoint_id=s.checkpoint_id
		JOIN jobs j ON j.job_id=c.job_id AND j.corpus_id=s.corpus_id
		WHERE s.corpus_id=$1 AND s.snapshot_key=$2 AND s.auth_scope_ref=$3 AND s.mention_id=ANY($4::text[])
		AND c.stage=$5 AND c.terminal_status=$6
		ORDER BY a.artifact_id,s.mention_id LIMIT $7`, request.CorpusId, extractionSnapshotKey(request.SnapshotRef),
		request.AuthScopeRef, mentionIDs, int16(pb.JobStage_JOB_STAGE_EXTRACT), int16(pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED), maximumReferences+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var refs []*pb.ArtifactRef
	seen := map[string]*pb.ArtifactRef{}
	count := 0
	for rows.Next() {
		count++
		if count > maximumReferences {
			return nil, ErrResultLimit
		}
		ref := &pb.ArtifactRef{ContentHash: new(pb.ContentHash)}
		var mention, schema string
		var size int64
		if err = rows.Scan(&mention, &ref.ArtifactId, &ref.ContentHash.Sha256, &ref.StorageKey, &ref.MediaType, &size, &schema); err != nil {
			return nil, err
		}
		version, e := strconv.ParseUint(schema, 10, 32)
		if e != nil || version == 0 || size <= 0 {
			return nil, domain.ErrPersistentIntegrity
		}
		ref.ByteSize, ref.SchemaVersion = uint64(size), uint32(version)
		if e = domain.ValidateWire(ref, domain.DefaultWireLimits); e != nil {
			return nil, errors.Join(e, domain.ErrPersistentIntegrity)
		}
		delete(wanted, mention)
		if old := seen[ref.ArtifactId]; old != nil {
			if !proto.Equal(old, ref) {
				return nil, domain.ErrPersistentIntegrity
			}
		} else {
			seen[ref.ArtifactId] = ref
			refs = append(refs, ref)
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(wanted) != 0 {
		return nil, fmt.Errorf("candidate support is absent from the selected extraction snapshot: %w", domain.ErrNotFound)
	}
	return refs, nil
}

func extractionSnapshotKey(snapshot *pb.SnapshotRef) string {
	raw, _ := proto.MarshalOptions{Deterministic: true}.Marshal(snapshot)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
