// Encodes the one structured chat envelope shared by generation and exact prompt
// counting. Schema, system role, escaped document payload and output reserve must
// remain identical across both calls. No provider I/O or model initialization occurs
// here. Count/usage parity is an invariant; latency gates remain in benchmark-targets.yaml.
package inference

import (
	"encoding/json"
	"unicode/utf8"
)

func encodeStructuredChat(request StructuredRequest) ([]byte, error) {
	for _, value := range []string{request.ModelID, request.SystemPrompt, request.ItemID, request.Text, request.SchemaName} {
		if value == "" || !utf8.ValidString(value) {
			return nil, &ProviderError{Code: "invalid_request", Safe: "structured provider request is incomplete or invalid UTF-8"}
		}
	}
	if !utf8.Valid(request.Schema) || !json.Valid(request.Schema) {
		return nil, &ProviderError{Code: "invalid_request", Safe: "structured provider schema is invalid"}
	}
	documentPayload, err := json.Marshal(struct {
		ItemID       string `json:"item_id"`
		DocumentText string `json:"document_text"`
	}{request.ItemID, request.Text})
	if err != nil {
		return nil, &ProviderError{Code: "encode", Safe: "failed to encode document payload", cause: err}
	}
	body, err := json.Marshal(chatRequest{
		Model:          request.ModelID,
		Messages:       []chatMessage{{Role: "system", Content: request.SystemPrompt}, {Role: "user", Content: string(documentPayload)}},
		ResponseFormat: responseFormat{Type: "json_schema", JSONSchema: jsonSchema{Name: request.SchemaName, Strict: true, Schema: request.Schema}},
		Temperature:    0, MaxTokens: request.MaxOutputTokens,
	})
	if err != nil {
		return nil, &ProviderError{Code: "encode", Safe: "failed to encode provider request", cause: err}
	}
	return body, nil
}
