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
//! Status: statistik BM25 lokal, analyzer lexical v1, dictionary reader dan pembobot
//! sparse aktif sebagai library; pipeline indeks, writer, dan layanan belum aktif.

pub mod analyzer;
pub mod dense;
pub mod dictionary;
mod letter_ranges;
pub mod lexical;
mod nfc_properties;
pub mod statistics;
