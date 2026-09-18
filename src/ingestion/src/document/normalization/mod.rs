//! Deklarasi module tree untuk src/ingestion/src/document/normalization.
//!
//! Peran dalam komponen:
//! Pembersihan artefak mekanis hasil parsing agar teks konsisten tanpa mengubah makna ketentuan. Implementasi transformasi berada di Rust.
//!
//! Integrasi dan perhatian performa:
//! Anak menyimpan pemetaan teks bersih ke sumber. Negasi, nomor, tahun, satuan, struktur daftar, dan penanda pengecualian harus dipertahankan; perubahan dapat diaudit.
//!
//! Benchmark dan gate penerimaan:
//! Ukur p50/p95/p99, throughput, waktu antre, serta RSS/VRAM sesuai workload. Ambang wajib ada di configs/benchmark-targets.yaml (REQUIRED_UNMEASURED); ukur cold/warm terpisah dan pertahankan kualitas sumber/versi.
//!
//! Status: scaffold; belum ada pipeline atau layanan yang aktif.

pub mod text;
