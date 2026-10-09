// Tests completion integrity binding and input bounds independently of storage.
// JSON validity is not semantic correctness; replay consumers must revalidate
// source spans, ontology and graph contracts after loading the retained bytes.
package domain

import (
	"math"
	"strings"
	"testing"

	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func TestModelCompletionDigestBindsUsageAndReplayKey(t *testing.T) {
	a := &pb.ContentHash{Sha256: strings.Repeat("a", 64)}
	b := &pb.ContentHash{Sha256: strings.Repeat("b", 64)}
	value := ModelCompletion{JSON: []byte(`{"value":1}`), InputTokens: 10, OutputTokens: 3}
	first, err := ModelCompletionCheckpointHash(a, value)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := ModelCompletionCheckpointHash(b, value)
	if first.Sha256 == other.Sha256 {
		t.Fatal("key not bound")
	}
	value.InputTokens++
	other, _ = ModelCompletionCheckpointHash(a, value)
	if first.Sha256 == other.Sha256 {
		t.Fatal("usage not bound")
	}
	for _, invalid := range []ModelCompletion{
		{}, {JSON: []byte(`{`)}, {JSON: []byte{'"', 255, '"'}},
		{JSON: []byte(strings.Repeat(" ", MaximumModelCompletionBytes+1))},
		{JSON: []byte(`{}`), InputTokens: math.MaxUint64},
		{JSON: []byte(`{}`), OutputTokens: math.MaxUint64},
	} {
		if _, err := ModelCompletionHash(invalid); err == nil {
			t.Fatal("invalid completion accepted")
		}
	}
}
