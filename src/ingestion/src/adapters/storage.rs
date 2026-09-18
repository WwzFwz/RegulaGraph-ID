//! Adapter pembacaan artefak sumber dan penulisan artefak batch lokal.
//!
//! Peran dalam komponen:
//! Menyediakan I/O sumber bagi worker tanpa memiliki publikasi database.
//!
//! Integrasi dan perhatian performa:
//! Pakai source locator/hash; atomic write artefak dan batas path; coordinator menerima descriptor batch, bukan salinan seluruh source berulang.
//!
//! Benchmark dan gate penerimaan:
//! Ukur p50/p95/p99, throughput, waktu antre, serta RSS/VRAM sesuai workload. Ambang wajib ada di configs/benchmark-targets.yaml (REQUIRED_UNMEASURED); ukur cold/warm terpisah dan pertahankan kualitas sumber/versi.
//!
//! Status: scaffold; belum ada pipeline atau layanan yang aktif.
