// Tests snapshot envelope construction using the actual CHUNK fixture with its
// snapshot removed, matching daemon output. Exact source body equality after
// restoring only envelope fields proves no legal/content records were rewritten.
// Storage authority for binding is a separate required integration boundary.
package indexing

import (
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
	"testing"
)

func TestInitialSnapshotSourcePreservesLegalRecords(t *testing.T) {
	f := newPlanningFixture(t)
	original := proto.Clone(f.source).(*pb.DocumentBatch)
	original.Context.SnapshotRef = nil
	ref := f.put(t, original, "artifact:unpublished-source")
	bound, err := domain.BindInitialSnapshotSource("job:source", original, ref, f.config.Snapshot, f.config.AuthScope)
	if err != nil {
		t.Fatal(err)
	}
	if original.Context.SnapshotRef != nil {
		t.Fatal("original mutated")
	}
	if bound.Meta.RecordId == original.Meta.RecordId || !proto.Equal(bound.Context.SnapshotRef, f.config.Snapshot) {
		t.Fatal("bound identity/snapshot missing")
	}
	if len(bound.DependencyManifest.Dependencies) != len(original.DependencyManifest.Dependencies)+1 || bound.DependencyManifest.Dependencies[0].DependencyId != ref.ArtifactId || !proto.Equal(bound.DependencyManifest.Dependencies[0].Fingerprint, ref.ContentHash) {
		t.Fatal("original dependency lost")
	}
	copy := proto.Clone(bound).(*pb.DocumentBatch)
	copy.Meta = proto.Clone(original.Meta).(*pb.RecordMeta)
	copy.Context = proto.Clone(original.Context).(*pb.RequestContext)
	copy.DependencyManifest = proto.Clone(original.DependencyManifest).(*pb.DependencyManifest)
	if !proto.Equal(copy, original) {
		t.Fatal("legal/text/chunk content changed")
	}
	replay, err := domain.BindInitialSnapshotSource("job:source", original, ref, f.config.Snapshot, f.config.AuthScope)
	if err != nil || !proto.Equal(bound, replay) {
		t.Fatal("binding replay drift", err)
	}
	if _, err = domain.BindInitialSnapshotSource("job:source", bound, ref, f.config.Snapshot, f.config.AuthScope); err == nil {
		t.Fatal("previous snapshot silently rebound")
	}
	if _, err = domain.BindInitialSnapshotSource("job:source", original, ref, f.config.Snapshot, "scope:foreign"); err == nil {
		t.Fatal("foreign scope admitted")
	}
	changed := proto.Clone(f.config.Snapshot).(*pb.SnapshotRef)
	changed.SnapshotId += "-different"
	next, err := domain.BindInitialSnapshotSource("job:source", original, ref, changed, f.config.AuthScope)
	if err != nil || proto.Equal(next, bound) {
		t.Fatal("different target did not change binding", err)
	}
}
