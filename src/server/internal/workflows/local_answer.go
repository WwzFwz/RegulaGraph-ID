// Composes the admitted resident local generator and exact tokenizer into the
// existing evidence-answer workflow. Startup hashes model bytes once; prepared
// query factories reuse this handle across snapshot changes. The caller owns
// authorized configuration, request deadlines, snapshot leases and graceful drain.
// Measure cold admission separately from warm RAG latency; benchmark acceptance
// and semantic review remain independent of successful composition.
package workflows

import (
	"context"
	"errors"
	"time"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/inference"
	"regulagraph.local/server/internal/answering"
	"regulagraph.local/server/internal/config"
)

type LocalAnswerRuntime struct {
	Workflow *EvidenceAnswerWorkflow
	model    *inference.PinnedLlama
	provider *inference.OpenAICompatibleProvider
}

func OpenLocalAnswer(ctx context.Context, cfg *config.AnswerGeneratorConfig, key, build string, timeout time.Duration) (*LocalAnswerRuntime, error) {
	if ctx == nil || cfg == nil || cfg.Binding.Model == nil || build == "" || cfg.MaximumEvidence < 1 || cfg.MaximumEvidence > 256 || cfg.MaximumContextTokens == 0 || cfg.OutputTokens >= cfg.Binding.Model.MaxTokens || cfg.MaximumContextTokens > uint64(cfg.Binding.Model.MaxTokens-cfg.OutputTokens) {
		return nil, errors.New("validated answer config, build and bounded context required")
	}
	producer := &pb.ProducerManifest{Software: "regulagraph-local-answer", Build: build, SchemaVersion: 1, ConfigHash: &pb.ContentHash{Sha256: cfg.FileHash}, Models: []*pb.ModelManifest{proto.Clone(cfg.Binding.Model).(*pb.ModelManifest)}, PromptHashes: []*pb.ContentHash{answering.DraftPromptHash()}, InputHashes: []*pb.ContentHash{cfg.Binding.TemplateHash}}
	provider, err := inference.NewOpenAICompatibleProvider(inference.OpenAICompatibleConfig{Endpoint: cfg.Endpoint, APIKey: key, Timeout: timeout, MaximumResponseBytes: 6*int64(cfg.MaximumOutputBytes) + (64 << 10)})
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			provider.Close()
		}
	}()
	model, err := inference.AdmitLlama(ctx, provider, cfg.Binding, cfg.MaximumInputBytes)
	if err != nil {
		return nil, err
	}
	generator, err := answering.NewDraftGenerator(model, model.CountPrompt, answering.DraftGeneratorConfig{Model: cfg.Binding.Model, Producer: producer, MaximumInputBytes: cfg.MaximumInputBytes, MaximumOutputBytes: cfg.MaximumOutputBytes, MaximumClaims: cfg.MaximumClaims, MaximumCitations: cfg.MaximumCitations, MaximumConcurrent: cfg.MaximumConcurrent, OutputTokens: cfg.OutputTokens, AllowUnreviewedDrafts: true})
	if err != nil {
		return nil, err
	}
	success = true
	return &LocalAnswerRuntime{Workflow: &EvidenceAnswerWorkflow{Generator: generator, ContextTokenizer: proto.Clone(cfg.Binding.Model.TokenizerHash).(*pb.ContentHash), CountContext: model.CountText, MaximumContextTokens: cfg.MaximumContextTokens, MaximumEvidence: cfg.MaximumEvidence}, model: model, provider: provider}, nil
}

func (r *LocalAnswerRuntime) Ready(ctx context.Context) error {
	if r == nil {
		return errors.New("answer runtime unavailable")
	}
	return r.model.Ready(ctx)
}
func (r *LocalAnswerRuntime) Close() {
	if r != nil {
		r.provider.Close()
	}
}
