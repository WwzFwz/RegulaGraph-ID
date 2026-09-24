// Produces and stores a revision-pinned registry candidate artifact for a claimed RESOLVE job.
// Legal scopes arrive as an explicit policy plan; this handoff never infers canonical identity
// from surface similarity. It checks the EXTRACT checkpoint, lease, and registry revision around
// the batched lookup, then persists immutable bytes and dependency evidence. Measure read/lookup/
// storage p95/p99, candidate coverage, bytes, and retry rate against required benchmark targets.
package workflows

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type SemanticCandidateStore interface {
	LoadLatestCheckpoint(context.Context, string) (*pb.Checkpoint, error)
	LoadArtifact(context.Context, string, string) (*pb.ArtifactRef, error)
	ReadFencedResolveRevision(context.Context, domain.SemanticJobFence,
		*pb.ArtifactRef, *pb.ExtractionBatch) (uint64, error)
	PrepareRegistryCandidateBatch(context.Context, *pb.ExtractionBatch, *pb.ArtifactRef,
		*pb.ProducerManifest, string, []domain.RegistryCandidatePlan, int, int, int, int) (*pb.RegistryCandidateBatch, error)
	RegisterArtifact(context.Context, string, *pb.ArtifactRef) error
	ReplaceArtifactDependencyManifest(context.Context, string, string, *pb.DependencyManifest) error
}

type SemanticCandidateArtifacts interface {
	ReadVerified(context.Context, *pb.ArtifactRef, uint64) ([]byte, error)
	Put(context.Context, *pb.ArtifactRef, io.Reader) (bool, error)
}

