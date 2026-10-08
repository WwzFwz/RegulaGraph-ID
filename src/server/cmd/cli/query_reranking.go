// Serializes explicit reranking diagnostics beside the authoritative evidence
// ProtoJSON. Scores/model/truncation use C01 types; envelope counts and duration
// describe local execution, never answer confidence or benchmark acceptance.
// Requested reranking cannot silently disappear or change evidence ordering.
package main

import (
	"encoding/json"
	"errors"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/workflows"
)

type queryRerankJSON struct {
	Model      json.RawMessage   `json:"model"`
	Scores     []json.RawMessage `json:"scores"`
	Batches    int               `json:"batches"`
	DurationNS int64             `json:"duration_ns"`
}

func queryRerankOutput(result *workflows.RAGResult, required bool) (*queryRerankJSON, error) {
	if result == nil || result.Evidence == nil {
		return nil, errors.New("query evidence required")
	}
	r := result.Reranking
	if r == nil {
		if required {
			return nil, errors.New("requested reranking missing")
		}
		return nil, nil
	}
	if !required || len(r.Scores) != len(result.Evidence.Items) || !proto.Equal(r.Evidence, result.Evidence) || r.Batches < 0 || r.Duration < 0 {
		return nil, errors.New("reranking accounting mismatch")
	}
	if err := domain.ValidateWire(r.Model, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	raw, err := protojson.Marshal(r.Model)
	if err != nil {
		return nil, err
	}
	out := &queryRerankJSON{Model: raw, Scores: []json.RawMessage{}, Batches: r.Batches, DurationNS: int64(r.Duration)}
	for i, score := range r.Scores {
		if err = domain.ValidateWire(score, domain.DefaultWireLimits); err != nil {
			return nil, err
		}
		if score.PairId != result.Evidence.Items[i].Meta.RecordId || !proto.Equal(score.ModelManifest, r.Model) {
			return nil, errors.New("rerank score identity/model mismatch")
		}
		raw, err = protojson.Marshal(score)
		if err != nil {
			return nil, err
		}
		out.Scores = append(out.Scores, raw)
	}
	return out, nil
}
