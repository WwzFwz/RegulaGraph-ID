// Proves the supported single-shard/single-replica serving route after full
// exact-ID readback. An exact count rejects extra points; dense and BM25 probes
// exercise the same production snapshot filters. This is backend readiness, not
// Recall@k or proof of legal relevance. The caller serializes all writes and owns
// the collection namespace. Measure publication overhead separately from query
// latency under configs/benchmark-targets.yaml (REQUIRED_UNMEASURED).
package qdrant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func (store *Store) Endpoint() string {
	if store == nil {
		return ""
	}
	return store.baseURL
}

func (store *Store) VerifyInitialServing(ctx context.Context, count uint64, sequence uint64, probe Point) error {
	return store.verifyServing(ctx, count, sequence, probe, true)
}

// VerifyInheritedServing refreshes an existing route without bootstrap or writes.
// Full exact point readback is a caller prerequisite; a count/probe is insufficient.
func (store *Store) VerifyInheritedServing(ctx context.Context, count uint64, sequence uint64, probe Point) error {
	return store.verifyServing(ctx, count, sequence, probe, false)
}

func (store *Store) verifyServing(ctx context.Context, count uint64, sequence uint64, probe Point, initial bool) error {
	if err := store.requireReady(); err != nil {
		return err
	}
	if count == 0 || probe.Record == nil || probe.Record.DenseVector == nil || probe.Record.SparseVector == nil || len(probe.Record.SparseVector.Indices) == 0 {
		return errors.New("readiness requires expected points and a complete probe")
	}
	if sequence == 0 || sequence > maxExactFilterSequence {
		return errors.New("readiness snapshot sequence outside exact filter range")
	}
	if _, err := store.projectPoint(probe); err != nil {
		return err
	}
	if probe.Record.Meta.Visibility.FromSeq > sequence || initial && probe.Record.Meta.Visibility.FromSeq != sequence || probe.Record.Meta.Visibility.ToSeq != nil {
		return errors.New("initial readiness probe must start at the target with open visibility")
	}
	// Refresh physical layout and payload-index checks; cached bootstrap success
	// does not prove that an operator has not changed the collection meanwhile.
	if initial {
		if err := store.EnsureCollection(ctx); err != nil {
			return err
		}
	} else if err := store.OpenExistingCollection(ctx); err != nil {
		return err
	}
	var info struct {
		Status    string          `json:"status"`
		Optimizer json.RawMessage `json:"optimizer_status"`
		Config    struct {
			Params struct {
				Shards           uint64 `json:"shard_number"`
				Replicas         uint64 `json:"replication_factor"`
				WriteConsistency uint64 `json:"write_consistency_factor"`
			} `json:"params"`
		} `json:"config"`
	}
	code, err := store.call(ctx, http.MethodGet, "/collections/"+store.collection, nil, &info)
	if err != nil {
		return err
	}
	if code == http.StatusNotFound || info.Status != "green" || string(info.Optimizer) != `"ok"` || info.Config.Params.Shards != 1 || info.Config.Params.Replicas != 1 || info.Config.Params.WriteConsistency != 1 {
		return errors.New("initial writer requires a healthy single-shard, single-replica route")
	}
	var result struct {
		Count *uint64 `json:"count"`
	}
	code, err = store.call(ctx, http.MethodPost, "/collections/"+store.collection+"/points/count?consistency=all", map[string]any{"exact": true}, &result)
	if err != nil {
		return err
	}
	if code == http.StatusNotFound || result.Count == nil || *result.Count != count {
		return errors.New("collection exact count differs from complete point manifest")
	}
	scope := SearchScope{SnapshotSeq: sequence, Limit: 1}
	dense, err := store.SearchDense(ctx, probe.Record.DenseVector.Values, scope)
	if err != nil {
		return err
	}
	if len(dense) == 0 {
		return errors.New("dense production route returned no visible point")
	}
	sparse, err := store.SearchSparse(ctx, &pb.SparseVector{Indices: []uint32{probe.Record.SparseVector.Indices[0]}, Values: []float32{1}}, scope)
	if err != nil {
		return err
	}
	if len(sparse) == 0 {
		return errors.New("BM25 production route returned no visible point")
	}
	return nil
}