// PrepareAndStoreCandidates uses the exact source checkpoint and caller-supplied legal scope
// plan. A registry change during preparation is retryable with a fresh candidate read because
// no immutable decision intent has been saved yet.
func (handoff *SemanticResolutionHandoff) PrepareAndStoreCandidates(ctx context.Context,
	job domain.JobRecord, producer *pb.ProducerManifest, recordID string,
	plans []domain.RegistryCandidatePlan, maximumScopes, maximumAliasesPerScope int,
) (*pb.RegistryCandidateBatch, *pb.ArtifactRef, error) {
	if handoff == nil || ctx == nil || producer == nil || recordID == "" ||
		job.JobID == "" || job.CorpusID == "" || job.LeaseOwner == "" || job.LeaseFence == 0 ||
		job.State != pb.JobState_JOB_STATE_RUNNING || job.Stage != pb.JobStage_JOB_STAGE_RESOLVE ||
		job.CancellationRequested || !job.LeaseExpiresAt.After(time.Now()) {
		return nil, nil, errors.New("live RESOLVE claim and candidate producer required")
	}
	store, storeOK := handoff.store.(SemanticCandidateStore)
	artifacts, artifactsOK := handoff.artifacts.(SemanticCandidateArtifacts)
	if !storeOK || !artifactsOK || maximumScopes <= 0 || maximumAliasesPerScope <= 0 {
		return nil, nil, errors.New("bounded candidate store and artifact writer required")
	}
	if err := domain.ValidateWire(producer, domain.DefaultWireLimits); err != nil {
		return nil, nil, fmt.Errorf("invalid candidate producer: %w", err)
	}
	attemptCtx, cancel := context.WithDeadline(ctx, job.LeaseExpiresAt)
	defer cancel()
	checkpoint, err := store.LoadLatestCheckpoint(attemptCtx, job.JobID)
	if err != nil || checkpoint == nil || checkpoint.GetMeta().GetCorpusId() != job.CorpusID ||
		checkpoint.JobId != job.JobID || checkpoint.Stage != pb.JobStage_JOB_STAGE_EXTRACT ||
		checkpoint.TerminalStatus != pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED ||
		checkpoint.Fence == 0 || checkpoint.Fence >= job.LeaseFence ||
		len(checkpoint.CompletedBatchKeys) != 1 || len(checkpoint.ArtifactHashes) != 1 {
		return nil, nil, fmt.Errorf("candidate preparation requires successful EXTRACT checkpoint: %w",
			errors.Join(err, domain.ErrPersistentIntegrity))
	}
	sourceRef, err := store.LoadArtifact(attemptCtx, job.CorpusID, checkpoint.CompletedBatchKeys[0])
	if err != nil || sourceRef == nil || sourceRef.MediaType != "application/x-protobuf" ||
		!proto.Equal(sourceRef.ContentHash, checkpoint.ArtifactHashes[0]) ||
		sourceRef.ByteSize == 0 || sourceRef.ByteSize >= handoff.maximumBytes {
		return nil, nil, fmt.Errorf("candidate source differs from EXTRACT checkpoint or byte budget: %w",
			errors.Join(err, domain.ErrPersistentIntegrity))
	}
	sourceBytes, err := artifacts.ReadVerified(attemptCtx, sourceRef, handoff.maximumBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("read verified candidate source: %w", err)
	}
	source := new(pb.ExtractionBatch)
	if err = domain.DecodeWire(sourceBytes, source, domain.DefaultWireLimits); err != nil ||
		source.GetMeta().GetCorpusId() != job.CorpusID ||
		source.Completeness != pb.Completeness_COMPLETENESS_COMPLETE ||
		len(source.Mentions) == 0 {
		return nil, nil, fmt.Errorf("candidate source is not complete EXTRACT with mentions: %w",
			errors.Join(err, domain.ErrPersistentIntegrity))
	}
	proof := domain.SemanticJobFence{JobID: job.JobID, OwnerID: job.LeaseOwner,
		SourceCheckpointID: checkpoint.Meta.RecordId, Fence: job.LeaseFence}
	before, err := store.ReadFencedResolveRevision(attemptCtx, proof, sourceRef, source)
	if err != nil {
		return nil, nil, fmt.Errorf("verify candidate source and lease: %w", err)
	}
	batch, err := store.PrepareRegistryCandidateBatch(attemptCtx, source, sourceRef,
		producer, recordID, plans, maximumScopes, maximumAliasesPerScope,
		handoff.maximumReferences, handoff.maximumCandidatesPerMention)
	if err != nil {
		return nil, nil, fmt.Errorf("prepare registry candidates: %w", err)
	}
	if batch.RegistryRevision != before || batch.GetMeta().GetCorpusId() != job.CorpusID {
		return nil, nil, fmt.Errorf("registry changed during candidate preparation: %w",
			domain.ErrCandidateViewChanged)
	}
	after, err := store.ReadFencedResolveRevision(attemptCtx, proof, sourceRef, source)
	if err != nil {
		return nil, nil, fmt.Errorf("verify candidate lease after lookup: %w", err)
	}
	if after != before {
		return nil, nil, fmt.Errorf("registry changed before candidate artifact storage: %w",
			domain.ErrCandidateViewChanged)
	}
	if err = domain.ValidateRegistryCandidateBatch(batch, source, sourceRef,
		handoff.maximumReferences, handoff.maximumCandidatesPerMention); err != nil {
		return nil, nil, fmt.Errorf("candidate closure before storage: %w", err)
	}
	raw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(batch)
	if err != nil || len(raw) == 0 || uint64(len(raw)) > handoff.maximumBytes-sourceRef.ByteSize {
		return nil, nil, fmt.Errorf("candidate artifact exceeds combined byte budget: %w", err)
	}
	digest := sha256.Sum256(raw)
	hexDigest := hex.EncodeToString(digest[:])
	ref := &pb.ArtifactRef{ArtifactId: "artifact:registry-candidates:" + hexDigest,
		ContentHash: &pb.ContentHash{Sha256: hexDigest},
		StorageKey:  "sha256/" + hexDigest[:2] + "/" + hexDigest[2:4] + "/" + hexDigest + ".bin",
		MediaType:   "application/x-protobuf", ByteSize: uint64(len(raw)),
		SchemaVersion: batch.GetMeta().GetSchemaVersion()}
	if err = domain.ValidateWire(ref, domain.DefaultWireLimits); err != nil {
		return nil, nil, fmt.Errorf("candidate artifact reference: %w", err)
	}
	if _, err = artifacts.Put(attemptCtx, ref, bytes.NewReader(raw)); err != nil {
		return nil, nil, fmt.Errorf("persist registry candidates: %w", err)
	}
	if err = store.RegisterArtifact(attemptCtx, job.CorpusID, ref); err != nil {
		return nil, nil, fmt.Errorf("register registry candidate artifact: %w", err)
	}
	if err = store.ReplaceArtifactDependencyManifest(attemptCtx, job.CorpusID,
		ref.ArtifactId, batch.Dependencies); err != nil {
		return nil, nil, fmt.Errorf("register registry candidate dependencies: %w", err)
	}
	return batch, ref, nil
}
