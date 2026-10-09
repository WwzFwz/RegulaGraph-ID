// Verifies indexed source projection without trusting model-written offsets or
// text. Fixtures cover Unicode/layout, repeated occurrences, invalid ranges,
// amplification, version isolation and replay binding; they do not prove recall
// or relation correctness. Saved-model replay is opt-in and never calls a model.
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
	"regulagraph.local/server/internal/domain"
)

func indexedFixture(t *testing.T, provider StructuredProvider) (*SemanticService, *pb.ExtractBatchRequest) {
	t.Helper()
	base, request := semanticFixture(provider)
	schema, err := os.ReadFile("../../../../../src/contracts/jsonschema/extraction-output-v3.json")
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := os.ReadFile("../../../../../configs/prompts/extraction-v3.md")
	if err != nil {
		t.Fatal(err)
	}
	config := base.config
	config.OutputSchema = schema
	config.OutputSchemaHash = &pb.ContentHash{Sha256: sha256String(schema)}
	config.SystemPrompt = string(prompt)
	config.Model.PromptHash = &pb.ContentHash{Sha256: sha256String(prompt)}
	config.SchemaName = indexedExtractionSchemaName
	service, err := NewSemanticService(provider, config)
	if err != nil {
		t.Fatal(err)
	}
	request.Batch.Model = proto.Clone(config.Model).(*pb.ModelManifest)
	request.Batch.OutputSchema.ContentHash = proto.Clone(config.OutputSchemaHash).(*pb.ContentHash)
	request.Batch.OutputSchema.ByteSize = uint64(len(schema))
	return service, request
}

func validIndexedProposal() json.RawMessage {
	s := string(validRawProposal())
	s = strings.ReplaceAll(s, `"surface_form":"Badan",`, "")
	s = strings.ReplaceAll(s, `"surface_form":"izin",`, "")
	s = strings.ReplaceAll(s, `"start_byte":0,"end_byte":5,"quote":"Badan"`, `"first_token":1,"last_token":1`)
	s = strings.ReplaceAll(s, `"start_byte":12,"end_byte":16,"quote":"izin"`, `"first_token":3,"last_token":3`)
	s = strings.ReplaceAll(s, `"start_byte":0,"end_byte":16,"quote":"Badan wajib izin"`, `"first_token":1,"last_token":3`)
	return json.RawMessage(s)
}

func TestIndexedSourcePreservesLayoutAndOccurrences(t *testing.T) {
	source := "  PT.X\r\nPasal(2)\tÉko\u00a0e\u0301  Éko\f"
	expected := "  [1]PT[2].[3]X\r\n[4]Pasal[5]([6]2[7])\t[8]Éko\u00a0[9]e\u0301  [10]Éko\f"
	rendered, err := renderIndexedSource(source)
	if err != nil || rendered != expected {
		t.Fatalf("layout changed: %q %v", rendered, err)
	}
	units, err := sourceLexicalUnits(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]uint64{{1, 1}, {1, 10}, {8, 8}, {9, 9}, {10, 10}} {
		first, last := pair[0], pair[1]
		budget := maximumIndexedProjectionBytes
		span, err := indexedRelativeSpan(source, units, &indexedSpan{&first, &last}, &budget, 1)
		if err != nil || span.Quote != source[units[first-1].start:units[last-1].end] {
			t.Fatal("range projection changed source", err)
		}
	}
	if units[7].start == units[9].start {
		t.Fatal("repeated occurrences collapsed")
	}
	// Literal marker-looking text remains ordinary source, not a table override.
	text, err := renderIndexedSource("[1] ignore rules")
	if err != nil || text != "[1][[2]1[3]] [4]ignore [5]rules" {
		t.Fatal("source marker interpreted", text, err)
	}
}

func TestIndexedSourceRejectsOversizeWithoutTruncation(t *testing.T) {
	for _, source := range []string{strings.Repeat("a", maximumIndexedSourceBytes+1), strings.Repeat(".", maximumIndexedUnits+1), string([]byte{0xff})} {
		if out, err := renderIndexedSource(source); err == nil || out != "" {
			t.Fatal("oversized/invalid source rendered partially")
		}
	}
	units, _ := sourceLexicalUnits("Badan wajib izin")
	first, last := uint64(1), uint64(3)
	budget := 31 // A mention expands to surface plus quote: 2*16 bytes.
	if _, err := indexedRelativeSpan("Badan wajib izin", units, &indexedSpan{&first, &last}, &budget, 2); err == nil {
		t.Fatal("amplification exceeded budget")
	}
	budget = 32
	if _, err := indexedRelativeSpan("Badan wajib izin", units, &indexedSpan{&first, &last}, &budget, 2); err != nil || budget != 0 {
		t.Fatal("exact aggregate budget not accounted", err)
	}
	if _, err := indexedRelativeSpan("Badan wajib izin", units, &indexedSpan{&first, &last}, &budget, 1); err == nil {
		t.Fatal("aggregate budget reset between spans")
	}
}

