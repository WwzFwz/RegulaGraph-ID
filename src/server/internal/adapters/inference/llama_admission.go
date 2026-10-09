// Admits a trusted local llama.cpp process against immutable operator model pins.
// GGUF bytes (including the tokenizer) are hashed once at startup. Request guards
// compare file identity/stat and server model path, alias, template, build and
// context window before returning counts or generated output. This detects drift
// in a trusted local process; it is not cryptographic attestation of remote RAM.
// Measure cold hashing separately from warm metadata guards and generation under
// benchmark-targets.yaml. No model loading or network I/O occurs during import.
// EXTRACT/RESOLVE additionally count the exact chat envelope before inference and
// reject context overflow or prompt-usage drift; no source truncation is permitted.
package inference

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type LlamaModelBinding struct {
	Model        *pb.ModelManifest
	GGUFPath     string
	ServerBuild  string
	TemplateHash *pb.ContentHash
}

// PinnedLlama owns detached metadata and a resident provider, not the server
// process. The caller stops admission/drains requests before closing HTTP pools.
type PinnedLlama struct {
	provider *OpenAICompatibleProvider
	counter  *LlamaTokenCounter
	binding  LlamaModelBinding
	file     os.FileInfo
}

func (b LlamaModelBinding) Validate() error {
	if b.Model == nil || b.TemplateHash == nil || b.GGUFPath == "" || !filepath.IsAbs(b.GGUFPath) || !utf8.ValidString(b.GGUFPath) || strings.ContainsRune(b.GGUFPath, 0) || b.ServerBuild == "" || len(b.ServerBuild) > 256 || !utf8.ValidString(b.ServerBuild) {
		return errors.New("explicit local model path, build, template and manifest required")
	}
	if err := domain.ValidateWire(b.Model, domain.DefaultWireLimits); err != nil {
		return err
	}
	if err := domain.ValidateWire(b.TemplateHash, domain.DefaultWireLimits); err != nil {
		return err
	}
	if (b.Model.Task != pb.ModelTask_MODEL_TASK_GENERATE && b.Model.Task != pb.ModelTask_MODEL_TASK_EXTRACT && b.Model.Task != pb.ModelTask_MODEL_TASK_RESOLVE) || b.Model.PromptHash == nil || !proto.Equal(b.Model.WeightsHash, b.Model.TokenizerHash) {
		return errors.New("GENERATE/EXTRACT/RESOLVE GGUF manifest must pin the container's embedded tokenizer")
	}
	return nil
}

func AdmitLlama(ctx context.Context, provider *OpenAICompatibleProvider, binding LlamaModelBinding, maximumInputBytes int) (*PinnedLlama, error) {
	if ctx == nil || provider == nil {
		return nil, errors.New("provider and startup context required")
	}
	if err := binding.Validate(); err != nil {
		return nil, err
	}
	u, err := url.Parse(provider.endpoint)
	if err != nil || !net.ParseIP(u.Hostname()).IsLoopback() {
		return nil, errors.New("model-file admission requires a literal loopback server")
	}
	counter, err := NewLlamaTokenCounter(provider, binding.Model.ModelId, maximumInputBytes, 1<<20)
	if err != nil {
		return nil, err
	}
	owned := binding
	owned.Model = proto.Clone(binding.Model).(*pb.ModelManifest)
	owned.TemplateHash = proto.Clone(binding.TemplateHash).(*pb.ContentHash)
	owned.GGUFPath = filepath.Clean(binding.GGUFPath)
	info, err := hashLlamaFile(ctx, owned.GGUFPath, owned.Model.WeightsHash.Sha256)
	if err != nil {
		return nil, err
	}
	p := &PinnedLlama{provider: provider, counter: counter, binding: owned, file: info}
	if err = p.Ready(ctx); err != nil {
		return nil, err
	}
	return p, nil
}

func hashLlamaFile(ctx context.Context, path, expected string) (os.FileInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("model file unavailable")
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() < 4 || before.Size() > 128<<30 {
		return nil, errors.New("model must be a bounded regular GGUF file")
	}
	h := sha256.New()
	buffer := make([]byte, 512<<10)
	var total int64
	for {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		n, e := f.Read(buffer)
		if total == 0 && (n < 4 || string(buffer[:4]) != "GGUF") {
			return nil, errors.New("model container is not GGUF")
		}
		total += int64(n)
		if total > before.Size() {
			return nil, errors.New("model changed during hashing")
		}
		_, _ = h.Write(buffer[:n])
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, errors.New("model file read failed")
		}
	}
	after, err := f.Stat()
	if err != nil || total != before.Size() || !sameLlamaFile(before, after) || hex.EncodeToString(h.Sum(nil)) != expected {
		return nil, errors.New("model bytes or file identity differ from pin")
	}
	return after, ctx.Err()
}

