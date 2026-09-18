//! Deklarasi module tree untuk src/ingestion/src/knowledge_graph/summarization.
//!
//! Peran dalam komponen:
//! Pembuatan ringkasan entitas dari kumpulan bukti yang terhubung. Ringkasan merupakan artefak turunan untuk membantu navigasi dan penyediaan konteks. Transformasi batch berjalan dalam Rust; inference semantik menggunakan adapter model.
//!
//! Integrasi dan perhatian performa:
//! Anak menyimpan daftar dependensi sumber, model/prompt version, dan status kesegaran. Perubahan isi sumber memicu invalidasi meskipun daftar source ID tetap sama.
//!
//! Benchmark dan gate penerimaan:
//! Ukur p50/p95/p99, throughput, waktu antre, serta RSS/VRAM sesuai workload. Ambang wajib ada di configs/benchmark-targets.yaml (REQUIRED_UNMEASURED); ukur cold/warm terpisah dan pertahankan kualitas sumber/versi.
//!
//! Status: scaffold; belum ada pipeline atau layanan yang aktif.

pub mod profiles;
