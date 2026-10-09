// Validates and serializes the explicit answer mode's existing C01 Answer.
// The production workflow already authenticates evidence/source citation closure;
// this boundary rejects missing drafts, snapshot drift and promoted semantic status.
// Tokens are actual model accounting, never a quality or latency acceptance claim.
package main

import (
	"encoding/json"
	"errors"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/workflows"
)

type queryAnswerJSON struct {
	Answer       json.RawMessage `json:"answer"`
	InputTokens  uint64          `json:"input_tokens"`
	OutputTokens uint64          `json:"output_tokens"`
}

func queryAnswerOutput(result *workflows.RAGResult, enabled bool) (*queryAnswerJSON, error) {
	if !enabled {
		return nil, nil
	}
	if result == nil || result.Evidence == nil || result.Evidence.Meta == nil || result.Answer == nil || result.Answer.Draft == nil || result.Answer.Draft.Answer == nil {
		return nil, errors.New("missing answer draft")
	}
	draft := result.Answer.Draft
	a := draft.Answer
	if err := domain.ValidateWire(a, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	if a.Meta.CorpusId != result.Evidence.Meta.CorpusId || !proto.Equal(a.Snapshot, result.Evidence.Snapshot) || a.CompletionStatus != pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED || (a.SemanticStatus != pb.SemanticStatus_SEMANTIC_STATUS_PARTIAL && a.SemanticStatus != pb.SemanticStatus_SEMANTIC_STATUS_ABSTAIN) {
		return nil, errors.New("answer identity or draft status mismatch")
	}
	for _, claim := range a.Claims {
		if claim.SupportStatus != pb.SupportStatus_SUPPORT_STATUS_UNREVIEWED {
			return nil, errors.New("model claim cannot be promoted to reviewed")
		}
	}
	raw, err := protojson.Marshal(a)
	if err != nil {
		return nil, err
	}
	return &queryAnswerJSON{Answer: raw, InputTokens: draft.InputTokens, OutputTokens: draft.OutputTokens}, nil
}
