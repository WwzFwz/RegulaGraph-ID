// Lifecycle session inference dan pemilihan execution provider.
//
// Peran dalam komponen:
// Header publik wrapper inference native.
//
// Integrasi dan perhatian performa:
// Model dimuat sekali per lifecycle; ukur cold load terpisah dari warm inference; ONNX Runtime 1.22.0 dan bundle model dipin saat bootstrap.
//
// Benchmark dan gate penerimaan:
// Ukur p50/p95/p99, throughput, waktu antre, serta RSS/VRAM sesuai workload. Ambang wajib ada di configs/benchmark-targets.yaml (REQUIRED_UNMEASURED); ukur cold/warm terpisah dan pertahankan kualitas sumber/versi.
//
// Status: native model bundle/session interface; enabled with REGULAGRAPH_MODEL_RUNTIME.
// Perhatian implementasi dan verifikasi:
// Define the runtime interface with explicit ownership, lifetimes, typed errors and cancellation; keep implementation in matching .cpp.
// Bukti verifikasi: Measure cold start separately; test load failure cleanup, concurrent reuse and cancellation without per-request reload; header inclusion must not allocate model resources.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

#pragma once
#ifdef REGULAGRAPH_WITH_ONNX
#include "regulagraph/v1/inference.pb.h"
#include <atomic>
#include <filesystem>
#include <memory>
#include <string>
#include <vector>
namespace regulagraph::inference {
struct Tokens { std::vector<std::int64_t> ids, types; };
struct TensorResults { std::vector<float> values; std::size_t rows=0, columns=0; };
// One owner controls scheduling; Encode is const/thread-safe, Run is serialized by its worker.
class ModelRuntime {
public:
    ModelRuntime(const std::filesystem::path& bundle, const std::string& manifest_sha256,
                 unsigned threads=4, std::size_t maximum_padded_tokens=16384);
    ~ModelRuntime();
    ModelRuntime(const ModelRuntime&)=delete;
    ModelRuntime& operator=(const ModelRuntime&)=delete;
    const v1::ModelManifest& Manifest() const;
    Tokens Encode(const std::string& text, const std::string* pair=nullptr) const;
    TensorResults Run(const std::vector<Tokens>& inputs, const std::atomic<bool>& cancel);
    std::size_t MaximumPaddedTokens() const;
private:
    struct Impl;
    std::unique_ptr<Impl> impl_;
};
std::string HashBytes(const std::string& bytes);
std::string ReadBounded(const std::filesystem::path& path, std::size_t maximum);
}
#endif
