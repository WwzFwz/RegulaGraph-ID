// Runs the internal Semantic gRPC gateway for bounded, schema-constrained extraction calls.
// Startup pins model, prompt, ontology, schema, and public runtime configuration by hash, then
// reuses one provider client. The listener remains loopback-only until transport authentication is
// deployed. Quality, provider latency, and cost targets remain REQUIRED_UNMEASURED.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"google.golang.org/grpc"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/inference"
	serverconfig "regulagraph.local/server/internal/config"
	"regulagraph.local/server/internal/domain"
)

const (
	maximumPromptBytes = 256 << 10
	maximumSchemaBytes = 1 << 20
)

type runtimeConfig struct {
	listen                   string
	providerEndpoint         string
	providerAPIKey           string
	providerTimeout          time.Duration
	providerMaxResponseBytes int
	promptPath               string
	schemaPath               string
	model                    *pb.ModelManifest
	ontologyVersion          string
	ontology                 *domain.Ontology
	build                    string
	maximumItems             int
	maximumInputBytes        int
	maximumConcurrent        int
	maximumCacheEntries      int
	maximumCacheBytes        int
	maximumMessageBytes      int
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		_ = json.NewEncoder(os.Stderr).Encode(map[string]string{
			"level": "error", "component": "semantic-gateway", "error": err.Error(),
		})
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	config, prompt, schema, configHash, err := loadConfig()
	if err != nil {
		return err
	}
	provider, err := inference.NewOpenAICompatibleProvider(inference.OpenAICompatibleConfig{
		Endpoint: config.providerEndpoint, APIKey: config.providerAPIKey,
		Timeout: config.providerTimeout, MaximumResponseBytes: int64(config.providerMaxResponseBytes),
	})
	if err != nil {
		return fmt.Errorf("configure structured provider: %w", err)
	}
	service, err := inference.NewSemanticService(provider, inference.SemanticConfig{
		Model: config.model, Ontology: config.ontology,
		OutputSchemaHash: contentHash(schema), SystemPrompt: string(prompt), OutputSchema: schema,
		SchemaName: "regulagraph_extraction_v1", Software: "regulagraph-semantic-gateway",
		Build: config.build, ConfigHash: configHash, TokenizerID: config.model.ModelId + ":provider",
		MaximumItems: config.maximumItems, MaximumInputBytes: config.maximumInputBytes,
		MaximumConcurrent: config.maximumConcurrent, MaximumCacheEntries: config.maximumCacheEntries,
		MaximumCacheBytes: int64(config.maximumCacheBytes),
	})
	if err != nil {
		return fmt.Errorf("configure semantic service: %w", err)
	}
	listener, err := net.Listen("tcp", config.listen)
	if err != nil {
		return fmt.Errorf("listen semantic gateway: %w", err)
	}
	defer listener.Close()
	server := grpc.NewServer(
		grpc.MaxRecvMsgSize(config.maximumMessageBytes),
		grpc.MaxSendMsgSize(config.maximumMessageBytes),
		grpc.MaxConcurrentStreams(uint32(config.maximumConcurrent*2)),
	)
	pb.RegisterSemanticServer(server, service)
	serveResult := make(chan error, 1)
	go func() { serveResult <- server.Serve(listener) }()
	select {
	case err = <-serveResult:
		if err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			return fmt.Errorf("serve semantic gateway: %w", err)
		}
		return nil
	case <-ctx.Done():
		stopped := make(chan struct{})
		go func() {
			server.GracefulStop()
			close(stopped)
		}()
		select {
		case <-stopped:
		case <-time.After(10 * time.Second):
			server.Stop()
		}
		err = <-serveResult
		if err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			return fmt.Errorf("stop semantic gateway: %w", err)
		}
		return nil
	}
}

