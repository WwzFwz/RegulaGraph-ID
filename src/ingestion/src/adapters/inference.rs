//! Client inference batch untuk extraction, resolution, embedding, dan summarization.
//!
//! Peran dalam komponen:
//! Memisahkan transformasi Rust dari eksekusi model.
//!
//! Integrasi dan perhatian performa:
//! Client belum aktif; catat model/tokenizer version, ukuran batch, deadline, waktu antre, dan penggunaan token. Output schema valid tidak menjamin fakta benar.
//!
//! Benchmark dan gate penerimaan:
//! Ukur p50/p95/p99, throughput, waktu antre, serta RSS/VRAM sesuai workload. Ambang wajib ada di configs/benchmark-targets.yaml (REQUIRED_UNMEASURED); ukur cold/warm terpisah dan pertahankan kualitas sumber/versi.
//!
//! Status: scaffold; belum ada pipeline atau layanan yang aktif.
