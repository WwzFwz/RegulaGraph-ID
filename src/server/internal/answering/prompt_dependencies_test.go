// Tests compact missing-dependency transport without changing source text,
// canonical omission reporting, or citation admission. Long synthetic IDs expose
// prompt overhead; byte reduction is not a tokenizer or quality benchmark.
package answering

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/inference"
)

func TestMissingPromptProjectionRetainsMultiplicityAndKinds(t *testing.T) {
	in := []string{"structure:one", "temporal-review:two", "structure:one", "unqualified"}
	want := []promptDependency{{"missing:1", "structure"}, {"missing:2", "temporal-review"}, {"missing:3", "structure"}, {"missing:4", "unspecified"}}
	if got := projectPromptDependencies(in, nil); !reflect.DeepEqual(got, want) {
		t.Fatal(got)
	}
	if !reflect.DeepEqual(projectPromptDependencies(in, nil), want) {
		t.Fatal("nondeterministic projection")
	}
	if raw, _ := json.Marshal(projectPromptDependencies(nil, nil)); string(raw) != "[]" {
		t.Fatal("empty omissions must remain an explicit empty array")
	}
	got := projectPromptDependencies(in, []string{"missing:1", "missing:3"})
	if got[0].Handle != "missing:2" || got[1].Handle != "missing:4" {
		t.Fatal("handles collide with selected evidence", got)
	}
}

func TestDraftCompactDependenciesPreserveCanonicalAnswer(t *testing.T) {
	for _, invalidCitation := range []bool{false, true} {
		t.Run(fmt.Sprintf("missing-handle-citation-%t", invalidCitation), func(t *testing.T) {
			var captured inference.StructuredRequest
			g, in := generatorFixture(t, draftProviderFunc(func(_ context.Context, r inference.StructuredRequest) (inference.StructuredResponse, error) {
				captured = r
				id := "evidence:one"
				if invalidCitation {
					id = "missing:1"
				}
				return inference.StructuredResponse{JSON: json.RawMessage(fmt.Sprintf(`{"status":"answer","claims":[{"text":"Isi pasal pertama.","evidence_ids":[%q]}]}`, id)), InputTokens: 100, OutputTokens: 20}, nil
			}))
			for i := 0; i < 30; i++ {
				in.Evidence.MissingDependencies = append(in.Evidence.MissingDependencies, fmt.Sprintf("temporal-review:%064x", i))
			}
			in.Evidence.Completeness = pb.Completeness_COMPLETENESS_PARTIAL
			var err error
			in.Context, err = BuildContext(context.Background(), in.Evidence, "context:compact", in.Context.TokenizerHash, 4096, 4, func(context.Context, string) (uint64, error) { return 100, nil })
			if err != nil {
				t.Fatal(err)
			}
			before := proto.Clone(in.Context)
			result, err := g.Generate(context.Background(), in)
			if invalidCitation {
				if err == nil {
					t.Fatal("missing handle became a citation")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !proto.Equal(before, in.Context) {
				t.Fatal("canonical context mutated")
			}
			var payload struct {
				Evidence []*pb.ContextBlock `json:"evidence"`
				Missing  []promptDependency `json:"missing_required_evidence"`
			}
			if err = json.Unmarshal([]byte(captured.Text), &payload); err != nil {
				t.Fatal(err)
			}
			if len(payload.Missing) != len(in.Context.OmittedRequiredRefs) || len(payload.Evidence) != len(in.Context.RenderedBlocks) {
				t.Fatal("lost prompt inputs")
			}
			for i, block := range payload.Evidence {
				if !proto.Equal(block, in.Context.RenderedBlocks[i]) {
					t.Fatal("evidence text/identity changed")
				}
			}
			for i, id := range in.Context.OmittedRequiredRefs {
				if result.Answer.MissingEvidence[i] != id || strings.Contains(captured.Text, id) {
					t.Fatal("canonical omissions lost or leaked into compact metadata")
				}
			}
			canonical, _ := json.Marshal(in.Context.OmittedRequiredRefs)
			projected, _ := json.Marshal(payload.Missing)
			if len(projected) >= len(canonical) {
				t.Fatal("long ID overhead not reduced")
			}
			last := result.Answer.RunManifest.InputHashes[len(result.Answer.RunManifest.InputHashes)-1].Sha256
			if last != fmt.Sprintf("%x", sha256.Sum256(canonical)) {
				t.Fatal("mapping audit hash missing")
			}
			if len(result.Answer.Citations) == 0 || result.Answer.Claims[0].EvidenceIds[0] != "evidence:one" || result.Answer.SemanticStatus != pb.SemanticStatus_SEMANTIC_STATUS_PARTIAL {
				t.Fatal("canonical citation/status lost")
			}
		})
	}
}
