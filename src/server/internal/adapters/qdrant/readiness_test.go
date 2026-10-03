// Rejects unsupported serving topology/health and malformed exact counts before
// readiness can be reported. HTTP fixtures supplement the live single-node test;
// they are not a multi-replica durability or search-quality benchmark.
package qdrant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInitialServingRejectsUnprovedRoute(t *testing.T) {
	for _, scenario := range []string{"two shards", "two replicas", "weak write", "unhealthy", "optimizer error", "missing count", "extra point", "empty dense route"} {
		t.Run(scenario, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodGet {
					var reply map[string]any
					if err := json.Unmarshal([]byte(collectionReply()), &reply); err != nil {
						t.Error(err)
						return
					}
					result := reply["result"].(map[string]any)
					result["status"] = "green"
					result["optimizer_status"] = "ok"
					params := result["config"].(map[string]any)["params"].(map[string]any)
					params["shard_number"], params["replication_factor"], params["write_consistency_factor"] = 1, 1, 1
					switch scenario {
					case "two shards":
						params["shard_number"] = 2
					case "two replicas":
						params["replication_factor"] = 2
					case "weak write":
						params["write_consistency_factor"] = 0
					case "unhealthy":
						result["status"] = "red"
					case "optimizer error":
						result["optimizer_status"] = map[string]any{"error": "failed"}
					}
					_ = json.NewEncoder(w).Encode(reply)
					return
				}
				if r.URL.Path == "/collections/index_one/points/count" {
					var input struct {
						Exact bool `json:"exact"`
					}
					if err := json.NewDecoder(r.Body).Decode(&input); err != nil || !input.Exact || r.URL.Query().Get("consistency") != "all" {
						t.Error("count lacks exact/all policy")
					}
					result := map[string]any{"count": 1}
					if scenario == "missing count" {
						delete(result, "count")
					}
					if scenario == "extra point" {
						result["count"] = 2
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "result": result})
					return
				}
				_, _ = w.Write([]byte(`{"status":"ok","result":{"points":[]}}`))
			}))
			defer server.Close()
			binding, point := qdrantFixture()
			store, err := New(server.URL, "", server.Client(), binding)
			if err != nil {
				t.Fatal(err)
			}
			if err = store.EnsureCollection(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err = store.VerifyInitialServing(context.Background(), 1, 7, point); err == nil {
				t.Fatal("unproved route became ready")
			}
		})
	}
}
