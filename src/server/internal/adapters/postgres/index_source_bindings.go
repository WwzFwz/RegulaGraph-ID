// Persists the provenance bridge from an unpublished CHUNK checkpoint to a
// snapshot-bound metadata envelope. The coordinator must verify the pure source
// transform before registration. This transaction checks original authority,
// immutable artifact metadata, source cancellation and the live initial publisher.
// Binding does not admit an incomplete final INDEX inventory or publish anything.
// Measure binding lock/pool time and throughput against benchmark-targets.yaml.
package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func (r *Repository) RegisterIndexSourceBinding(ctx context.Context, binding domain.IndexSourceBinding) error {
	if err := domain.ValidateIndexSourceBinding(binding); err != nil {
		return err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var snapshotID, corpus, stagedHash, stagedGeneration string
	var sequence, fence, current int64
	var state int16
	var initial bool
	err = tx.QueryRow(ctx, `SELECT s.snapshot_id,s.corpus_id,s.sequence,s.fence,c.publisher_fence,s.state,s.parent_snapshot_id IS NULL,
	 COALESCE(s.manifest_hash,''),COALESCE(s.representation_generation,'')
	 FROM snapshots s JOIN corpus_state c ON c.corpus_id=s.corpus_id WHERE s.publication_id=$1 FOR UPDATE OF s,c`, binding.PublicationID).Scan(&snapshotID, &corpus, &sequence, &fence, &current, &state, &initial, &stagedHash, &stagedGeneration)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !initial || snapshotID != binding.Snapshot.SnapshotId || corpus != binding.Snapshot.CorpusId || uint64(sequence) != binding.Snapshot.Sequence || uint64(fence) != binding.Fence || current != fence ||
		(state != int16(pb.SnapshotState_SNAPSHOT_STATE_STAGING) && state != int16(pb.SnapshotState_SNAPSHOT_STATE_VALIDATING)) {
		return ErrConflict
	}
	if stagedHash != "" && stagedHash != binding.Snapshot.ManifestHash.Sha256 || stagedGeneration != "" && stagedGeneration != binding.Snapshot.RepresentationGeneration {
		return ErrConflict
	}
	var cancelled bool
	err = tx.QueryRow(ctx, `SELECT cancellation_requested FROM jobs WHERE job_id=$1 AND corpus_id=$2 FOR SHARE`, binding.SourceJobID, corpus).Scan(&cancelled)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if cancelled {
		return ErrConflict
	}
	if err = verifyOriginalIndexSourceCheckpoint(ctx, tx, corpus, binding.SourceJobID, binding.Original); err != nil {
		return err
	}
	registered, err := loadArtifact(ctx, tx, corpus, binding.Bound.ArtifactId)
	if err != nil {
		return err
	}
	if !proto.Equal(registered, binding.Bound) {
		return domain.ErrPersistentIntegrity
	}
	marshal := proto.MarshalOptions{Deterministic: true}
	snapshot, err := marshal.Marshal(binding.Snapshot)
	if err != nil {
		return err
	}
	original, err := marshal.Marshal(binding.Original)
	if err != nil {
		return err
	}
	bound, err := marshal.Marshal(binding.Bound)
	if err != nil {
		return err
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(snapshot))
	_, err = tx.Exec(ctx, `INSERT INTO index_source_snapshots(publication_id,corpus_id,fence,snapshot_payload,snapshot_hash,auth_scope)
	 VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(publication_id) DO NOTHING`, binding.PublicationID, corpus, fence, snapshot, hash, binding.AuthScope)
	if err != nil {
		return err
	}
	var storedHash, scope string
	var payload []byte
	var storedFence int64
	if err = tx.QueryRow(ctx, `SELECT snapshot_payload,snapshot_hash,auth_scope,fence FROM index_source_snapshots WHERE publication_id=$1`, binding.PublicationID).Scan(&payload, &storedHash, &scope, &storedFence); err != nil {
		return err
	}
	if fmt.Sprintf("%x", sha256.Sum256(payload)) != storedHash {
		return domain.ErrPersistentIntegrity
	}
	if storedHash != hash || scope != binding.AuthScope || storedFence != fence {
		return ErrConflict
	}
	_, err = tx.Exec(ctx, `INSERT INTO index_source_bindings(publication_id,source_job_id,original_artifact_id,bound_artifact_id,original_reference,bound_reference)
	 VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(publication_id,source_job_id) DO NOTHING`, binding.PublicationID, binding.SourceJobID, binding.Original.ArtifactId, binding.Bound.ArtifactId, original, bound)
	if err != nil {
		return err
	}
	var oldOriginal, oldBound []byte
	if err = tx.QueryRow(ctx, `SELECT original_reference,bound_reference FROM index_source_bindings WHERE publication_id=$1 AND source_job_id=$2`, binding.PublicationID, binding.SourceJobID).Scan(&oldOriginal, &oldBound); err != nil {
		return err
	}
	if string(oldOriginal) != string(original) || string(oldBound) != string(bound) {
		return ErrConflict
	}
	return tx.Commit(ctx)
}

// Called with the publication row locked, so binding and staging cannot freeze
// different snapshot metadata whichever transaction runs first.
func verifyIndexSourceSnapshot(ctx context.Context, query indexQuerier, publication string, snapshot *pb.SnapshotRef) error {
	var raw []byte
	var hash string
	err := query.QueryRow(ctx, `SELECT snapshot_payload,snapshot_hash FROM index_source_snapshots WHERE publication_id=$1`, publication).Scan(&raw, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if hash != fmt.Sprintf("%x", sha256.Sum256(raw)) {
		return domain.ErrPersistentIntegrity
	}
	stored := new(pb.SnapshotRef)
	if err = domain.DecodeWire(raw, stored, domain.DefaultWireLimits); err != nil {
		return err
	}
	if !proto.Equal(stored, snapshot) {
		return ErrConflict
	}
	return nil
}

func verifyBoundIndexSourceCheckpoint(ctx context.Context, query indexQuerier, corpus, job string, ref *pb.ArtifactRef) (bool, error) {
	var originalBytes, boundBytes []byte
	err := query.QueryRow(ctx, `SELECT b.original_reference,b.bound_reference FROM index_source_bindings b
	 JOIN index_source_snapshots s ON s.publication_id=b.publication_id
	 WHERE b.bound_artifact_id=$1 AND b.source_job_id=$2 AND s.corpus_id=$3`, ref.GetArtifactId(), job, corpus).Scan(&originalBytes, &boundBytes)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	original, bound := new(pb.ArtifactRef), new(pb.ArtifactRef)
	if err = domain.DecodeWire(originalBytes, original, domain.DefaultWireLimits); err != nil {
		return true, err
	}
	if err = domain.DecodeWire(boundBytes, bound, domain.DefaultWireLimits); err != nil {
		return true, err
	}
	if !proto.Equal(bound, ref) {
		return true, domain.ErrPersistentIntegrity
	}
	registered, err := loadArtifact(ctx, query, corpus, bound.ArtifactId)
	if err != nil {
		return true, err
	}
	if !proto.Equal(registered, bound) {
		return true, domain.ErrPersistentIntegrity
	}
	return true, verifyOriginalIndexSourceCheckpoint(ctx, query, corpus, job, original)
}
