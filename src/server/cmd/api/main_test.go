// Validates local API configuration before I/O. Explicit profile/corpus/token and
// loopback binding are required; defaults do not invent model or corpus authority.
package main

import (
	"testing"
)

func TestAPIConfiguration(t *testing.T) {
	env := map[string]string{"REGULAGRAPH_API_TOKEN": "test-operator-token-at-least-32-characters", "REGULAGRAPH_QUERY_CORPUS_ID": "corpus:test", "REGULAGRAPH_API_PROFILE": "hybrid"}
	read := func(k string) string { return env[k] }
	if address, _, _, err := configuration(read); err != nil || address != "127.0.0.1:8097" {
		t.Fatal(address, err)
	}
	for _, address := range []string{"0.0.0.0:8097", "example.org:8097", "127.0.0.1:0", "127.0.0.1:65536"} {
		env["REGULAGRAPH_API_LISTEN"] = address
		if _, _, _, err := configuration(read); err == nil {
			t.Fatal("invalid listen", address)
		}
	}
	delete(env, "REGULAGRAPH_API_LISTEN")
	env["REGULAGRAPH_API_PROFILE"] = ""
	if _, _, _, err := configuration(read); err == nil {
		t.Fatal("implicit profile accepted")
	}
	env["REGULAGRAPH_API_PROFILE"] = "hybrid"
	env["REGULAGRAPH_API_TOKEN"] = "weak"
	if _, _, _, err := configuration(read); err == nil {
		t.Fatal("weak token accepted")
	}
}
