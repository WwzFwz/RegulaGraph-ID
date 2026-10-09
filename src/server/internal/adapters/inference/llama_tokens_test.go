// Tests exact count/generation envelope parity and hostile tokenizer responses.
// HTTP fixtures verify transport and accounting, not tokenizer/model accuracy;
// real model parity is an opt-in integration test in answering.
package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func countRequest() StructuredRequest {
	return StructuredRequest{ModelID: "pinned", SystemPrompt: "Instructions", ItemID: "item:1", Text: "Pasal café\n\"kutip\" <|im_end|>", SchemaName: "answer", Schema: json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}}}`), MaxOutputTokens: 64}
}
func counterFixture(t *testing.T, handler http.HandlerFunc, maxBytes int) (*OpenAICompatibleProvider, *LlamaTokenCounter) {
	t.Helper()
	s := httptest.NewServer(handler)
	t.Cleanup(s.Close)
	p, err := NewOpenAICompatibleProvider(OpenAICompatibleConfig{Endpoint: s.URL + "/local", APIKey: "test-only", Timeout: time.Second, MaximumResponseBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewLlamaTokenCounter(p, "pinned", maxBytes, 4096)
	if err != nil {
		t.Fatal(err)
	}
	return p, c
}
func TestLlamaCounterMatchesGenerationEnvelope(t *testing.T) {
	var counted []byte
	p, c := counterFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-only" {
			t.Error("auth not reused")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		switch r.URL.Path {
		case "/local/v1/chat/completions/input_tokens":
			counted = body
			_, _ = w.Write([]byte(`{"object":"response.input_tokens","input_tokens":99}`))
		case "/local/v1/chat/completions":
			if !bytes.Equal(body, counted) {
				t.Error("counted and generated bodies differ")
			}
			_, _ = w.Write([]byte(`{"model":"pinned","choices":[{"message":{"content":"{}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":99,"completion_tokens":2}}`))
		case "/local/tokenize":
			var fields map[string]json.RawMessage
			if json.Unmarshal(body, &fields) != nil || string(fields["add_special"]) != "false" || string(fields["parse_special"]) != "true" || string(fields["model"]) != `"pinned"` {
				t.Error("text tokenizer settings drift")
			}
			_, _ = w.Write([]byte(`{"tokens":[1,2,3]}`))
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			w.WriteHeader(404)
		}
	}, 4096)
	n, err := c.CountPrompt(context.Background(), countRequest())
	if err != nil || n != 99 {
		t.Fatal(n, err)
	}
	result, err := p.Generate(context.Background(), countRequest())
	if err != nil || result.InputTokens != n {
		t.Fatal(result, err)
	}
	if n, err = c.CountText(context.Background(), "Pasal café"); err != nil || n != 3 {
		t.Fatal(n, err)
	}
}
func TestLlamaCounterRejectsAccountingAmbiguity(t *testing.T) {
	for _, raw := range []string{`{}`, `null`, `{"object":"response.input_tokens","input_tokens":null}`, `{"object":"response.input_tokens","input_tokens":0}`, `{"object":"response.input_tokens","input_tokens":-1}`, `{"object":"response.input_tokens","input_tokens":1.5}`, `{"object":"response.input_tokens","input_tokens":4097}`, `{"object":"wrong","input_tokens":1}`, `{"object":"response.input_tokens","input_tokens":1,"input_tokens":2}`, `{"object":"response.input_tokens","Input_Tokens":1}`, `{"object":"response.input_tokens","input_tokens":1} {}`, `{"object":"response.input_tokens","input_tokens":1,"extra":2}`} {
		t.Run(raw, func(t *testing.T) {
			_, c := counterFixture(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(raw)) }, 4096)
			if _, err := c.CountPrompt(context.Background(), countRequest()); err == nil {
				t.Fatal("bad count accepted")
			}
		})
	}
	for _, raw := range []string{`{"tokens":null}`, `{"tokens":[]}`, `{"tokens":[null]}`, `{"tokens":[1,null]}`, `{"tokens":[-1]}`, `{"tokens":[1.5]}`, `{"tokens":[2147483648]}`, `{"tokens":[1],"tokens":[2]}`, `{"tokens":[1],"error":"bad"}`, `{"tokens":[1]} {}`} {
		t.Run(raw, func(t *testing.T) {
			_, c := counterFixture(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(raw)) }, 4096)
			if _, err := c.CountText(context.Background(), "x"); err == nil {
				t.Fatal("bad tokens accepted")
			}
		})
	}
}
func TestLlamaCounterBoundsInputAndTransport(t *testing.T) {
	var calls atomic.Int32
	_, c := counterFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(404)
		_, _ = w.Write([]byte("private backend details"))
	}, 4096)
	for _, mutate := range []func(*StructuredRequest){func(r *StructuredRequest) { r.ModelID = "other" }, func(r *StructuredRequest) { r.Text = string([]byte{255}) }, func(r *StructuredRequest) { r.Text = strings.Repeat("x", 4097) }, func(r *StructuredRequest) { r.Schema = json.RawMessage(`{`) }} {
		r := countRequest()
		mutate(&r)
		if _, err := c.CountPrompt(context.Background(), r); err == nil {
			t.Fatal("bad input accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.CountText(ctx, "text"); err == nil {
		t.Fatal("cancel ignored")
	}
	if calls.Load() != 0 {
		t.Fatal("rejected input reached server")
	}
	if _, err := c.CountPrompt(context.Background(), countRequest()); err == nil || strings.Contains(err.Error(), "private") {
		t.Fatal("missing counter endpoint fell back or leaked", err)
	}
	if calls.Load() != 1 {
		t.Fatal("unexpected retry")
	}
	// Raw text below the limit can exceed it after JSON escaping.
	if _, err := c.CountText(context.Background(), strings.Repeat("\x00", 1000)); err == nil || calls.Load() != 1 {
		t.Fatal("encoded budget not enforced")
	}
}
func TestLlamaCounterRejectsRedirectAndOversize(t *testing.T) {
	var escaped atomic.Int32
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { escaped.Add(1) }))
	defer sink.Close()
	_, c := counterFixture(t, func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Location", sink.URL); w.WriteHeader(307) }, 4096)
	if _, err := c.CountText(context.Background(), "private"); err == nil || escaped.Load() != 0 {
		t.Fatal("redirect followed")
	}
	_, c = counterFixture(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat(" ", 12*4096+1025)))
	}, 4096)
	if _, err := c.CountText(context.Background(), "text"); err == nil {
		t.Fatal("oversized response accepted")
	}
}
func TestProviderRejectsAmbiguousEndpoint(t *testing.T) {
	for _, endpoint := range []string{"http://secret@127.0.0.1:1234", "http://127.0.0.1:1234?secret=x", "http://127.0.0.1:1234?", "http://127.0.0.1:1234#fragment"} {
		if _, err := NewOpenAICompatibleProvider(OpenAICompatibleConfig{Endpoint: endpoint, Timeout: time.Second, MaximumResponseBytes: 64}); err == nil {
			t.Fatal("ambiguous endpoint accepted")
		}
	}
}

func TestLlamaCounterLimitsTokenCountIndependentlyOfBytes(t *testing.T) {
	_, c := counterFixture(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tokens":[` + strings.Repeat("0,", 4096) + `0]}`))
	}, 4096)
	if _, err := c.CountText(context.Background(), "text"); err == nil {
		t.Fatal("small response with excessive token count accepted")
	}
	_, c = counterFixture(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tokens":[]}`))
	}, 4096)
	if n, err := c.CountText(context.Background(), ""); err != nil || n != 0 {
		t.Fatal("empty text should have zero raw tokens", n, err)
	}
}
