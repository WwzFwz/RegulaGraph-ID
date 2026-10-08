// Verifies the operator bridge from a pinned native embedding manifest to the
// Rust worker's binary C01 configuration. Tests exact byte pins, replay, model
// drift and interrupted exports; these are interoperability checks, not model
// quality or required latency benchmarks.
package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func TestWorkerModelExport(t *testing.T) {
	hash := &pb.ContentHash{Sha256: strings.Repeat("a", 64)}
	model := &pb.ModelManifest{ModelId: "model:embedding", Version: "v1", WeightsHash: hash, TokenizerHash: hash, Task: pb.ModelTask_MODEL_TASK_EMBED, MaxTokens: 512, Precision: "fp32", Backend: "onnx", Dimensions: proto.Uint32(1024), Pooling: "cls", Normalization: "l2"}
	directory := t.TempDir()
	path, pin, err := exportWorkerEmbeddingModel(directory, model)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoded := new(pb.ModelManifest)
	if err = domain.DecodeWire(raw, decoded, domain.DefaultWireLimits); err != nil || !proto.Equal(model, decoded) || pin != fmt.Sprintf("%x", sha256.Sum256(raw)) {
		t.Fatal("worker pin or model changed", err)
	}
	if again, hash, err := exportWorkerEmbeddingModel(directory, model); err != nil || again != path || hash != pin {
		t.Fatal("exact replay failed", err)
	}
	changed := proto.Clone(model).(*pb.ModelManifest)
	changed.Version = "v2"
	if _, _, err = exportWorkerEmbeddingModel(directory, changed); err == nil {
		t.Fatal("model drift overwrote export")
	}
	if actual, err := os.ReadFile(path); err != nil || !bytes.Equal(actual, raw) {
		t.Fatal("rejected export changed bytes", err)
	}
	if err = os.WriteFile(path, raw[:len(raw)/2], 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = exportWorkerEmbeddingModel(directory, model); err == nil {
		t.Fatal("partial export accepted")
	}
	changed.Task = pb.ModelTask_MODEL_TASK_RERANK
	if _, _, err = exportWorkerEmbeddingModel(t.TempDir(), changed); err == nil {
		t.Fatal("wrong task accepted")
	}
}
