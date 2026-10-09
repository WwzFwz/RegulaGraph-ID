// Injects corrupted boundary responses over a real PostgreSQL RESOLVE fixture.
// The graph coordinator must reconstruct the exact committed decision and reject
// a changed intent, changed ledger response or missing operation. No model quality
// is inferred; the caller fixture owns registered FileStore bytes and checkpoints.
package postgres

import (
	"context"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/workflows"
	"testing"
)

type graphReceiptDriftStore struct {
	*Repository
	fault string
}

func (s graphReceiptDriftStore) VerifyRegistryCandidateView(ctx context.Context, corpus string, input domain.SemanticRegistryInputs,
	revision uint64, scopes, aliases, refs, candidates int) error {
	if s.fault == "candidate view" {
		return domain.ErrResolutionReplan
	}
	return s.Repository.VerifyRegistryCandidateView(ctx, corpus, input, revision, scopes, aliases, refs, candidates)
}

func (s graphReceiptDriftStore) LoadSemanticResolutionIntent(ctx context.Context, corpus, job string) (domain.SemanticResolutionIntent, error) {
	intent, err := s.Repository.LoadSemanticResolutionIntent(ctx, corpus, job)
	if err == nil && s.fault == "intent" {
		intent.Preview.ModelManifest.Version += "-different"
	}
	return intent, err
}
func (s graphReceiptDriftStore) ReadCommittedSemanticResolution(ctx context.Context, input domain.SemanticRegistryInputs,
	request *pb.RegistryResolveRequest, approvals []domain.ReviewedLink, refs, candidates int) (*pb.RegistryResolveResponse, error) {
	if s.fault == "missing operation" {
		return nil, ErrNotFound
	}
	response, err := s.Repository.ReadCommittedSemanticResolution(ctx, input, request, approvals, refs, candidates)
	if err == nil && s.fault == "decision" {
		response = proto.Clone(response).(*pb.RegistryResolveResponse)
		response.Assignments[0].GetDecision().Reason += " changed"
	}
	return response, err
}
func checkGraphReceiptRejections(t *testing.T, ctx context.Context, repo *Repository, reader workflows.DocumentArtifactReader,
	corpus, job, checkpoint string, extract, resolve *pb.ArtifactRef) {
	t.Helper()
	for _, fault := range []string{"intent", "decision", "missing operation"} {
		t.Run("graph receipt rejects "+fault, func(t *testing.T) {
			if _, err := workflows.ReadGraphResolutionReceipt(ctx, graphReceiptDriftStore{repo, fault}, reader, corpus, job, checkpoint, extract, resolve, 64, 8); err == nil {
				t.Fatal("uncommitted graph decision admitted")
			}
		})
	}
}
