// Describes reuse of an unchanged, published dense/BM25 generation by a new
// graph snapshot. Physical ownership and source envelopes keep their original
// snapshot; query visibility uses Target. Storage must prove Parent membership,
// backend readback and immutable routing before admitting this mapping. No wire
// schema or model computation is introduced. Benchmark readback/page/receipt p95
// and RSS under configs/benchmark-targets.yaml; targets remain unmeasured.
package domain

import (
	"errors"

	pb "regulagraph.local/server/gen/regulagraph/v1"
)

type IndexReuseBinding struct {
	PublicationID          string
	Fence                  uint64
	Target, Parent, Source *pb.SnapshotRef
	Index                  IndexCatalogBinding
	Expected               *pb.BackendGeneration
}

func ValidateIndexReuseBinding(b IndexReuseBinding) error {
	if err := ValidateIndexCatalogBinding(b.Index); err != nil {
		return err
	}
	for _, s := range []*pb.SnapshotRef{b.Target, b.Parent, b.Source} {
		if err := ValidateWire(s, DefaultWireLimits); err != nil {
			return err
		}
		if s.CorpusId != b.Index.Generation.Meta.CorpusId || s.RepresentationGeneration != b.Index.Generation.Meta.RecordId || s.Sequence > 1<<53-1 {
			return errors.New("reused index corpus/generation/sequence mismatch")
		}
	}
	if err := ValidateWire(&pb.RecordMeta{SchemaVersion: 1, CorpusId: b.Target.CorpusId, RecordId: b.PublicationID}, DefaultWireLimits); err != nil {
		return err
	}
	if err := ValidateWire(b.Expected, DefaultWireLimits); err != nil {
		return err
	}
	if b.Fence == 0 || b.Fence > 1<<63-1 || b.Target.Sequence <= b.Parent.Sequence || b.Source.Sequence > b.Parent.Sequence || b.Target.SnapshotId == b.Parent.SnapshotId || b.PublicationID == b.Index.PublicationID ||
		b.Expected.Backend != pb.BackendKind_BACKEND_KIND_QDRANT || b.Expected.Generation != b.Index.Generation.Meta.RecordId || b.Expected.ExpectedCounts.Expected == 0 || b.Expected.ExpectedCounts.Accepted != b.Expected.ExpectedCounts.Expected || b.Expected.ExpectedCounts.Rejected != 0 {
		return errors.New("invalid unchanged index reuse boundary")
	}
	return nil
}
