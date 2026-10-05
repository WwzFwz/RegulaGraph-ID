// Coordinates the interview preview: immutable BM25 retrieval followed by a
// bounded optional model draft. Only one generation runs at a time; overload is
// explicit and request deadlines/cancellation propagate. Evidence survives model
// failure without being relabelled as a generated answer. This does not publish
// a production snapshot, assert legal applicability, or claim GraphRAG quality.
package workflows

import (
	"context"
	"errors"
	"time"

	"regulagraph.local/server/internal/answering"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/retrieval"
)

var ErrPreviewBusy = errors.New("demo is answering another question; retry shortly")

type Preview struct {
	index     *retrieval.PreviewIndex
	generator *answering.PreviewGenerator
	slots     chan struct{}
}

func NewPreview(index *retrieval.PreviewIndex, generator *answering.PreviewGenerator) *Preview {
	return &Preview{index: index, generator: generator, slots: make(chan struct{}, 1)}
}
func (p *Preview) Ask(ctx context.Context, question string, generate bool) (*domain.PreviewAnswer, error) {
	start := time.Now()
	sources, err := p.index.Search(ctx, question, 5)
	if err != nil {
		return nil, err
	}
	result := &domain.PreviewAnswer{Status: "evidence_only", Sources: sources, RetrievalMS: float64(time.Since(start).Microseconds()) / 1000, Notice: "Demo BM25 pada sampel PDF. Teks historis; keberlakuan hukum dan dukungan semantik jawaban belum diverifikasi."}
	if len(sources) == 0 {
		result.Status = "no_evidence"
		return result, nil
	}
	if !generate || p.generator == nil {
		return result, nil
	}
	select {
	case p.slots <- struct{}{}:
		defer func() { <-p.slots }()
	default:
		return nil, ErrPreviewBusy
	}
	result.Model = p.generator.Model()
	start = time.Now()
	claims, abstain, err := p.generator.Generate(ctx, question, sources)
	result.GenerationMS = float64(time.Since(start).Microseconds()) / 1000
	if err != nil {
		result.Status = "generation_failed"
		result.Notice = "Model gagal atau hasil tidak lolos validasi. Kutipan pencarian tetap tersedia; coba lagi atau gunakan pencarian saja."
		return result, nil
	}
	if abstain {
		result.Status = "abstain"
		return result, nil
	}
	result.Status = "unreviewed_draft"
	result.Claims = claims
	return result, nil
}
