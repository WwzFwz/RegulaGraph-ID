// Tests explicit local admission configuration and producer fingerprint changes.
// These tests perform no model loading; runtime metadata/token checks are covered
// in adapter tests and require a separate real-model run for acceptance evidence.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalSemanticAdmissionPins(t *testing.T) {
	configureFixture(t)
	_, _, _, generic, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("REGULAGRAPH_SEMANTIC_ADMISSION", "llama.cpp")
	t.Setenv("REGULAGRAPH_SEMANTIC_GGUF_PATH", filepath.Join(t.TempDir(), "model.gguf"))
	t.Setenv("REGULAGRAPH_SEMANTIC_LLAMA_BUILD", "test-build")
	t.Setenv("REGULAGRAPH_SEMANTIC_TEMPLATE_SHA256", strings.Repeat("c", 64))
	t.Setenv("REGULAGRAPH_WORKER_EXTRACTION_TOKENIZER_SHA256", os.Getenv("REGULAGRAPH_WORKER_EXTRACTION_WEIGHTS_SHA256"))
	config, _, _, pinned, err := loadConfig()
	if err != nil || config.localBinding == nil || pinned.Sha256 == generic.Sha256 {
		t.Fatalf("local pins missing: %v", err)
	}
	t.Setenv("REGULAGRAPH_SEMANTIC_TEMPLATE_SHA256", strings.Repeat("d", 64))
	_, _, _, changed, err := loadConfig()
	if err != nil || changed.Sha256 == pinned.Sha256 {
		t.Fatalf("template not fingerprinted: %v", err)
	}
	t.Setenv("REGULAGRAPH_SEMANTIC_LLAMA_BUILD", "other-build")
	_, _, _, changedBuild, err := loadConfig()
	if err != nil || changedBuild.Sha256 == changed.Sha256 {
		t.Fatalf("build not fingerprinted: %v", err)
	}
	for _, mode := range []string{"provider", "", "unknown"} {
		t.Setenv("REGULAGRAPH_SEMANTIC_ADMISSION", mode)
		if _, _, _, _, err := loadConfig(); err == nil {
			t.Fatalf("silently ignored local pins in mode %q", mode)
		}
	}
	t.Setenv("REGULAGRAPH_SEMANTIC_ADMISSION", "llama.cpp")
	t.Setenv("REGULAGRAPH_SEMANTIC_GGUF_PATH", "relative.gguf")
	if _, _, _, _, err := loadConfig(); err == nil {
		t.Fatal("relative path accepted")
	}
}
