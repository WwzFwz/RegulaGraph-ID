// Exercises AS_OF interval boundaries, unresolved policy and mixed-version text
// admission. These fixtures prove deterministic filtering, not legal accuracy
// or retrieval recall; production gates remain benchmark-targets.yaml.
package retrieval

import (
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"testing"
)

func TestHydrationTemporalAdmission(t *testing.T) {
	known := func(y int32) *pb.DateAssertion {
		return &pb.DateAssertion{Knowledge: pb.DateKnowledge_DATE_KNOWLEDGE_KNOWN, Value: &pb.CalendarDate{Year: y, Month: 1, Day: 1}}
	}
	unknown := func() *pb.DateAssertion { return &pb.DateAssertion{Knowledge: pb.DateKnowledge_DATE_KNOWLEDGE_UNKNOWN} }
	unbounded := func() *pb.DateAssertion {
		return &pb.DateAssertion{Knowledge: pb.DateKnowledge_DATE_KNOWLEDGE_UNBOUNDED}
	}
	filter := func(start, end *pb.DateAssertion, status pb.LegalStatus) *pb.IndexProvisionFilter {
		return &pb.IndexProvisionFilter{LegalInterval: &pb.LegalInterval{Start: start, End: end}, LegalStatus: status}
	}
	active := func() *pb.IndexProvisionFilter {
		return filter(known(2026), known(2028), pb.LegalStatus_LEGAL_STATUS_ACTIVE)
	}
	outside := func() *pb.IndexProvisionFilter {
		return filter(known(2020), known(2026), pb.LegalStatus_LEGAL_STATUS_ACTIVE)
	}
	unresolved := func() *pb.IndexProvisionFilter {
		return filter(unknown(), unbounded(), pb.LegalStatus_LEGAL_STATUS_UNKNOWN)
	}
	repealed := func() *pb.IndexProvisionFilter {
		return filter(known(2020), known(2028), pb.LegalStatus_LEGAL_STATUS_REPEALED)
	}
	report, exclude, review := pb.UnresolvedPolicy_UNRESOLVED_POLICY_REPORT, pb.UnresolvedPolicy_UNRESOLVED_POLICY_EXCLUDE, pb.UnresolvedPolicy_UNRESOLVED_POLICY_REQUIRE_REVIEW
	for _, tc := range []struct {
		name                       string
		filters                    []*pb.IndexProvisionFilter
		policy                     pb.UnresolvedPolicy
		accept, uncertain, wantErr bool
	}{
		{"start inclusive", []*pb.IndexProvisionFilter{active()}, report, true, false, false},
		{"end exclusive", []*pb.IndexProvisionFilter{outside()}, report, false, false, false},
		{"before start", []*pb.IndexProvisionFilter{filter(known(2027), unbounded(), pb.LegalStatus_LEGAL_STATUS_ACTIVE)}, report, false, false, false},
		{"unknown report", []*pb.IndexProvisionFilter{unresolved()}, report, true, true, false},
		{"unknown exclude", []*pb.IndexProvisionFilter{unresolved()}, exclude, false, false, false},
		{"unknown review", []*pb.IndexProvisionFilter{unresolved()}, review, false, true, true},
		{"mixed in out", []*pb.IndexProvisionFilter{active(), outside()}, report, false, false, true},
		{"mixed out unknown report", []*pb.IndexProvisionFilter{outside(), unresolved()}, report, false, false, true},
		{"mixed in unknown exclude", []*pb.IndexProvisionFilter{active(), unresolved()}, exclude, false, false, true},
		{"mixed status report", []*pb.IndexProvisionFilter{active(), repealed()}, report, true, true, false},
		{"mixed status exclude", []*pb.IndexProvisionFilter{active(), repealed()}, exclude, false, false, false},
		{"mixed status review", []*pb.IndexProvisionFilter{active(), repealed()}, review, false, true, true},
		{"historically applicable repeal", []*pb.IndexProvisionFilter{repealed()}, report, true, false, false},
		{"repealed missing end", []*pb.IndexProvisionFilter{filter(known(2020), unbounded(), pb.LegalStatus_LEGAL_STATUS_REPEALED)}, report, true, true, false},
		{"not effective missing start", []*pb.IndexProvisionFilter{filter(unbounded(), unbounded(), pb.LegalStatus_LEGAL_STATUS_NOT_YET_EFFECTIVE)}, report, true, true, false},
		{"conflict", []*pb.IndexProvisionFilter{filter(known(2020), unbounded(), pb.LegalStatus_LEGAL_STATUS_CONFLICT)}, report, true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, u, reason, err := filterEvidenceDate(&pb.IndexRecord{FilterMetadata: &pb.FilterMetadata{ProvisionFilters: tc.filters}}, &pb.TemporalScope{EffectiveAt: &pb.CalendarDate{Year: 2026, Month: 1, Day: 1}, UnresolvedPolicy: tc.policy})
			if a != tc.accept || u != tc.uncertain || (err != nil) != tc.wantErr {
				t.Fatalf("accept=%v uncertain=%v reason=%s err=%v", a, u, reason, err)
			}
			if !a && err == nil && reason == "" {
				t.Fatal("excluded evidence lost its reason")
			}
		})
	}
}
