// Verifies ASSEMBLE enablement is explicit, rejects malformed settings and does
// not touch graph dependencies when disabled. Execution and storage correctness
// belong to workflow/backend tests; these cases prove startup configuration only.
package main

import "testing"

func TestGraphExecutorEnablement(t *testing.T) {
	for _, raw := range []string{"", "false", "0", "true", "invalid"} {
		t.Run("value_"+raw, func(t *testing.T) {
			t.Setenv("REGULAGRAPH_ASSEMBLE_ENABLED", raw)
			executor, err := newGraphExecutor(runtimeConfig{}, nil, nil, nil, nil)
			if raw == "true" || raw == "invalid" {
				if err == nil {
					t.Fatal("invalid enabled dependency/config accepted")
				}
				return
			}
			if err != nil || executor != nil {
				t.Fatal("disabled graph stage initialized", err)
			}
		})
	}
}
