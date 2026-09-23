// Mengatur penilaian ulang relevansi kandidat dan pemilihan bukti berikutnya.
//
// Peran dalam komponen:
// Menggunakan adapter cross-encoder tanpa menempatkan kebijakan ranking di adapter.
//
// Kontrak integrasi dan perhatian implementasi:
// Ukur efek budget kandidat, panjang input, batch, dan perlindungan rantai bukti; tidak menetapkan top-k tetap tanpa eksperimen.
//
// Benchmark dan gate penerimaan:
// [RERANK] Bandingkan nDCG@k dan kelengkapan bukti sebelum/sesudah reranking; ukur p50/p95, batch size, panjang pasangan, dan truncation. Kandidat yang hilang sebelum reranking tidak dapat dipulihkan. Target mutu dan budget latency wajib mengikuti configs/benchmark-targets.yaml; perubahan memerlukan persetujuan pengguna.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: korelasi dan pengurutan hasil batch reranker aktif; pemilihan budget pasangan,
// transport inference, dan evaluasi kualitas belum aktif.
// Rekomendasi implementasi berikutnya:
// Select budgeted candidate pairs, call reranker in batches and map scores back without losing required multi-hop evidence.
// Bukti verifikasi: Test truncation, partial results and stable ties; measure ranking quality together with queue-inclusive latency.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

package retrieval

import (
	"errors"
	"math"
	"sort"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

// RerankedCandidate keeps the original fused evidence/provenance and explicit
// truncation metadata. A score is relevance ranking, never answer confidence.
// InputTokens and RetainedTokens are bounded by model.MaxTokens, but their exact
// equality depends on the selected tokenizer's special-token accounting.
type RerankedCandidate struct {
	Candidate   FusedCandidate
	Score       float64
	InputTokens uint32
	Truncation  *pb.TruncationInfo
}

// CorrelateRerankBatch requires one successful response per input candidate.
// pairIDs[i] is the stable pair sent for candidates[i]. Partial model output is
// an error, not permission to silently lose multi-hop evidence. The caller owns
// candidate selection and must verify query, text, snapshot, and model generation
// before sending the batch; this function checks the returned request/model IDs.
func CorrelateRerankBatch(candidates []FusedCandidate, pairIDs []string,
	response *pb.RerankBatchResponse, requestID string, model *pb.ModelManifest,
	maximumCandidates int) ([]RerankedCandidate, error) {
	if response == nil || model == nil || requestID == "" || maximumCandidates <= 0 ||
		len(candidates) == 0 || len(candidates) > maximumCandidates ||
		len(pairIDs) != len(candidates) || len(response.Results) != len(candidates) ||
		response.RequestId != requestID || !proto.Equal(response.Model, model) {
		return nil, errors.New("rerank batch cardinality, request, or model mismatch")
	}
	if model.Task != pb.ModelTask_MODEL_TASK_RERANK {
		return nil, errors.New("rerank batch requires a reranker model")
	}
	if err := domain.ValidateWire(response, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	positions := make(map[string]int, len(pairIDs))
	seenEvidence := make(map[string]bool, len(candidates))
	for index, pairID := range pairIDs {
		if pairID == "" || positions[pairID] != 0 {
			// The map uses index+1 so the first pair remains distinguishable.
			return nil, errors.New("rerank pair IDs must be distinct and nonempty")
		}
		if candidates[index].EvidenceKey == "" || seenEvidence[candidates[index].EvidenceKey] {
			return nil, errors.New("rerank evidence keys must be distinct and nonempty")
		}
		positions[pairID] = index + 1
		seenEvidence[candidates[index].EvidenceKey] = true
	}
	result := make([]RerankedCandidate, len(candidates))
	seenResults := make(map[string]bool, len(candidates))
	for _, item := range response.Results {
		if item == nil || seenResults[item.PairId] || positions[item.PairId] == 0 ||
			item.GetScore() == nil || item.GetError() != nil {
			return nil, errors.New("rerank returned duplicate, unknown, or failed pair")
		}
		score := item.GetScore()
		if math.IsNaN(score.Score) || math.IsInf(score.Score, 0) ||
			score.Truncation == nil || score.InputTokens == 0 ||
			score.InputTokens > model.MaxTokens ||
			score.Truncation.RetainedTokens > uint64(model.MaxTokens) ||
			score.Truncation.RetainedTokens > score.Truncation.OriginalTokens ||
			(score.Truncation.Truncated &&
				score.Truncation.RetainedTokens >= score.Truncation.OriginalTokens) ||
			(!score.Truncation.Truncated &&
				score.Truncation.RetainedTokens != score.Truncation.OriginalTokens) {
			return nil, errors.New("rerank returned invalid score or truncation")
		}
		index := positions[item.PairId] - 1
		candidate := candidates[index]
		candidate.Provenance = make([]*pb.Candidate, len(candidates[index].Provenance))
		for provenanceIndex, source := range candidates[index].Provenance {
			if source == nil {
				return nil, errors.New("rerank candidate has missing provenance")
			}
			candidate.Provenance[provenanceIndex] = proto.Clone(source).(*pb.Candidate)
		}
		result[index] = RerankedCandidate{Candidate: candidate, Score: score.Score,
			InputTokens: score.InputTokens,
			Truncation:  proto.Clone(score.Truncation).(*pb.TruncationInfo)}
		seenResults[item.PairId] = true
	}
	sort.SliceStable(result, func(i, j int) bool {
		return result[i].Score > result[j].Score
	})
	return result, nil
}
