// Readback tests cover exact-ID verification rather than approximate ANN
// search. Fixtures model Qdrant's cosine normalization on upload; a live
// multi-replica readiness test remains necessary before publication.
package qdrant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestVerifyPointsChecksExactPayloadAndBothVectors(t *testing.T) {
	var scenario atomic.Int32
	var expected qdrantPoint
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(collectionReply()))
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/collections/index_one/points" ||
			r.URL.Query().Get("consistency") != "all" {
			t.Errorf("unexpected readback request: %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var input struct {
			IDs         []string `json:"ids"`
			WithPayload bool     `json:"with_payload"`
			WithVector  bool     `json:"with_vector"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if len(input.IDs) != 1 || input.IDs[0] != testPointID || !input.WithPayload || !input.WithVector {
			t.Error("readback request lacks expected IDs or data")
		}
		payload := expected.Payload
		sparse := expected.Vector["bm25"]
		if scenario.Load() == 1 {
			payload.ChunkID = "chunk:other"
		}
		if scenario.Load() == 2 {
			sparse = map[string]any{"indices": []uint32{2}, "values": []float32{99}}
		}
		result := []any{map[string]any{"id": testPointID, "payload": payload,
			"vector": map[string]any{"dense": []float64{0.242535625, 0.9701425}, "bm25": sparse}}}
		if scenario.Load() == 3 {
			result = []any{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "result": result})
	}))
	defer server.Close()
	binding, point := qdrantFixture()
	store, err := New(server.URL, "", server.Client(), binding)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureCollection(context.Background()); err != nil {
		t.Fatal(err)
	}
	expected, err = store.projectPoint(point)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.VerifyPoints(context.Background(), []Point{point}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name     string
		scenario int32
	}{
		{"wrong payload", 1}, {"wrong sparse vector", 2}, {"missing point", 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			scenario.Store(test.scenario)
			if err := store.VerifyPoints(context.Background(), []Point{point}); err == nil {
				t.Fatal("corrupt or absent point accepted")
			}
		})
	}
}

func TestReadbackRejectsNullDenseCoordinate(t *testing.T) {
	if verifyDense([]float32{1, 0}, json.RawMessage(`[1,null]`)) {
		t.Fatal("null coordinate was decoded as zero")
	}
}

func TestReadbackPreservesExactSnapshotNumber(t *testing.T) {
	if equalJSON(map[string]any{"from_seq": uint64(maxExactFilterSequence)},
		json.RawMessage(`{"from_seq":9007199254740990.6}`)) {
		t.Fatal("fractional visibility was rounded into a valid integer")
	}
}
