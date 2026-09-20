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
//! Rekomendasi implementasi berikutnya (belum merupakan fitur aktif):
//! Read coordinator-authorized artifacts by locator/hash and emit immutable batch artifacts for Go publication.
//! Bukti verifikasi: Test checksum mismatch, truncated reads and retry cleanup; reject unsafe paths and avoid writing publication markers.
//! Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.
