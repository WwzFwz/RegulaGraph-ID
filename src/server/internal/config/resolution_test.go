// Verifies pinned RESOLVE configuration admission before storage/network side effects.
// Drift, malformed ProtoJSON, wrong tasks and oversized input must be rejected. These
// fixtures exercise configuration correctness, not model quality or latency acceptance.
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"google.golang.org/protobuf/encoding/protojson"
	"os"
	"path/filepath"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"strings"
	"testing"
)

func resolutionConfigFile(t *testing.T, raw []byte) (string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	return path, hex.EncodeToString(digest[:])
}
func TestPinnedResolutionConfiguration(t *testing.T) {
	hash := &pb.ContentHash{Sha256: strings.Repeat("a", 64)}
	manifest := &pb.ProducerManifest{Software: "gateway", Build: "test", SchemaVersion: 1, ConfigHash: hash,
		Models: []*pb.ModelManifest{{ModelId: "local:resolver", Version: "v1", Task: pb.ModelTask_MODEL_TASK_RESOLVE,
			WeightsHash: hash, TokenizerHash: hash, PromptHash: hash, MaxTokens: 1024, Precision: "fp32", Backend: "fixture"}}}
	raw, err := protojson.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	path, pin := resolutionConfigFile(t, raw)
	if _, err = LoadResolutionProducer(path, pin); err != nil {
		t.Fatal(err)
	}
	if _, err = LoadResolutionProducer(path, strings.Repeat("b", 64)); err == nil {
		t.Fatal("tampered producer accepted")
	}
	for _, invalid := range []string{`{"unknown":true}`, string(raw[:len(raw)-1]) + `,"software":"duplicate"}`,
		strings.ReplaceAll(string(raw), "MODEL_TASK_RESOLVE", "MODEL_TASK_EXTRACT"), strings.Repeat(" ", (1<<20)+1)} {
		path, pin = resolutionConfigFile(t, []byte(invalid))
		if _, err = LoadResolutionProducer(path, pin); err == nil {
			t.Fatal("invalid producer accepted")
		}
	}
	path, pin = resolutionConfigFile(t, []byte(`{"type":"object"}`))
	schema, err := LoadResolutionOutputSchema(path, pin)
	if err != nil || schema.GetContentHash().GetSha256() != pin || schema.MediaType != "application/schema+json" {
		t.Fatalf("schema: %v %v", schema, err)
	}
	path, pin = resolutionConfigFile(t, []byte(`{"type":`))
	if _, err = LoadResolutionOutputSchema(path, pin); err == nil {
		t.Fatal("malformed schema accepted")
	}
}
