// Stores one immutable RESOLVE intent under the EXTRACT checkpoint fence before registry CAS.
// Hash-checked payload allows a successor lease to replay exactly the same request and review
// assertions if the process crashes before saving RESOLVE output. The row is append-only; a
// changed retry is rejected instead of silently changing a committed decision. Measure intent
// write/read and job-lock wait p95/p99; required targets remain REQUIRED_UNMEASURED.
package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

const maximumSemanticIntentBytes = 16 << 20

func encodeSemanticIntent(intent domain.SemanticResolutionIntent) ([]byte, error) {
	if intent.SourceCheckpointID == "" || intent.CandidateRef == nil ||
		intent.Request == nil || intent.Preview == nil {
		return nil, errors.New("complete semantic intent is required")
	}
	parts := make([][]byte, 4)
	var err error
	parts[0], err = (proto.MarshalOptions{Deterministic: true}).Marshal(intent.CandidateRef)
	if err != nil {
		return nil, err
	}
	parts[1], err = (proto.MarshalOptions{Deterministic: true}).Marshal(intent.Request)
	if err != nil {
		return nil, err
	}
	parts[2], err = json.Marshal(intent.Approvals)
	if err != nil {
		return nil, err
	}
	parts[3], err = (proto.MarshalOptions{Deterministic: true}).Marshal(intent.Preview)
	if err != nil {
		return nil, err
	}
	total := 16
	for _, part := range parts {
		if len(part) == 0 || len(part) > maximumSemanticIntentBytes-total {
			return nil, errors.New("semantic intent exceeds byte budget")
		}
		total += len(part)
	}
	var payload bytes.Buffer
	payload.Grow(total)
	for _, part := range parts {
		if err = binary.Write(&payload, binary.BigEndian, uint32(len(part))); err != nil {
			return nil, err
		}
		payload.Write(part)
	}
	return payload.Bytes(), nil
}

func decodeSemanticIntent(payload []byte, checkpointID string) (domain.SemanticResolutionIntent, error) {
	if len(payload) == 0 || len(payload) > maximumSemanticIntentBytes {
		return domain.SemanticResolutionIntent{}, domain.ErrPersistentIntegrity
	}
	reader := bytes.NewReader(payload)
	parts := make([][]byte, 4)
	for index := range parts {
		var length uint32
		if err := binary.Read(reader, binary.BigEndian, &length); err != nil || length == 0 ||
			uint64(length) > uint64(reader.Len()) {
			return domain.SemanticResolutionIntent{}, domain.ErrPersistentIntegrity
		}
		parts[index] = make([]byte, length)
		if _, err := io.ReadFull(reader, parts[index]); err != nil {
			return domain.SemanticResolutionIntent{}, domain.ErrPersistentIntegrity
		}
	}
	if reader.Len() != 0 {
		return domain.SemanticResolutionIntent{}, domain.ErrPersistentIntegrity
	}
	intent := domain.SemanticResolutionIntent{SourceCheckpointID: checkpointID,
		CandidateRef: new(pb.ArtifactRef), Request: new(pb.RegistryResolveRequest),
		Preview: new(pb.ResolutionBatch)}
	for index, message := range []proto.Message{intent.CandidateRef, intent.Request, intent.Preview} {
		part := parts[index]
		if index == 2 {
			part = parts[3]
		}
		if err := domain.DecodeWire(part, message, domain.DefaultWireLimits); err != nil {
			return domain.SemanticResolutionIntent{}, fmt.Errorf("invalid persisted semantic intent: %w", domain.ErrPersistentIntegrity)
		}
	}
	if err := json.Unmarshal(parts[2], &intent.Approvals); err != nil {
		return domain.SemanticResolutionIntent{}, domain.ErrPersistentIntegrity
	}
	return intent, nil
}

