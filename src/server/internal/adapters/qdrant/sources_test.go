// Exercises source lookup wire shape and rejection of cross-paired references,
// pagination overflow, missing arrays and stale snapshots. HTTP fixtures verify
// boundaries; real Qdrant coverage is in the published native graph integration.
package qdrant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func TestSourceChunkLookup(t *testing.T) {
	for _, scenario := range []string{"valid", "wrong_source", "wrong_version", "wrong_regulation", "future", "overflow", "missing_array"} {
		t.Run(scenario, func(t *testing.T) {
			binding, point := qdrantFixture()
			projection, err := (&Store{binding: binding}).projectPoint(point)
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/collections/index_one/points/scroll" || r.URL.Query().Get("consistency") != "all" {
					t.Error("wrong source lookup route")
				}
				var body map[string]any
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					t.Error("request shape")
				}
				if body["limit"] != float64(3) || body["with_vector"] != false {
					t.Error("unbounded or vector-bearing lookup")
				}
				filter := body["filter"].(map[string]any)
				choices := filter["should"].([]any)
				nested := choices[0].(map[string]any)["nested"].(map[string]any)
				if nested["key"] != "provision_filters" || len(nested["filter"].(map[string]any)["must"].([]any)) != 3 || len(filter["must"].([]any)) != 3 || len(filter["must_not"].([]any)) != 1 {
					t.Error("source pairs or snapshot unbound")
				}
				var next any
				switch scenario {
				case "wrong_source":
					projection.Payload.ProvisionFilters[0].SourceBlobID = "blob:other"
				case "wrong_version":
					projection.Payload.ProvisionFilters[0].VersionID = "version:other"
				case "wrong_regulation":
					projection.Payload.ProvisionFilters[0].RegulationID = "reg:other"
				case "future":
					projection.Payload.FromSeq = 8
				case "overflow":
					next = testPointID
				}
				result := map[string]any{"points": []any{map[string]any{"id": point.ID, "payload": projection.Payload}}, "next_page_offset": next}
				if scenario == "missing_array" {
					delete(result, "points")
				}
				json.NewEncoder(w).Encode(map[string]any{"status": "ok", "result": result})
			}))
			defer server.Close()
			store, err := New(server.URL, "", server.Client(), binding)
			if err != nil {
				t.Fatal(err)
			}
			store.ready.Store(true)
			hits, err := store.FindSourceChunks(context.Background(), []*pb.SourceVersionRef{{SourceBlobId: "blob:one", ProvisionVersionId: "version:one", RegulationId: "regulation:one"}}, SearchScope{SnapshotSeq: 7, Limit: 2})
			if scenario == "valid" {
				if err != nil || len(hits) != 1 || hits[0].RecordID != point.Record.Meta.RecordId {
					t.Fatal(hits, err)
				}
			} else if err == nil || hits != nil {
				t.Fatal("bad source lookup accepted", hits)
			}
		})
	}
}
