// Verifies ordered multi-date plans, strict admission and independent scope
// ownership. Calendar fixtures establish contracts, not retrieval accuracy.
package query

import (
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func TestPlanComparisonScopes(t *testing.T) {
	scope := &pb.TemporalScope{Mode: pb.TemporalMode_TEMPORAL_MODE_COMPARE, UnresolvedPolicy: pb.UnresolvedPolicy_UNRESOLVED_POLICY_REPORT, CompareDates: []*pb.CalendarDate{{Year: 2026, Month: 1, Day: 1}, {Year: 2024, Month: 2, Day: 29}}}
	before := proto.Clone(scope)
	plans, err := PlanComparisonScopes(scope)
	if err != nil || len(plans) != 2 {
		t.Fatal(plans, err)
	}
	for i, plan := range plans {
		if plan.Mode != pb.TemporalMode_TEMPORAL_MODE_AS_OF || len(plan.CompareDates) != 0 || !proto.Equal(plan.EffectiveAt, scope.CompareDates[i]) || plan.UnresolvedPolicy != scope.UnresolvedPolicy {
			t.Fatal("lost input order/policy", plan)
		}
	}
	plans[0].EffectiveAt.Year++
	if !proto.Equal(scope, before) || plans[1].EffectiveAt.Year != 2024 {
		t.Fatal("date plans alias caller/sibling")
	}
	for _, mode := range []string{"nil", "duplicate", "missing", "single date", "invalid calendar", "ambiguous", "unsupported", "too many"} {
		t.Run(mode, func(t *testing.T) {
			input := proto.Clone(scope).(*pb.TemporalScope)
			switch mode {
			case "nil":
				input = nil
			case "duplicate":
				input.CompareDates[1] = proto.Clone(input.CompareDates[0]).(*pb.CalendarDate)
			case "missing":
				input.CompareDates[1] = nil
			case "single date":
				input.CompareDates = input.CompareDates[:1]
			case "invalid calendar":
				input.CompareDates[1].Year = 2025
			case "ambiguous":
				input.EffectiveAt = input.CompareDates[0]
			case "unsupported":
				input.Mode = pb.TemporalMode_TEMPORAL_MODE_CURRENT
			case "too many":
				for len(input.CompareDates) <= MaximumComparisonDates {
					input.CompareDates = append(input.CompareDates, &pb.CalendarDate{Year: int32(2000 + len(input.CompareDates)), Month: 1, Day: 1})
				}
			}
			if _, err := PlanComparisonScopes(input); err == nil {
				t.Fatal("invalid comparison accepted")
			}
		})
	}
}
