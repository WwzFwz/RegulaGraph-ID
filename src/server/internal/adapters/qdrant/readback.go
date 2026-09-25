// Readback verifies a bounded set of point IDs, legal payloads and vector
// content via Qdrant's exact ID retrieval path. It is one prerequisite for
// publication, not proof that every query replica or ANN route has converged.
// Measure readback latency/bytes and missing-point rate per benchmark policy.
package qdrant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"reflect"
	"strings"

	"google.golang.org/protobuf/proto"
)

// VerifyPoints checks all requested IDs without top-k approximation. A caller
// paginates the full expected point manifest and pins the same backend route
// and consistency policy that later queries can use.
func (store *Store) VerifyPoints(ctx context.Context, expected []Point) error {
	if err := store.requireReady(); err != nil {
		return err
	}
	if len(expected) == 0 || len(expected) > 64 {
		return errors.New("readback point count outside budget")
	}
	byID := make(map[string]qdrantPoint, len(expected))
	ids := make([]string, 0, len(expected))
	budget := 0
	for _, point := range expected {
		if point.Record == nil || !pointUUID.MatchString(point.ID) {
			return errors.New("invalid readback point")
		}
		budget += proto.Size(point.Record)
		if budget > 512<<10 {
			return errors.New("readback byte budget exceeded")
		}
		id := strings.ToLower(point.ID)
		if _, duplicate := byID[id]; duplicate {
			return errors.New("duplicate readback ID")
		}
		projected, err := store.projectPoint(point)
		if err != nil {
			return err
		}
		byID[id] = projected
		ids = append(ids, point.ID)
	}
	request := map[string]any{"ids": ids, "with_payload": true, "with_vector": true}
	var raw json.RawMessage
	status, err := store.call(ctx, http.MethodPost,
		"/collections/"+store.collection+"/points?consistency=all", request, &raw)
	if err != nil {
		return err
	}
	if status == http.StatusNotFound || len(raw) == 0 || string(raw) == "null" {
		return errors.New("readback result absent")
	}
	var returned []struct {
		ID      string                     `json:"id"`
		Payload json.RawMessage            `json:"payload"`
		Vector  map[string]json.RawMessage `json:"vector"`
	}
	if json.Unmarshal(raw, &returned) != nil || len(returned) != len(expected) {
		return errors.New("readback point set incomplete")
	}
	seen := make(map[string]bool, len(returned))
	for _, actual := range returned {
		id := strings.ToLower(actual.ID)
		want, found := byID[id]
		if !pointUUID.MatchString(actual.ID) || !found || seen[id] {
			return errors.New("readback returned foreign or duplicate point")
		}
		seen[id] = true
		if !equalJSON(want.Payload, actual.Payload) {
			return errors.New("readback legal payload mismatch")
		}
		if len(actual.Vector) != 2 || !verifyDense(want.Vector["dense"].([]float32), actual.Vector["dense"]) ||
			!verifySparse(want.Vector["bm25"], actual.Vector["bm25"]) {
			return errors.New("readback vector mismatch")
		}
	}
	return nil
}

func equalJSON(expected any, actual json.RawMessage) bool {
	wantBytes, err := json.Marshal(expected)
	if err != nil {
		return false
	}
	var want, got any
	return decodeExactJSON(wantBytes, &want) &&
		decodeExactJSON(actual, &got) && reflect.DeepEqual(want, got)
}

func decodeExactJSON(data []byte, result *any) bool {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if decoder.Decode(result) != nil {
		return false
	}
	var trailing any
	return decoder.Decode(&trailing) == io.EOF
}

func verifyDense(expected []float32, actual json.RawMessage) bool {
	var coordinates []json.RawMessage
	if json.Unmarshal(actual, &coordinates) != nil || len(coordinates) != len(expected) {
		return false
	}
	norm := 0.0
	for _, value := range expected {
		norm += float64(value) * float64(value)
	}
	if norm <= 0 || math.IsInf(norm, 0) {
		return false
	}
	norm = math.Sqrt(norm)
	for i, value := range expected {
		var got *float64
		if json.Unmarshal(coordinates[i], &got) != nil || got == nil || math.IsNaN(*got) || math.IsInf(*got, 0) ||
			math.Abs(*got-float64(value)/norm) > 1e-5 {
			return false
		}
	}
	return true
}

func verifySparse(expected any, actual json.RawMessage) bool {
	want, ok := expected.(map[string]any)
	if !ok {
		return false
	}
	indices, ok := want["indices"].([]uint32)
	if !ok {
		return false
	}
	values, ok := want["values"].([]float32)
	if !ok {
		return false
	}
	var got struct {
		Indices []uint32  `json:"indices"`
		Values  []float64 `json:"values"`
	}
	if json.Unmarshal(actual, &got) != nil || len(got.Indices) != len(indices) || len(got.Values) != len(values) {
		return false
	}
	for i := range indices {
		if got.Indices[i] != indices[i] || math.IsNaN(got.Values[i]) || math.IsInf(got.Values[i], 0) ||
			float32(got.Values[i]) != values[i] {
			return false
		}
	}
	return true
}
