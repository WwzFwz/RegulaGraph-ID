// Bridges the initial index writer to durable publication/checkpoint authority.
// A session advisory lock serializes collection bootstrap and dispatch; immutable
// point/operation bindings make a late identical upsert harmless after connection
// loss. No mutable closure or deletion is supported by this protocol. Intent is
// committed before Qdrant I/O; a failed write remains recoverable, never compensated
// by merely changing SQL status. Measure pool occupancy, lock wait and retry p95
// under benchmark-targets.yaml; production gates remain unmeasured.
package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func (r *Repository) AcquireIndexWriteLock(ctx context.Context, publication string) (func(), error) {
	if !storageIDPattern.MatchString(publication) {
		return nil, errors.New("invalid index publication ID")
	}
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	key := "regulagraph:index-writer:" + publication
	var locked bool
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1,0))`, key).Scan(&locked); err != nil || !locked {
		conn.Release()
		if err != nil {
			return nil, err
		}
		return nil, domain.ErrLeaseUnavailable
	}
	return sync.OnceFunc(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		var unlocked bool
		if err := conn.QueryRow(cleanup, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, key).Scan(&unlocked); err != nil || !unlocked {
			raw := conn.Hijack()
			_ = raw.Close(cleanup)
			return
		}
		conn.Release()
	}), nil
}

// VerifyIndexSourceCheckpoint requires the exact latest successful CHUNK artifact
// from the named corpus/job, even if EXTRACT/RESOLVE has since advanced the job.
// It does not infer membership from a missing visibility. The initial preparer
// additionally requires source.Context.SnapshotRef == planned target snapshot.
// The initial writer separately proves full chunk coverage of all declared jobs.
func (r *Repository) VerifyIndexSourceCheckpoint(ctx context.Context, corpus, job string, ref *pb.ArtifactRef) error {
	if !storageIDPattern.MatchString(corpus) || !storageIDPattern.MatchString(job) || ref == nil {
		return errors.New("source job required")
	}
	if err := domain.ValidateWire(ref, domain.DefaultWireLimits); err != nil {
		return err
	}
	registered, err := r.LoadArtifact(ctx, corpus, ref.ArtifactId)
	if err != nil {
		return err
	}
	if !proto.Equal(registered, ref) {
		return domain.ErrPersistentIntegrity
	}
	var raw []byte
	var digest string
	var terminal int16
	var fence int64
	var checkpointID string
	err = r.pool.QueryRow(ctx, `SELECT c.payload,c.payload_hash,COALESCE(c.terminal_status,0),c.fence,c.checkpoint_id FROM job_checkpoints c
 JOIN jobs j ON j.job_id=c.job_id WHERE j.job_id=$1 AND j.corpus_id=$2 AND NOT j.cancellation_requested
 AND c.stage=$3 ORDER BY c.fence DESC,c.created_at DESC LIMIT 1`, job, corpus,
		int16(pb.JobStage_JOB_STAGE_CHUNK)).Scan(&raw, &digest, &terminal, &fence, &checkpointID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != digest {
		return domain.ErrPersistentIntegrity
	}
	checkpoint := new(pb.Checkpoint)
	if err = domain.DecodeWire(raw, checkpoint, domain.DefaultWireLimits); err != nil {
		return err
	}
	if terminal != int16(pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED) || checkpoint.Fence != uint64(fence) || checkpoint.Meta.RecordId != checkpointID ||
		checkpoint.JobId != job || checkpoint.Meta.CorpusId != corpus || checkpoint.Stage != pb.JobStage_JOB_STAGE_CHUNK || checkpoint.TerminalStatus != pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED ||
		len(checkpoint.CompletedBatchKeys) != 1 || len(checkpoint.ArtifactHashes) != 1 || checkpoint.CompletedBatchKeys[0] != ref.ArtifactId || !proto.Equal(checkpoint.ArtifactHashes[0], ref.ContentHash) {
		return domain.ErrPersistentIntegrity
	}
	return nil
}

func (r *Repository) VerifyIndexDictionary(ctx context.Context, corpus string, a *pb.LexicalDictionaryArtifact) error {
	// Artifacts are already hash-verified and structurally checked by the writer.
	if a == nil || a.Meta.GetCorpusId() != corpus || a.MappingFingerprint == nil {
		return errors.New("corpus-bound dictionary required")
	}
	if err := domain.ValidateWire(a, domain.DefaultWireLimits); err != nil {
		return err
	}
	entries, err := r.LoadLexicalDictionary(ctx, corpus, a.AnalyzerId, a.RegistryRevision, 50_000)
	if err != nil {
		return err
	}
	revision, err := domain.LexicalRevisionName(a.RegistryRevision)
	if err != nil {
		return err
	}
	digest, err := domain.FingerprintLexicalDictionary(a.AnalyzerId, revision, entries)
	if err != nil {
		return err
	}
	if len(entries) != len(a.Entries) || fmt.Sprintf("%x", digest) != a.MappingFingerprint.Sha256 {
		return domain.ErrPersistentIntegrity
	}
	return nil
}

func (r *Repository) EnsureIndexWriteIntent(ctx context.Context, b domain.IndexCatalogBinding, operation, digest string, count uint64) error {
	if err := domain.ValidateIndexCatalogBinding(b); err != nil {
		return err
	}
	if !storageIDPattern.MatchString(operation) || !sha256Pattern.MatchString(digest) || count == 0 {
		return errors.New("invalid initial index intent")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = lockIndexPublication(ctx, tx, b); err != nil {
		return err
	}
	stored, err := loadIndexBinding(ctx, tx, b.Generation.Meta.CorpusId, b.Generation.Meta.RecordId)
	if err != nil {
		return err
	}
	if !equalIndexBinding(b, stored) {
		return ErrConflict
	}
	var expectedDigest, gen string
	var expectedCount int64
	err = tx.QueryRow(ctx, `SELECT operations_checksum,generation,expected_count FROM publication_backends WHERE publication_id=$1 AND backend=$2`, b.PublicationID, int16(pb.BackendKind_BACKEND_KIND_QDRANT)).Scan(&expectedDigest, &gen, &expectedCount)
	if err != nil {
		return err
	}
	if expectedDigest != digest || gen != b.Generation.Meta.RecordId || uint64(expectedCount) != count {
		return ErrConflict
	}
	_, err = tx.Exec(ctx, `INSERT INTO publication_operations(operation_key,publication_id,backend,payload_hash,fence,status)
 VALUES($1,$2,$3,$4,$5,'planned') ON CONFLICT(operation_key) DO NOTHING`, operation, b.PublicationID, int16(pb.BackendKind_BACKEND_KIND_QDRANT), digest, int64(b.Fence))
	if err != nil {
		return err
	}
	var actualPublication, actualDigest, state string
	var fence int64
	var backend int16
	err = tx.QueryRow(ctx, `SELECT publication_id,backend,payload_hash,fence,status FROM publication_operations WHERE operation_key=$1`, operation).Scan(&actualPublication, &backend, &actualDigest, &fence, &state)
	if err != nil {
		return err
	}
	if actualPublication != b.PublicationID || backend != int16(pb.BackendKind_BACKEND_KIND_QDRANT) || actualDigest != digest || uint64(fence) != b.Fence || (state != "planned" && state != "applied") {
		return ErrConflict
	}
	return tx.Commit(ctx)
}
