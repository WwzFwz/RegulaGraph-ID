//! Deklarasi module tree untuk src/ingestion/src/domain.
//!
//! Peran dalam komponen:
//! Definisi data dan invariant lintas komponen: dokumen, versi pasal, chunk, canonical entity, relasi, bukti, dan jawaban. Domain menjadi bahasa bersama kedua alur ingestion dan tanya jawab. Representasi lokal Rust mengacu pada src/contracts lintas runtime; definisi wire tidak digandakan.
//!
//! Integrasi dan perhatian performa:
//! Semua anak memakai ID stabil, schema version, dan referensi sumber yang eksplisit. Kontrak tidak mengimpor SDK eksternal; perubahan kontrak harus ditinjau terhadap seluruh konsumennya.
//!
//! Benchmark dan gate penerimaan:
//! Ukur p50/p95/p99, throughput, waktu antre, serta RSS/VRAM sesuai workload. Ambang wajib ada di configs/benchmark-targets.yaml (REQUIRED_UNMEASURED); ukur cold/warm terpisah dan pertahankan kualitas sumber/versi.
//!
//! Status: validator record chunk lokal aktif; domain lain dan integrasi worker masih scaffold.

pub mod chunks;
pub mod documents;
pub mod entities;
pub mod evidence;
pub mod relations;
pub mod wire;
