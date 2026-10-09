// Checks the discovery reader port before traversal consumes any record. The
// adapter authenticates physical storage; this boundary rejects wrong snapshots,
// disconnected/unsupported assertions, conflicting repeats and dishonest budgets.
// It does not assert legal applicability or source-text entailment. Returned
// records are cloned so reader reuse cannot mutate previously accumulated paths.
package graph

import (
	"errors"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func admitNeighborhood(out *TraversalResult, batch *domain.GraphNeighborhood, seeds []string, limits domain.GraphReadLimits) error {
	bad := errors.New("invalid snapshot-bound graph neighborhood")
	if batch == nil || !proto.Equal(batch.Snapshot, out.Snapshot) || len(batch.Assertions) > limits.Assertions || len(batch.Supports) > limits.Supports || len(batch.Entities) > len(seeds)+2*limits.Assertions {
		return bad
	}
	selected := map[string]bool{}
	for _, id := range seeds {
		selected[id] = true
	}
	entities := map[string]bool{}
	assertions := map[string]bool{}
	supports := map[string]bool{}
	var bytes uint64
	check := func(value proto.Message, meta *pb.RecordMeta) bool {
		if domain.ValidateWire(value, domain.DefaultWireLimits) != nil || meta == nil || meta.SchemaVersion != 1 || meta.CorpusId != out.Snapshot.CorpusId || meta.Visibility == nil || meta.Visibility.FromSeq != out.Snapshot.Sequence || meta.Visibility.ToSeq != nil {
			return false
		}
		bytes += uint64(proto.Size(value))
		return bytes <= limits.Bytes
	}
	for _, e := range batch.Entities {
		if e == nil || !check(e, e.Meta) || entities[e.Meta.RecordId] {
			return bad
		}
		entities[e.Meta.RecordId] = true
		if old := out.Entities[e.Meta.RecordId]; old != nil && !proto.Equal(old, e) {
			return bad
		}
	}
	for _, id := range seeds {
		if !entities[id] {
			return bad
		}
	}
	for _, a := range batch.Assertions {
		if a == nil || !check(a, a.Meta) || assertions[a.Meta.RecordId] || !entities[a.SubjectId] || !entities[a.ObjectId] || (!selected[a.SubjectId] && !selected[a.ObjectId]) {
			return bad
		}
		assertions[a.Meta.RecordId] = true
		if old := out.Assertions[a.Meta.RecordId]; old != nil && !proto.Equal(old, a) {
			return bad
		}
	}
	covered := map[string]bool{}
	for _, s := range batch.Supports {
		if s == nil || !check(s, s.Meta) || supports[s.Meta.RecordId] || !assertions[s.AssertionId] {
			return bad
		}
		supports[s.Meta.RecordId] = true
		covered[s.AssertionId] = true
		if old := out.Supports[s.Meta.RecordId]; old != nil && !proto.Equal(old, s) {
			return bad
		}
	}
	if len(covered) != len(assertions) || bytes > batch.ReadBytes || batch.ReadBytes > limits.Bytes {
		return bad
	}
	for _, e := range batch.Entities {
		out.Entities[e.Meta.RecordId] = proto.Clone(e).(*pb.CanonicalEntity)
	}
	for _, a := range batch.Assertions {
		out.Assertions[a.Meta.RecordId] = proto.Clone(a).(*pb.RelationAssertion)
	}
	for _, s := range batch.Supports {
		out.Supports[s.Meta.RecordId] = proto.Clone(s).(*pb.SupportRecord)
	}
	out.ReadBytes += batch.ReadBytes
	return nil
}
