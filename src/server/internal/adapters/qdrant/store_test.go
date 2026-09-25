// The transport tests exercise real HTTP request/response shapes and reject
// stale, malformed or incompatible backend observations. They do not replace
// a live Qdrant readiness/fault test or a corpus quality benchmark.
package qdrant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

const testPointID = "b1786f40-3fa9-4b84-bcb9-71b05ad3c289"

func qdrantFixture() (Binding, Point) {
	hash := &pb.ContentHash{Sha256: strings.Repeat("a", 64)}
	artifact := &pb.ArtifactRef{ArtifactId: "artifact:one", ContentHash: hash,
		StorageKey: "objects/a", MediaType: "application/x-protobuf", SchemaVersion: 1}
	generation := &pb.IndexGeneration{
		Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: "corpus:one", RecordId: "generation:one"},
		DenseManifest: &pb.ModelManifest{ModelId: "model:one", Version: "1", WeightsHash: hash,
			TokenizerHash: hash, Task: pb.ModelTask_MODEL_TASK_EMBED, Dimensions: proto.Uint32(2),
			MaxTokens: 512, Precision: "fp32", Backend: "fixture"},
		LexicalAnalyzer:   proto.Clone(artifact).(*pb.ArtifactRef),
		LexicalDictionary: proto.Clone(artifact).(*pb.ArtifactRef),
		LexicalStatistics: proto.Clone(artifact).(*pb.ArtifactRef),
		OntologyVersion:   "ontology:v1",
		FilterFormat:      pb.IndexFilterFormat_INDEX_FILTER_FORMAT_PAIRED_PROVISION_V1,
	}
	visibility := &pb.Visibility{FromSeq: 7}
	interval := &pb.LegalInterval{Start: &pb.DateAssertion{Knowledge: pb.DateKnowledge_DATE_KNOWLEDGE_UNKNOWN},
		End: &pb.DateAssertion{Knowledge: pb.DateKnowledge_DATE_KNOWLEDGE_UNKNOWN}}
	record := &pb.IndexRecord{
		Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: "corpus:one", RecordId: "index:one",
			Visibility: proto.Clone(visibility).(*pb.Visibility)},
		GenerationId: "generation:one", ChunkId: "chunk:one",
		ProvisionVersionRefs: []string{"version:one"},
		DenseVector:          &pb.DenseVector{Values: []float32{0.2, 0.8}, Dimensions: 2, ModelId: "model:one"},
		SparseVector:         &pb.SparseVector{Indices: []uint32{2}, Values: []float32{1.25}},
		FilterMetadata: &pb.FilterMetadata{Visibility: visibility,
			ProvisionFilters: []*pb.IndexProvisionFilter{{ProvisionVersionId: "version:one",
				RegulationId: "regulation:one", SourceBlobId: "blob:one", Jurisdiction: "ID",
				LegalInterval: interval, LegalStatus: pb.LegalStatus_LEGAL_STATUS_ACTIVE}}},
		Dependencies: &pb.DependencyManifest{ArtifactId: "dependency:one", ProducerManifest: &pb.ProducerManifest{
			Software: "fixture", Build: "fixture", SchemaVersion: 1, ConfigHash: hash}},
	}
	return Binding{Collection: "index_one", CorpusID: "corpus:one", Generation: generation}, Point{ID: testPointID, Record: record}
}

func collectionReply() string {
	return `{"status":"ok","result":{"config":{"params":{"vectors":{"dense":{"size":2,"distance":"Cosine"}},"sparse_vectors":{"bm25":{}}},"metadata":{"regulagraph_corpus_id":"corpus:one","regulagraph_generation_id":"generation:one","regulagraph_filter_format":"PAIRED_PROVISION_V1"}},"payload_schema":{"corpus_id":{"data_type":"keyword"},"generation_id":{"data_type":"keyword"},"from_seq":{"data_type":"integer"},"to_seq":{"data_type":"integer"},"provision_filters[].provision_version_id":{"data_type":"keyword"}}}}`
}

