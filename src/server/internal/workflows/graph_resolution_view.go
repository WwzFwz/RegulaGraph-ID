// Verifies the supported EXTRACT dependency contract and complete RESOLVE candidate
// context at a graph publication's registry revision. The historical ledger reader
// supplies already authenticated, bounded source/candidate bytes, avoiding repeated
// storage reads. This does not change recorded revisions or create an admission
// receipt. Scheduling must repeat authority/freshness under its transaction locks.
// Measure candidate SQL p95/p99, bytes and replan frequency against the required
// targets in configs/benchmark-targets.yaml; quality/performance are unmeasured.
package workflows

import (
	"context"
	"errors"
	"math"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type GraphResolutionViewStore interface {
	GraphResolutionReceiptStore
	VerifyRetainedRegistryRevision(context.Context, string, uint64) error
	VerifyRegistryCandidateView(context.Context, string, domain.SemanticRegistryInputs, uint64, int, int, int, int) error
}

// ReadGraphResolutionForAssembly adds dependency checks to the historical receipt
// read at the same or a later retained view. Cross-revision use still needs the
// durable reaffirmation-policy source receipt. A return proves only this read,
// not a right to publish/schedule or a mutation of the historical resolution.
func ReadGraphResolutionForAssembly(ctx context.Context, store GraphResolutionViewStore, reader DocumentArtifactReader,
	corpus, job, checkpoint string, extractionRef, resolutionRef *pb.ArtifactRef, targetRevision uint64,
	maximumEdges, maximumCandidates int) (*pb.ResolutionBatch, error) {
	if store == nil || targetRevision == 0 || targetRevision > math.MaxInt64 {
		return nil, errors.New("graph registry store and retained target revision required")
	}
	if err := store.VerifyRetainedRegistryRevision(ctx, corpus, targetRevision); err != nil {
		return nil, err
	}
	return readGraphResolutionReceipt(ctx, store, reader, corpus, job, checkpoint, extractionRef, resolutionRef,
		maximumEdges, maximumCandidates, func(extraction *pb.ExtractionBatch, resolution *pb.ResolutionBatch, candidates *pb.RegistryCandidateBatch, input domain.SemanticRegistryInputs) error {
			if !domain.ResolutionViewCanReaffirm(resolution, targetRevision) {
				return domain.ErrResolutionReplan
			}
			if err := validateGraphExtractionDependencies(extraction); err != nil {
				return err
			}
			if len(extraction.Mentions) == 0 {
				// The production empty path observes no candidate scopes. Do not
				// silently accept unknown negative dependencies without a reader.
				if len(resolution.Dependencies.LookupScopeRevisions) != 0 || len(resolution.Dependencies.Dependencies) != 1 ||
					resolution.Dependencies.Dependencies[0].DependencyId != extractionRef.ArtifactId ||
					!proto.Equal(resolution.Dependencies.Dependencies[0].Fingerprint, extractionRef.ContentHash) {
					return errors.New("empty RESOLVE has unsupported dependencies")
				}
				return nil
			}
			// Candidate validation already accounts for every mention/scope. Share
			// the reader's aggregate alias budget across unique scopes instead of
			// multiplying a reference limit by itself. The original production
			// lookup has this same product cap; overflow remains an error, never
			// truncation or a successful comparison of a partial candidate set.
			aliasesPerScope, err := domain.GraphCandidateAliasBudget(candidates, maximumEdges)
			if err != nil {
				return err
			}
			return store.VerifyRegistryCandidateView(ctx, corpus, input, targetRevision,
				maximumEdges, aliasesPerScope, maximumEdges, maximumCandidates)
		})
}

// Current production Rust EXTRACT consumes only its immutable DocumentBatch and
// pinned model/prompt/ontology. BIND's registry observations belong to the source
// document and are checked separately. This guard runs on ORIGINAL extraction,
// before the explicit snapshot-envelope policy adds its own provenance dependencies.
// Future extractors using registry/other artifacts need corresponding readers.
func validateGraphExtractionDependencies(extraction *pb.ExtractionBatch) error {
	return domain.ValidateGraphExtractionDependencies(extraction)
}
