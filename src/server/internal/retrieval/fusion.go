// Menggabungkan daftar kandidat dari jalur lexical, dense, dan graph.
//
// Peran dalam komponen:
// Menghasilkan kandidat bersama dengan provenance dan ranking asal yang masih terlacak.
//
// Kontrak integrasi dan perhatian implementasi:
// RRF menjadi baseline eksperimen; jangan menjumlahkan raw score yang berbeda skala.
// Deduplikasi memakai identitas bukti/versi. Caller wajib membentuk evidence_key dari identitas
// bukti/versi yang sudah diverifikasi dan memfilter semua cabang pada snapshot yang sama.
// Skor fusion tidak ditafsirkan sebagai probabilitas relevansi model.
//
// Benchmark dan gate penerimaan:
// [RETRIEVAL] Ukur Recall@k, nDCG@k, kelengkapan semua bukti multi-hop, serta latency
// p50/p95/p99 per jenis pertanyaan. Catat jumlah kandidat, hop, snapshot, dan kondisi
// cold/warm. Ambang wajib mengikuti configs/benchmark-targets.yaml; jangan menganggap batas
// hop/kandidat atau graph selalu meningkatkan kualitas.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: fungsi RRF deterministik dan berbatas sudah aktif; integrasi retriever dan pengukuran
// kualitas/performa corpus produksi belum dilakukan.
// Rekomendasi implementasi berikutnya:
// Hubungkan daftar kandidat yang sudah difilter dari snapshot yang sama, lalu evaluasi dampak
// fusion terhadap recall, nDCG, bukti multi-hop, dan biaya kandidat.
// Bukti verifikasi: tes tie, cabang kosong, duplikasi antar cabang, rank invalid, keputusan
// filter absen/ditolak, dan batas input; benchmark pada corpus tetap diperlukan.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.
package retrieval

import (
	"errors"
	"fmt"
	"math"
	"sort"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

// RankedBranch contains one retriever's filtered, rank-ordered candidates.
type RankedBranch struct {
	Kind       pb.RetrieverKind
	Candidates []*pb.Candidate
}

// RRFConfig is explicit so experiments can pin K, branch weights, and input budgets.
type RRFConfig struct {
	K                  uint32
	Weights            map[pb.RetrieverKind]float64
	MaximumPerBranch   int
	MaximumTotalInputs int
}

// FusedCandidate preserves all original branch ranks and scores for later evidence hydration.
// Score is only a fusion score; it is never substituted for a model relevance probability.
type FusedCandidate struct {
	EvidenceKey string
	Score       float64
	Provenance  []*pb.Candidate
	BestRank    uint32
}

// FuseRRF rejects oversized or unfiltered input instead of silently dropping possible evidence.
// A repeated evidence key across branches is one result; within a branch it is an input error.
func FuseRRF(branches []RankedBranch, config RRFConfig) ([]FusedCandidate, error) {
	if config.K == 0 || config.MaximumPerBranch <= 0 || config.MaximumTotalInputs <= 0 ||
		len(branches) == 0 || len(branches) > 3 {
		return nil, errors.New("bounded RRF configuration and at least one branch are required")
	}
	byKey := make(map[string]*FusedCandidate)
	seenKinds := map[pb.RetrieverKind]bool{}
	total := 0
	ordered := append([]RankedBranch(nil), branches...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Kind < ordered[j].Kind })
	for _, branch := range ordered {
		if branch.Kind != pb.RetrieverKind_RETRIEVER_KIND_DENSE &&
			branch.Kind != pb.RetrieverKind_RETRIEVER_KIND_BM25 &&
			branch.Kind != pb.RetrieverKind_RETRIEVER_KIND_GRAPH || seenKinds[branch.Kind] {
			return nil, errors.New("RRF branches require distinct known retriever kinds")
		}
		seenKinds[branch.Kind] = true
		weight, ok := config.Weights[branch.Kind]
		if !ok || math.IsNaN(weight) || math.IsInf(weight, 0) || weight <= 0 {
			return nil, errors.New("each RRF branch requires a finite positive weight")
		}
		if len(branch.Candidates) > config.MaximumPerBranch ||
			len(branch.Candidates) > config.MaximumTotalInputs-total {
			return nil, errors.New("RRF input exceeds configured candidate budget")
		}
		total += len(branch.Candidates)
		lastRank := uint32(0)
		seenInBranch := map[string]bool{}
		for _, candidate := range branch.Candidates {
			if candidate == nil || candidate.Retriever != branch.Kind || candidate.Rank <= lastRank ||
				seenInBranch[candidate.EvidenceKey] {
				return nil, fmt.Errorf("invalid rank, retriever, or duplicate evidence in %s branch", branch.Kind)
			}
			if err := domain.ValidateWire(candidate, domain.DefaultWireLimits); err != nil {
				return nil, fmt.Errorf("invalid RRF candidate: %w", err)
			}
			if len(candidate.FilterDecisions) == 0 {
				return nil, errors.New("candidate has no recorded filter decisions")
			}
			for _, decision := range candidate.FilterDecisions {
				if !decision.Accepted {
					return nil, errors.New("rejected candidate reached RRF before filtering")
				}
			}
			lastRank = candidate.Rank
			seenInBranch[candidate.EvidenceKey] = true
			item := byKey[candidate.EvidenceKey]
			if item == nil {
				item = &FusedCandidate{EvidenceKey: candidate.EvidenceKey, BestRank: candidate.Rank}
				byKey[candidate.EvidenceKey] = item
			}
			if candidate.Rank < item.BestRank {
				item.BestRank = candidate.Rank
			}
			item.Score += weight / (float64(config.K) + float64(candidate.Rank))
			if math.IsNaN(item.Score) || math.IsInf(item.Score, 0) {
				return nil, errors.New("RRF score overflow")
			}
			item.Provenance = append(item.Provenance, proto.Clone(candidate).(*pb.Candidate))
		}
	}
	result := make([]FusedCandidate, 0, len(byKey))
	for _, item := range byKey {
		result = append(result, *item)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Score != result[j].Score {
			return result[i].Score > result[j].Score
		}
		if result[i].BestRank != result[j].BestRank {
			return result[i].BestRank < result[j].BestRank
		}
		return result[i].EvidenceKey < result[j].EvidenceKey
	})
	return result, nil
}