func loadConfig() (runtimeConfig, []byte, json.RawMessage, *pb.ContentHash, error) {
	config := runtimeConfig{
		listen:           os.Getenv("REGULAGRAPH_SEMANTIC_LISTEN"),
		providerEndpoint: os.Getenv("REGULAGRAPH_SEMANTIC_PROVIDER_ENDPOINT"),
		providerAPIKey:   os.Getenv("REGULAGRAPH_SEMANTIC_PROVIDER_API_KEY"),
		promptPath:       os.Getenv("REGULAGRAPH_SEMANTIC_PROMPT_PATH"),
		schemaPath:       os.Getenv("REGULAGRAPH_WORKER_EXTRACTION_OUTPUT_SCHEMA"),
		ontologyVersion:  os.Getenv("REGULAGRAPH_WORKER_EXTRACTION_ONTOLOGY_VERSION"),
		build:            os.Getenv("REGULAGRAPH_BUILD_ID"),
	}
	if config.listen == "" || config.providerEndpoint == "" || config.promptPath == "" || config.schemaPath == "" ||
		config.ontologyVersion == "" || config.build == "" {
		return runtimeConfig{}, nil, nil, nil, errors.New("semantic listen/provider, prompt/schema paths, ontology version, and build ID are required")
	}
	var err error
	config.ontology, err = serverconfig.LoadOntology(os.Getenv("REGULAGRAPH_ONTOLOGY_PATH"), os.Getenv("REGULAGRAPH_ONTOLOGY_SHA256"))
	if err != nil {
		return runtimeConfig{}, nil, nil, nil, fmt.Errorf("load pinned ontology: %w", err)
	}
	if config.ontology.Version() != config.ontologyVersion {
		return runtimeConfig{}, nil, nil, nil, errors.New("semantic ontology version differs from pinned ontology bytes")
	}
	if err := requireLoopback(config.listen); err != nil {
		return runtimeConfig{}, nil, nil, nil, err
	}
	prompt, err := readBoundedFile(config.promptPath, maximumPromptBytes)
	if err != nil {
		return runtimeConfig{}, nil, nil, nil, fmt.Errorf("read semantic prompt: %w", err)
	}
	schemaBytes, err := readBoundedFile(config.schemaPath, maximumSchemaBytes)
	if err != nil {
		return runtimeConfig{}, nil, nil, nil, fmt.Errorf("read extraction schema: %w", err)
	}
	if len(strings.TrimSpace(string(prompt))) == 0 || !json.Valid(schemaBytes) {
		return runtimeConfig{}, nil, nil, nil, errors.New("semantic prompt must be non-empty and extraction schema must be valid JSON")
	}
	promptHash, err := requiredHashEnv("REGULAGRAPH_WORKER_EXTRACTION_PROMPT_SHA256")
	if err != nil {
		return runtimeConfig{}, nil, nil, nil, err
	}
	if promptHash.Sha256 != hashBytes(prompt) {
		return runtimeConfig{}, nil, nil, nil, errors.New("semantic prompt bytes differ from REGULAGRAPH_WORKER_EXTRACTION_PROMPT_SHA256")
	}
	weightsHash, err := requiredHashEnv("REGULAGRAPH_WORKER_EXTRACTION_WEIGHTS_SHA256")
	if err != nil {
		return runtimeConfig{}, nil, nil, nil, err
	}
	tokenizerHash, err := requiredHashEnv("REGULAGRAPH_WORKER_EXTRACTION_TOKENIZER_SHA256")
	if err != nil {
		return runtimeConfig{}, nil, nil, nil, err
	}
	modelID := os.Getenv("REGULAGRAPH_WORKER_EXTRACTION_MODEL_ID")
	modelVersion := os.Getenv("REGULAGRAPH_WORKER_EXTRACTION_MODEL_VERSION")
	precision := os.Getenv("REGULAGRAPH_WORKER_EXTRACTION_PRECISION")
	backend := os.Getenv("REGULAGRAPH_WORKER_EXTRACTION_BACKEND")
	if modelID == "" || modelVersion == "" || precision == "" || backend == "" {
		return runtimeConfig{}, nil, nil, nil, errors.New("semantic model ID, version, precision, and backend are required")
	}
	maxTokens, err := positiveIntEnv("REGULAGRAPH_WORKER_EXTRACTION_MAX_TOKENS", 8192)
	if err != nil {
		return runtimeConfig{}, nil, nil, nil, err
	}
	if uint64(maxTokens) > math.MaxUint32 {
		return runtimeConfig{}, nil, nil, nil, errors.New("semantic model maximum tokens exceeds uint32")
	}
	config.model = &pb.ModelManifest{
		ModelId: modelID, Version: modelVersion, WeightsHash: weightsHash, TokenizerHash: tokenizerHash,
		Task: pb.ModelTask_MODEL_TASK_EXTRACT, MaxTokens: uint32(maxTokens), Precision: precision,
		Backend: backend, PromptHash: promptHash,
	}
	if config.providerTimeout, err = durationEnv("REGULAGRAPH_SEMANTIC_PROVIDER_TIMEOUT", 90*time.Second); err != nil {
		return runtimeConfig{}, nil, nil, nil, err
	}
	integerTargets := []struct {
		name     string
		fallback int
		target   *int
	}{
		{"REGULAGRAPH_SEMANTIC_PROVIDER_MAX_RESPONSE_BYTES", 4 << 20, &config.providerMaxResponseBytes},
		{"REGULAGRAPH_SEMANTIC_MAX_ITEMS", 256, &config.maximumItems},
		{"REGULAGRAPH_SEMANTIC_MAX_INPUT_BYTES", 4 << 20, &config.maximumInputBytes},
		{"REGULAGRAPH_SEMANTIC_MAX_CONCURRENT", 8, &config.maximumConcurrent},
		{"REGULAGRAPH_SEMANTIC_CACHE_ENTRIES", 4096, &config.maximumCacheEntries},
		{"REGULAGRAPH_SEMANTIC_CACHE_BYTES", 256 << 20, &config.maximumCacheBytes},
		{"REGULAGRAPH_SEMANTIC_MAX_MESSAGE_BYTES", 16 << 20, &config.maximumMessageBytes},
	}
	for _, entry := range integerTargets {
		*entry.target, err = positiveIntEnv(entry.name, entry.fallback)
		if err != nil {
			return runtimeConfig{}, nil, nil, nil, err
		}
	}
	if config.maximumInputBytes >= config.maximumMessageBytes {
		return runtimeConfig{}, nil, nil, nil, errors.New("semantic maximum input bytes must be smaller than gRPC maximum message bytes")
	}
	if uint64(config.maximumConcurrent) > math.MaxUint32/2 {
		return runtimeConfig{}, nil, nil, nil, errors.New("semantic maximum concurrency exceeds the gRPC stream limit")
	}
	publicConfig := struct {
		ProviderEndpoint  string `json:"provider_endpoint"`
		ProviderTimeout   string `json:"provider_timeout"`
		MaxResponseBytes  int    `json:"max_response_bytes"`
		OntologyVersion   string `json:"ontology_version"`
		OntologyHash      string `json:"ontology_hash"`
		Build             string `json:"build"`
		MaximumItems      int    `json:"maximum_items"`
		MaximumInput      int    `json:"maximum_input_bytes"`
		MaximumConcurrent int    `json:"maximum_concurrent"`
		MaximumCacheItems int    `json:"maximum_cache_entries"`
		MaximumCacheBytes int    `json:"maximum_cache_bytes"`
		PromptHash        string `json:"prompt_hash"`
		SchemaHash        string `json:"schema_hash"`
		ModelID           string `json:"model_id"`
		ModelVersion      string `json:"model_version"`
	}{
		ProviderEndpoint: config.providerEndpoint, ProviderTimeout: config.providerTimeout.String(),
		MaxResponseBytes: config.providerMaxResponseBytes, OntologyVersion: config.ontologyVersion, OntologyHash: config.ontology.ContentHash().Sha256,
		Build: config.build, MaximumItems: config.maximumItems, MaximumInput: config.maximumInputBytes,
		MaximumConcurrent: config.maximumConcurrent, MaximumCacheItems: config.maximumCacheEntries,
		MaximumCacheBytes: config.maximumCacheBytes, PromptHash: promptHash.Sha256,
		SchemaHash: hashBytes(schemaBytes), ModelID: modelID, ModelVersion: modelVersion,
	}
	encodedConfig, err := json.Marshal(publicConfig)
	if err != nil {
		return runtimeConfig{}, nil, nil, nil, fmt.Errorf("fingerprint semantic config: %w", err)
	}
	return config, prompt, json.RawMessage(schemaBytes), contentHash(encodedConfig), nil
}

func readBoundedFile(path string, maximum int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maximum {
		return nil, fmt.Errorf("file must be regular and contain 1..%d bytes", maximum)
	}
	payload, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(payload)) != info.Size() {
		return nil, errors.New("file size changed while it was being read")
	}
	return payload, nil
}

func requiredHashEnv(name string) (*pb.ContentHash, error) {
	value := os.Getenv(name)
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size || value != strings.ToLower(value) {
		return nil, fmt.Errorf("%s must be a lowercase SHA-256 hex digest", name)
	}
	return &pb.ContentHash{Sha256: value}, nil
}

func contentHash(value []byte) *pb.ContentHash { return &pb.ContentHash{Sha256: hashBytes(value)} }

func hashBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func requireLoopback(endpoint string) error {
	host, _, err := net.SplitHostPort(endpoint)
	if err != nil {
		return fmt.Errorf("semantic listener must be host:port: %w", err)
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("semantic listener must be loopback until TLS is configured")
	}
	return nil
}

func durationEnv(name string, fallback time.Duration) (time.Duration, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", name)
	}
	return value, nil
}

func positiveIntEnv(name string, fallback int) (int, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return value, nil
}
