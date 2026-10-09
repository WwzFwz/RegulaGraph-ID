// Counts context text and the complete structured chat request with llama.cpp's
// resident tokenizer. Counting reuses the generation client's route/auth/timeout
// and shared envelope; unavailable endpoints fail without a word/byte estimate.
// The operator must pin the server model, tokenizer and template before admission.
// Measure tokenization RPCs, input bytes and p95/p99 as part of answering latency;
// count/usage parity is required, while benchmark-targets.yaml remains unmeasured.
package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
)

// LlamaTokenCounter is specific to a single admitted llama.cpp model. It is not
// a generic OpenAI token estimator. Model file/template admission is the caller's
// responsibility; an endpoint returning counts is not proof of model identity.
type LlamaTokenCounter struct {
	provider  *OpenAICompatibleProvider
	model     string
	maxInput  int
	maxTokens uint64
}

func NewLlamaTokenCounter(provider *OpenAICompatibleProvider, model string, maximumInputBytes int, maximumTokens uint64) (*LlamaTokenCounter, error) {
	if provider == nil || provider.client == nil || model == "" || !utf8.ValidString(model) || len(model) > 1024 || maximumInputBytes < 1 || maximumInputBytes > 8<<20 || maximumTokens == 0 || maximumTokens > 1<<20 {
		return nil, errors.New("token counter requires a model, shared provider and bounded limits")
	}
	return &LlamaTokenCounter{provider: provider, model: model, maxInput: maximumInputBytes, maxTokens: maximumTokens}, nil
}

// CountPrompt sends exactly the chat body that Generate will send, including the
// schema and escaped source envelope. The server applies its actual chat template.
func (c *LlamaTokenCounter) CountPrompt(ctx context.Context, request StructuredRequest) (uint64, error) {
	if c == nil || ctx == nil {
		return 0, errors.New("token counter and context required")
	}
	if request.ModelID != c.model || len(request.ModelID)+len(request.SystemPrompt)+len(request.SystemContext)+len(request.Text)+len(request.ItemID)+len(request.SchemaName)+len(request.Schema) > c.maxInput {
		return 0, errors.New("token counter model or input byte limit mismatch")
	}
	body, err := encodeStructuredChat(request)
	if err != nil {
		return 0, err
	}
	payload, err := c.post(ctx, c.provider.endpoint+"/input_tokens", body)
	if err != nil {
		return 0, err
	}
	fields, err := tokenResponseFields(payload, "object", "input_tokens")
	if err != nil {
		return 0, err
	}
	var kind string
	var n uint64
	if json.Unmarshal(fields["object"], &kind) != nil || kind != "response.input_tokens" || json.Unmarshal(fields["input_tokens"], &n) != nil || n == 0 || n > c.maxTokens {
		return 0, errors.New("invalid complete prompt token count")
	}
	return n, ctx.Err()
}

// CountText excludes the chat envelope and automatic BOS; full prompt admission
// is performed separately by CountPrompt. Special-token spelling follows the
// server's chat tokenizer so source text cannot hide token-budget consumption.
func (c *LlamaTokenCounter) CountText(ctx context.Context, value string) (uint64, error) {
	if c == nil || ctx == nil || !utf8.ValidString(value) {
		return 0, errors.New("valid token counter context and UTF-8 required")
	}
	if len(value) > c.maxInput {
		return 0, errors.New("context tokenization input exceeds byte limit")
	}
	body, err := json.Marshal(struct {
		Model        string `json:"model"`
		Content      string `json:"content"`
		AddSpecial   bool   `json:"add_special"`
		ParseSpecial bool   `json:"parse_special"`
	}{c.model, value, false, true})
	if err != nil {
		return 0, err
	}
	endpoint := strings.TrimSuffix(c.provider.endpoint, "/v1/chat/completions") + "/tokenize"
	payload, err := c.post(ctx, endpoint, body)
	if err != nil {
		return 0, err
	}
	fields, err := tokenResponseFields(payload, "tokens")
	if err != nil {
		return 0, err
	}
	n, err := countTokenIDs(ctx, fields["tokens"], c.maxTokens)
	if err != nil {
		return 0, err
	}
	if value != "" && n == 0 {
		return 0, errors.New("empty context tokenizer output")
	}
	return n, ctx.Err()
}

// Stream scalar IDs so small numeric spellings cannot cause an oversized slice
// allocation before maximumTokens is checked. null is not an integer token zero.
func countTokenIDs(ctx context.Context, raw []byte, maximum uint64) (uint64, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	first, err := d.Token()
	if err != nil || first != json.Delim('[') {
		return 0, errors.New("token IDs must be an array")
	}
	var n uint64
	for d.More() {
		if err = ctx.Err(); err != nil {
			return 0, err
		}
		if n >= maximum {
			return 0, errors.New("context token count exceeds limit")
		}
		token, e := d.Token()
		number, ok := token.(json.Number)
		if e != nil || !ok {
			return 0, errors.New("token ID must be an integer")
		}
		id, e := strconv.ParseInt(string(number), 10, 32)
		if e != nil || id < 0 {
			return 0, errors.New("invalid context token ID")
		}
		n++
	}
	if _, err = d.Token(); err != nil {
		return 0, errors.New("invalid token ID array")
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return 0, errors.New("trailing token ID content")
	}
	return n, ctx.Err()
}

func (c *LlamaTokenCounter) post(ctx context.Context, endpoint string, body []byte) ([]byte, error) {
	if len(body) > c.maxInput {
		return nil, errors.New("encoded tokenization request exceeds byte limit")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("invalid tokenization request")
	}
	r.Header.Set("Content-Type", "application/json")
	if c.provider.apiKey != "" {
		r.Header.Set("Authorization", "Bearer "+c.provider.apiKey)
	}
	response, err := c.provider.client.Do(r)
	if err != nil {
		return nil, providerTransportError(ctx, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, &ProviderError{Code: fmt.Sprintf("http_%d", response.StatusCode), Safe: "tokenizer rejected request", Retryable: response.StatusCode == 429 || response.StatusCode >= 500}
	}
	// Token arrays can exceed a generated answer's byte budget. Bound separately,
	// before decode/allocation, and never trust a remote content-length alone.
	payload, err := readBounded(response.Body, 12*int64(c.maxTokens)+1024)
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return payload, nil
}

// These small protocol objects have exact keys. Reject duplicates, null values,
// case variants and trailing JSON rather than accepting last-wins accounting.
func tokenResponseFields(raw []byte, keys ...string) (map[string]json.RawMessage, error) {
	if !utf8.Valid(raw) {
		return nil, errors.New("invalid tokenizer response encoding")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		return nil, errors.New("tokenizer response must be an object")
	}
	allowed := make(map[string]bool, len(keys))
	for _, key := range keys {
		allowed[key] = true
	}
	fields := make(map[string]json.RawMessage, len(keys))
	for d.More() {
		keyToken, e := d.Token()
		key, ok := keyToken.(string)
		if e != nil || !ok || !allowed[key] || fields[key] != nil {
			return nil, errors.New("invalid tokenizer response keys")
		}
		var value json.RawMessage
		if e = d.Decode(&value); e != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, errors.New("invalid tokenizer response value")
		}
		fields[key] = value
	}
	if _, err = d.Token(); err != nil || len(fields) != len(keys) {
		return nil, errors.New("incomplete tokenizer response")
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return nil, errors.New("trailing tokenizer response")
	}
	return fields, nil
}
