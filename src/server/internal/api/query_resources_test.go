// Tests snapshot-aware cache ownership under rollover, concurrent releases and
// shutdown. A close counter substitutes only the external driver; retention and
// reference counting are production code. Tests do not prove backend readiness
// or required latency/throughput; native API tests cover actual graph clients.
package api

import (
	"sync"
	"sync/atomic"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func cacheIndex(snapshot string) *domain.PinnedIndex {
	return &domain.PinnedIndex{Snapshot: &pb.SnapshotRef{SnapshotId: snapshot}, Binding: domain.IndexCatalogBinding{PublicationID: "publication:a", Fence: 1, Endpoint: "endpoint", Collection: "collection", Generation: &pb.IndexGeneration{Meta: &pb.RecordMeta{RecordId: "generation:a"}}}}
}
func TestQueryResourceRolloverRetainsActiveBorrowers(t *testing.T) {
	var c queryResourceCache
	oldIndex := cacheIndex("snapshot:a")
	newIndex := cacheIndex("snapshot:b")
	var oldClosed, newClosed atomic.Int32
	old := &queryResource{binding: oldIndex.Binding, snapshot: proto.Clone(oldIndex.Snapshot).(*pb.SnapshotRef), close: func() { oldClosed.Add(1) }}
	done, ok := c.install(old)
	if !ok {
		t.Fatal("install")
	}
	if got, _ := c.acquire(newIndex); got != nil {
		t.Fatal("reused graph resources across snapshots sharing vector generation")
	}
	_, otherDone := c.acquire(oldIndex)
	if otherDone == nil {
		t.Fatal("same snapshot missed cache")
	}
	next := &queryResource{binding: newIndex.Binding, snapshot: newIndex.Snapshot, close: func() { newClosed.Add(1) }}
	newDone, ok := c.install(next)
	if !ok {
		t.Fatal("rollover")
	}
	if oldClosed.Load() != 0 {
		t.Fatal("closed driver while borrowed")
	}
	done()
	done()
	if oldClosed.Load() != 0 {
		t.Fatal("ignored second borrower")
	}
	otherDone()
	if oldClosed.Load() != 1 {
		t.Fatal("old driver not closed exactly once")
	}
	c.close()
	if newClosed.Load() != 0 {
		t.Fatal("shutdown closed borrowed generation")
	}
	newDone()
	c.close()
	if newClosed.Load() != 1 {
		t.Fatal("shutdown leaked/doubled driver")
	}
	if got, _ := c.acquire(newIndex); got != nil {
		t.Fatal("closed cache admitted query")
	}
}
func TestQueryResourceConcurrentReleaseAndClosedInstall(t *testing.T) {
	var c queryResourceCache
	idx := cacheIndex("snapshot:a")
	var closed atomic.Int32
	r := &queryResource{binding: idx.Binding, close: func() { closed.Add(1) }}
	first, _ := c.install(r)
	releases := []func(){first}
	for range 64 {
		got, done := c.acquire(idx)
		if got == nil {
			t.Fatal("cache miss")
		}
		releases = append(releases, done)
	}
	c.close()
	var wg sync.WaitGroup
	for _, release := range releases {
		wg.Add(1)
		go func(done func()) { defer wg.Done(); done(); done() }(release)
	}
	wg.Wait()
	if closed.Load() != 1 {
		t.Fatal("concurrent close count", closed.Load())
	}
	if done, ok := c.install(&queryResource{close: func() { closed.Add(1) }}); ok || done != nil || closed.Load() != 2 {
		t.Fatal("closed install leaked resource")
	}
}
