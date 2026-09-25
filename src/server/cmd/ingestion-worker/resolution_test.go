// Checks optional RESOLVE startup admission and fair rotation with the existing stages.
// Tests do not open a listener or infer model quality; malformed enablement and nonlocal
// endpoints must fail before dependencies are created.
package main

import (
	"reflect"
	"regulagraph.local/server/internal/domain"
	"testing"
)

func TestResolutionStartupRejectsInvalidEnablement(t *testing.T) {
	for _, value := range []string{"", "false"} {
		t.Setenv("REGULAGRAPH_RESOLUTION_ENABLED", value)
		_, _, enabled, err := resolutionConfig(runtimeConfig{}, nil)
		if err != nil || enabled {
			t.Fatal("disabled resolution requires dependencies")
		}
	}
	t.Setenv("REGULAGRAPH_RESOLUTION_ENABLED", "yes")
	if _, _, _, err := resolutionConfig(runtimeConfig{}, nil); err == nil {
		t.Fatal("ambiguous enablement accepted")
	}
	t.Setenv("REGULAGRAPH_RESOLUTION_ENABLED", "true")
	t.Setenv("REGULAGRAPH_RESOLUTION_ENDPOINT", "example.com:50053")
	if _, _, _, err := resolutionConfig(runtimeConfig{}, nil); err == nil {
		t.Fatal("remote insecure resolution accepted")
	}
}

func TestCoordinatorRotatesAcrossResolutionWithoutStarvingBinding(t *testing.T) {
	var order []int
	var attempts []coordinatorAttempt
	for i := 0; i < 3; i++ {
		index := i
		attempts = append(attempts, func() (domain.JobRecord, map[string]any, error) {
			order = append(order, index)
			return domain.JobRecord{}, nil, nil
		})
	}
	for first := 0; first < 3; first++ {
		for _, attempt := range coordinatorAttempts(first, attempts...) {
			_, _, _ = attempt()
		}
	}
	if !reflect.DeepEqual(order, []int{0, 1, 2, 1, 2, 0, 2, 0, 1}) {
		t.Fatalf("rotation: %v", order)
	}
}
