//! Deklarasi module tree untuk src/ingestion/src/indexing.
//!
//! Peran dalam komponen:
//! Persiapan representasi dense/lexical dan record indeks di worker Rust.
//!
//! Integrasi dan perhatian performa:
//! Hasilnya batch untuk Go publisher. Learned sparse dan BM25 tetap dibedakan; folder ini tidak menulis publication marker.
//!
//! Benchmark dan gate penerimaan:
//! Ukur p50/p95/p99, throughput, waktu antre, serta RSS/VRAM sesuai workload. Ambang wajib ada di configs/benchmark-targets.yaml (REQUIRED_UNMEASURED); ukur cold/warm terpisah dan pertahankan kualitas sumber/versi.
//!
//! Status: scaffold; belum ada pipeline atau layanan yang aktif.

pub mod dense;
pub mod lexical;
