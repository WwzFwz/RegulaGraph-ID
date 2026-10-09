// Exercises actual named-vector writes, exact readback and production-filtered
// serving probes against a disposable Qdrant 1.18 endpoint. Synthetic vectors
// establish transport/storage correctness, not model quality or required latency.
// Collections have unique test-owned names and are removed after the test.
package qdrant

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func TestInitialServingAgainstQdrant(t *testing.T) {
	endpoint := os.Getenv("REGULAGRAPH_TEST_QDRANT_ENDPOINT")
	if endpoint == "" {
		t.Skip("disposable Qdrant required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	binding, point := qdrantFixture()
	binding.Collection = fmt.Sprintf("readiness_%d", time.Now().UnixNano())
	store, err := New(endpoint, "", &http.Client{Timeout: 5 * time.Second}, binding)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_, err := store.call(cleanup, http.MethodDelete, "/collections/"+binding.Collection, nil, nil)
		if err != nil {
			t.Error(err)
		}
	}()
	if err = store.EnsureCollection(ctx); err != nil {
		t.Fatal(err)
	}
	if err = store.VerifyInitialServing(ctx, 1, 7, point); err == nil {
		t.Fatal("empty collection was ready")
	}
	for range 2 {
		if err = store.Upsert(ctx, []Point{point}); err != nil {
			t.Fatal(err)
		}
		if err = store.VerifyPoints(ctx, []Point{point}); err != nil {
			t.Fatal(err)
		}
		if err = store.VerifyInitialServing(ctx, 1, 7, point); err != nil {
			t.Fatal(err)
		}
	}
	if err = store.VerifyInitialServing(ctx, 1, 6, point); err == nil {
		t.Fatal("future point passed serving probe")
	}
	if err = store.VerifyInheritedServing(ctx, 1, 8, point); err != nil {
		t.Fatal("visible inherited point rejected", err)
	}
	if err = store.VerifyInheritedServing(ctx, 1, 6, point); err == nil {
		t.Fatal("future inherited point passed serving probe")
	}
	if err = store.VerifyInitialServing(ctx, 2, 7, point); err == nil {
		t.Fatal("missing point passed exact count")
	}
	extra := Point{ID: "b1786f40-3fa9-4b84-bcb9-71b05ad3c290", Record: proto.Clone(point.Record).(*pb.IndexRecord)}
	extra.Record.Meta.RecordId = "index:extra"
	if err = store.Upsert(ctx, []Point{extra}); err != nil {
		t.Fatal(err)
	}
	if err = store.VerifyPoints(ctx, []Point{point}); err != nil {
		t.Fatal(err)
	}
	if err = store.VerifyInitialServing(ctx, 1, 7, point); err == nil {
		t.Fatal("extra point passed complete-manifest check")
	}
	if err = store.VerifyInheritedServing(ctx, 1, 8, point); err == nil {
		t.Fatal("extra inherited point passed complete-manifest check")
	}
	if err = store.VerifyInitialServing(ctx, 2, 7, point); err != nil {
		t.Fatal(err)
	}
}
