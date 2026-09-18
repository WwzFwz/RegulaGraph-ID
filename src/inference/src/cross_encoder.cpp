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
// Status: scaffold; belum ada pipeline atau layanan yang aktif.
#include "regulagraph/inference/cross_encoder.hpp"

namespace regulagraph::inference {
// Implementasi runtime akan ditambahkan bersama pemilihan backend.
}
