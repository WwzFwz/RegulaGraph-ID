// Verifies v2 quote alignment against exact bytes and v1 compatibility through the production
// gateway. Cases cover ambiguous/overlapping matches, Unicode, invalid context, output schema
// selection, producer identity, and bounded work. Fixtures prove mechanics, not model accuracy.
package inference

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func quoteText(s string) *string { return &s }

// Replays saved local-model content through production projection/ontology/C01 gates.
// Source text is real, but corpus/source IDs and the absolute base are fixture identities;
// this diagnostic does not prove durable ingestion, legal accuracy or model attestation.
func TestQuotedExtractionSavedModelProjection(t *testing.T) {
	directory := os.Getenv("REGULAGRAPH_TEST_QUOTE_REPLAY_DIR")
	if directory == "" {
		t.Skip("explicit saved model diagnostic directory required")
	}
	requestBytes, err := os.ReadFile(filepath.Join(directory, "actual-request.json"))
	if err != nil {
		t.Fatal(err)
	}
	responseBytes, err := os.ReadFile(filepath.Join(directory, "actual-response.json"))
	if err != nil {
		t.Fatal(err)
	}
	var sent chatRequest
	var received chatResponse
	if err = json.Unmarshal(requestBytes, &sent); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(responseBytes, &received); err != nil {
		t.Fatal(err)
	}
	if len(sent.Messages) == 0 || len(received.Choices) != 1 || received.Choices[0].FinishReason != "stop" || received.Model != sent.Model {
		t.Fatal("saved diagnostic is incomplete or mismatched")
	}
	var input struct {
		Text string `json:"document_text"`
	}
	if err = json.Unmarshal([]byte(sent.Messages[len(sent.Messages)-1].Content), &input); err != nil || input.Text == "" {
		t.Fatal("saved source text missing")
	}
	service, request := quotedFixture(t, &providerDouble{raw: json.RawMessage(received.Choices[0].Message.Content)})
	request.Items[0].Text = input.Text
	request.Items[0].Provenance.Spans[0].EndByte = 100 + uint64(len(input.Text))
	result, err := service.ExtractBatch(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if problem := result.Results[0].GetError(); problem != nil {
		t.Fatalf("saved model output rejected: %v", problem)
	}
	proposal := result.Results[0].GetProposal()
	t.Logf("projection only: mentions=%d assertions=%d supports=%d; fixture identities, no quality acceptance", len(proposal.Mentions), len(proposal.Assertions), len(proposal.Supports))
}

func TestSemanticSchemaSelectionRejectsAmbiguousOrWrongTaskIdentity(t *testing.T) {
	for _, schema := range []string{
		`null`, `[]`, `{"$id":null}`, `{"$id":""}`,
		`{"$id":"https://regulagraph.local/schema/extraction-output-v1.json","$id":"https://regulagraph.local/schema/extraction-output-v2.json"}`,
		`{"$ID":"https://regulagraph.local/schema/extraction-output-v2.json"}`,
		`{"$id":"https://regulagraph.local/schema/resolution-output-v1.json"}`,
		`{"$id":"https://example.invalid/unknown"}`,
	} {
		if _, err := SemanticSchemaName(pb.ModelTask_MODEL_TASK_EXTRACT, json.RawMessage(schema)); err == nil {
			t.Fatalf("accepted ambiguous schema: %s", schema)
		}
	}
	for _, version := range []string{"v1", "v2"} {
		schema, err := os.ReadFile("../../../../../src/contracts/jsonschema/extraction-output-" + version + ".json")
		if err != nil {
			t.Fatal(err)
		}
		name, err := SemanticSchemaName(pb.ModelTask_MODEL_TASK_EXTRACT, schema)
		if err != nil || name != "regulagraph_extraction_"+version {
			t.Fatalf("wrong schema selection: %s %v", name, err)
		}
		if _, err := SemanticSchemaName(pb.ModelTask_MODEL_TASK_RESOLVE, schema); err == nil {
			t.Fatal("EXTRACT schema used for RESOLVE")
		}
	}
}

func TestQuoteAlignmentRequiresUniqueExactContext(t *testing.T) {
	for _, test := range []struct {
		name, text, quote, prefix, suffix string
		start                             int
		invalid                           bool
	}{
		{name: "unique Unicode", text: "é Badan wajib izin.", quote: "Badan", start: 3},
		{name: "disambiguate second", text: "Badan A; Badan B", quote: "Badan", prefix: "; ", suffix: " B", start: 9},
		{name: "ambiguous", text: "Badan A; Badan B", quote: "Badan", invalid: true},
		{name: "overlapping", text: "ababa", quote: "aba", invalid: true},
		{name: "no normalization", text: "Badan  A", quote: "Badan A", invalid: true},
		{name: "invented prefix", text: "Badan A", quote: "Badan", prefix: "X", invalid: true},
		{name: "nonadjacent context", text: "X -- Badan", quote: "Badan", prefix: "X ", invalid: true},
		{name: "empty", text: "text", invalid: true},
		{name: "oversize context", text: strings.Repeat("a", 300) + "b", quote: "b", prefix: strings.Repeat("a", 257), invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			a := quoteAlignment{text: test.text, remaining: maximumQuoteScanBytes, positions: map[string]int{}}
			span, err := a.relative(&quotedSpan{Quote: quoteText(test.quote), Prefix: quoteText(test.prefix), Suffix: quoteText(test.suffix)})
			if test.invalid {
				if err == nil {
					t.Fatal("invalid/ambiguous span accepted")
				}
				return
			}
			if err != nil || *span.Start != uint64(test.start) || *span.End != uint64(test.start+len(test.quote)) {
				t.Fatalf("wrong byte span: %+v %v", span, err)
			}
			remaining := a.remaining
			if _, err = a.relative(&quotedSpan{Quote: quoteText(test.quote), Prefix: quoteText(test.prefix), Suffix: quoteText(test.suffix)}); err != nil || a.remaining != remaining {
				t.Fatal("repeated anchor rescanned source")
			}
		})
	}
}

