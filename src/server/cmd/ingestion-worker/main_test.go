// Tests coordinator environment parsing without opening PostgreSQL or worker connections.
// The suite protects startup bounds and loopback transport policy; it does not measure daemon throughput
// or replace the integration and benchmark gates in configs/benchmark-targets.yaml.
package main

import (
	"testing"
	"time"

	"regulagraph.local/server/internal/domain"
)

func TestRequireLoopback(t *testing.T) {
	for _, endpoint := range []string{"127.0.0.1:50051", "[::1]:50051", "localhost:50051"} {
		if err := requireLoopback(endpoint); err != nil {
			t.Fatalf("expected loopback %s: %v", endpoint, err)
		}
	}
	for _, endpoint := range []string{"0.0.0.0:50051", "10.0.0.2:50051", "worker:50051", "missing-port"} {
		if err := requireLoopback(endpoint); err == nil {
			t.Fatalf("expected rejection for %s", endpoint)
		}
	}
}

func TestCoordinatorAlternatesParseAndBindingPreference(t *testing.T) {
	order := []string{}
	parse := func() (domain.JobRecord, map[string]any, error) {
		order = append(order, "parse")
		return domain.JobRecord{}, nil, nil
	}
	binding := func() (domain.JobRecord, map[string]any, error) {
		order = append(order, "bind")
		return domain.JobRecord{}, nil, nil
	}
	for _, first := range []int{0, 1} {
		attempts := coordinatorAttempts(first, parse, binding)
		_, _, _ = attempts[0]()
	}
	if got := order; len(got) != 2 || got[0] != "parse" || got[1] != "bind" {
		t.Fatalf("coordinator preference did not alternate: %v", got)
	}
}

func TestLoadConfigIncludesBoundedBindingRuntime(t *testing.T) {
	values := map[string]string{
		"REGULAGRAPH_POSTGRES_DSN": "postgres://fixture", "REGULAGRAPH_WORKER_ENDPOINT": "127.0.0.1:50051",
		"REGULAGRAPH_COORDINATOR_OWNER": "coordinator:test", "REGULAGRAPH_COORDINATOR_AUTH_SCOPE": "scope:test",
		"REGULAGRAPH_WORKER_ARTIFACT_ROOT": t.TempDir(), "REGULAGRAPH_CORPUS_JURISDICTION": "ID",
		"REGULAGRAPH_BUILD_ID": "test-build", "REGULAGRAPH_BIND_MAX_BATCH_BYTES": "4096",
		"REGULAGRAPH_BIND_MAX_RECORDS":         "200",
		"REGULAGRAPH_BIND_REGISTRY_BATCH_SIZE": "128",
	}
	for name, value := range values {
		t.Setenv(name, value)
	}
	config, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.artifactRoot == "" || config.jurisdiction != "ID" || config.buildID != "test-build" ||
		config.bindMaxBytes != 4096 || config.bindMaxRecords != 200 || config.bindRegistryBatch != 128 {
		t.Fatalf("binding runtime configuration was not retained: %+v", config)
	}
	t.Setenv("REGULAGRAPH_BIND_MAX_RECORDS", "0")
	if _, err = loadConfig(); err == nil {
		t.Fatal("zero BIND record bound accepted")
	}
}

func TestTypedEnvironmentLimits(t *testing.T) {
	t.Setenv("TEST_DURATION", "125ms")
	value, err := durationEnv("TEST_DURATION", time.Second)
	if err != nil || value != 125*time.Millisecond {
		t.Fatalf("duration=%s err=%v", value, err)
	}
	t.Setenv("TEST_DURATION", "0s")
	if _, err = durationEnv("TEST_DURATION", time.Second); err == nil {
		t.Fatal("zero duration accepted")
	}
	t.Setenv("TEST_INTEGER", "4096")
	integer, err := positiveIntEnv("TEST_INTEGER", 1)
	if err != nil || integer != 4096 {
		t.Fatalf("integer=%d err=%v", integer, err)
	}
}

func TestOpaqueCoordinatorIdentifiers(t *testing.T) {
	if !coordinatorIDPattern.MatchString("coordinator:local-1") || !printableOpaqueID("scope/ingestion:v1") {
		t.Fatal("valid coordinator identifiers rejected")
	}
	if coordinatorIDPattern.MatchString("owner with spaces") || printableOpaqueID("scope with spaces") || printableOpaqueID("") {
		t.Fatal("invalid coordinator identifiers accepted")
	}
}
