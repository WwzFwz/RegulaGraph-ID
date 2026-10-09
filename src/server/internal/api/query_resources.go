// Owns prepared query resources across snapshot rollover. A retired generation
// stays alive until its last request/readiness borrower releases it; unused
// generations close immediately. Preparation is serialized by the runtime, while
// release is concurrent and idempotent. Measure cache churn, pool count, wait time
// and p95/p99 under benchmark-targets.yaml; this lifecycle does not prove quality.
package api

import (
	"sync"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/inference"
	"regulagraph.local/server/internal/adapters/neo4j"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/workflows"
)

type queryResource struct {
	binding  domain.IndexCatalogBinding
	snapshot *pb.SnapshotRef
	prepared *workflows.PreparedQuery
	native   *inference.NativeClient
	graph    *neo4j.Store
	close    func()
	users    int
	retired  bool
}
type queryResourceCache struct {
	mu      sync.Mutex
	current *queryResource
	closed  bool
}

func (c *queryResourceCache) acquire(index *domain.PinnedIndex) (*queryResource, func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r := c.current
	if c.closed || r == nil || !sameEvidenceBinding(r.binding, index.Binding) || (r.snapshot != nil && !proto.Equal(r.snapshot, index.Snapshot)) {
		return nil, nil
	}
	r.users++
	return r, c.release(r)
}
func (c *queryResourceCache) release(r *queryResource) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			r.users--
			close := r.retired && r.users == 0
			cleanup := r.close
			if close {
				r.close = nil
			}
			c.mu.Unlock()
			if close && cleanup != nil {
				cleanup()
			}
		})
	}
}
func (c *queryResourceCache) install(r *queryResource) (func(), bool) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		if r.close != nil {
			r.close()
		}
		return nil, false
	}
	old := c.current
	c.current = r
	r.users = 1
	var cleanup func()
	if old != nil {
		old.retired = true
		if old.users == 0 {
			cleanup = old.close
			old.close = nil
		}
	}
	release := c.release(r)
	c.mu.Unlock()
	if cleanup != nil {
		cleanup()
	}
	return release, true
}
func (c *queryResourceCache) close() {
	c.mu.Lock()
	c.closed = true
	r := c.current
	c.current = nil
	var cleanup func()
	if r != nil {
		r.retired = true
		if r.users == 0 {
			cleanup = r.close
			r.close = nil
		}
	}
	c.mu.Unlock()
	if cleanup != nil {
		cleanup()
	}
}
