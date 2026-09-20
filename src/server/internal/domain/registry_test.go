// Verifies conservative regulation identity planning before any canonical registry write.
// Cases emphasize false-merge prevention, deterministic replay, explicit review, and bounded input.
package domain

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func TestPlanRegulationIdentitiesIsDeterministicAndIssuerScoped(t *testing.T) {
	first := identityBatch(identityObservation("observation:b", "source-blob:a", map[string][]string{
		"regulation_type": {" Undang-Undang "}, "number": {"1"}, "year": {"2026"},
		"issuer": {"Indonesia"}, "page_title": {"Undang-Undang Nomor 1 Tahun 2026"},
	}))
	first.Observations = append(first.Observations, identityObservation("observation:a", "source-blob:a", map[string][]string{
		"issuer": {" indonesia "}, "year": {"2026"}, "number": {"1"},
		"page_title": {"Undang-Undang Nomor 1 Tahun 2026"}, "regulation_type": {"undang-undang"},
	}))
	policy := RegulationIdentityPolicy{Jurisdiction: "ID", MaximumItems: 10}
	forward, err := PlanRegulationIdentities(first, policy)
	if err != nil {
		t.Fatal(err)
	}
	first.Observations[0], first.Observations[1] = first.Observations[1], first.Observations[0]
	reversed, err := PlanRegulationIdentities(first, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(forward.Candidates) != 1 || len(forward.Reviews) != 0 || !reflect.DeepEqual(forward, reversed) ||
		forward.Candidates[0].ObservationIDs[0] != "observation:a" {
		t.Fatalf("identity plan is not deterministic: %#v %#v", forward, reversed)
	}

	other := identityBatch(identityObservation("observation:c", "source-blob:a", map[string][]string{
		"regulation_type": {"Undang-Undang"}, "number": {"1"}, "year": {"2026"},
		"issuer": {"Kementerian Contoh"}, "page_title": {"Undang-Undang Nomor 1 Tahun 2026"},
	}))
	differentIssuer, err := PlanRegulationIdentities(other, policy)
	if err != nil {
		t.Fatal(err)
	}
	if forward.Candidates[0].IdentityKey == differentIssuer.Candidates[0].IdentityKey {
		t.Fatal("same number/year with a different issuer was merged")
	}
}

func TestPlanRegulationIdentitiesRoutesMissingAndConflictingFieldsToReview(t *testing.T) {
	missing := identityBatch(identityObservation("observation:a", "source-blob:a", map[string][]string{
		"regulation_type": {"Peraturan"}, "number": {"1"}, "year": {"2026"}, "page_title": {"Peraturan Contoh"},
	}))
	plan, err := PlanRegulationIdentities(missing, RegulationIdentityPolicy{Jurisdiction: "ID", MaximumItems: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Candidates) != 0 || len(plan.Reviews) != 1 || plan.Reviews[0].GetStatus() != pb.ReviewState_REVIEW_STATE_UNREVIEWED {
		t.Fatalf("missing issuer did not enter review: %#v", plan)
	}
	if err = ValidateWire(plan.Reviews[0], DefaultWireLimits); err != nil {
		t.Fatalf("review item is not wire-valid: %v", err)
	}

	conflict := identityBatch(identityObservation("observation:a", "source-blob:a", map[string][]string{
		"regulation_type": {"Peraturan", "Undang-Undang"}, "number": {"1"}, "year": {"2026"},
		"issuer": {"Indonesia"}, "page_title": {"Peraturan Contoh"},
	}))
	plan, err = PlanRegulationIdentities(conflict, RegulationIdentityPolicy{Jurisdiction: "ID", MaximumItems: 10})
	if err != nil || len(plan.Candidates) != 0 || len(plan.Reviews) != 1 {
		t.Fatalf("conflicting type did not enter review: plan=%#v err=%v", plan, err)
	}
}

func TestPlanRegulationIdentitiesRequiresExplicitPolicyAndBounds(t *testing.T) {
	batch := identityBatch(identityObservation("observation:a", "source-blob:a", map[string][]string{
		"regulation_type": {"Peraturan"}, "number": {"1"}, "year": {"2026"},
		"issuer": {"Indonesia"}, "page_title": {"Peraturan Contoh"},
	}))
	if _, err := PlanRegulationIdentities(batch, RegulationIdentityPolicy{MaximumItems: 10}); err == nil {
		t.Fatal("missing jurisdiction policy was accepted")
	}
	if _, err := PlanRegulationIdentities(batch, RegulationIdentityPolicy{Jurisdiction: "ID", MaximumItems: 0}); err == nil {
		t.Fatal("unbounded policy was accepted")
	}
}

func TestPlanRegulationIdentitiesRejectsUnknownBlobAndReviewsUntrustedObservation(t *testing.T) {
	fields := map[string][]string{
		"regulation_type": {"Peraturan"}, "number": {"1"}, "year": {"2026"},
		"issuer": {"Indonesia"}, "page_title": {"Peraturan Contoh"},
	}
	unknown := identityBatch(identityObservation("observation:a", "source-blob:unknown", fields))
	if _, err := PlanRegulationIdentities(unknown, RegulationIdentityPolicy{Jurisdiction: "ID", MaximumItems: 10}); err == nil {
		t.Fatal("observation for a blob outside the batch was ignored")
	}
	duplicate := identityBatch(
		identityObservation("observation:a", "source-blob:a", fields),
		identityObservation("observation:a", "source-blob:a", fields),
	)
	if _, err := PlanRegulationIdentities(duplicate, RegulationIdentityPolicy{Jurisdiction: "ID", MaximumItems: 10}); err == nil {
		t.Fatal("duplicate observation identity was accepted")
	}

	failedObservation := identityObservation("observation:a", "source-blob:a", fields)
	failedObservation.Status = pb.ObservationStatus_OBSERVATION_STATUS_FAILED
	failedObservation.Error = &pb.OperationError{Code: pb.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, SafeMessage: "fixture failure"}
	plan, err := PlanRegulationIdentities(identityBatch(failedObservation), RegulationIdentityPolicy{Jurisdiction: "ID", MaximumItems: 10})
	if err != nil || len(plan.Candidates) != 0 || len(plan.Reviews) != 1 {
		t.Fatalf("failed observation did not enter review: plan=%#v err=%v", plan, err)
	}

	jurisdictionConflict := identityObservation("observation:a", "source-blob:a", fields)
	jurisdictionConflict.PortalMetadata = append(jurisdictionConflict.PortalMetadata,
		&pb.NamedValue{Name: "jurisdiction", Value: &pb.NamedValue_Text{Text: "ID-JB"}})
	plan, err = PlanRegulationIdentities(identityBatch(jurisdictionConflict), RegulationIdentityPolicy{Jurisdiction: "ID", MaximumItems: 10})
	if err != nil || len(plan.Candidates) != 0 || len(plan.Reviews) != 1 {
		t.Fatalf("sourced jurisdiction conflict did not enter review: plan=%#v err=%v", plan, err)
	}
}

func TestRegulationCandidateRejectsStaleIdentityKey(t *testing.T) {
	batch := identityBatch(identityObservation("observation:a", "source-blob:a", map[string][]string{
		"regulation_type": {"Peraturan"}, "number": {"1"}, "year": {"2026"},
		"issuer": {"Indonesia"}, "page_title": {"Peraturan Contoh"},
	}))
	plan, err := PlanRegulationIdentities(batch, RegulationIdentityPolicy{Jurisdiction: "ID", MaximumItems: 10})
	if err != nil || len(plan.Candidates) != 1 {
		t.Fatalf("fixture candidate failed: %#v %v", plan, err)
	}
	if _, err = plan.Candidates[0].CanonicalClaim(); err != nil {
		t.Fatalf("complete candidate did not produce claim: %v", err)
	}
	plan.Candidates[0].IssuerLabel = "Issuer Lain"
	if _, err = plan.Candidates[0].CanonicalClaim(); err == nil {
		t.Fatal("candidate mutation retained a stale exact identity key")
	}
}

func identityBatch(observations ...*pb.SourceObservation) *pb.DocumentBatch {
	return &pb.DocumentBatch{
		Meta:    &pb.RecordMeta{SchemaVersion: 1, CorpusId: "corpus:fixture", RecordId: "batch:fixture"},
		Context: fixtureRequestContext(),
		Sources: []*pb.SourceBlob{{
			Meta:      &pb.RecordMeta{SchemaVersion: 1, CorpusId: "corpus:fixture", RecordId: "source-blob:a"},
			RawSha256: fixtureHash("a"), MediaType: "application/pdf", ByteSize: 1,
			ArtifactRef: &pb.ArtifactRef{ArtifactId: "artifact:a", ContentHash: fixtureHash("a"), StorageKey: "objects/a.pdf", MediaType: "application/pdf", ByteSize: 1, SchemaVersion: 1},
		}},
		Observations:       observations,
		DependencyManifest: &pb.DependencyManifest{ArtifactId: "dependencies:fixture", ProducerManifest: fixtureProducerManifest()},
		Completeness:       pb.Completeness_COMPLETENESS_COMPLETE,
	}
}

func identityObservation(id, sourceID string, fields map[string][]string) *pb.SourceObservation {
	metadata := []*pb.NamedValue{}
	for name, values := range fields {
		for _, value := range values {
			metadata = append(metadata, &pb.NamedValue{Name: name, Value: &pb.NamedValue_Text{Text: value}})
		}
	}
	return &pb.SourceObservation{
		Meta:     &pb.RecordMeta{SchemaVersion: 1, CorpusId: "corpus:fixture", RecordId: id},
		PortalId: "bpk", DetailUrl: "https://peraturan.bpk.go.id/Details/1/test",
		FetchedAt: fixtureTimestamp(), Status: pb.ObservationStatus_OBSERVATION_STATUS_COMPLETE,
		MetadataHash: fixtureHash("d"), SourceBlobId: &sourceID, PortalMetadata: metadata,
	}
}

func fixtureRequestContext() *pb.RequestContext {
	return &pb.RequestContext{
		SchemaVersion: 1, RequestId: "request:fixture", TraceId: "trace:fixture", CorpusId: "corpus:fixture",
		Deadline: fixtureTimestamp(), ConfigFingerprint: fixtureHash("c"), AuthScopeRef: "scope:fixture",
	}
}

func fixtureProducerManifest() *pb.ProducerManifest {
	return &pb.ProducerManifest{Software: "test", Build: "test", SchemaVersion: 1, ConfigHash: fixtureHash("b")}
}

func fixtureHash(character string) *pb.ContentHash {
	return &pb.ContentHash{Sha256: strings.Repeat(character, 64)}
}

func fixtureTimestamp() *timestamppb.Timestamp {
	return timestamppb.New(time.Date(2026, 9, 21, 1, 2, 3, 0, time.UTC))
}
