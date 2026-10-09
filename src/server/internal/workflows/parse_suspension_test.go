// Verifies operator suspension leaves pending extraction untouched while other document
// stages retain round-robin progress. Recreating an enabled executor resumes the same
// pending input through normal validation/checkpoint gates. Fixtures test scheduling
// correctness, not database recovery, model quality or benchmark throughput.
package workflows

import (
	"context"
	"errors"
	"reflect"
	"testing"

	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func TestExtractionSuspensionLeavesPendingJobAndResumes(t *testing.T) {
	store := extractFixtureStore()
	worker := &parseWorkerFake{completion: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED}
	config := parseExecutorConfig()
	config.DisableExtraction = true
	executor, err := NewParseExecutor(store, worker, config)
	if err != nil {
		t.Fatal(err)
	}
	before := store.job
	for i := 0; i < 6; i++ {
		job, response, err := executor.RunOnce(context.Background())
		if !errors.Is(err, domain.ErrLeaseUnavailable) || job.JobID != "" || response != nil {
			t.Fatalf("suspended job was claimed: %v %v %v", job, response, err)
		}
	}
	if !reflect.DeepEqual(before, store.job) || worker.calls != 0 || store.checkpoint != nil || len(store.transitions) != 0 || store.catalogued != nil {
		t.Fatal("suspension modified durable work or called the worker")
	}
	for _, stage := range store.claimOrder {
		if stage == "extract" {
			t.Fatal("suspension reached the extraction claim boundary")
		}
	}
	config.DisableExtraction = false
	resumed, err := NewParseExecutor(store, worker, config)
	if err != nil {
		t.Fatal(err)
	}
	job, response, err := resumed.RunOnce(context.Background())
	if err != nil || job.JobID != before.JobID || response.GetExtractionBatch() == nil || worker.calls != 1 || store.catalogued == nil {
		t.Fatalf("same pending extraction did not resume normally: %v", err)
	}
}

func TestExtractionSuspensionPreservesOtherStageRotationAndErrors(t *testing.T) {
	store := parseFixtureStore(false)
	unavailable := domain.ErrLeaseUnavailable
	store.claimParseErr, store.claimStructureErr, store.claimChunkErr = unavailable, unavailable, unavailable
	worker := &parseWorkerFake{}
	config := parseExecutorConfig()
	config.DisableExtraction = true
	executor, err := NewParseExecutor(store, worker, config)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, _, err := executor.RunOnce(context.Background()); !errors.Is(err, unavailable) {
			t.Fatal(err)
		}
	}
	want := []string{"parse", "structure", "chunk", "structure", "chunk", "parse", "chunk", "parse", "structure"}
	if !reflect.DeepEqual(store.claimOrder, want) {
		t.Fatalf("enabled stages did not rotate: %v", store.claimOrder)
	}
	storageFailure := errors.New("storage unavailable")
	store.claimParseErr = storageFailure
	store.claimOrder = nil
	if _, _, err := executor.RunOnce(context.Background()); !errors.Is(err, storageFailure) || len(store.claimOrder) != 1 || worker.calls != 0 {
		t.Fatalf("claim error was suppressed: %v order=%v", err, store.claimOrder)
	}
}
