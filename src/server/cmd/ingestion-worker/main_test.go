// Tests coordinator environment parsing without opening PostgreSQL or worker connections.
// The suite protects startup bounds and loopback transport policy; it does not measure daemon throughput
// or replace the integration and benchmark gates in configs/benchmark-targets.yaml.
package main

import (
	"testing"
	"time"
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
