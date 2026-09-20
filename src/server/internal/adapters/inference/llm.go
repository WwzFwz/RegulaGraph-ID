// Package inference provides bounded clients and the internal Semantic gRPC gateway for model work.
// This file owns an OpenAI-compatible structured-output HTTP adapter; it sends one item per call,
// never retries implicitly, caps response bytes, and reports provider token usage. Model quality,
// provider latency, and cost targets remain REQUIRED_UNMEASURED in configs/benchmark-targets.yaml.
package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type StructuredRequest struct {
	ModelID      string
	SystemPrompt string
	ItemID       string
	Text         string
	SchemaName   string
	Schema       json.RawMessage
}

type StructuredResponse struct {
	JSON         json.RawMessage
	InputTokens  uint64
	OutputTokens uint64
}

type StructuredProvider interface {
	Generate(context.Context, StructuredRequest) (StructuredResponse, error)
}

type ProviderError struct {
	Code      string
	Safe      string
	Retryable bool
	cause     error
}

func (e *ProviderError) Error() string { return e.Safe }
func (e *ProviderError) Unwrap() error { return e.cause }

type OpenAICompatibleConfig struct {
	Endpoint             string
	APIKey               string
	Timeout              time.Duration
	MaximumResponseBytes int64
}

type OpenAICompatibleProvider struct {
	endpoint string
	apiKey   string
	client   *http.Client
	maxBytes int64
}

func NewOpenAICompatibleProvider(config OpenAICompatibleConfig) (*OpenAICompatibleProvider, error) {
	if config.Timeout <= 0 || config.MaximumResponseBytes <= 0 {
		return nil, errors.New("provider timeout and response byte limit must be positive")
	}
	endpoint, err := url.Parse(config.Endpoint)
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" {
		return nil, errors.New("provider endpoint must be an absolute URL")
	}
	if endpoint.Scheme != "https" && !(endpoint.Scheme == "http" && loopbackHost(endpoint.Hostname())) {
		return nil, errors.New("provider endpoint must use HTTPS unless it is loopback")
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/v1/chat/completions"
	return &OpenAICompatibleProvider{
		endpoint: endpoint.String(),
		apiKey:   config.APIKey,
		client: &http.Client{
			Timeout: config.Timeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		maxBytes: config.MaximumResponseBytes,
	}, nil
}

type chatRequest struct {
	Model          string         `json:"model"`
	Messages       []chatMessage  `json:"messages"`
	ResponseFormat responseFormat `json:"response_format"`
	Temperature    float64        `json:"temperature"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type responseFormat struct {
	Type       string     `json:"type"`
	JSONSchema jsonSchema `json:"json_schema"`
}

type jsonSchema struct {
	Name   string          `json:"name"`
	Strict bool            `json:"strict"`
	Schema json.RawMessage `json:"schema"`
}

type chatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
			Refusal string `json:"refusal"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     *uint64 `json:"prompt_tokens"`
		CompletionTokens *uint64 `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	} `json:"error,omitempty"`
}

func (p *OpenAICompatibleProvider) Generate(ctx context.Context, request StructuredRequest) (StructuredResponse, error) {
	if request.ModelID == "" || request.SystemPrompt == "" || request.ItemID == "" || request.Text == "" || request.SchemaName == "" || len(request.Schema) == 0 {
		return StructuredResponse{}, &ProviderError{Code: "invalid_request", Safe: "structured provider request is incomplete"}
	}
	documentPayload, err := json.Marshal(struct {
		ItemID       string `json:"item_id"`
		DocumentText string `json:"document_text"`
	}{ItemID: request.ItemID, DocumentText: request.Text})
	if err != nil {
		return StructuredResponse{}, &ProviderError{Code: "encode", Safe: "failed to encode document payload", cause: err}
	}
	body, err := json.Marshal(chatRequest{
		Model: request.ModelID,
		Messages: []chatMessage{
			{Role: "system", Content: request.SystemPrompt},
			{Role: "user", Content: string(documentPayload)},
		},
		ResponseFormat: responseFormat{Type: "json_schema", JSONSchema: jsonSchema{Name: request.SchemaName, Strict: true, Schema: request.Schema}},
		Temperature:    0,
	})
	if err != nil {
		return StructuredResponse{}, &ProviderError{Code: "encode", Safe: "failed to encode provider request", cause: err}
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return StructuredResponse{}, &ProviderError{Code: "request", Safe: "failed to create provider request", cause: err}
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	response, err := p.client.Do(httpRequest)
	if err != nil {
		return StructuredResponse{}, providerTransportError(ctx, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		retryable := response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500
		return StructuredResponse{}, &ProviderError{Code: fmt.Sprintf("http_%d", response.StatusCode), Safe: "provider rejected the structured request", Retryable: retryable}
	}
	payload, err := readBounded(response.Body, p.maxBytes)
	if err != nil {
		return StructuredResponse{}, err
	}
	var decoded chatResponse
	if err = json.Unmarshal(payload, &decoded); err != nil {
		return StructuredResponse{}, &ProviderError{Code: "malformed_response", Safe: "provider returned malformed JSON", cause: err}
	}
	if decoded.Error != nil {
		return StructuredResponse{}, &ProviderError{Code: "provider_error", Safe: "provider returned a structured error"}
	}
	if decoded.Model != request.ModelID {
		return StructuredResponse{}, &ProviderError{Code: "model_mismatch", Safe: "provider served a model different from the pinned request"}
	}
	if len(decoded.Choices) != 1 || decoded.Choices[0].Message.Refusal != "" || decoded.Choices[0].Message.Content == "" {
		return StructuredResponse{}, &ProviderError{Code: "no_output", Safe: "provider returned no structured output"}
	}
	if decoded.Choices[0].FinishReason != "stop" {
		return StructuredResponse{}, &ProviderError{Code: "incomplete_output", Safe: "provider did not complete the structured output"}
	}
	if decoded.Usage == nil || decoded.Usage.PromptTokens == nil || decoded.Usage.CompletionTokens == nil {
		return StructuredResponse{}, &ProviderError{Code: "missing_usage", Safe: "provider omitted token usage"}
	}
	raw := json.RawMessage(decoded.Choices[0].Message.Content)
	if !json.Valid(raw) {
		return StructuredResponse{}, &ProviderError{Code: "malformed_output", Safe: "provider structured output is not valid JSON"}
	}
	return StructuredResponse{JSON: raw, InputTokens: *decoded.Usage.PromptTokens, OutputTokens: *decoded.Usage.CompletionTokens}, nil
}

func readBounded(reader io.Reader, maximum int64) ([]byte, error) {
	payload, err := io.ReadAll(io.LimitReader(reader, maximum+1))
	if err != nil {
		return nil, &ProviderError{Code: "read", Safe: "failed to read provider response", Retryable: true, cause: err}
	}
	if int64(len(payload)) > maximum {
		return nil, &ProviderError{Code: "too_large", Safe: "provider response exceeds the configured byte limit"}
	}
	return payload, nil
}

func providerTransportError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return &ProviderError{Code: "cancelled", Safe: "provider request was cancelled", Retryable: true, cause: ctx.Err()}
	}
	return &ProviderError{Code: "unavailable", Safe: "provider is unavailable", Retryable: true, cause: err}
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
