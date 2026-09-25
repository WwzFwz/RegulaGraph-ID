//! Deklarasi module tree untuk src/ingestion/src/adapters.
//!
//! Peran dalam komponen:
//! Adapter Rust untuk pembacaan sumber dan panggilan engine native atau inference.
//!
//! Integrasi dan perhatian performa:
//! Boundary native menjelaskan kepemilikan buffer dan lifetime; boundary inference memakai batch serta model/snapshot identity.
//!
//! Benchmark dan gate penerimaan:
//! Ukur p50/p95/p99, throughput, waktu antre, serta RSS/VRAM sesuai workload. Ambang wajib ada di configs/benchmark-targets.yaml (REQUIRED_UNMEASURED); ukur cold/warm terpisah dan pertahankan kualitas sumber/versi.
//!
//! Status: artifact store serta persistence TextArtifact/DocumentBatch dipakai worker PARSE; client semantic/native inference tersedia;
//! object storage jarak jauh dan publication integration lanjutan belum aktif.

pub mod document_batches;
pub mod extraction_batches;
pub mod inference;
pub mod native_inference;
pub mod pdf_engine;
pub mod storage;
pub mod text_artifacts;
