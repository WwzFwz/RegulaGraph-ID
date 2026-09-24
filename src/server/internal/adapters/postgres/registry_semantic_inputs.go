// Authenticates EXTRACT and candidate protobuf bytes against registered immutable artifact
// references before semantic registry writes, and checks persisted human review rows for LINK.
// The calling workflow owns checkpoint/fence authorization and must fetch bytes from verified
// artifact storage; this adapter rejects hash, metadata, corpus, and review drift before CAS.
// Measure read/hash/validation p95/p99 separately from registry lock time; targets unmeasured.
package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func (r *Repository) decodeSemanticInputs(ctx context.Context, corpusID string,
	input SemanticRegistryInputs) (*pb.ExtractionBatch, *pb.RegistryCandidateBatch, error) {
	if !storageIDPattern.MatchString(corpusID) || input.SourceRef == nil || input.CandidateRef == nil ||
		input.SourceRef.ArtifactId == input.CandidateRef.ArtifactId ||
		len(input.SourceBytes) == 0 || len(input.CandidateBytes) == 0 {
		return nil, nil, errors.New("distinct registered EXTRACT and candidate artifacts are required")
	}
	for _, artifact := range []struct {
		ref        *pb.ArtifactRef
		raw        []byte
		typedMedia string
	}{
		{input.SourceRef, input.SourceBytes, domain.ExtractionBatchMediaType},
		{input.CandidateRef, input.CandidateBytes, "application/x-protobuf"},
	} {
		if err := domain.ValidateWire(artifact.ref, domain.DefaultWireLimits); err != nil {
			return nil, nil, fmt.Errorf("invalid registry artifact ref: %w", err)
		}
		if (artifact.ref.MediaType != "application/x-protobuf" && artifact.ref.MediaType != artifact.typedMedia) ||
			len(artifact.raw) > domain.DefaultWireLimits.MaxBytes ||
			artifact.ref.ByteSize != uint64(len(artifact.raw)) {
			return nil, nil, errors.New("registry artifact media or byte size differs")
		}
		digest := sha256.Sum256(artifact.raw)
		if hex.EncodeToString(digest[:]) != artifact.ref.ContentHash.Sha256 {
			return nil, nil, errors.New("registry artifact bytes differ from ref hash")
		}
		stored, err := r.LoadArtifact(ctx, corpusID, artifact.ref.ArtifactId)
		if err != nil {
			return nil, nil, fmt.Errorf("registry artifact is not registered: %w", err)
		}
		if !proto.Equal(stored, artifact.ref) {
			return nil, nil, fmt.Errorf("registry artifact metadata differs from persisted ref: %w", domain.ErrPersistentIntegrity)
		}
	}
	source := new(pb.ExtractionBatch)
	if err := domain.DecodeWire(input.SourceBytes, source, domain.DefaultWireLimits); err != nil {
		return nil, nil, fmt.Errorf("invalid EXTRACT artifact bytes: %w", err)
	}
	candidates := new(pb.RegistryCandidateBatch)
	if err := domain.DecodeWire(input.CandidateBytes, candidates, domain.DefaultWireLimits); err != nil {
		return nil, nil, fmt.Errorf("invalid candidate artifact bytes: %w", err)
	}
	if source.GetMeta().GetCorpusId() != corpusID || candidates.GetMeta().GetCorpusId() != corpusID ||
		input.SourceRef.SchemaVersion != source.GetMeta().GetSchemaVersion() ||
		input.CandidateRef.SchemaVersion != candidates.GetMeta().GetSchemaVersion() ||
		!proto.Equal(candidates.SourceExtractionBatch, input.SourceRef) {
		return nil, nil, errors.New("registered semantic artifacts belong to another source or corpus")
	}
	return source, candidates, nil
}

func verifyStoredSemanticReviews(ctx context.Context, tx pgx.Tx, corpusID, sourceArtifactID string,
	candidateRef *pb.ArtifactRef, proposals []*pb.ResolutionProposal,
	approvals map[string]ReviewedLink) error {
	if len(approvals) == 0 {
		return nil
	}
	byReviewID := make(map[string]ReviewedLink, len(approvals))
	proposalHashes := make(map[string]string, len(approvals))
	for _, proposal := range proposals {
		if _, approved := approvals[proposal.Meta.RecordId]; !approved {
			continue
		}
		raw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(proposal)
		if err != nil {
			return fmt.Errorf("encode reviewed proposal: %w", err)
		}
		digest := sha256.Sum256(raw)
		proposalHashes[proposal.Meta.RecordId] = hex.EncodeToString(digest[:])
	}
	ids := make([]string, 0, len(approvals))
	for _, approval := range approvals {
		if byReviewID[approval.ReviewID].ReviewID != "" {
			return errors.New("one review cannot authorize multiple proposals")
		}
		byReviewID[approval.ReviewID] = approval
		ids = append(ids, approval.ReviewID)
	}
	rows, err := tx.Query(ctx, `SELECT review_id,source_artifact_id,candidate_artifact_id,
		candidate_hash,proposal_id,proposal_hash,canonical_id,actor,reason,revoked_at IS NOT NULL
		FROM registry_semantic_reviews WHERE corpus_id=$1 AND review_id=ANY($2) FOR SHARE`,
		corpusID, ids)
	if err != nil {
		return fmt.Errorf("load semantic reviews: %w", err)
	}
	defer rows.Close()
	seen := make(map[string]bool, len(ids))
	for rows.Next() {
		var reviewID, sourceID, candidateID, candidateHash, proposalID, proposalHash string
		var canonicalID, actor, reason string
		var revoked bool
		if err = rows.Scan(&reviewID, &sourceID, &candidateID, &candidateHash,
			&proposalID, &proposalHash, &canonicalID, &actor, &reason, &revoked); err != nil {
			return fmt.Errorf("scan semantic review: %w", err)
		}
		approval, found := byReviewID[reviewID]
		if !found || seen[reviewID] || revoked || sourceID != sourceArtifactID ||
			candidateID != candidateRef.ArtifactId || candidateHash != candidateRef.ContentHash.Sha256 ||
			proposalID != approval.ProposalID || proposalHash != proposalHashes[approval.ProposalID] ||
			canonicalID != approval.CanonicalID ||
			actor != approval.Actor || reason != approval.Reason {
			return fmt.Errorf("LINK review record differs from authorization: %w",
				errors.Join(ErrConflict, domain.ErrResolutionReplan))
		}
		seen[reviewID] = true
	}
	if err = rows.Err(); err != nil {
		return fmt.Errorf("read semantic reviews: %w", err)
	}
	if len(seen) != len(ids) {
		return fmt.Errorf("LINK lacks a persisted review record: %w", ErrConflict)
	}
	return nil
}
