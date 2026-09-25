// Menyediakan adapter inference skor pasangan pertanyaan-teks.
//
// Peran dalam komponen:
// Mendukung src/server/internal/retrieval/reranking.go tanpa menentukan kandidat akhir di adapter.
//
// Kontrak integrasi dan perhatian implementasi:
// Catat model, max length, precision, batch size, dan truncation; skor bukan confidence jawaban.
//
// Benchmark dan gate penerimaan:
// [MODEL] Ukur cold load terpisah dari inference warm, throughput token/embedding/pasangan, p50/p95, RAM/VRAM, truncation, dan biaya. Catat model/version, precision, hardware, dan batch size. Cache harus memasukkan versi model serta input yang relevan; target numerik wajib mengikuti configs/benchmark-targets.yaml.
//
// [RERANK] Bandingkan nDCG@k dan kelengkapan bukti sebelum/sesudah reranking; ukur p50/p95, batch size, panjang pasangan, dan truncation. Kandidat yang hilang sebelum reranking tidak dapat dipulihkan. Target mutu dan budget latency wajib mengikuti configs/benchmark-targets.yaml; perubahan memerlukan persetujuan pengguna.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: reusable C01 client active with manifest, score and per-pair accounting validation.
//
// Batas runtime: file Go ini adalah client inference. Eksekusi tensor berada di src/inference atau provider eksternal; tidak ada pemuatan model lokal di handler Go.
// Perhatian implementasi dan verifikasi:
// Batch query-document pairs with stable IDs and verify model/results correlation, truncation and cancellation.
// Bukti verifikasi: Test partial scores, timeout, unexpected pairs and length limits; keep original evidence identity through ranking.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

package inference

import (
	"context"
	"errors"
	"google.golang.org/grpc"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func (c *NativeClient) RerankBatch(ctx context.Context, request *pb.RerankBatchRequest) (*pb.RerankBatchResponse, error) {
	if request == nil {
		return nil, errors.New("rerank request required")
	}
	call, cancel, err := c.request(ctx, request, request.Context, request.Model, len(request.Pairs))
	if err != nil {
		return nil, err
	}
	defer cancel()
	result, err := c.client.RerankBatch(call, request, grpc.MaxCallSendMsgSize(nativeMessageBytes), grpc.MaxCallRecvMsgSize(nativeMessageBytes))
	if err != nil {
		return nil, err
	}
	if err = domain.VerifyRerankResults(request, result); err != nil {
		return nil, err
	}
	for _, item := range result.Results {
		if failure := item.GetError(); failure != nil {
			if failure.ItemId != nil && *failure.ItemId != item.PairId {
				return nil, errors.New("rerank error pair mismatch")
			}
			continue
		}
		score := item.GetScore()
		if score == nil {
			return nil, errors.New("rerank result missing score/error")
		}
		if err = nativeTokens(request.Model, score.InputTokens, score.Truncation); err != nil {
			return nil, err
		}
	}
	return result, nil
}
