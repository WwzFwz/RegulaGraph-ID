// Verifies legal-calendar planning independently of host time zone and storage.
// Covers midnight/leap dates, clock ownership, ambiguous input, replayed audits,
// and proto ownership; these fixtures do not prove legal applicability of sources.
package query

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func TestCurrentTemporalResolution(t *testing.T) {
	for _, tt := range []struct {
		zone, instant string
		year          int32
		month, day    uint32
	}{
		{"Asia/Jakarta", "2025-12-31T16:59:59Z", 2025, 12, 31},
		{"Asia/Jakarta", "2025-12-31T17:00:00Z", 2026, 1, 1},
		{"Asia/Jayapura", "2024-02-28T15:00:00Z", 2024, 2, 29},
		{"UTC", "2025-12-31T17:00:00Z", 2025, 12, 31},
	} {
		t.Run(tt.zone+tt.instant, func(t *testing.T) {
			zone, err := LoadQueryTimeZone(tt.zone)
			if err != nil {
				t.Fatal(err)
			}
			instant, _ := time.Parse(time.RFC3339, tt.instant)
			scope := &pb.TemporalScope{Mode: pb.TemporalMode_TEMPORAL_MODE_CURRENT, UnresolvedPolicy: pb.UnresolvedPolicy_UNRESOLVED_POLICY_REPORT}
			before := proto.Clone(scope)
			calls := 0
			resolved, report, err := ResolveTemporalScope(scope, zone, func() time.Time { calls++; return instant })
			want := &pb.CalendarDate{Year: tt.year, Month: tt.month, Day: tt.day}
			if err != nil || calls != 1 || resolved.Mode != pb.TemporalMode_TEMPORAL_MODE_AS_OF || !proto.Equal(resolved.EffectiveAt, want) || !proto.Equal(scope, before) {
				t.Fatal(resolved, report, err, calls)
			}
			if err := ValidateTemporalResolution(scope, report); err != nil {
				t.Fatal(err)
			}
			resolved.EffectiveAt.Year++
			if !proto.Equal(report.EffectiveDate, want) {
				t.Fatal("audit aliases execution scope")
			}
			report.EffectiveDate.Year++
			if ValidateTemporalResolution(scope, report) == nil {
				t.Fatal("accepted drifted date")
			}
		})
	}
}

func TestTemporalScopeInvalidAndExplicit(t *testing.T) {
	zone, _ := LoadQueryTimeZone("Asia/Jakarta")
	date := &pb.CalendarDate{Year: 2026, Month: 1, Day: 1}
	asOf := &pb.TemporalScope{Mode: pb.TemporalMode_TEMPORAL_MODE_AS_OF, EffectiveAt: date, UnresolvedPolicy: pb.UnresolvedPolicy_UNRESOLVED_POLICY_REPORT}
	resolved, report, err := ResolveTemporalScope(asOf, nil, func() time.Time { t.Fatal("AS_OF read clock"); return time.Time{} })
	if err != nil || !proto.Equal(resolved, asOf) || ValidateTemporalResolution(asOf, report) != nil {
		t.Fatal(err)
	}
	resolved.EffectiveAt.Year++
	if date.Year != 2026 {
		t.Fatal("mutated input")
	}
	for _, name := range []string{"Local", "not/a-zone", " Asia/Jakarta"} {
		if _, err := LoadQueryTimeZone(name); err == nil {
			t.Fatal("accepted zone", name)
		}
	}
	for _, tt := range []struct {
		name  string
		scope *pb.TemporalScope
		zone  *time.Location
		now   time.Time
	}{
		{"missing", nil, zone, time.Now()},
		{"no zone", &pb.TemporalScope{Mode: pb.TemporalMode_TEMPORAL_MODE_CURRENT, UnresolvedPolicy: pb.UnresolvedPolicy_UNRESOLVED_POLICY_REPORT}, nil, time.Now()},
		{"ambiguous current", &pb.TemporalScope{Mode: pb.TemporalMode_TEMPORAL_MODE_CURRENT, EffectiveAt: date, UnresolvedPolicy: pb.UnresolvedPolicy_UNRESOLVED_POLICY_REPORT}, zone, time.Now()},
		{"zero clock", &pb.TemporalScope{Mode: pb.TemporalMode_TEMPORAL_MODE_CURRENT, UnresolvedPolicy: pb.UnresolvedPolicy_UNRESOLVED_POLICY_REPORT}, zone, time.Time{}},
		{"compare", &pb.TemporalScope{Mode: pb.TemporalMode_TEMPORAL_MODE_COMPARE, CompareDates: []*pb.CalendarDate{date, {Year: 2027, Month: 1, Day: 1}}, UnresolvedPolicy: pb.UnresolvedPolicy_UNRESOLVED_POLICY_REPORT}, zone, time.Now()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := ResolveTemporalScope(tt.scope, tt.zone, func() time.Time { return tt.now }); err == nil {
				t.Fatal("invalid temporal scope accepted")
			}
		})
	}
}
