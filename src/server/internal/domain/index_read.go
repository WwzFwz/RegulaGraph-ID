// Defines the local read boundary between published index storage and retrieval.
// A pin is a server-owned lease locator, not user authorization; the PostgreSQL
// reader rechecks its owner, scope and expiry before returning data. These types
// reuse C01 records and introduce no wire schema. Measure batch bytes, read p95
// and lease overhead under configs/benchmark-targets.yaml (REQUIRED_UNMEASURED).
package domain

import pb "regulagraph.local/server/gen/regulagraph/v1"

type PinnedIndex struct {
	Pin      SnapshotPin
	Snapshot *pb.SnapshotRef
	Binding  IndexCatalogBinding
	// SourceSnapshot is the immutable build/source envelope snapshot. Snapshot
	// remains the reader's current visibility snapshot. Nil means the same one.
	SourceSnapshot *pb.SnapshotRef
}

func (p *PinnedIndex) EvidenceSnapshot() *pb.SnapshotRef {
	if p == nil {
		return nil
	}
	if p.SourceSnapshot != nil {
		return p.SourceSnapshot
	}
	return p.Snapshot
}

type IndexCatalogRecord struct {
	PointID string
	Record  *pb.IndexRecord
}
