// Client kontrak pekerjaan batch worker Rust.
//
// Peran dalam komponen:
// Menghubungkan workflow Go ke transformasi dokumen/graph.
//
// Integrasi dan perhatian performa:
// Belum ada protokol transport aktif. Pertahankan job/source/snapshot ID, deadline, status, batch size, dan referensi artefak; hindari pengiriman ulang seluruh dokumen.
//
// Benchmark dan gate penerimaan:
// Ukur p50/p95/p99, throughput, waktu antre, serta RSS/VRAM sesuai workload. Ambang wajib ada di configs/benchmark-targets.yaml (REQUIRED_UNMEASURED); ukur cold/warm terpisah dan pertahankan kualitas sumber/versi.
//
// Status: scaffold; belum ada pipeline atau layanan yang aktif.
package worker
