// Reads a committed semantic receipt without claiming or mutating a RESOLVE job.
// ASSEMBLE preparation uses this historical read after the original job is STAGED;
// immutable operation hashes and every decision are checked against registered
// source/candidate bytes and stored review assertions. No missing operation is
// created, no candidate lookup is silently refreshed, and registry revision is
// not advanced. Callers separately authenticate corpus access, source checkpoint,
// publication authority and artifact bytes. Measure read/hash/replay p95/p99 and
// bounded result bytes under benchmark-targets.yaml (REQUIRED_UNMEASURED).
package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func (r *Repository) ReadCommittedSemanticResolution(ctx context.Context, input domain.SemanticRegistryInputs,
	request *pb.RegistryResolveRequest, approvals []domain.ReviewedLink,
	maximumReferences, maximumCandidatesPerMention int) (*pb.RegistryResolveResponse, error) {
	if r == nil || r.pool == nil || ctx == nil || request == nil || request.Context == nil ||
		!storageIDPattern.MatchString(request.OperationKey) || request.ExpectedRevision == 0 || request.ExpectedRevision > math.MaxInt64-1 ||
		len(request.Proposals) == 0 || len(request.Proposals) > maximumRegistryClaims || len(approvals) > len(request.Proposals) || maximumReferences <= 0 || maximumCandidatesPerMention <= 0 {
		return nil, errors.New("bounded historical semantic operation required")
	}
	if err := domain.ValidateWire(request, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	source, candidates, err := r.decodeSemanticInputs(ctx, request.Context.CorpusId, input)
	if err != nil {
		return nil, err
	}
	// Action-specific candidate cardinality must be checked before inspecting
	// the exact candidate ID in LINK approvals, just as in the write path.
	if err = domain.ValidateResolutionProposalsAgainstCandidates(source, input.SourceRef, candidates,
		request.Proposals, maximumReferences, maximumCandidatesPerMention); err != nil {
		return nil, err
	}
	reviewed, err := checkReviewedLinks(request.Proposals, approvals)
	if err != nil {
		return nil, err
	}
	digest, err := semanticRequestHash(request, candidates, approvals)
	if err != nil {
		return nil, err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var revision int64
	err = tx.QueryRow(ctx, `SELECT registry_revision FROM corpus_state WHERE corpus_id=$1`, request.Context.CorpusId).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	receipt, found, err := loadSemanticOperation(ctx, tx, request, digest, revision, reviewed)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrNotFound
	}
	if err = domain.ValidateRegistryResolveReceipt(source, input.SourceRef, candidates, request, receipt, maximumReferences, maximumCandidatesPerMention); err != nil {
		return nil, fmt.Errorf("committed semantic receipt no longer matches source inputs: %w", domain.ErrPersistentIntegrity)
	}
	if err = checkStoredSemanticReviews(ctx, tx, request.Context.CorpusId, input.SourceRef.ArtifactId, input.CandidateRef, request.Proposals, reviewed, false); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return receipt, nil
}
