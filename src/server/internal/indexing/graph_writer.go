// Binds a fully source-validated graph write set to a physical Neo4j generation,
// then reserves immutable catalog+intent before any backend mutation. Offline
// projection rejects conflicting shared records before I/O. Exact retries reuse
// the same namespace and output bytes; failures leave the intent for recovery.
// Returned backend proof is not a durable receipt or active snapshot. Publisher
// must atomically recheck authority when recording readiness/activation. Measure
// preflight/write/seal/queue p95 and RSS under required benchmark-targets.yaml.
package indexing

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/neo4j"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/workflows"
)

type GraphGenerationBackend interface {
	Endpoint() string
	Database() string
	Binding() neo4j.Binding
	Describe([]*pb.GraphDelta) (neo4j.ReadyProof, error)
	EnsureSchema(context.Context) error
	ApplyGraphDelta(context.Context, *pb.GraphDelta) error
	VerifyAndSeal(context.Context, []*pb.GraphDelta) (neo4j.ReadyProof, error)
}

type GraphGenerationAuthority interface {
	workflows.CompletedGraphAuthority
	ReserveGraphGeneration(context.Context, domain.SnapshotPin, domain.CompletedGraphInventory, domain.GraphCatalogBinding) error
}

// DescribePreparedGraph is pure backend projection; expected counts/hash may be
// staged in PublicationManifest only after the other backend requirements exist.
func DescribePreparedGraph(prepared *workflows.PreparedGraphOutputs, backend GraphGenerationBackend) (domain.GraphCatalogBinding, error) {
	var out domain.GraphCatalogBinding
	if prepared == nil || backend == nil {
		return out, errors.New("prepared graph and backend required")
	}
	completed := prepared.Completed()
	if err := domain.ValidateCompletedGraphInventory(completed); err != nil {
		return out, err
	}
	proof, err := backend.Describe(prepared.Deltas())
	if err != nil {
		return out, err
	}
	b := backend.Binding()
	p := completed.Inventory.Assignments[0].Plan
	if b.CorpusID != p.Meta.CorpusId || b.PublicationID != p.PublicationId || b.Fence != p.PublicationFence || b.Sequence != p.TargetSequence || b.RegistryRevision != p.RegistryRevision || !proto.Equal(b.BaseSnapshot, p.Context.SnapshotRef) || proof.Generation != b.Generation || proof.Operations != uint64(len(completed.Outputs)) {
		return out, errors.New("graph backend differs from admitted plans")
	}
	raw, err := json.Marshal(completed.Inventory)
	if err != nil {
		return out, err
	}
	out = domain.GraphCatalogBinding{Binding: b, Endpoint: backend.Endpoint(), Database: backend.Database(), BindingHash: proof.BindingHash, OperationsHash: proof.OperationsHash, InventoryHash: fmt.Sprintf("%x", sha256.Sum256(raw)), Records: proof.Records, Edges: proof.Edges, Operations: proof.Operations, Outputs: completed.Outputs}
	if err = domain.ValidateGraphCatalogBinding(out); err != nil {
		return domain.GraphCatalogBinding{}, err
	}
	return out, nil
}

func WritePreparedGraph(ctx context.Context, authority GraphGenerationAuthority, backend GraphGenerationBackend,
	prepared *workflows.PreparedGraphOutputs, pin domain.SnapshotPin) (neo4j.ReadyProof, error) {
	var empty neo4j.ReadyProof
	if ctx == nil || authority == nil {
		return empty, errors.New("graph authority and context required")
	}
	bounded, cancel := context.WithDeadline(ctx, pin.ExpiresAt)
	defer cancel()
	b, err := DescribePreparedGraph(prepared, backend)
	if err != nil {
		return empty, err
	}
	if err = authority.ReserveGraphGeneration(bounded, pin, prepared.Completed(), b); err != nil {
		return empty, err
	}
	if err = backend.EnsureSchema(bounded); err != nil {
		return empty, err
	}
	deltas := prepared.Deltas()
	for _, delta := range deltas {
		if err = backend.ApplyGraphDelta(bounded, delta); err != nil {
			return empty, err
		}
	}
	proof, err := backend.VerifyAndSeal(bounded, deltas)
	if err != nil {
		return empty, err
	}
	if proof.Generation != b.Binding.Generation || proof.BindingHash != b.BindingHash || proof.OperationsHash != b.OperationsHash || proof.Records != b.Records || proof.Edges != b.Edges || proof.Operations != b.Operations {
		return empty, domain.ErrPersistentIntegrity
	}
	if err = prepared.Revalidate(bounded, authority, pin); err != nil {
		return empty, err
	}
	return proof, nil
}
