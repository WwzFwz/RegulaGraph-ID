// Loads the complete local answer-generator profile from exact pinned JSON bytes.
// The file owns corpus, C01 model, local server/template and answer limits, but
// never credentials. Loading validates configuration without contacting a server;
// inference admission separately hashes GGUF and checks resident properties.
// The answer profile accepts GENERATE only; shared local admission also supports
// semantic tasks, which must not broaden the answering configuration boundary.
// Required quality/latency remains benchmark-targets.yaml, not a config assertion.
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"unicode/utf8"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/inference"
	"regulagraph.local/server/internal/answering"
)

type AnswerGeneratorConfig struct {
	Corpus, Endpoint, FileHash                                                                                 string
	Binding                                                                                                    inference.LlamaModelBinding
	MaximumInputBytes, MaximumOutputBytes, MaximumClaims, MaximumCitations, MaximumConcurrent, MaximumEvidence int
	OutputTokens                                                                                               uint32
	MaximumContextTokens                                                                                       uint64
}

func LoadAnswerGenerator(path, expectedHash, corpus string) (*AnswerGeneratorConfig, error) {
	if path == "" || !lowerSHA256.MatchString(expectedHash) || !candidatePolicyCorpusIDPattern.MatchString(corpus) {
		return nil, errors.New("answer generator path/hash/corpus required")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("answer generator config unavailable")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > 64<<10 {
		return nil, errors.New("answer generator config must be a regular file within 64 KiB")
	}
	raw, err := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	if err != nil || len(raw) > 64<<10 || !utf8.Valid(raw) {
		return nil, errors.New("answer generator config bytes invalid")
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != expectedHash {
		return nil, errors.New("answer generator config hash mismatch")
	}
	if err = rejectDuplicateCandidateJSONKeys(raw); err != nil {
		return nil, err
	}
	root, err := candidatePolicyObject(raw, []string{"schema_version", "corpus", "endpoint", "gguf_path", "server_build", "chat_template_sha256", "model", "limits", "allow_unreviewed_drafts"}, nil)
	if err != nil {
		return nil, err
	}
	if _, err = candidatePolicyObject(root["limits"], []string{"maximum_input_bytes", "maximum_output_bytes", "maximum_claims", "maximum_citations", "maximum_concurrent", "maximum_evidence", "output_tokens", "maximum_context_tokens"}, nil); err != nil {
		return nil, err
	}
	var file struct {
		SchemaVersion   int    `json:"schema_version"`
		Corpus          string `json:"corpus"`
		Endpoint        string `json:"endpoint"`
		GGUFPath        string `json:"gguf_path"`
		ServerBuild     string `json:"server_build"`
		TemplateHash    string `json:"chat_template_sha256"`
		AllowUnreviewed bool   `json:"allow_unreviewed_drafts"`
		Limits          struct {
			MaximumInputBytes    int    `json:"maximum_input_bytes"`
			MaximumOutputBytes   int    `json:"maximum_output_bytes"`
			MaximumClaims        int    `json:"maximum_claims"`
			MaximumCitations     int    `json:"maximum_citations"`
			MaximumConcurrent    int    `json:"maximum_concurrent"`
			MaximumEvidence      int    `json:"maximum_evidence"`
			OutputTokens         uint32 `json:"output_tokens"`
			MaximumContextTokens uint64 `json:"maximum_context_tokens"`
		} `json:"limits"`
	}
	if err = json.Unmarshal(raw, &file); err != nil {
		return nil, err
	}
	if file.SchemaVersion != 1 || file.Corpus != corpus || !file.AllowUnreviewed {
		return nil, errors.New("answer schema/corpus mismatch or draft policy not enabled")
	}
	u, err := url.Parse(file.Endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || !net.ParseIP(u.Hostname()).IsLoopback() || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, errors.New("answer generator requires an explicit literal loopback URL")
	}
	model := new(pb.ModelManifest)
	if err = protojson.Unmarshal(root["model"], model); err != nil {
		return nil, err
	}
	if model.Task != pb.ModelTask_MODEL_TASK_GENERATE {
		return nil, errors.New("answer generator requires a GENERATE model")
	}
	if !proto.Equal(model.PromptHash, answering.DraftPromptHash()) {
		return nil, errors.New("answer system prompt pin mismatch")
	}
	binding := inference.LlamaModelBinding{Model: model, GGUFPath: file.GGUFPath, ServerBuild: file.ServerBuild, TemplateHash: &pb.ContentHash{Sha256: file.TemplateHash}}
	if err = binding.Validate(); err != nil {
		return nil, err
	}
	l := file.Limits
	if l.MaximumInputBytes < 1 || l.MaximumInputBytes > 8<<20 || l.MaximumOutputBytes < 1 || l.MaximumOutputBytes > 1<<20 || l.MaximumClaims < 1 || l.MaximumClaims > 256 || l.MaximumCitations < 1 || l.MaximumCitations > 4096 || l.MaximumConcurrent < 1 || l.MaximumConcurrent > 128 || l.MaximumEvidence < 1 || l.MaximumEvidence > 256 || l.OutputTokens == 0 || l.OutputTokens >= model.MaxTokens || l.MaximumContextTokens == 0 || l.MaximumContextTokens > uint64(model.MaxTokens-l.OutputTokens) {
		return nil, errors.New("invalid bounded answer budgets")
	}
	return &AnswerGeneratorConfig{Corpus: corpus, Endpoint: file.Endpoint, FileHash: expectedHash, Binding: binding, MaximumInputBytes: l.MaximumInputBytes, MaximumOutputBytes: l.MaximumOutputBytes, MaximumClaims: l.MaximumClaims, MaximumCitations: l.MaximumCitations, MaximumConcurrent: l.MaximumConcurrent, MaximumEvidence: l.MaximumEvidence, OutputTokens: l.OutputTokens, MaximumContextTokens: l.MaximumContextTokens}, nil
}
