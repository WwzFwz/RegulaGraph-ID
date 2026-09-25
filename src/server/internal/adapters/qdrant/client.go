// Package qdrant owns the bounded HTTP transport for X01 named dense and
// BM25-sparse vectors. Stores are explicitly constructed from trusted generation
// bindings; network I/O never occurs during import. Measure client p95/p99,
// queue time, bytes and filtered recall against configs/benchmark-targets.yaml.
package qdrant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

const maxResponseBytes = 4 << 20
const maxRequestBytes = 8 << 20
const maxPointsPerWrite = 256

// Qdrant numeric range filters use floating-point payload values. Above 2^53-1
// a uint64 sequence can alias another sequence before Go can post-filter it.
const maxExactFilterSequence = (1 << 53) - 1

var collectionName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// Binding must come from the PostgreSQL generation catalog, never a query.
type Binding struct {
	Collection string
	CorpusID   string
	Generation *pb.IndexGeneration
}

type Store struct {
	baseURL    string
	apiKey     string
	client     *http.Client
	binding    Binding
	collection string
	ready      atomic.Bool
	ensureMu   sync.Mutex
}

// New has no network side effect. The caller supplies a reusable HTTP client
// with timeout and connection pool configured for its workload.
func New(endpoint, apiKey string, client *http.Client, binding Binding) (*Store, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed == nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" {
		return nil, errors.New("qdrant endpoint must be an HTTP(S) origin")
	}
	if client == nil || !collectionName.MatchString(binding.Collection) || binding.CorpusID == "" {
		return nil, errors.New("qdrant client, collection and corpus are required")
	}
	if err := domain.ValidatePairedIndexGeneration(binding.Generation); err != nil {
		return nil, fmt.Errorf("qdrant generation: %w", err)
	}
	if binding.Generation.Meta.CorpusId != binding.CorpusID || binding.Generation.DenseManifest.GetDimensions() == 0 ||
		binding.Generation.DenseManifest.GetDimensions() > 16384 {
		return nil, errors.New("qdrant generation corpus or dimension mismatch")
	}
	binding.Generation = proto.Clone(binding.Generation).(*pb.IndexGeneration)
	ownedClient := *client
	if ownedClient.Timeout <= 0 {
		ownedClient.Timeout = 30 * time.Second
	}
	ownedClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse // never forward credentials or vector data
	}
	return &Store{baseURL: strings.TrimSuffix(endpoint, "/"), apiKey: apiKey, client: &ownedClient,
		binding: binding, collection: binding.Collection}, nil
}

type qdrantEnvelope struct {
	Status json.RawMessage `json:"status"`
	Result json.RawMessage `json:"result"`
}

func (store *Store) call(ctx context.Context, method, path string, input any, output any) (int, error) {
	if store == nil || ctx == nil {
		return 0, errors.New("qdrant store and context are required")
	}
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return 0, err
		}
		if len(encoded) > maxRequestBytes {
			return 0, errors.New("qdrant request exceeds byte budget")
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, store.baseURL+path, body)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Accept", "application/json")
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if store.apiKey != "" {
		request.Header.Set("api-key", store.apiKey)
	}
	response, err := store.client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return response.StatusCode, err
	}
	if len(data) > maxResponseBytes {
		return response.StatusCode, errors.New("qdrant response exceeds byte budget")
	}
	if response.StatusCode == http.StatusNotFound {
		return response.StatusCode, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response.StatusCode, fmt.Errorf("qdrant HTTP %d: %s", response.StatusCode, boundedError(data))
	}
	var envelope qdrantEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil || len(envelope.Result) == 0 || string(envelope.Result) == "null" {
		return response.StatusCode, errors.New("qdrant response lacks result")
	}
	if string(envelope.Status) != `"ok"` {
		return response.StatusCode, errors.New("qdrant response status is not ok")
	}
	if output != nil && json.Unmarshal(envelope.Result, output) != nil {
		return response.StatusCode, errors.New("qdrant result shape is invalid")
	}
	return response.StatusCode, nil
}

func boundedError(data []byte) string {
	if len(data) > 256 {
		data = data[:256]
	}
	return strings.TrimSpace(string(data))
}
