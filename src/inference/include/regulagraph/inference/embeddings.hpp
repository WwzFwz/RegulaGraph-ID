// Adapter operasi embedding dan pascaproses tensor.
//
// Peran dalam komponen:
// Header publik wrapper inference native.
//
// Integrasi dan perhatian performa:
// Pertahankan tokenizer, pooling, normalisasi, model/dimensi, serta batas panjang saat ekspor dari Python.
//
// Benchmark dan gate penerimaan:
// Ukur p50/p95/p99, throughput, waktu antre, serta RSS/VRAM sesuai workload. Ambang wajib ada di configs/benchmark-targets.yaml (REQUIRED_UNMEASURED); ukur cold/warm terpisah dan pertahankan kualitas sumber/versi.
//
// Status: validates native exported CLS/L2 tensors; pooling executes inside the pinned graph.
// Perhatian implementasi dan verifikasi:
// Define the embeddings interface with explicit ownership, lifetimes, typed errors and cancellation; keep implementation in matching .cpp.
// Bukti verifikasi: Check Python/native numeric and retrieval parity, dimensions/non-finite values, multilingual long inputs and queue-inclusive latency; header inclusion must not allocate model resources.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

#pragma once
#ifdef REGULAGRAPH_WITH_ONNX
#include "regulagraph/inference/runtime.hpp"
namespace regulagraph::inference {
v1::Embedding CheckedEmbedding(const TensorResults& tensor, std::size_t row,
                                const v1::ModelManifest& model, std::size_t tokens);
}
#endif
