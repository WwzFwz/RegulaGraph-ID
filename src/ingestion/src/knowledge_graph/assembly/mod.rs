//! Deklarasi module tree untuk src/ingestion/src/knowledge_graph/assembly.
//!
//! Peran dalam komponen:
//! Penggabungan entitas yang telah diselesaikan identitasnya dan relasi menjadi perubahan graph yang konsisten. Transformasi batch berjalan dalam Rust; inference semantik menggunakan adapter model.
//!
//! Integrasi dan perhatian performa:
//! Anak memetakan endpoint ke canonical ID, mempertahankan banyak bukti untuk satu relasi, dan menghasilkan perubahan idempotent. Penghapusan satu sumber tidak boleh menghapus relasi yang masih didukung sumber lain.
//!
//! Benchmark dan gate penerimaan:
//! Ukur p50/p95/p99, throughput, waktu antre, serta RSS/VRAM sesuai workload. Ambang wajib ada di configs/benchmark-targets.yaml (REQUIRED_UNMEASURED); ukur cold/warm terpisah dan pertahankan kualitas sumber/versi.
//!
//! Status: scaffold; belum ada pipeline atau layanan yang aktif.

pub mod builder;
