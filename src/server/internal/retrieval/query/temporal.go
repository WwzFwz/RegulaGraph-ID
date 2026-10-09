// Resolves explicit query temporal intent into one immutable calendar scope.
// CURRENT uses an operator-configured IANA zone and one trusted clock sample;
// document observation/snapshot timestamps never stand in for legal dates.
// AS_OF preserves the supplied date; COMPARE needs multi-scope orchestration.
// This bounded local planner performs no model/database work. Test midnight,
// time-zone and ownership invariants; quality/latency acceptance still follows
// configs/benchmark-targets.yaml (REQUIRED_UNMEASURED).
package query

import (
	"errors"
	"time"
	_ "time/tzdata" // Bundled IANA fallback on hosts without a zone database.

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

const TemporalPolicyVersion = "explicit-calendar-v1"

// TemporalResolution is a local execution audit, not a replacement wire schema.
// EffectiveDate is the single AS_OF date passed to every retrieval/answer stage.
type TemporalResolution struct {
	Policy        string           `json:"policy"`
	RequestedMode string           `json:"requested_mode"`
	EffectiveDate *pb.CalendarDate `json:"effective_date"`
	TimeZone      string           `json:"time_zone,omitempty"`
	ResolvedAt    *time.Time       `json:"resolved_at,omitempty"`
}

// Empty configuration disables CURRENT; never use the machine-local zone.
func LoadQueryTimeZone(name string) (*time.Location, error) {
	if name == "" {
		return nil, nil
	}
	if name == "Local" {
		return nil, errors.New("explicit IANA query time zone required; Local is not allowed")
	}
	return time.LoadLocation(name)
}

func ResolveTemporalScope(scope *pb.TemporalScope, zone *time.Location, clock func() time.Time) (*pb.TemporalScope, *TemporalResolution, error) {
	if scope == nil {
		return nil, nil, errors.New("explicit temporal scope required")
	}
	if err := domain.ValidateWire(scope, domain.DefaultWireLimits); err != nil {
		return nil, nil, err
	}
	resolved := proto.Clone(scope).(*pb.TemporalScope)
	report := &TemporalResolution{Policy: TemporalPolicyVersion}
	switch scope.Mode {
	case pb.TemporalMode_TEMPORAL_MODE_AS_OF:
		report.RequestedMode = "AS_OF"
	case pb.TemporalMode_TEMPORAL_MODE_CURRENT:
		if scope.EffectiveAt != nil || len(scope.CompareDates) != 0 || zone == nil || zone.String() == "Local" || clock == nil {
			return nil, nil, errors.New("CURRENT requires configured time zone and clock, without caller-supplied dates")
		}
		instant := clock().UTC()
		local := instant.In(zone)
		if instant.IsZero() || local.Year() < 1 || local.Year() > 9999 {
			return nil, nil, errors.New("invalid CURRENT clock sample")
		}
		resolved.Mode = pb.TemporalMode_TEMPORAL_MODE_AS_OF
		resolved.EffectiveAt = &pb.CalendarDate{Year: int32(local.Year()), Month: uint32(local.Month()), Day: uint32(local.Day())}
		report.RequestedMode, report.TimeZone, report.ResolvedAt = "CURRENT", zone.String(), &instant
	default:
		return nil, nil, errors.New("only explicit AS_OF and configured CURRENT are supported")
	}
	report.EffectiveDate = proto.Clone(resolved.EffectiveAt).(*pb.CalendarDate)
	return resolved, report, nil
}

// ValidateTemporalResolution lets API/CLI reject date drift in returned audits.
// It replays the stored instant, never reads a new clock (including at midnight).
func ValidateTemporalResolution(scope *pb.TemporalScope, report *TemporalResolution) error {
	if report == nil || report.Policy != TemporalPolicyVersion || report.EffectiveDate == nil {
		return errors.New("temporal resolution audit required")
	}
	zone, err := LoadQueryTimeZone(report.TimeZone)
	if err != nil {
		return err
	}
	clock := func() time.Time { return time.Time{} }
	if report.ResolvedAt != nil {
		clock = func() time.Time { return *report.ResolvedAt }
	}
	_, expected, err := ResolveTemporalScope(scope, zone, clock)
	if err != nil {
		return err
	}
	if expected.RequestedMode != report.RequestedMode || !proto.Equal(expected.EffectiveDate, report.EffectiveDate) || expected.TimeZone != report.TimeZone || (expected.ResolvedAt == nil) != (report.ResolvedAt == nil) {
		return errors.New("temporal resolution differs from requested scope")
	}
	return nil
}
