// Authenticates a historical RESOLVE and proves unchanged candidate context at
// a retained target view. Both source-receipt creation and job admission share
// this check; no decision is regenerated or rewritten. The caller compares the
// registry stamp under its write locks before persisting a reuse receipt.
// Document identities, publication and checkpoint authority remain separate
// required gates. Profile historical SQL, bytes and replan cost under the
// required benchmark suite; this proves safe reuse, not model correctness.
package postgres

import (
	"context"
	"errors"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func (r *Repository) verifyGraphResolutionAtRevision(ctx context.Context, corpus, job string, extractionRef *pb.ArtifactRef,
	extract *pb.ExtractionBatch, resolve *pb.ResolutionBatch, input domain.GraphSourceBindingInputs, target uint64, refs, candidatesLimit int) error {
	if !domain.ResolutionViewCanReaffirm(resolve, target) {
		return domain.ErrResolutionReplan
	}
	if err := r.VerifyRetainedRegistryRevision(ctx, corpus, target); err != nil {
		return err
	}
	if err := domain.ValidateGraphResolutionSource(extract, extractionRef, resolve, refs); err != nil {
		return err
	}
	if err := domain.ValidateGraphExtractionDependencies(extract); err != nil {
		return err
	}
	if len(extract.Mentions) == 0 {
		deps := resolve.Dependencies
		if len(input.Candidates) != 0 || len(deps.LookupScopeRevisions) != 0 || len(deps.Dependencies) != 1 || deps.Dependencies[0].DependencyId != extractionRef.ArtifactId || !proto.Equal(deps.Dependencies[0].Fingerprint, extractionRef.ContentHash) {
			return errors.New("empty RESOLVE has unsupported dependencies")
		}
		return nil
	}
	intent, err := r.LoadSemanticResolutionIntent(ctx, corpus, job)
	if err != nil {
		return err
	}
	if intent.Request == nil || !proto.Equal(intent.Preview, resolve) {
		return domain.ErrPersistentIntegrity
	}
	semantic := domain.SemanticRegistryInputs{SourceRef: extractionRef, SourceBytes: input.Extraction, CandidateRef: intent.CandidateRef, CandidateBytes: input.Candidates}
	_, candidateBatch, err := r.decodeSemanticInputs(ctx, corpus, semantic)
	if err != nil {
		return err
	}
	receipt, err := r.ReadCommittedSemanticResolution(ctx, semantic, intent.Request, intent.Approvals, refs, candidatesLimit)
	if err != nil {
		return err
	}
	reconstructed, err := domain.AssembleResolutionBatchFromReceipt(extract, extractionRef, candidateBatch, intent.CandidateRef, intent.Request, receipt, resolve.ModelManifest, resolve.Dependencies.ProducerManifest, resolve.TokenUsage, resolve.Meta.RecordId, refs, candidatesLimit)
	if err != nil {
		return err
	}
	if !proto.Equal(reconstructed, resolve) {
		return domain.ErrPersistentIntegrity
	}
	aliasLimit, err := domain.GraphCandidateAliasBudget(candidateBatch, refs)
	if err != nil {
		return err
	}
	return r.VerifyRegistryCandidateView(ctx, corpus, semantic, target, refs, aliasLimit, refs, candidatesLimit)
}

// VerifyRetainedRegistryRevision also covers mention-free RESOLVE, which has no
// candidate lookup to enforce the history window. This read holds no authority.
func (r *Repository) VerifyRetainedRegistryRevision(ctx context.Context, corpus string, target uint64) error {
	if ctx == nil || !storageIDPattern.MatchString(corpus) || target == 0 || target > 1<<63-1 {
		return domain.ErrResolutionReplan
	}
	var valid bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM corpus_state WHERE corpus_id=$1 AND registry_history_floor>0 AND registry_history_floor<=$2 AND registry_revision>=$2)`, corpus, int64(target)).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return domain.ErrResolutionReplan
	}
	return nil
}

// GraphPreparationRegistryRevision freezes the intended view before preflight.
// An existing publication binding wins over a newer current revision for exact
// retry; ReservePublication/BindPublicationRegistry still authenticate the target.
func (r *Repository) GraphPreparationRegistryRevision(ctx context.Context, corpus, publication string) (uint64, error) {
	if !storageIDPattern.MatchString(corpus) || !storageIDPattern.MatchString(publication) {
		return 0, ErrConflict
	}
	var target, current, floor int64
	err := r.pool.QueryRow(ctx, `SELECT COALESCE(b.registry_revision,c.registry_revision),c.registry_revision,c.registry_history_floor
 FROM corpus_state c LEFT JOIN snapshot_registry_bindings b ON b.corpus_id=c.corpus_id AND b.publication_id=$2 WHERE c.corpus_id=$1`, corpus, publication).Scan(&target, &current, &floor)
	if err != nil {
		return 0, err
	}
	if floor <= 0 || target < floor || target > current {
		return 0, ErrConflict
	}
	return uint64(target), nil
}
