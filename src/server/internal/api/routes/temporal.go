// Admits explicit temporal modes and validates the workflow's frozen date audit.
// HTTP serializes the same calendar date used by retrieval/generation; it never
// samples a second clock or infers a legal date from text. Tests must reject
// missing/mismatched audits. Measure request latency under benchmark-targets.yaml.
package routes

import (
	"errors"
	"fmt"
	"net/http"

	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/retrieval/query"
	"regulagraph.local/server/internal/workflows"
)

func supportedTemporalRequest(scope *pb.TemporalScope, current, compare bool) bool {
	if compare && scope.GetMode() == pb.TemporalMode_TEMPORAL_MODE_COMPARE {
		_, err := query.PlanComparisonScopes(scope)
		return err == nil
	}
	if scope == nil || len(scope.CompareDates) != 0 {
		return false
	}
	return scope.Mode == pb.TemporalMode_TEMPORAL_MODE_AS_OF && scope.EffectiveAt != nil || current && scope.Mode == pb.TemporalMode_TEMPORAL_MODE_CURRENT && scope.EffectiveAt == nil
}

func resultEffectiveDate(request *pb.QuestionRequest, result *workflows.RAGResult) (*pb.CalendarDate, error) {
	if request == nil || request.TemporalScope == nil || result == nil {
		return nil, errors.New("request and result required")
	}
	if result.Temporal == nil {
		if request.TemporalScope.Mode == pb.TemporalMode_TEMPORAL_MODE_AS_OF && request.TemporalScope.EffectiveAt != nil {
			return request.TemporalScope.EffectiveAt, nil
		}
		return nil, errors.New("CURRENT result requires frozen temporal audit")
	}
	if err := query.ValidateTemporalResolution(request.TemporalScope, result.Temporal); err != nil {
		return nil, err
	}
	return result.Temporal.EffectiveDate, nil
}

func writeTemporalHeaders(w http.ResponseWriter, request *pb.QuestionRequest, result *workflows.RAGResult, expectedZone string) error {
	if request != nil && request.GetTemporalScope().GetMode() == pb.TemporalMode_TEMPORAL_MODE_CURRENT && (result == nil || result.Temporal == nil || result.Temporal.TimeZone != expectedZone) {
		return errors.New("CURRENT result differs from configured time zone")
	}
	date, err := resultEffectiveDate(request, result)
	if err != nil {
		return err
	}
	w.Header().Set("X-Effective-Date", fmt.Sprintf("%04d-%02d-%02d", date.Year, date.Month, date.Day))
	if result.Temporal != nil && result.Temporal.TimeZone != "" {
		w.Header().Set("X-Query-Time-Zone", result.Temporal.TimeZone)
	}
	return nil
}
