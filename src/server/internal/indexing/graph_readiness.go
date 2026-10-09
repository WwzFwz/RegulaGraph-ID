// Acknowledges the exact sealed generation through source-admitted PostgreSQL
// authority. Write, readback and receipt are retryable; receipt failure cannot
// return success or activate a snapshot. The final publication coordinator must
// also acknowledge the index backend and use the guarded active-pointer CAS.
// No per-delta RPC/model work is repeated. Measure write/receipt recovery latency
// and contention against configs/benchmark-targets.yaml; fixtures are not SLA proof.
package indexing

import (
	"context"
	"errors"

	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/workflows"
)

type GraphReadinessAuthority interface {
	GraphGenerationAuthority
	RecordGraphReadiness(context.Context, domain.SnapshotPin, domain.CompletedGraphInventory, domain.GraphCatalogBinding, *pb.BackendReceipt) error
}

func AcknowledgePreparedGraph(ctx context.Context, authority GraphReadinessAuthority, backend GraphGenerationBackend,
	prepared *workflows.PreparedGraphOutputs, pin domain.SnapshotPin) (*pb.BackendReceipt, error) {
	if ctx == nil || authority == nil {
		return nil, errors.New("graph readiness authority required")
	}
	// Describe again after Write is deliberately avoided: a caller-provided
	// backend cannot change binding between proof verification and receipt.
	b, err := DescribePreparedGraph(prepared, backend)
	if err != nil {
		return nil, err
	}
	proof, err := WritePreparedGraph(ctx, authority, backend, prepared, pin)
	if err != nil {
		return nil, err
	}
	if proof.Generation != b.Binding.Generation || proof.BindingHash != b.BindingHash || proof.OperationsHash != b.OperationsHash || proof.Records != b.Records || proof.Edges != b.Edges || proof.Operations != b.Operations {
		return nil, domain.ErrPersistentIntegrity
	}
	want := b.ExpectedBackend()
	receipt := &pb.BackendReceipt{PublicationId: b.Binding.PublicationID, Backend: want.Backend, Fence: b.Binding.Fence, Generation: want.Generation, OperationsChecksum: want.OperationsChecksum, Counts: want.ExpectedCounts, DurableAck: true, SearchReady: true}
	if err = authority.RecordGraphReadiness(ctx, pin, prepared.Completed(), b, receipt); err != nil {
		return nil, err
	}
	return receipt, nil
}