func TestStoreCreatesMissingPayloadIndexesBeforeReadiness(t *testing.T) {
	indexed := map[string]string{}
	reads := 0
	created := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/collections/index_one":
			if !created {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			reads++
			if reads == 1 {
				_, _ = w.Write([]byte(strings.Replace(collectionReply(), `"payload_schema":{"corpus_id":{"data_type":"keyword"},"generation_id":{"data_type":"keyword"},"from_seq":{"data_type":"integer"},"to_seq":{"data_type":"integer"},"provision_filters[].provision_version_id":{"data_type":"keyword"}}`, `"payload_schema":{}`, 1)))
			} else {
				_, _ = w.Write([]byte(collectionReply()))
			}
		case r.Method == http.MethodPut && r.URL.Path == "/collections/index_one":
			created = true
			_, _ = w.Write([]byte(`{"status":"ok","result":true}`))
		case r.Method == http.MethodPut && r.URL.Path == "/collections/index_one/index":
			if r.URL.Query().Get("wait") != "true" || r.URL.Query().Get("ordering") != "strong" {
				t.Error("payload index creation lacks completion/ordering")
			}
			var body struct {
				FieldName   string `json:"field_name"`
				FieldSchema string `json:"field_schema"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			indexed[body.FieldName] = body.FieldSchema
			_, _ = w.Write([]byte(`{"status":"ok","result":{"status":"completed","operation_id":1}}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	binding, _ := qdrantFixture()
	store, err := New(server.URL, "", server.Client(), binding)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureCollection(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !store.ready.Load() || reads != 2 || len(indexed) != len(requiredPayloadIndexes) {
		t.Fatalf("indexes=%v reads=%d ready=%v", indexed, reads, store.ready.Load())
	}
	for _, index := range requiredPayloadIndexes {
		if indexed[index.field] != index.kind {
			t.Fatalf("missing index %q", index.field)
		}
	}
}

func TestStoreRejectsWrongPayloadIndexType(t *testing.T) {
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			writes++
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(strings.Replace(collectionReply(), `"from_seq":{"data_type":"integer"}`, `"from_seq":{"data_type":"keyword"}`, 1)))
	}))
	defer server.Close()
	binding, _ := qdrantFixture()
	store, err := New(server.URL, "", server.Client(), binding)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureCollection(context.Background()); err == nil || store.ready.Load() || writes != 0 {
		t.Fatalf("incompatible index admitted: err=%v ready=%v writes=%d", err, store.ready.Load(), writes)
	}
}

func TestStoreRejectsIntegerIndexWithoutRangeSupport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(strings.Replace(collectionReply(),
			`"from_seq":{"data_type":"integer"}`,
			`"from_seq":{"data_type":"integer","params":{"type":"integer","lookup":true,"range":false}}`, 1)))
	}))
	defer server.Close()
	binding, _ := qdrantFixture()
	store, err := New(server.URL, "", server.Client(), binding)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureCollection(context.Background()); err == nil || store.ready.Load() {
		t.Fatalf("range-disabled snapshot index admitted: err=%v ready=%v", err, store.ready.Load())
	}
}

func TestStoreAcceptsIntegerIndexWithImplicitRangeDefault(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(strings.Replace(collectionReply(),
			`"from_seq":{"data_type":"integer"}`,
			`"from_seq":{"data_type":"integer","params":{"type":"integer"}}`, 1)))
	}))
	defer server.Close()
	binding, _ := qdrantFixture()
	store, err := New(server.URL, "", server.Client(), binding)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureCollection(context.Background()); err != nil || !store.ready.Load() {
		t.Fatalf("default range-capable index rejected: err=%v ready=%v", err, store.ready.Load())
	}
}

func TestStoreRejectsUnobservedPayloadIndexCompletion(t *testing.T) {
	created := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			if !created {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(strings.Replace(collectionReply(), `"payload_schema":{"corpus_id":{"data_type":"keyword"},"generation_id":{"data_type":"keyword"},"from_seq":{"data_type":"integer"},"to_seq":{"data_type":"integer"},"provision_filters[].provision_version_id":{"data_type":"keyword"}}`, `"payload_schema":{}`, 1)))
			return
		}
		if r.URL.Path == "/collections/index_one" {
			created = true
			_, _ = w.Write([]byte(`{"status":"ok","result":true}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"ok","result":{"status":"completed","operation_id":1}}`))
	}))
	defer server.Close()
	binding, _ := qdrantFixture()
	store, err := New(server.URL, "", server.Client(), binding)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureCollection(context.Background()); err == nil || store.ready.Load() {
		t.Fatalf("missing index incorrectly admitted: err=%v ready=%v", err, store.ready.Load())
	}
}

