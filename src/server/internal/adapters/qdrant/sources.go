// Locates candidate chunks by exact paired source/version/regulation membership
// without embedding or ranking. Corpus/generation/visibility remain pinned.
// A bounded scroll must be exhausted: overflow is explicit, never silently loses
// support text. Payload is discovery metadata only; PostgreSQL/source hydration
// authenticates returned chunks. Measure matching fan-out, bytes and p95/p99 under
// required retrieval gates; transport already caps response bytes globally.
package qdrant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"

	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

var ErrSourceLookupBudget = errors.New("source chunk lookup exceeds candidate budget")

func (store *Store) FindSourceChunks(ctx context.Context, sources []*pb.SourceVersionRef, scope SearchScope) ([]Hit, error) {
	if err := store.requireReady(); err != nil {
		return nil, err
	}
	if ctx == nil || len(sources) < 1 || len(sources) > 128 || scope.Limit < 1 || scope.Limit > 256 || scope.SnapshotSeq == 0 || scope.SnapshotSeq > maxExactFilterSequence || scope.ProvisionVersionID != "" {
		return nil, errors.New("bounded paired source lookup and snapshot required")
	}
	match := func(key, value string) any {
		return map[string]any{"key": key, "match": map[string]any{"value": value}}
	}
	seen := map[string]bool{}
	var choices []any
	for _, source := range sources {
		if source == nil || domain.ValidateWire(source, domain.DefaultWireLimits) != nil {
			return nil, errors.New("invalid source lookup identity")
		}
		key := source.SourceBlobId + "\x00" + source.ProvisionVersionId + "\x00" + source.RegulationId
		if seen[key] {
			continue
		}
		seen[key] = true
		choices = append(choices, map[string]any{"nested": map[string]any{"key": "provision_filters", "filter": map[string]any{"must": []any{match("source_blob_id", source.SourceBlobId), match("provision_version_id", source.ProvisionVersionId), match("regulation_id", source.RegulationId)}}}})
	}
	request := map[string]any{"limit": scope.Limit + 1, "with_payload": true, "with_vector": false,
		"filter": map[string]any{"must": []any{match("corpus_id", store.binding.CorpusID), match("generation_id", store.binding.Generation.Meta.RecordId), map[string]any{"key": "from_seq", "range": map[string]any{"lte": scope.SnapshotSeq}}}, "should": choices, "must_not": []any{map[string]any{"key": "to_seq", "range": map[string]any{"lte": scope.SnapshotSeq}}}}}
	var result struct {
		Points json.RawMessage `json:"points"`
		Next   json.RawMessage `json:"next_page_offset"`
	}
	status, err := store.call(ctx, http.MethodPost, "/collections/"+store.collection+"/points/scroll?consistency=all", request, &result)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound || len(result.Points) == 0 || string(result.Points) == "null" {
		return nil, errors.New("source lookup lacks points array")
	}
	var points []searchPoint
	if err = json.Unmarshal(result.Points, &points); err != nil || points == nil || len(points) > scope.Limit+1 {
		return nil, errors.New("invalid source lookup response")
	}
	if len(points) > scope.Limit || (len(result.Next) != 0 && string(result.Next) != "null") {
		return nil, ErrSourceLookupBudget
	}
	for i := range points {
		zero := 0.0
		points[i].Score = &zero
		matched := false
		for _, f := range points[i].Payload.ProvisionFilters {
			matched = matched || seen[f.SourceBlobID+"\x00"+f.VersionID+"\x00"+f.RegulationID]
		}
		if !matched {
			return nil, errors.New("source lookup returned unrequested source/version pair")
		}
	}
	hits, err := store.decodeHits(points, scope)
	if err != nil {
		return nil, err
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].RecordID < hits[j].RecordID })
	return hits, nil
}