func sameLlamaFile(a, b os.FileInfo) bool {
	return a != nil && b != nil && b.Mode().IsRegular() && os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

func (p *PinnedLlama) Ready(ctx context.Context) error {
	if p == nil || ctx == nil {
		return errors.New("admitted model and context required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Stat(p.binding.GGUFPath)
	if err != nil || !sameLlamaFile(p.file, info) {
		return errors.New("admitted model file changed; restart admission")
	}
	endpoint := strings.TrimSuffix(p.provider.endpoint, "/v1/chat/completions") + "/props"
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return errors.New("invalid model readiness request")
	}
	if p.provider.apiKey != "" {
		r.Header.Set("Authorization", "Bearer "+p.provider.apiKey)
	}
	response, err := p.provider.client.Do(r)
	if err != nil {
		return providerTransportError(ctx, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errors.New("local model properties unavailable")
	}
	raw, err := readBounded(response.Body, 1<<20)
	if err != nil {
		return err
	}
	fields, err := llamaPropertyObject(raw)
	if err != nil {
		return err
	}
	text := func(key string) string { var s string; _ = json.Unmarshal(fields[key], &s); return s }
	var sleeping, mutable *bool
	if json.Unmarshal(fields["is_sleeping"], &sleeping) != nil || sleeping == nil || *sleeping || json.Unmarshal(fields["endpoint_props"], &mutable) != nil || mutable == nil || *mutable {
		return errors.New("local model must be resident with read-only properties")
	}
	settings, err := llamaPropertyObject(fields["default_generation_settings"])
	if err != nil {
		return err
	}
	var n uint32
	if json.Unmarshal(settings["n_ctx"], &n) != nil || n != p.binding.Model.MaxTokens {
		return errors.New("local model context window differs from manifest")
	}
	template := text("chat_template")
	digest := sha256.Sum256([]byte(template))
	if template == "" || text("model_alias") != p.binding.Model.ModelId || text("build_info") != p.binding.ServerBuild || filepath.Clean(text("model_path")) != p.binding.GGUFPath || hex.EncodeToString(digest[:]) != p.binding.TemplateHash.Sha256 {
		return errors.New("local model alias, path, build or chat template differs from pin")
	}
	info, err = os.Stat(p.binding.GGUFPath)
	if err != nil || !sameLlamaFile(p.file, info) {
		return errors.New("model file changed during readiness")
	}
	return ctx.Err()
}

func (p *PinnedLlama) Generate(ctx context.Context, r StructuredRequest) (StructuredResponse, error) {
	if p == nil || r.ModelID != p.binding.Model.ModelId {
		return StructuredResponse{}, errors.New("generator model differs from pin")
	}
	if err := p.Ready(ctx); err != nil {
		return StructuredResponse{}, err
	}
	semantic := p.binding.Model.Task == pb.ModelTask_MODEL_TASK_EXTRACT || p.binding.Model.Task == pb.ModelTask_MODEL_TASK_RESOLVE
	var inputTokens uint64
	if semantic {
		if sha256String([]byte(r.SystemPrompt)) != p.binding.Model.PromptHash.Sha256 || r.MaxOutputTokens == 0 || r.MaxOutputTokens >= p.binding.Model.MaxTokens {
			return StructuredResponse{}, errors.New("semantic prompt or output budget differs from admitted model policy")
		}
		var err error
		inputTokens, err = p.counter.CountPrompt(ctx, r)
		if err != nil {
			return StructuredResponse{}, err
		}
		if inputTokens > uint64(p.binding.Model.MaxTokens-r.MaxOutputTokens) {
			return StructuredResponse{}, &ProviderError{Code: "context_window", Safe: "complete semantic prompt plus output budget exceeds admitted context window"}
		}
		// Counting and inference are separate calls; reject resident-model drift
		// between them before spending tokens on generation.
		if err := p.Ready(ctx); err != nil {
			return StructuredResponse{}, err
		}
	}
	response, err := p.provider.Generate(ctx, r)
	if err != nil {
		return StructuredResponse{}, err
	}
	if err = p.Ready(ctx); err != nil {
		return StructuredResponse{}, err
	}
	if semantic && response.InputTokens != inputTokens {
		return StructuredResponse{}, &ProviderError{Code: "prompt_usage_mismatch", Safe: "semantic prompt usage differs from exact preflight token count"}
	}
	return response, nil
}

func (p *PinnedLlama) CountPrompt(ctx context.Context, r StructuredRequest) (uint64, error) {
	if err := p.Ready(ctx); err != nil {
		return 0, err
	}
	return p.counter.CountPrompt(ctx, r)
}

func (p *PinnedLlama) CountText(ctx context.Context, value string) (uint64, error) {
	if p == nil {
		return 0, errors.New("admitted tokenizer required")
	}
	// Packing can call this per candidate; the full prompt and generation guards
	// recheck metadata rather than issuing a redundant props RPC for every block.
	return p.counter.CountText(ctx, value)
}

// Properties permit future extra fields but never duplicate/case-colliding keys.
// Bound the object cardinality; nested authority objects are validated separately.
func llamaPropertyObject(raw []byte) (map[string]json.RawMessage, error) {
	if !utf8.Valid(raw) {
		return nil, errors.New("invalid model properties encoding")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		return nil, errors.New("model properties must be an object")
	}
	fields := map[string]json.RawMessage{}
	seen := map[string]bool{}
	for d.More() {
		keyToken, e := d.Token()
		key, ok := keyToken.(string)
		fold := strings.ToLower(key)
		if e != nil || !ok || seen[fold] || len(fields) >= 1024 {
			return nil, errors.New("invalid model property keys")
		}
		seen[fold] = true
		var value json.RawMessage
		if e = d.Decode(&value); e != nil {
			return nil, errors.New("invalid model property value")
		}
		fields[key] = value
	}
	if _, err = d.Token(); err != nil {
		return nil, errors.New("invalid model properties")
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return nil, errors.New("trailing model properties")
	}
	return fields, nil
}
