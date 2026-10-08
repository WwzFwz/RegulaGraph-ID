// Tests opt-in INDEX daemon wiring: disabled startup needs no INDEX dependency,
// malformed configuration fails explicitly and enablement requires dependencies.
package main

import "testing"

func TestIndexEnablement(t *testing.T) {
	for _, raw := range []string{"", "false", "0", "off", "true"} {
		t.Run(raw, func(t *testing.T) {
			t.Setenv("REGULAGRAPH_INDEX_ENABLED", raw)
			executor, err := newIndexExecutor(runtimeConfig{}, nil, nil, nil)
			if raw == "off" || raw == "true" {
				if err == nil {
					t.Fatal("invalid enabled setup accepted")
				}
			} else if err != nil || executor != nil {
				t.Fatal("disabled INDEX initialized", err)
			}
		})
	}
}
