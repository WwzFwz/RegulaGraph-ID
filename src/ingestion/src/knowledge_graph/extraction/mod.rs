//! Deklarasi module tree untuk src/ingestion/src/knowledge_graph/extraction.
//!
//! Peran dalam komponen:
//! Ekstraksi penyebutan entitas dan relasi dari konteks dokumen dengan keluaran terstruktur dan bukti sumber. Transformasi batch berjalan dalam Rust; inference semantik menggunakan adapter model.
//!
//! Integrasi dan perhatian performa:
//! Anak memakai schema yang konsisten, mencatat model dan prompt version, serta membawa rentang sumber untuk entitas dan relasi. Pisahkan hubungan eksplisit dari interpretasi dan pertahankan kondisi, negasi, serta pengecualian.
//!
//! Benchmark dan gate penerimaan:
//! Ukur p50/p95/p99, throughput, waktu antre, serta RSS/VRAM sesuai workload. Ambang wajib ada di configs/benchmark-targets.yaml (REQUIRED_UNMEASURED); ukur cold/warm terpisah dan pertahankan kualitas sumber/versi.
//!
//! Status: extractor memvalidasi batch EXTRACT terhadap sumber sebelum persistence.
//! Prompt terpin berada di configs/prompts dan dieksekusi melalui gateway Go;
//! kelulusan validator bukan bukti kualitas model atau acceptance corpus.

pub mod extractor;
