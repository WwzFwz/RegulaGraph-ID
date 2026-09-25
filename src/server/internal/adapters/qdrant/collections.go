// Collection admission checks the physical named-vector layout before any X01
// upsert or query. It also admits the snapshot filter's payload indexes before
// serving this store; replica visibility and complete snapshot remain separate
// publication gates. Measure cold ensure time separately from warm query latency.
package qdrant

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
)

type collectionDetails struct {
	PointsCount uint64 `json:"points_count"`
	Config      struct {
		Params struct {
			Vectors map[string]struct {
				Size     uint32 `json:"size"`
				Distance string `json:"distance"`
			} `json:"vectors"`
			SparseVectors map[string]struct {
				Modifier string `json:"modifier"`
			} `json:"sparse_vectors"`
		} `json:"params"`
		Metadata map[string]string `json:"metadata"`
	} `json:"config"`
	PayloadSchema map[string]struct {
		DataType string `json:"data_type"`
		Params   *struct {
			Range *bool `json:"range"`
		} `json:"params"`
	} `json:"payload_schema"`
}

// Index only fields used by the production snapshot filter. In particular,
// nested provision IDs must retain Qdrant's array path syntax.
var requiredPayloadIndexes = []struct{ field, kind string }{
	{"corpus_id", "keyword"},
	{"generation_id", "keyword"},
	{"from_seq", "integer"},
	{"to_seq", "integer"},
	{"provision_filters[].provision_version_id", "keyword"},
}

// EnsureCollection creates a fresh physical family or verifies an existing
// one. A different generation cannot silently reuse the same collection.
func (store *Store) EnsureCollection(ctx context.Context) error {
	if store == nil {
		return errors.New("qdrant store is required")
	}
	store.ensureMu.Lock()
	defer store.ensureMu.Unlock()
	store.ready.Store(false)
	path := "/collections/" + store.collection
	var details collectionDetails
	status, err := store.call(ctx, http.MethodGet, path, nil, &details)
	if err != nil {
		return err
	}
	if status == http.StatusNotFound {
		input := map[string]any{
			"vectors": map[string]any{"dense": map[string]any{
				"size": store.binding.Generation.DenseManifest.GetDimensions(), "distance": "Cosine"}},
			"sparse_vectors": map[string]any{"bm25": map[string]any{}},
			"metadata": map[string]string{
				"regulagraph_corpus_id":     store.binding.CorpusID,
				"regulagraph_generation_id": store.binding.Generation.Meta.RecordId,
				"regulagraph_filter_format": "PAIRED_PROVISION_V1",
			},
		}
		var created bool
		if _, err := store.call(ctx, http.MethodPut, path, input, &created); err != nil {
			return err
		}
		if !created {
			return errors.New("qdrant collection create was not acknowledged")
		}
		if _, err := store.call(ctx, http.MethodGet, path, nil, &details); err != nil {
			return err
		}
	}
	params := details.Config.Params
	dense, denseOK := params.Vectors["dense"]
	sparse, sparseOK := params.SparseVectors["bm25"]
	meta := details.Config.Metadata
	if !denseOK || !sparseOK || len(params.Vectors) != 1 || len(params.SparseVectors) != 1 ||
		dense.Size != store.binding.Generation.DenseManifest.GetDimensions() ||
		dense.Distance != "Cosine" || (sparse.Modifier != "" && sparse.Modifier != "none") ||
		meta["regulagraph_corpus_id"] != store.binding.CorpusID ||
		meta["regulagraph_generation_id"] != store.binding.Generation.Meta.RecordId ||
		meta["regulagraph_filter_format"] != "PAIRED_PROVISION_V1" {
		return fmt.Errorf("qdrant collection %q representation binding mismatch", store.collection)
	}
	missing := make([]struct{ field, kind string }, 0, len(requiredPayloadIndexes))
	for _, index := range requiredPayloadIndexes {
		if actual, ok := details.PayloadSchema[index.field]; ok {
			if actual.DataType != index.kind || index.kind == "integer" &&
				actual.Params != nil && actual.Params.Range != nil && !*actual.Params.Range {
				return fmt.Errorf("qdrant payload index %q has incompatible type", index.field)
			}
		} else {
			missing = append(missing, index)
		}
	}
	// Qdrant builds filter-aware HNSW edges when payload indexes precede data.
	// Repairing an already-populated collection requires an explicit rebuild
	// workflow rather than silently admitting degraded query latency.
	if len(missing) > 0 && details.PointsCount != 0 {
		return errors.New("qdrant populated collection lacks required payload indexes")
	}
	for _, index := range missing {
		var result struct {
			Status string `json:"status"`
		}
		_, err := store.call(ctx, http.MethodPut, path+"/index?wait=true&ordering=strong",
			map[string]string{"field_name": index.field, "field_schema": index.kind}, &result)
		if err != nil {
			return fmt.Errorf("qdrant payload index %q failed: %w", index.field, err)
		}
		if result.Status != "completed" {
			return fmt.Errorf("qdrant payload index %q was not completed", index.field)
		}
	}
	if len(missing) > 0 {
		verifiedLayout := details.Config
		details = collectionDetails{}
		if _, err := store.call(ctx, http.MethodGet, path, nil, &details); err != nil {
			return err
		}
		if !reflect.DeepEqual(details.Config, verifiedLayout) {
			return errors.New("qdrant collection layout changed during payload indexing")
		}
		for _, index := range requiredPayloadIndexes {
			actual, ok := details.PayloadSchema[index.field]
			if !ok || actual.DataType != index.kind || index.kind == "integer" &&
				actual.Params != nil && actual.Params.Range != nil && !*actual.Params.Range {
				return fmt.Errorf("qdrant payload index %q is not visible after completion", index.field)
			}
		}
	}
	store.ready.Store(true)
	return nil
}

func (store *Store) requireReady() error {
	if store == nil || !store.ready.Load() {
		return errors.New("qdrant collection layout has not been verified")
	}
	return nil
}
