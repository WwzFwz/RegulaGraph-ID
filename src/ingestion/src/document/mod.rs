//! Deklarasi module tree untuk src/ingestion/src/document.
//!
//! Peran dalam komponen:
//! Pengambilan dan transformasi dokumen menjadi teks terstruktur, chunk, serta informasi perubahan dan versi. Komponen ini menyediakan masukan yang dapat dilacak untuk knowledge graph dan indexing. Rust menangani transformasi dokumen; akuisisi jaringan dan penjadwalan berada di Go.
//!
//! Integrasi dan perhatian performa:
//! Anak-anak mempertahankan source ID, content hash, lokasi teks, struktur induk, dan status parsing. Bedakan perubahan isi file dari perubahan keberlakuan ketentuan; hasil gagal tidak diterbitkan sebagai hasil lengkap.
//!
//! Benchmark dan gate penerimaan:
//! Ukur p50/p95/p99, throughput, waktu antre, serta RSS/VRAM sesuai workload. Ambang wajib ada di configs/benchmark-targets.yaml (REQUIRED_UNMEASURED); ukur cold/warm terpisah dan pertahankan kualitas sumber/versi.
//!
//! Status: parser PDFium, normalizer, structural chunking, dan incremental change planner aktif serta
//! terhubung ke kontrak C01; versioning dan executable worker masih scaffold.

pub mod change_detection;
pub mod chunking;
pub mod normalization;
pub mod parsing;
pub mod versioning;
