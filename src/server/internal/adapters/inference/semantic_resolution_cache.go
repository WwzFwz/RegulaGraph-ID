// Keeps bounded process-local resolution results and coalesces concurrent operation retries.
// Fingerprints include every semantic input; terminal items survive partial retry while transient
// errors are retried. This cache is an optimization, not durable storage or a registry commit.
// Measure warm/cold latency, eviction, and bytes; required targets remain unmeasured.
package inference

import (
	"context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"sync"
)

type resolutionEntry struct {
	fingerprint [32]byte
	items       map[string]resolveOutcome
	response    *pb.SemanticResolveResponse
	running     chan struct{}
	size        int64
}
type resolutionCache struct {
	mu                  sync.Mutex
	entries             map[string]*resolutionEntry
	order               []string
	maximum             int
	maximumBytes, bytes int64
}

func newResolutionCache(maximum int, bytes int64) *resolutionCache {
	return &resolutionCache{entries: map[string]*resolutionEntry{}, maximum: maximum, maximumBytes: bytes}
}
func (c *resolutionCache) execute(ctx context.Context, key string, fingerprint [32]byte,
	run func(map[string]resolveOutcome) (*pb.SemanticResolveResponse, map[string]resolveOutcome, error),
) (*pb.SemanticResolveResponse, error) {
	for {
		if ctx.Err() != nil {
			return nil, status.FromContextError(ctx.Err()).Err()
		}
		c.mu.Lock()
		entry := c.entries[key]
		if entry == nil {
			entry = &resolutionEntry{fingerprint: fingerprint, items: map[string]resolveOutcome{}}
			c.entries[key] = entry
			c.order = append(c.order, key)
		}
		if entry.fingerprint != fingerprint {
			c.mu.Unlock()
			return nil, status.Error(codes.FailedPrecondition, "operation key was reused for different resolution input")
		}
		if entry.response != nil {
			result := proto.Clone(entry.response).(*pb.SemanticResolveResponse)
			c.mu.Unlock()
			return result, nil
		}
		if entry.running != nil {
			done := entry.running
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, status.FromContextError(ctx.Err()).Err()
			case <-done:
				continue
			}
		}
		entry.running = make(chan struct{})
		saved := cloneResolveItems(entry.items)
		c.mu.Unlock()
		response, retained, err := run(saved)
		c.mu.Lock()
		c.bytes -= entry.size
		entry.items = cloneResolveItems(retained)
		entry.size = 0
		for _, outcome := range entry.items {
			entry.size += int64(proto.Size(outcome.result)) + 64
		}
		cacheable := err == nil && response != nil
		if cacheable {
			for _, result := range response.Results {
				if failure := result.GetError(); failure != nil && failure.Retryable {
					cacheable = false
				}
			}
		}
		if cacheable {
			entry.response = proto.Clone(response).(*pb.SemanticResolveResponse)
			entry.items = nil
			entry.size = int64(proto.Size(entry.response))
		}
		c.bytes += entry.size
		close(entry.running)
		entry.running = nil
		c.evictLocked()
		c.mu.Unlock()
		return response, err
	}
}
func cloneResolveItems(items map[string]resolveOutcome) map[string]resolveOutcome {
	copy := make(map[string]resolveOutcome, len(items))
	for id, item := range items {
		item.result = proto.Clone(item.result).(*pb.ResolveItemResult)
		copy[id] = item
	}
	return copy
}
func (c *resolutionCache) evictLocked() {
	remaining := len(c.order)
	for remaining > 0 && (len(c.entries) > c.maximum || c.bytes > c.maximumBytes) {
		key := c.order[0]
		c.order = c.order[1:]
		entry := c.entries[key]
		if entry.running != nil {
			c.order = append(c.order, key)
			remaining--
			continue
		}
		c.bytes -= entry.size
		delete(c.entries, key)
		remaining = len(c.order)
	}
}
