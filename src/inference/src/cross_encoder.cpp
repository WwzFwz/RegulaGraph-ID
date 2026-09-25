// Adapter penilaian pasangan query-teks oleh reranker.
//
// Peran dalam komponen:
// Lokasi implementasi untuk header inference terkait.
//
// Integrasi dan perhatian performa:
// Catat panjang pasangan, batch size, precision, dan truncation; pemilihan kandidat akhir tetap di Go.
//
// Benchmark dan gate penerimaan:
// Ukur p50/p95/p99, throughput, waktu antre, serta RSS/VRAM sesuai workload. Ambang wajib ada di configs/benchmark-targets.yaml (REQUIRED_UNMEASURED); ukur cold/warm terpisah dan pertahankan kualitas sumber/versi.
//
// Status: validasi raw logit reranker aktif; layanan C01 memegang tokenisasi/pair correlation.
// Perhatian implementasi dan verifikasi:
// Implement batched pair tokenization/scoring with stable pair IDs, raw-logit score interpretation (bukan confidence terkalibrasi) and explicit truncation.
// Bukti verifikasi: Check Python/native score/rank parity including long legal clauses; measure reranking quality, batch wait and throughput.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

#include "regulagraph/inference/cross_encoder.hpp"
#ifdef REGULAGRAPH_WITH_ONNX
#include <cmath>
#include <stdexcept>
namespace regulagraph::inference {
v1::RerankScore CheckedScore(const TensorResults& t, std::size_t row,
                            const v1::ModelManifest& m, std::size_t tokens) {
    if (m.task()!=v1::MODEL_TASK_RERANK || t.columns!=1 || row>=t.rows || t.values.size()!=t.rows ||
        tokens==0 || tokens>m.max_tokens() || !std::isfinite(t.values[row]))
        throw std::runtime_error("rerank shape/model/token mismatch");
    v1::RerankScore output; output.set_score(t.values[row]);
    output.set_input_tokens(static_cast<std::uint32_t>(tokens));
    output.mutable_truncation()->set_original_tokens(tokens);
    output.mutable_truncation()->set_retained_tokens(tokens);
    return output;
}
}
#endif
