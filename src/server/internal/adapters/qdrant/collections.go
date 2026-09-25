// Collection admission checks the physical named-vector layout before any X01
// upsert or query. It does not prove payload indexes, replica visibility, or a
// complete snapshot; those remain publication/readiness gates. Measure cold
// ensure time separately from warm query latency.
package qdrant

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

type collectionDetails struct {
	Config struct {
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
	store.ready.Store(true)
	return nil
}

func (store *Store) requireReady() error {
	if store == nil || !store.ready.Load() {
		return errors.New("qdrant collection layout has not been verified")
	}
	return nil
}
