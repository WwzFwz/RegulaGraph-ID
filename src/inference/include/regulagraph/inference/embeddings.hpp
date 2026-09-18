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
// Status: scaffold; belum ada pipeline atau layanan yang aktif.
#pragma once

namespace regulagraph::inference {
// Kontrak fungsi belum diimplementasikan.
}
