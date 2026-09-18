//! Deklarasi module tree untuk src/ingestion/src/document/parsing.
//!
//! Peran dalam komponen:
//! Konversi format sumber menjadi teks dan struktur dokumen, termasuk paragraf, pasal, ayat, halaman, dan tabel. OCR menjadi jalur untuk sumber yang tidak menyediakan teks memadai. Implementasi transformasi berada di Rust.
//!
//! Integrasi dan perhatian performa:
//! Parser anak harus mempertahankan urutan baca serta pemetaan teks ke halaman atau rentang sumber. Kemampuan dan ketidakpastian parser dilaporkan; fallback OCR tidak boleh menghapus bukti asli.
//!
//! Benchmark dan gate penerimaan:
//! Ukur p50/p95/p99, throughput, waktu antre, serta RSS/VRAM sesuai workload. Ambang wajib ada di configs/benchmark-targets.yaml (REQUIRED_UNMEASURED); ukur cold/warm terpisah dan pertahankan kualitas sumber/versi.
//!
//! Status: scaffold; belum ada pipeline atau layanan yang aktif.

pub mod html;
pub mod ocr;
pub mod pdf;
