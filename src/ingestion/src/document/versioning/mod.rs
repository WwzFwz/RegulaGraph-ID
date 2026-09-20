//! Deklarasi module tree untuk src/ingestion/src/document/versioning.
//!
//! Peran dalam komponen:
//! Pemodelan identitas dan versi ketentuan serta perubahan yang didukung sumber. Komponen ini menyiapkan metadata temporal yang dipakai retrieval dan graph. Implementasi transformasi berada di Rust.
//!
//! Integrasi dan perhatian performa:
//! Anak memisahkan identitas pasal dari teks versinya, menyimpan bukti perubahan, serta membedakan waktu berlaku dan waktu observasi. Status yang belum diketahui harus dapat direpresentasikan tanpa tebakan.
//!
//! Benchmark dan gate penerimaan:
//! Ukur p50/p95/p99, throughput, waktu antre, serta RSS/VRAM sesuai workload. Ambang wajib ada di configs/benchmark-targets.yaml (REQUIRED_UNMEASURED); ukur cold/warm terpisah dan pertahankan kualitas sumber/versi.
//!
//! Status: validator timeline dan selector versi as-of aktif sebagai library; extraction event,
//! canonical registry, persistence, worker integration, gold corpus, dan benchmark belum aktif.

pub mod provisions;