func TestStoreRefusesPayloadIndexRepairOnExistingCollection(t *testing.T) {
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			writes++
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		body := strings.Replace(collectionReply(),
			`"payload_schema":{"corpus_id":{"data_type":"keyword"},"generation_id":{"data_type":"keyword"},"from_seq":{"data_type":"integer"},"to_seq":{"data_type":"integer"},"provision_filters[].provision_version_id":{"data_type":"keyword"}}`,
			`"payload_schema":{}`, 1)
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	binding, _ := qdrantFixture()
	store, err := New(server.URL, "", server.Client(), binding)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureCollection(context.Background()); err == nil || store.ready.Load() || writes != 0 {
		t.Fatalf("late payload indexing admitted: err=%v ready=%v writes=%d", err, store.ready.Load(), writes)
	}
}

func TestStoreCreateUpsertAndSnapshotQuery(t *testing.T) {
	created, wrote, queried := false, false, false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("api-key") != "fixture-key" {
			t.Error("API key missing")
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/collections/index_one":
			if !created {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(collectionReply()))
		case r.Method == http.MethodPut && r.URL.Path == "/collections/index_one":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["sparse_vectors"] == nil {
				t.Error("sparse layout absent")
			}
			created = true
			_, _ = w.Write([]byte(`{"status":"ok","result":true}`))
		case r.Method == http.MethodPut && r.URL.Path == "/collections/index_one/points":
			if r.URL.Query().Get("wait") != "true" || r.URL.Query().Get("ordering") != "strong" {
				t.Error("write not strongly acknowledged")
			}
			var body struct {
				Points []struct {
					Payload pointPayload   `json:"payload"`
					Vector  map[string]any `json:"vector"`
				} `json:"points"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if len(body.Points) != 1 || body.Points[0].Payload.ProvisionFilters[0].VersionID != "version:one" ||
				body.Points[0].Vector["bm25"] == nil {
				t.Error("paired/vector payload missing")
			}
			wrote = true
			_, _ = w.Write([]byte(`{"status":"ok","result":{"status":"completed","operation_id":1}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/collections/index_one/points/query":
			if r.URL.Query().Get("consistency") != "all" {
				t.Error("read consistency missing")
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			filter := body["filter"].(map[string]any)
			if len(filter["must"].([]any)) != 4 || len(filter["must_not"].([]any)) != 1 {
				t.Error("snapshot or nested filter missing")
			}
			queried = true
			_, _ = w.Write([]byte(`{"status":"ok","result":{"points":[{"id":"` + testPointID + `","score":0.9,"payload":{"corpus_id":"corpus:one","generation_id":"generation:one","record_id":"index:one","chunk_id":"chunk:one","from_seq":7,"provision_filters":[{"provision_version_id":"version:one","regulation_id":"regulation:one","source_blob_id":"blob:one","jurisdiction":"ID","legal_status":"LEGAL_STATUS_ACTIVE","legal_interval":{"start":{"knowledge":"DATE_KNOWLEDGE_UNKNOWN"},"end":{"knowledge":"DATE_KNOWLEDGE_UNKNOWN"}}}]}}]}}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	binding, point := qdrantFixture()
	store, err := New(server.URL, "fixture-key", server.Client(), binding)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Upsert(context.Background(), []Point{point}); err == nil {
		t.Fatal("write before collection verification")
	}
	if err := store.EnsureCollection(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := store.Upsert(context.Background(), []Point{point}); err != nil {
		t.Fatal(err)
	}
	hits, err := store.SearchSparse(context.Background(), &pb.SparseVector{Indices: []uint32{2}, Values: []float32{1}},
		SearchScope{SnapshotSeq: 7, ProvisionVersionID: "version:one", Limit: 10})
	if err != nil || len(hits) != 1 || hits[0].ChunkID != "chunk:one" {
		t.Fatalf("search hits=%v err=%v", hits, err)
	}
	if !created || !wrote || !queried {
		t.Fatal("backend flow incomplete")
	}
}

func TestStoreRejectsMismatchedCollection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(strings.Replace(collectionReply(), `"size":2`, `"size":3`, 1)))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	binding, _ := qdrantFixture()
	store, err := New(server.URL, "", server.Client(), binding)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureCollection(context.Background()); err == nil {
		t.Fatal("incompatible vector family admitted")
	}
	if _, err := store.SearchDense(context.Background(), []float32{1, 0}, SearchScope{SnapshotSeq: 7, Limit: 1}); err == nil {
		t.Fatal("search against unverified collection admitted")
	}
}

func TestStoreRejectsStaleBackendHit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(collectionReply()))
			return
		}
		_, _ = w.Write([]byte(`{"status":"ok","result":{"points":[{"id":"` + testPointID + `","score":0.9,"payload":{"corpus_id":"corpus:one","generation_id":"generation:one","record_id":"index:one","chunk_id":"chunk:one","from_seq":7,"to_seq":8,"provision_filters":[{"provision_version_id":"version:one","regulation_id":"regulation:one","source_blob_id":"blob:one","jurisdiction":"ID","legal_status":"LEGAL_STATUS_ACTIVE","legal_interval":{}}]}}]}}`))
	}))
	defer server.Close()
	binding, _ := qdrantFixture()
	store, err := New(server.URL, "", server.Client(), binding)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureCollection(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SearchDense(context.Background(), []float32{0.2, 0.8},
		SearchScope{SnapshotSeq: 8, ProvisionVersionID: "version:one", Limit: 1}); err == nil {
		t.Fatal("closed record was accepted from backend")
	}
}

func TestStoreRejectsMalformedSearchResponses(t *testing.T) {
	cases := map[string]struct {
		body  string
		valid bool
	}{
		"null result":    {`{"status":"ok","result":null}`, false},
		"missing points": {`{"status":"ok","result":{}}`, false},
		"null points":    {`{"status":"ok","result":{"points":null}}`, false},
		"missing score":  {`{"status":"ok","result":{"points":[{"id":"` + testPointID + `","payload":{"corpus_id":"corpus:one","generation_id":"generation:one","record_id":"index:one","chunk_id":"chunk:one","from_seq":7,"provision_filters":[{"provision_version_id":"version:one","regulation_id":"regulation:one","source_blob_id":"blob:one","jurisdiction":"ID","legal_status":"LEGAL_STATUS_ACTIVE","legal_interval":{"start":{"knowledge":"DATE_KNOWLEDGE_UNKNOWN"},"end":{"knowledge":"DATE_KNOWLEDGE_UNKNOWN"}}}]}}]}}`, false},
		"empty points":   {`{"status":"ok","result":{"points":[]}}`, true},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodGet {
					_, _ = w.Write([]byte(collectionReply()))
					return
				}
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			binding, _ := qdrantFixture()
			store, err := New(server.URL, "", server.Client(), binding)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.EnsureCollection(context.Background()); err != nil {
				t.Fatal(err)
			}
			_, err = store.SearchDense(context.Background(), []float32{0.2, 0.8}, SearchScope{SnapshotSeq: 7, Limit: 1})
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v err=%v", test.valid, err)
			}
		})
	}
}

func TestStoreRevokesReadinessAfterLayoutDrift(t *testing.T) {
	reads, unexpected := 0, false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			unexpected = true
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		reads++
		body := collectionReply()
		if reads > 1 {
			body = strings.Replace(body, `"size":2`, `"size":3`, 1)
		}
		_, _ = w.Write([]byte(body))
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
	if err := store.EnsureCollection(context.Background()); err == nil {
		t.Fatal("changed layout accepted")
	}
	if err := store.Upsert(context.Background(), []Point{point}); err == nil {
		t.Fatal("write after drift")
	}
	if _, err := store.SearchDense(context.Background(), []float32{0.2, 0.8}, SearchScope{SnapshotSeq: 7, Limit: 1}); err == nil {
		t.Fatal("search after drift")
	}
	if unexpected {
		t.Fatal("backend write/query occurred after readiness revoked")
	}
}

func TestStoreNeverFollowsBackendRedirect(t *testing.T) {
	forwarded := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded = true
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/capture", http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	binding, _ := qdrantFixture()
	store, err := New(source.URL, "secret-for-test", source.Client(), binding)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureCollection(context.Background()); err == nil {
		t.Fatal("backend redirect accepted")
	}
	if forwarded {
		t.Fatal("backend redirect forwarded credentials or request")
	}
}

func TestStoreRejectsCorruptPairedSearchHit(t *testing.T) {
	paired := `{"provision_version_id":"version:one","regulation_id":"regulation:one","source_blob_id":"blob:one","jurisdiction":"ID","legal_status":"LEGAL_STATUS_ACTIVE","legal_interval":{"start":{"knowledge":"DATE_KNOWLEDGE_UNKNOWN"},"end":{"knowledge":"DATE_KNOWLEDGE_UNKNOWN"}}}`
	cases := map[string]string{
		"unknown status":  strings.Replace(paired, "LEGAL_STATUS_ACTIVE", "garbage", 1),
		"empty interval":  strings.Replace(paired, `{"start":{"knowledge":"DATE_KNOWLEDGE_UNKNOWN"},"end":{"knowledge":"DATE_KNOWLEDGE_UNKNOWN"}}`, `{}`, 1),
		"no jurisdiction": strings.Replace(paired, `"jurisdiction":"ID"`, `"jurisdiction":""`, 1),
		"duplicate pair":  paired + "," + paired,
	}
	for name, filters := range cases {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodGet {
					_, _ = w.Write([]byte(collectionReply()))
					return
				}
				_, _ = w.Write([]byte(`{"status":"ok","result":{"points":[{"id":"` + testPointID + `","score":0.9,"payload":{"corpus_id":"corpus:one","generation_id":"generation:one","record_id":"index:one","chunk_id":"chunk:one","from_seq":7,"provision_filters":[` + filters + `]}}]}}`))
			}))
			defer server.Close()
			binding, _ := qdrantFixture()
			store, err := New(server.URL, "", server.Client(), binding)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.EnsureCollection(context.Background()); err != nil {
				t.Fatal(err)
			}
			if _, err := store.SearchDense(context.Background(), []float32{0.2, 0.8},
				SearchScope{SnapshotSeq: 7, Limit: 1}); err == nil {
				t.Fatal("corrupt paired legal evidence accepted")
			}
		})
	}
}

func TestStoreRejectsOversizedSparseQueryBeforeNetwork(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("oversized query reached backend")
		}
		_, _ = w.Write([]byte(collectionReply()))
	}))
	defer server.Close()
	binding, _ := qdrantFixture()
	store, err := New(server.URL, "", server.Client(), binding)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureCollection(context.Background()); err != nil {
		t.Fatal(err)
	}
	indices := make([]uint32, 16385)
	values := make([]float32, len(indices))
	for i := range indices {
		indices[i], values[i] = uint32(i+1), 1
	}
	if _, err := store.SearchSparse(context.Background(), &pb.SparseVector{Indices: indices, Values: values},
		SearchScope{SnapshotSeq: 7, Limit: 1}); err == nil {
		t.Fatal("oversized sparse query admitted")
	}
}

func TestStoreRejectsInexactSnapshotSequence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("inexact sequence reached backend")
		}
		_, _ = w.Write([]byte(collectionReply()))
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
	if _, err := store.SearchDense(context.Background(), []float32{0.2, 0.8},
		SearchScope{SnapshotSeq: maxExactFilterSequence + 1, Limit: 1}); err == nil {
		t.Fatal("inexact query sequence admitted")
	}
	point.Record.Meta.Visibility.FromSeq = maxExactFilterSequence + 1
	point.Record.FilterMetadata.Visibility.FromSeq = maxExactFilterSequence + 1
	if err := store.Upsert(context.Background(), []Point{point}); err == nil {
		t.Fatal("inexact point sequence admitted")
	}
}
