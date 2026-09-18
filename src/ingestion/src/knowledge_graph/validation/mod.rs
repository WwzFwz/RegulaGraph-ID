//! Deklarasi module tree untuk src/ingestion/src/knowledge_graph/validation.
//!
//! Peran dalam komponen:
//! Pemeriksaan invariant struktural dan kualitas graph sebelum hasil dipublikasikan ke snapshot yang digunakan retrieval. Transformasi batch berjalan dalam Rust; inference semantik menggunakan adapter model.
//!
//! Integrasi dan perhatian performa:
//! Anak melaporkan endpoint hilang, merge mencurigakan, bukti terputus, konflik versi, dan duplikasi. Bedakan error pemblokir dari diagnosis; node terpisah bisa memang benar.
//!
//! Benchmark dan gate penerimaan:
//! Ukur p50/p95/p99, throughput, waktu antre, serta RSS/VRAM sesuai workload. Ambang wajib ada di configs/benchmark-targets.yaml (REQUIRED_UNMEASURED); ukur cold/warm terpisah dan pertahankan kualitas sumber/versi.
//!
//! Status: scaffold; belum ada pipeline atau layanan yang aktif.

pub mod checks;