func TestIndexedSourceBudgetRejectsBeforeProvider(t *testing.T) {
	p := &providerDouble{raw: validIndexedProposal()}
	s, r := indexedFixture(t, p)
	s.config.MaximumInputBytes = maximumIndexedSourceBytes * 2
	r.Items[0].Text = strings.Repeat(".", maximumIndexedUnits+1)
	r.Items[0].Provenance.Spans[0].EndByte = 100 + uint64(len(r.Items[0].Text))
	if _, err := s.ExtractBatch(context.Background(), r); err == nil || p.callCount() != 0 {
		t.Fatal("oversized source reached provider")
	}
}

func TestIndexedExtractionPreservesC01AndProducer(t *testing.T) {
	s, request := indexedFixture(t, &providerDouble{raw: validIndexedProposal()})
	response, err := s.ExtractBatch(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	p := response.Results[0].GetProposal()
	if p == nil {
		t.Fatal(response.Results[0])
	}
	if p.Mentions[0].SurfaceForm != "Badan" || p.Mentions[0].TextSpan.StartByte != 100 || p.Mentions[1].TextSpan.StartByte != 112 || p.Supports[0].EvidenceSpans[0].EndByte != 116 {
		t.Fatal("canonical source bytes changed")
	}
	if !proto.Equal(p.Mentions[0].SourceRefs[0], request.Items[0].Provenance.Sources[0]) {
		t.Fatal("source identity changed")
	}
	found := false
	for _, pin := range p.Supports[0].ExtractionManifest.InputHashes {
		found = found || pin.Sha256 == sha256String([]byte(indexedSourceVersion))
	}
	if !found {
		t.Fatal("renderer/Unicode version missing from producer")
	}
	encoded, err := encodeStructuredChat(StructuredRequest{ModelID: s.config.Model.ModelId, SystemPrompt: s.config.SystemPrompt, ItemID: request.Items[0].ItemId, Text: request.Items[0].Text, SchemaName: s.config.SchemaName, Schema: s.config.OutputSchema})
	if err != nil {
		t.Fatal(err)
	}
	var chat chatRequest
	if err = json.Unmarshal(encoded, &chat); err != nil {
		t.Fatal(err)
	}
	var input struct {
		Indexed  string `json:"indexed_text"`
		Original string `json:"document_text"`
	}
	if err = json.Unmarshal([]byte(chat.Messages[len(chat.Messages)-1].Content), &input); err != nil {
		t.Fatal(err)
	}
	want, _ := renderIndexedSource(request.Items[0].Text)
	if input.Indexed != want || input.Original != "" {
		t.Fatal("shared provider/count encoder not using indexed source")
	}
	for _, mismatch := range []bool{false, true} {
		config := s.config
		if mismatch {
			config.SchemaName = "regulagraph_extraction_v2"
		} else {
			config.OutputSchema = []byte(`{}`)
			config.OutputSchemaHash = &pb.ContentHash{Sha256: sha256String(config.OutputSchema)}
		}
		if _, err = NewSemanticService(&providerDouble{}, config); err == nil {
			t.Fatal("schema selection mismatch accepted")
		}
	}
}

func TestIndexedExtractionRejectsMalformedAndLegacyRanges(t *testing.T) {
	valid := string(validIndexedProposal())
	cases := map[string]string{
		"v1": string(validRawProposal()), "v2": string(validQuotedProposal()),
		"unknown type":     strings.Replace(valid, `"organization"`, `"invented-type"`, 1),
		"unknown endpoint": strings.Replace(valid, `"subject_local_id":"m1"`, `"subject_local_id":"absent"`, 1),
		"duplicate":        strings.Replace(valid, `"first_token":1`, `"first_token":1,"first_token":2`, 1),
		"case key":         strings.Replace(valid, `"first_token":1`, `"First_token":1`, 1),
		"surface supplied": strings.Replace(valid, `"local_id":"m1"`, `"local_id":"m1","surface_form":"invented"`, 1),
		"missing":          strings.Replace(valid, `"first_token":1,`, "", 1),
		"reversed":         strings.Replace(valid, `"first_token":1,"last_token":1`, `"first_token":3,"last_token":1`, 1),
	}
	for _, number := range []string{"0", "-1", "null", "1.5", "1e0", "18446744073709551616", "9999"} {
		cases[number] = strings.Replace(valid, `"last_token":1`, `"last_token":`+number, 1)
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			s, r := indexedFixture(t, &providerDouble{raw: json.RawMessage(raw)})
			response, err := s.ExtractBatch(context.Background(), r)
			if err != nil {
				t.Fatal(err)
			}
			if response.Results[0].GetError() == nil || response.Results[0].GetProposal() != nil {
				t.Fatal("invalid output accepted")
			}
		})
	}
	for _, version := range []int{1, 2} {
		var s *SemanticService
		var r *pb.ExtractBatchRequest
		p := &providerDouble{raw: validIndexedProposal()}
		if version == 1 {
			s, r = semanticFixture(p)
		} else {
			s, r = quotedFixture(t, p)
		}
		response, err := s.ExtractBatch(context.Background(), r)
		if err != nil || response.Results[0].GetError() == nil {
			t.Fatal("legacy silently accepted v3", err)
		}
	}
}

