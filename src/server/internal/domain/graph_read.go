// Carries a published graph route admitted under a live snapshot lease and an
// explicit authorization scope. Catalog bytes alone do not grant read access;
// PostgreSQL rechecks the pin, receipts and retained registry binding. Consumers
// preserve Snapshot/RegistryRevision while reading evidence across graph batches.
// Measure admission/pin p95 and cache behavior under benchmark-targets.yaml.
package domain

import pb "regulagraph.local/server/gen/regulagraph/v1"

type PinnedGraph struct {
	Pin       SnapshotPin
	Snapshot  *pb.SnapshotRef
	Catalog   GraphCatalogBinding
	AuthScope string
}
