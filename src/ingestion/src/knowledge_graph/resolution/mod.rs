//! Deklarasi module tree untuk src/ingestion/src/knowledge_graph/resolution.
//!
//! Peran dalam komponen:
//! Penyelesaian identitas entitas dan pengelolaan canonical ID serta alias. Folder ini menentukan penyebutan mana yang menunjuk objek yang sama. Transformasi batch berjalan dalam Rust; inference semantik menggunakan adapter model.
//!
//! Integrasi dan perhatian performa:
//! Anak menerima mention beserta tipe dan konteks sumber. Penggabungan harus terlacak dan dapat dikoreksi; identitas pasal mencakup peraturan induknya, dan versi teks tidak disatukan secara destruktif.
//!
//! Benchmark dan gate penerimaan:
//! Ukur p50/p95/p99, throughput, waktu antre, serta RSS/VRAM sesuai workload. Ambang wajib ada di configs/benchmark-targets.yaml (REQUIRED_UNMEASURED); ukur cold/warm terpisah dan pertahankan kualitas sumber/versi.
//!
//! Status: blocking kandidat dan helper alias LINK aktif sebagai library; stage RESOLVE belum aktif.

pub mod aliases;
pub mod blocking;
pub mod resolver;
