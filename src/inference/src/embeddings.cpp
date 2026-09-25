// Adapter operasi embedding dan pascaproses tensor.
//
// Peran dalam komponen:
// Lokasi implementasi untuk header inference terkait.
//
// Integrasi dan perhatian performa:
// Pertahankan tokenizer, pooling, normalisasi, model/dimensi, serta batas panjang saat ekspor dari Python.
//
// Benchmark dan gate penerimaan:
// Ukur p50/p95/p99, throughput, waktu antre, serta RSS/VRAM sesuai workload. Ambang wajib ada di configs/benchmark-targets.yaml (REQUIRED_UNMEASURED); ukur cold/warm terpisah dan pertahankan kualitas sumber/versi.
//
// Status: native output validation active; CLS pooling and L2 normalization are exported in ONNX.
// Perhatian implementasi dan verifikasi:
// Implement exact tokenization, pooling and normalization for the selected export; return item-correlated vectors and truncation metadata.
// Bukti verifikasi: Check Python/native numeric and retrieval parity, dimensions/non-finite values, multilingual long inputs and queue-inclusive latency.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

#include "regulagraph/inference/embeddings.hpp"
#ifdef REGULAGRAPH_WITH_ONNX
#include <cmath>
#include <stdexcept>
namespace regulagraph::inference {
v1::Embedding CheckedEmbedding(const TensorResults& t, std::size_t row,
                                const v1::ModelManifest& m, std::size_t tokens) {
    if (m.task()!=v1::MODEL_TASK_EMBED || !m.has_dimensions() || t.columns!=m.dimensions() ||
        row>=t.rows || t.values.size()!=t.rows*t.columns || tokens==0 || tokens>m.max_tokens())
        throw std::runtime_error("embedding shape/model/token mismatch");
    v1::Embedding output; double norm=0;
    for (std::size_t j=0;j<t.columns;++j) {
        const float value=t.values[row*t.columns+j];
        if (!std::isfinite(value)) throw std::runtime_error("nonfinite embedding");
        norm+=double(value)*value; output.add_values(value);
    }
    // Integrity sanity check, not a numeric/retrieval parity acceptance threshold.
    if (std::abs(norm-1.0)>0.01) throw std::runtime_error("embedding is not L2 normalized");
    output.set_input_tokens(static_cast<std::uint32_t>(tokens));
    output.mutable_truncation()->set_original_tokens(tokens);
    output.mutable_truncation()->set_retained_tokens(tokens);
    return output;
}
}
#endif
