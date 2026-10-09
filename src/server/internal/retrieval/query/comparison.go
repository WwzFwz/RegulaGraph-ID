// Expands explicit COMPARE dates into bounded, ordered AS_OF scopes without
// inferring dates or sorting away the caller's requested comparison direction.
// Reject duplicate/invalid/over-budget dates; never prune them to fit a limit.
// Scope cloning preserves unresolved policy and the knowledge snapshot. Measure
// multi-date latency/coverage using configs/benchmark-targets.yaml; this planner
// establishes input invariants, not legal accuracy or benchmark acceptance.
package query

import (
	"errors"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

// MaximumComparisonDates bounds one request, not a retrieval-quality target.
const MaximumComparisonDates = 8

func PlanComparisonScopes(scope *pb.TemporalScope) ([]*pb.TemporalScope, error) {
	if scope == nil || scope.Mode != pb.TemporalMode_TEMPORAL_MODE_COMPARE || scope.EffectiveAt != nil || len(scope.CompareDates) < 2 || len(scope.CompareDates) > MaximumComparisonDates {
		return nil, errors.New("COMPARE requires 2..8 explicit distinct dates and no single effective date")
	}
	if err := domain.ValidateWire(scope, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	seen := map[[3]int32]bool{}
	result := make([]*pb.TemporalScope, 0, len(scope.CompareDates))
	for _, date := range scope.CompareDates {
		key := [3]int32{date.Year, int32(date.Month), int32(date.Day)}
		if seen[key] {
			return nil, errors.New("COMPARE dates must be distinct")
		}
		seen[key] = true
		child := proto.Clone(scope).(*pb.TemporalScope)
		child.Mode, child.CompareDates = pb.TemporalMode_TEMPORAL_MODE_AS_OF, nil
		child.EffectiveAt = proto.Clone(date).(*pb.CalendarDate)
		result = append(result, child)
	}
	return result, nil
}
