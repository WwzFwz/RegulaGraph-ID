// Selects the versioned provider-output decoder from a pinned schema without ambiguous JSON keys.
// Used by gateway composition and service admission so provider schema names and local projection
// agree. Only schema identification is validated here; source evidence is validated after inference.
// Parsing is bounded by the caller's schema byte limit and occurs once at startup.
package inference

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"

	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func SemanticSchemaName(task pb.ModelTask, schema json.RawMessage) (string, error) {
	d := json.NewDecoder(bytes.NewReader(schema))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return "", errors.New("semantic schema must be an object")
	}
	seen := map[string]bool{}
	id := ""
	for d.More() {
		token, err = d.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] || (strings.EqualFold(key, "$id") && key != "$id") {
			return "", errors.New("semantic schema has duplicate or ambiguous identity keys")
		}
		seen[key] = true
		var value json.RawMessage
		if err := d.Decode(&value); err != nil {
			return "", err
		}
		if key == "$id" {
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, &id) != nil || id == "" {
				return "", errors.New("semantic schema identity must be a nonempty string")
			}
		}
	}
	if token, err = d.Token(); err != nil || token != json.Delim('}') {
		return "", errors.New("invalid semantic schema object")
	}
	if err := rejectTrailingJSON(d); err != nil {
		return "", err
	}
	switch task {
	case pb.ModelTask_MODEL_TASK_EXTRACT:
		switch id {
		case "", "https://regulagraph.local/schema/extraction-output-v1.json":
			return "regulagraph_extraction_v1", nil
		case quotedExtractionSchemaID:
			return "regulagraph_extraction_v2", nil
		}
	case pb.ModelTask_MODEL_TASK_RESOLVE:
		if id == "" || id == "https://regulagraph.local/schema/resolution-output-v1.json" {
			return "regulagraph_resolution_v1", nil
		}
	}
	return "", errors.New("semantic schema identity is unsupported for the configured task")
}
