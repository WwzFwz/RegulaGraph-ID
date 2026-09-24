// Tests fail-closed runtime configuration, prompt identity, and secret-free fingerprints without
// opening a listener or contacting a provider. These checks do not establish model quality.
package main

import (
	"bytes"
	"os"
	"path/filepath"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"strings"
	"testing"
)

func configureFixture(t *testing.T) ([]byte, string) {
	t.Helper()
	ontology, err := os.ReadFile("../../../../configs/ontology-v1.jsonc")
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	prompt := []byte("trusted extraction prompt\n")
	schema := []byte(`{"type":"object"}`)
	promptPath := filepath.Join(directory, "prompt.md")
	schemaPath := filepath.Join(directory, "schema.json")
	if err := os.WriteFile(promptPath, prompt, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(schemaPath, schema, 0o600); err != nil {
		t.Fatal(err)
	}
	ontologyPath := filepath.Join(directory, "ontology.jsonc")
	if err := os.WriteFile(ontologyPath, ontology, 0o600); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{
		"REGULAGRAPH_ONTOLOGY_PATH":                      ontologyPath,
		"REGULAGRAPH_ONTOLOGY_SHA256":                    hashBytes(ontology),
		"REGULAGRAPH_SEMANTIC_LISTEN":                    "127.0.0.1:50052",
		"REGULAGRAPH_SEMANTIC_PROVIDER_ENDPOINT":         "https://provider.invalid",
		"REGULAGRAPH_SEMANTIC_PROVIDER_API_KEY":          "secret-one",
		"REGULAGRAPH_SEMANTIC_PROMPT_PATH":               promptPath,
		"REGULAGRAPH_WORKER_EXTRACTION_OUTPUT_SCHEMA":    schemaPath,
		"REGULAGRAPH_WORKER_EXTRACTION_PROMPT_SHA256":    hashBytes(prompt),
		"REGULAGRAPH_WORKER_EXTRACTION_MODEL_ID":         "model:fixture",
		"REGULAGRAPH_WORKER_EXTRACTION_MODEL_VERSION":    "v1",
		"REGULAGRAPH_WORKER_EXTRACTION_WEIGHTS_SHA256":   strings.Repeat("a", 64),
		"REGULAGRAPH_WORKER_EXTRACTION_TOKENIZER_SHA256": strings.Repeat("b", 64),
		"REGULAGRAPH_WORKER_EXTRACTION_PRECISION":        "provider",
		"REGULAGRAPH_WORKER_EXTRACTION_BACKEND":          "openai-compatible",
		"REGULAGRAPH_WORKER_EXTRACTION_ONTOLOGY_VERSION": "id-regulation-ontology-v1",
		"REGULAGRAPH_BUILD_ID":                           "test-build",
	}
	for name, value := range values {
		t.Setenv(name, value)
	}
	return prompt, schemaPath
}

func TestLoadConfigSupportsPinnedResolutionTask(t *testing.T) {
	configureFixture(t)
	t.Setenv("REGULAGRAPH_SEMANTIC_TASK", "RESOLVE")
	for _, suffix := range []string{"OUTPUT_SCHEMA", "ONTOLOGY_VERSION", "PROMPT_SHA256", "MODEL_ID", "MODEL_VERSION", "WEIGHTS_SHA256", "TOKENIZER_SHA256", "PRECISION", "BACKEND"} {
		t.Setenv("REGULAGRAPH_WORKER_RESOLUTION_"+suffix, os.Getenv("REGULAGRAPH_WORKER_EXTRACTION_"+suffix))
	}
	config, _, _, _, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.model.Task != pb.ModelTask_MODEL_TASK_RESOLVE || semanticSchemaName(config.model.Task) != "regulagraph_resolution_v1" {
		t.Fatal("gateway did not select RESOLVE")
	}
	t.Setenv("REGULAGRAPH_SEMANTIC_TASK", "UNKNOWN")
	if _, _, _, _, err = loadConfig(); err == nil {
		t.Fatal("unknown semantic task accepted")
	}
}

func TestLoadConfigPinsPromptAndExcludesAPIKeyFromFingerprint(t *testing.T) {
	prompt, _ := configureFixture(t)
	config, loadedPrompt, schema, firstFingerprint, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(prompt, loadedPrompt) || config.model.PromptHash.Sha256 != hashBytes(prompt) || !bytes.Equal(schema, []byte(`{"type":"object"}`)) {
		t.Fatalf("pinned semantic inputs changed: config=%+v prompt=%q schema=%s", config.model, loadedPrompt, schema)
	}
	t.Setenv("REGULAGRAPH_SEMANTIC_PROVIDER_API_KEY", "secret-two")
	_, _, _, secondFingerprint, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if firstFingerprint.Sha256 != secondFingerprint.Sha256 {
		t.Fatal("secret value leaked into public configuration fingerprint")
	}
}

func TestLoadConfigRejectsPromptDriftAndNonLoopbackListener(t *testing.T) {
	configureFixture(t)
	t.Setenv("REGULAGRAPH_WORKER_EXTRACTION_PROMPT_SHA256", strings.Repeat("c", 64))
	if _, _, _, _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "prompt bytes differ") {
		t.Fatalf("prompt drift was accepted: %v", err)
	}
	t.Setenv("REGULAGRAPH_WORKER_EXTRACTION_PROMPT_SHA256", hashBytes([]byte("trusted extraction prompt\n")))
	t.Setenv("REGULAGRAPH_SEMANTIC_LISTEN", "0.0.0.0:50052")
	if _, _, _, _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("non-loopback plaintext listener was accepted: %v", err)
	}
}

func TestRequiredHashEnvRejectsUppercaseDigest(t *testing.T) {
	t.Setenv("HASH", strings.Repeat("A", 64))
	if _, err := requiredHashEnv("HASH"); err == nil {
		t.Fatal("uppercase digest was silently normalized")
	}
}