func TestQuoteAlignmentRejectsMissingFieldsAndExhaustedBudget(t *testing.T) {
	a := quoteAlignment{text: "Badan", remaining: 0, positions: map[string]int{}}
	if _, err := a.relative(&quotedSpan{Quote: quoteText("Badan"), Prefix: quoteText(""), Suffix: quoteText("")}); err == nil {
		t.Fatal("scan budget ignored")
	}
	a.remaining = 100
	if _, err := a.relative(&quotedSpan{Quote: quoteText("Badan")}); err == nil {
		t.Fatal("missing locator fields accepted")
	}
}

func quotedFixture(t *testing.T, provider StructuredProvider) (*SemanticService, *pb.ExtractBatchRequest) {
	t.Helper()
	base, request := semanticFixture(provider)
	schema, err := os.ReadFile("../../../../../src/contracts/jsonschema/extraction-output-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := os.ReadFile("../../../../../configs/prompts/extraction-v2.md")
	if err != nil {
		t.Fatal(err)
	}
	config := base.config
	config.OutputSchema = schema
	config.OutputSchemaHash = &pb.ContentHash{Sha256: sha256String(schema)}
	config.SystemPrompt = string(prompt)
	config.Model.PromptHash = &pb.ContentHash{Sha256: sha256String(prompt)}
	config.SchemaName = "regulagraph_extraction_v2"
	service, err := NewSemanticService(provider, config)
	if err != nil {
		t.Fatal(err)
	}
	request.Batch.Model = proto.Clone(config.Model).(*pb.ModelManifest)
	request.Batch.OutputSchema.ContentHash = proto.Clone(config.OutputSchemaHash).(*pb.ContentHash)
	request.Batch.OutputSchema.ByteSize = uint64(len(schema))
	return service, request
}

func validQuotedProposal() json.RawMessage {
	s := string(validRawProposal())
	s = strings.ReplaceAll(s, `"start_byte":0,"end_byte":5,`, `"prefix":"","suffix":"",`)
	s = strings.ReplaceAll(s, `"start_byte":12,"end_byte":16,`, `"prefix":"","suffix":"",`)
	s = strings.ReplaceAll(s, `"start_byte":0,"end_byte":16,`, `"prefix":"","suffix":"",`)
	return json.RawMessage(s)
}

func TestQuotedExtractionProjectsSourceBoundEvidence(t *testing.T) {
	service, request := quotedFixture(t, &providerDouble{raw: validQuotedProposal()})
	result, err := service.ExtractBatch(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	p := result.Results[0].GetProposal()
	if p == nil {
		t.Fatalf("quote proposal rejected: %v", result.Results[0])
	}
	if p.Mentions[0].TextSpan.StartByte != 100 || p.Mentions[1].TextSpan.StartByte != 112 || p.Supports[0].EvidenceSpans[0].EndByte != 116 {
		t.Fatal("absolute source offsets changed")
	}
	if !proto.Equal(p.Mentions[0].SourceRefs[0], request.Items[0].Provenance.Sources[0]) || !proto.Equal(p.Supports[0].ExtractionManifest, service.ProducerManifest()) {
		t.Fatal("source identity or producer lost")
	}
}

func TestQuotedExtractionRejectsMalformedAndWrongVersionOutput(t *testing.T) {
	for name, raw := range map[string]json.RawMessage{
		"v1 offsets":            validRawProposal(),
		"duplicate quote":       json.RawMessage(strings.Replace(string(validQuotedProposal()), `"quote":"Badan"`, `"quote":"X","quote":"Badan"`, 1)),
		"case folded":           json.RawMessage(strings.Replace(string(validQuotedProposal()), `"quote":"Badan"`, `"Quote":"Badan"`, 1)),
		"missing context":       json.RawMessage(strings.Replace(string(validQuotedProposal()), `"prefix":"",`, ``, 1)),
		"invented source":       json.RawMessage(strings.Replace(string(validQuotedProposal()), `"quote":"Badan"`, `"quote":"Pemerintah"`, 1)),
		"unsupported assertion": json.RawMessage(strings.Replace(string(validQuotedProposal()), `"subject_local_id":"m1"`, `"subject_local_id":"unknown"`, 1)),
	} {
		t.Run(name, func(t *testing.T) {
			service, request := quotedFixture(t, &providerDouble{raw: raw})
			response, err := service.ExtractBatch(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			if response.Results[0].GetError() == nil {
				t.Fatal("invalid provider output accepted")
			}
		})
	}
	legacy, request := semanticFixture(&providerDouble{raw: validQuotedProposal()})
	response, err := legacy.ExtractBatch(context.Background(), request)
	if err != nil || response.Results[0].GetError() == nil {
		t.Fatal("v1 silently accepted v2 quote-only output")
	}
}
