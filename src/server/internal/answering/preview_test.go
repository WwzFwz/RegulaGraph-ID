// Tests model draft/reference validation, abstention and explicit provider errors.
// These adversarial fixtures cannot establish semantic support or model quality.
package answering

import (
	"context"
	"encoding/json"
	"regulagraph.local/server/internal/adapters/inference"
	"regulagraph.local/server/internal/domain"
	"testing"
)

func TestPreviewRejectsInventedCitations(t *testing.T) {
	for _, test := range []struct {
		raw            string
		valid, abstain bool
	}{
		{`{"status":"answer","claims":[{"text":"Sesuai kutipan.","source_ids":["S1"]}]}`, true, false},
		{`{"status":"answer","claims":[{"text":"Klaim.","source_ids":["S9"]}]}`, false, false},
		{`{"status":"answer","claims":[{"text":"Klaim.","source_ids":[]}]}`, false, false},
		{`{"status":"answer","claims":[{"text":"Klaim.","source_ids":["S1","S1"]}]}`, false, false},
		{`{"status":"abstain","claims":[]}`, true, true},
		{`{"status":"abstain","claims":[{"text":"Klaim.","source_ids":["S1"]}]}`, false, false},
	} {
		t.Run(test.raw, func(t *testing.T) {
			g, e := NewPreviewGenerator(draftProviderFunc(func(_ context.Context, r inference.StructuredRequest) (inference.StructuredResponse, error) {
				if r.MaxOutputTokens != 768 || !json.Valid(r.Schema) {
					t.Fatal("invalid bounded provider request")
				}
				return inference.StructuredResponse{JSON: json.RawMessage(test.raw)}, nil
			}), "local")
			if e != nil {
				t.Fatal(e)
			}
			_, abstain, e := g.Generate(context.Background(), "Apa syaratnya?", []domain.PreviewPassage{{ID: "S1", Text: "Bukti"}})
			if (e == nil) != test.valid || abstain != test.abstain {
				t.Fatalf("valid=%v abstain=%v error=%v", test.valid, abstain, e)
			}
		})
	}
}
