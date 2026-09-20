//! Deklarasi module tree untuk src/ingestion/src/document/chunking.
//!
//! Peran dalam komponen:
//! Pembuatan unit teks berdasarkan struktur regulasi dengan hubungan ke konteks induk. Folder ini mendukung granularitas pencarian dan konteks ekstraksi yang dapat berbeda. Implementasi transformasi berada di Rust.
//!
//! Integrasi dan perhatian performa:
//! Anak mempertahankan parent ID, identitas versi pasal, batas sumber, dan token count. Potongan panjang boleh dipecah dengan hubungan yang utuh; ukuran dan overlap menjadi parameter evaluasi, bukan angka tetap tanpa pengukuran.
//!
//! Benchmark dan gate penerimaan:
//! Ukur p50/p95/p99, throughput, waktu antre, serta RSS/VRAM sesuai workload. Ambang wajib ada di configs/benchmark-targets.yaml (REQUIRED_UNMEASURED); ukur cold/warm terpisah dan pertahankan kualitas sumber/versi.
//!
//! Status: parser struktur, structural chunk builder, dan parent index aktif sebagai library;
//! integrasi worker/wire, benchmark corpus, dan publikasi belum aktif.

pub mod builder;
pub mod parents;
pub mod structural;
