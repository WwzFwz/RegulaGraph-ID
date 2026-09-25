// Menyediakan client Go untuk embedding dokumen/pertanyaan dengan BGE-M3 sebagai kandidat baseline.
//
// Peran dalam komponen:
// Memanggil runtime native atau provider melalui kontrak inference; eksekusi tensor tidak berada dalam package Go ini.
//
// Kontrak integrasi dan perhatian implementasi:
// Bedakan dense, learned sparse, dan multi-vector; batching/truncation serta versi model dicatat, dan load tidak terjadi saat inisialisasi paket.
//
// Benchmark dan gate penerimaan:
// [MODEL] Ukur cold load terpisah dari inference warm, throughput token/embedding/pasangan, p50/p95, RAM/VRAM, truncation, dan biaya. Catat model/version, precision, hardware, dan batch size. Cache harus memasukkan versi model serta input yang relevan; target numerik wajib mengikuti configs/benchmark-targets.yaml.
//
// [INDEX] Ukur konsistensi ID/versi antarindeks, freshness lag, throughput indexing, ukuran indeks, dan retrieval Recall@k. Gate: penghapusan/upsert idempotent dan model/dimensi representasi cocok. Target kapasitas wajib mengikuti profil corpus/hardware dalam configs/benchmark-targets.yaml.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: reusable C01 client active with exact manifest, correlation and vector validation.
//
// Batas runtime: file Go ini adalah client inference. Eksekusi tensor berada di src/inference atau provider eksternal; tidak ada pemuatan model lokal di handler Go.
// Perhatian implementasi dan verifikasi:
// Reuse a native client; send purpose/model-bound batches and validate one-to-one results before retrieval/indexing.
// Bukti verifikasi: Test reordered/duplicate/missing outputs, dimension drift and explicit per-item errors; trace queue vs compute time.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

package inference

import (
	"context"
	"errors"
	"google.golang.org/grpc"
	"math"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func (c *NativeClient) EmbedBatch(ctx context.Context, request *pb.EmbedBatchRequest) (*pb.EmbedBatchResponse, error) {
	if request == nil {
		return nil, errors.New("embedding request required")
	}
	call, cancel, err := c.request(ctx, request, request.Context, request.Model, len(request.Items))
	if err != nil {
		return nil, err
	}
	defer cancel()
	result, err := c.client.EmbedBatch(call, request, grpc.MaxCallSendMsgSize(nativeMessageBytes), grpc.MaxCallRecvMsgSize(nativeMessageBytes))
	if err != nil {
		return nil, err
	}
	limits := domain.DefaultWireLimits
	limits.MaxBytes = nativeMessageBytes
	limits.MaxItems = 128*4096 + 100000
	if err = domain.VerifyEmbeddingResultsWithLimits(request, result, limits); err != nil {
		return nil, err
	}
	for _, item := range result.Results {
		if failure := item.GetError(); failure != nil {
			if failure.ItemId != nil && *failure.ItemId != item.ItemId {
				return nil, errors.New("embedding error item mismatch")
			}
			continue
		}
		embedding := item.GetEmbedding()
		if embedding == nil {
			return nil, errors.New("embedding result missing value/error")
		}
		if err = nativeTokens(request.Model, embedding.InputTokens, embedding.Truncation); err != nil {
			return nil, err
		}
		norm := 0.0
		for _, value := range embedding.Values {
			norm += float64(value) * float64(value)
		}
		if math.IsNaN(norm) || math.IsInf(norm, 0) || (request.Model.Normalization == "l2" && math.Abs(norm-1) > 0.01) {
			return nil, errors.New("embedding normalization invalid")
		}
	}
	return result, nil
}