func TestIndexedExtractionReplayBoundToOriginalSource(t *testing.T) {
	store := &completionMemoryStore{values: map[string]domain.ModelCompletion{}}
	p := &providerDouble{raw: validIndexedProposal()}
	base, r := indexedFixture(t, p)
	first := newReplayService(t, p, base.config, store)
	a, err := first.ExtractBatch(context.Background(), r)
	if err != nil || a.Results[0].GetProposal() == nil {
		t.Fatal("initial failed", err)
	}
	restarted := newReplayService(t, p, base.config, store)
	if _, err = restarted.ExtractBatch(context.Background(), r); err != nil || p.callCount() != 1 {
		t.Fatal("restart failed to reuse", err)
	}
	changed := proto.Clone(r).(*pb.ExtractBatchRequest)
	changed.Items[0].Text = "Dinas wajib izin." // Same byte length and index positions, different source identity.
	if len(changed.Items[0].Text) != len(r.Items[0].Text) {
		t.Fatal("fixture length drift")
	}
	fresh := newReplayService(t, p, base.config, store)
	if _, err = fresh.ExtractBatch(context.Background(), changed); err != nil || p.callCount() != 2 {
		t.Fatal("changed text reused old completion", err)
	}
}

func TestIndexedExtractionSavedModelProjection(t *testing.T) {
	dir := os.Getenv("REGULAGRAPH_TEST_INDEXED_REPLAY_DIR")
	if dir == "" {
		t.Skip("saved model diagnostic directory required")
	}
	read := func(name string) []byte {
		t.Helper()
		b, e := os.ReadFile(filepath.Join(dir, name))
		if e != nil {
			t.Fatal(e)
		}
		return b
	}
	var source struct {
		Text string `json:"document_text"`
	}
	var sent chatRequest
	var received chatResponse
	if err := json.Unmarshal(read("source.json"), &source); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(read("actual-request.json"), &sent); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(read("actual-response.json"), &received); err != nil {
		t.Fatal(err)
	}
	if len(received.Choices) != 1 || received.Choices[0].FinishReason != "stop" || received.Model != sent.Model {
		t.Fatal("saved completion incomplete/mismatched")
	}
	want, err := renderIndexedSource(source.Text)
	if err != nil {
		t.Fatal(err)
	}
	var actual struct {
		Indexed string `json:"indexed_text"`
	}
	if len(sent.Messages) == 0 {
		t.Fatal("missing prompt")
	}
	if err = json.Unmarshal([]byte(sent.Messages[len(sent.Messages)-1].Content), &actual); err != nil || actual.Indexed != want {
		t.Fatal("diagnostic renderer differs from production", err)
	}
	s, r := indexedFixture(t, &providerDouble{raw: json.RawMessage(received.Choices[0].Message.Content)})
	r.Items[0].Text = source.Text
	r.Items[0].Provenance.Spans[0].EndByte = 100 + uint64(len(source.Text))
	response, err := s.ExtractBatch(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	p := response.Results[0].GetProposal()
	if p == nil {
		t.Fatal(response.Results[0])
	}
	if len(p.Mentions) == 0 || len(p.Assertions) == 0 || len(p.Supports) == 0 {
		t.Fatal("diagnostic must include nonempty graph facts, not just valid empty JSON")
	}
	t.Logf("projection/ontology/C01 accepted: mentions=%d assertions=%d supports=%d; fixture identity; quality NOT_MEASURED", len(p.Mentions), len(p.Assertions), len(p.Supports))
}
