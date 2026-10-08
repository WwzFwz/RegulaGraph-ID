// Verifies native model pins before runtime/network setup: exact file bytes,
// task and bounded regular-file input. This validates configuration only, not
// model capability, quality or inference parity.
package config

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func TestLoadNativeModelPinsBytesAndTask(t *testing.T) {
	hash := &pb.ContentHash{Sha256: strings.Repeat("a", 64)}
	model := &pb.ModelManifest{ModelId: "model:reranker", Version: "v1", WeightsHash: hash, TokenizerHash: hash, Task: pb.ModelTask_MODEL_TASK_RERANK, MaxTokens: 512, Precision: "fp32", Backend: "onnx"}
	raw, err := protojson.Marshal(model)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "model.pbjson")
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	pin := fmt.Sprintf("%x", sha256.Sum256(raw))
	loaded, err := LoadNativeModel(path, pin, pb.ModelTask_MODEL_TASK_RERANK)
	if err != nil || !proto.Equal(model, loaded) {
		t.Fatal("valid pin", err)
	}
	for _, tc := range []struct {
		path, hash string
		task       pb.ModelTask
	}{
		{path, strings.Repeat("b", 64), pb.ModelTask_MODEL_TASK_RERANK},
		{path, pin, pb.ModelTask_MODEL_TASK_EMBED},
		{path, pin, pb.ModelTask_MODEL_TASK_UNSPECIFIED},
		{filepath.Dir(path), pin, pb.ModelTask_MODEL_TASK_RERANK},
		{path, strings.ToUpper(pin), pb.ModelTask_MODEL_TASK_RERANK},
	} {
		if _, err = LoadNativeModel(tc.path, tc.hash, tc.task); err == nil {
			t.Fatal("invalid config accepted")
		}
	}
	if err = os.WriteFile(path, []byte(strings.Repeat("x", (64<<10)+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = LoadNativeModel(path, pin, pb.ModelTask_MODEL_TASK_RERANK); err == nil {
		t.Fatal("oversized manifest accepted")
	}
}

func TestLoadNativeModelRejectsAmbiguousJSON(t *testing.T) {
	for _, raw := range []string{`{"modelId":"one","model_id":"two"}`, `{"modelId":"one","modelId":"two"}`, `{"unknown":true}`, `{}`} {
		path := filepath.Join(t.TempDir(), "model.pbjson")
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		pin := fmt.Sprintf("%x", sha256.Sum256([]byte(raw)))
		if _, err := LoadNativeModel(path, pin, pb.ModelTask_MODEL_TASK_RERANK); err == nil {
			t.Fatal("invalid manifest accepted")
		}
	}
}