// SaveSemanticResolutionIntent is called after output preflight and before registry CAS. A
// replay with the same job ID must carry byte-identical intent, even after a new lease claim.
func (r *Repository) SaveSemanticResolutionIntent(ctx context.Context,
	proof domain.SemanticJobFence, sourceRef *pb.ArtifactRef, source *pb.ExtractionBatch,
	intent domain.SemanticResolutionIntent) error {
	if r == nil || r.pool == nil || ctx == nil || !storageIDPattern.MatchString(proof.JobID) ||
		!storageIDPattern.MatchString(proof.OwnerID) || proof.Fence == 0 ||
		proof.SourceCheckpointID != intent.SourceCheckpointID ||
		intent.Request == nil || !storageIDPattern.MatchString(intent.Request.OperationKey) ||
		source == nil || sourceRef == nil || intent.Preview == nil ||
		!proto.Equal(intent.Preview.SourceExtractionBatch, sourceRef) ||
		intent.Preview.GetMeta().GetCorpusId() != source.GetMeta().GetCorpusId() {
		return errors.New("fenced semantic intent and source are required")
	}
	if err := domain.ValidateResolutionBatchClosure(intent.Preview, source, sourceRef,
		domain.DefaultWireLimits.MaxItems); err != nil {
		return fmt.Errorf("semantic intent output closure: %w", err)
	}
	payload, err := encodeSemanticIntent(intent)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(payload)
	hash := hex.EncodeToString(digest[:])
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("begin semantic intent: %w", err)
	}
	defer tx.Rollback(ctx)
	if err = verifySemanticJobFence(ctx, tx, proof, source.Meta.CorpusId, sourceRef, source); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO semantic_resolution_intents
		(job_id,corpus_id,source_checkpoint_id,operation_key,payload,payload_hash)
		VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (job_id) DO NOTHING`, proof.JobID,
		source.Meta.CorpusId, intent.SourceCheckpointID, intent.Request.OperationKey, payload, hash)
	if err != nil {
		return fmt.Errorf("save semantic intent: %w", err)
	}
	if tag.RowsAffected() == 0 {
		var oldCorpus, oldCheckpoint, oldOperation, oldHash string
		var oldPayload []byte
		if err = tx.QueryRow(ctx, `SELECT corpus_id,source_checkpoint_id,operation_key,payload_hash,payload
			FROM semantic_resolution_intents WHERE job_id=$1 FOR SHARE`, proof.JobID).
			Scan(&oldCorpus, &oldCheckpoint, &oldOperation, &oldHash, &oldPayload); err != nil {
			return fmt.Errorf("read semantic intent replay: %w", err)
		}
		if oldCorpus != source.Meta.CorpusId || oldCheckpoint != intent.SourceCheckpointID ||
			oldOperation != intent.Request.OperationKey || oldHash != hash || !bytes.Equal(oldPayload, payload) {
			return fmt.Errorf("semantic intent changed across retry: %w", ErrConflict)
		}
	}
	if err = verifySemanticLeaseStillLive(ctx, tx, proof); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// LoadSemanticResolutionIntent validates the immutable bytes before a successor reuses them.
func (r *Repository) LoadSemanticResolutionIntent(ctx context.Context,
	corpusID, jobID string) (domain.SemanticResolutionIntent, error) {
	if !storageIDPattern.MatchString(corpusID) || !storageIDPattern.MatchString(jobID) {
		return domain.SemanticResolutionIntent{}, errors.New("valid corpus and job ID required")
	}
	var checkpointID, operationKey, storedHash string
	var payload []byte
	err := r.pool.QueryRow(ctx, `SELECT source_checkpoint_id,operation_key,payload,payload_hash
		FROM semantic_resolution_intents WHERE corpus_id=$1 AND job_id=$2`, corpusID, jobID).
		Scan(&checkpointID, &operationKey, &payload, &storedHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.SemanticResolutionIntent{}, ErrNotFound
	}
	if err != nil {
		return domain.SemanticResolutionIntent{}, fmt.Errorf("load semantic intent: %w", err)
	}
	digest := sha256.Sum256(payload)
	if hex.EncodeToString(digest[:]) != storedHash {
		return domain.SemanticResolutionIntent{}, fmt.Errorf("semantic intent hash differs: %w", domain.ErrPersistentIntegrity)
	}
	intent, err := decodeSemanticIntent(payload, checkpointID)
	if err != nil || intent.Request == nil || intent.Request.Context == nil ||
		intent.Request.OperationKey != operationKey ||
		intent.Request.Context.CorpusId != corpusID ||
		intent.Preview.GetMeta().GetCorpusId() != corpusID {
		return domain.SemanticResolutionIntent{}, fmt.Errorf("semantic intent metadata differs: %w", domain.ErrPersistentIntegrity)
	}
	return intent, nil
}
