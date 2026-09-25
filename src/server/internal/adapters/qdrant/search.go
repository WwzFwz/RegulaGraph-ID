// Search performs one snapshot-filtered branch query against a verified
// generation. Backend hits are rechecked before they become candidates;
// legal-effective-date and multi-version completeness are checked after
// hydration by the retrieval owner. Measure each branch's p95/p99, candidate
// count and false exclusions against configs/benchmark-targets.yaml.
package qdrant

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type SearchScope struct {
	SnapshotSeq        uint64
	ProvisionVersionID string // Empty means any paired version in the chunk.
	Limit              int
}

type Hit struct {
	PointID             string
	RecordID            string
	ChunkID             string
	ProvisionVersionIDs []string
	Score               float64
}

// SearchDense uses the model-bound named vector. A typed mismatch fails before
// network I/O instead of silently searching an incompatible representation.
func (store *Store) SearchDense(ctx context.Context, vector []float32, scope SearchScope) ([]Hit, error) {
	if store == nil || len(vector) != int(store.binding.Generation.DenseManifest.GetDimensions()) || !finiteVector(vector) {
		return nil, errors.New("dense query dimension or value mismatch")
	}
	return store.search(ctx, "dense", vector, scope)
}

// SearchSparse accepts already encoded frozen-BM25 query weights. Empty query
// vectors must be handled by the retrieval planner without a backend request.
func (store *Store) SearchSparse(ctx context.Context, vector *pb.SparseVector, scope SearchScope) ([]Hit, error) {
	if vector == nil || len(vector.Indices) == 0 || len(vector.Indices) > 16384 ||
		len(vector.Indices) != len(vector.Values) ||
		!finiteVector(vector.Values) {
		return nil, errors.New("sparse query shape or value mismatch")
	}
	for i, index := range vector.Indices {
		if index == 0 || vector.Values[i] <= 0 || i > 0 && index <= vector.Indices[i-1] {
			return nil, errors.New("sparse query indices must be positive and sorted")
		}
	}
	return store.search(ctx, "bm25", map[string]any{"indices": vector.Indices, "values": vector.Values}, scope)
}

func finiteVector(values []float32) bool {
	for _, value := range values {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return false
		}
	}
	return true
}

func (store *Store) search(ctx context.Context, using string, vector any, scope SearchScope) ([]Hit, error) {
	if err := store.requireReady(); err != nil {
		return nil, err
	}
	if scope.SnapshotSeq == 0 || scope.SnapshotSeq > maxExactFilterSequence ||
		scope.Limit <= 0 || scope.Limit > 1000 ||
		(scope.ProvisionVersionID != "" && !asciiID(scope.ProvisionVersionID)) {
		return nil, errors.New("snapshot sequence and bounded query limit are required")
	}
	must := []any{
		map[string]any{"key": "corpus_id", "match": map[string]any{"value": store.binding.CorpusID}},
		map[string]any{"key": "generation_id", "match": map[string]any{"value": store.binding.Generation.Meta.RecordId}},
		map[string]any{"key": "from_seq", "range": map[string]any{"lte": scope.SnapshotSeq}},
	}
	if scope.ProvisionVersionID != "" {
		must = append(must, map[string]any{"nested": map[string]any{
			"key": "provision_filters", "filter": map[string]any{"must": []any{
				map[string]any{"key": "provision_version_id", "match": map[string]any{"value": scope.ProvisionVersionID}},
			}},
		}})
	}
	request := map[string]any{
		"query": vector, "using": using, "limit": scope.Limit,
		"with_payload": true, "with_vector": false,
		"filter": map[string]any{"must": must, "must_not": []any{
			map[string]any{"key": "to_seq", "range": map[string]any{"lte": scope.SnapshotSeq}},
		}},
	}
	var result struct {
		Points json.RawMessage `json:"points"`
	}
	status, err := store.call(ctx, http.MethodPost,
		"/collections/"+store.collection+"/points/query?consistency=all", request, &result)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound || len(result.Points) == 0 || string(result.Points) == "null" {
		return nil, errors.New("qdrant query lacks a points array")
	}
	var points []struct {
		ID      string       `json:"id"`
		Score   *float64     `json:"score"`
		Payload pointPayload `json:"payload"`
	}
	if err := json.Unmarshal(result.Points, &points); err != nil || points == nil || len(points) > scope.Limit {
		return nil, errors.New("qdrant query collection missing or result exceeds limit")
	}
	hits := make([]Hit, 0, len(points))
	seen := make(map[string]bool, len(points))
	for _, point := range points {
		payload := point.Payload
		canonicalID := strings.ToLower(point.ID)
		if !pointUUID.MatchString(point.ID) || seen[canonicalID] || point.Score == nil ||
			math.IsNaN(*point.Score) || math.IsInf(*point.Score, 0) ||
			payload.CorpusID != store.binding.CorpusID ||
			payload.GenerationID != store.binding.Generation.Meta.RecordId ||
			!asciiID(payload.RecordID) || !asciiID(payload.ChunkID) ||
			payload.FromSeq == 0 || payload.FromSeq > scope.SnapshotSeq ||
			payload.ToSeq != nil && (*payload.ToSeq <= scope.SnapshotSeq || *payload.ToSeq > maxExactFilterSequence) {
			return nil, errors.New("qdrant returned an untrusted or out-of-snapshot hit")
		}
		seen[canonicalID] = true
		versions := make([]string, 0, len(payload.ProvisionFilters))
		pairs := make(map[string]bool, len(payload.ProvisionFilters))
		versionMatched := scope.ProvisionVersionID == ""
		for _, filter := range payload.ProvisionFilters {
			interval := new(pb.LegalInterval)
			status, known := pb.LegalStatus_value[filter.LegalStatus]
			pairKey := filter.VersionID + "\x00" + filter.SourceBlobID
			if !known || pairs[pairKey] ||
				protojson.Unmarshal(filter.LegalInterval, interval) != nil ||
				domain.ValidateWire(&pb.IndexProvisionFilter{
					ProvisionVersionId: filter.VersionID, RegulationId: filter.RegulationID,
					SourceBlobId: filter.SourceBlobID, Jurisdiction: filter.Jurisdiction,
					LegalStatus: pb.LegalStatus(status), LegalInterval: interval,
				}, domain.DefaultWireLimits) != nil {
				return nil, errors.New("qdrant hit has incomplete paired legal evidence")
			}
			pairs[pairKey] = true
			versions = append(versions, filter.VersionID)
			if filter.VersionID == scope.ProvisionVersionID {
				versionMatched = true
			}
		}
		if !versionMatched || len(versions) == 0 {
			return nil, errors.New("qdrant hit lacks requested legal version")
		}
		hits = append(hits, Hit{PointID: point.ID, RecordID: payload.RecordID,
			ChunkID: payload.ChunkID, ProvisionVersionIDs: versions, Score: *point.Score})
	}
	return hits, nil
}

func asciiID(value string) bool {
	if len(value) == 0 || len(value) > 256 {
		return false
	}
	for _, char := range value {
		if char < 33 || char > 126 {
			return false
		}
	}
	return true
}
