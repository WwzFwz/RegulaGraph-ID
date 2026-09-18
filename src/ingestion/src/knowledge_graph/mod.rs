//! Deklarasi module tree untuk src/ingestion/src/knowledge_graph.
//!
//! Peran dalam komponen:
//! Pembangunan knowledge graph melalui extraction, resolution, assembly, summarization, dan validation. Graph menyimpan entitas serta hubungan yang dapat ditelusuri ke sumber. Transformasi batch berjalan dalam Rust; inference semantik menggunakan adapter model.
//!
//! Integrasi dan perhatian performa:
//! Anak meneruskan mention ID, canonical ID, jenis relasi, sumber teks, versi, dan status validasi. Ringkasan atau hasil interpretasi tidak menggantikan bukti primer; ekstraksi terstruktur belum menjamin kebenaran semantik.
//!
//! Benchmark dan gate penerimaan:
//! Ukur p50/p95/p99, throughput, waktu antre, serta RSS/VRAM sesuai workload. Ambang wajib ada di configs/benchmark-targets.yaml (REQUIRED_UNMEASURED); ukur cold/warm terpisah dan pertahankan kualitas sumber/versi.
//!
//! Status: scaffold; belum ada pipeline atau layanan yang aktif.

pub mod assembly;
pub mod extraction;
pub mod resolution;
pub mod schema;
pub mod summarization;
pub mod validation;
