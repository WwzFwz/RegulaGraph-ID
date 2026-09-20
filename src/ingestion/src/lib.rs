//! Deklarasi module tree untuk src/ingestion/src.
//!
//! Peran dalam komponen:
//! Kode library Rust untuk domain lokal, dokumen, graph engineering, persiapan indeks, dan adapter native.
//!
//! Integrasi dan perhatian performa:
//! lib.rs mengekspos module tree, termasuk proyeksi C01 dan penyimpanan artefak lokal; tidak ada
//! executable worker sampai kontrak transport diimplementasikan.
//!
//! Benchmark dan gate penerimaan:
//! Ukur p50/p95/p99, throughput, waktu antre, serta RSS/VRAM sesuai workload. Ambang wajib ada di configs/benchmark-targets.yaml (REQUIRED_UNMEASURED); ukur cold/warm terpisah dan pertahankan kualitas sumber/versi.
//!
//! Status: pipeline struktur/chunk I01, proyeksi structure/chunk C01, dan artifact store lokal aktif
//! sebagai library; worker batch, versioning, graph, serta indexing belum aktif.

pub mod adapters;
pub mod document;
pub mod domain;
pub mod indexing;
pub mod knowledge_graph;

// Generated, unknown-field-preserving wire types. Authoritative schemas live in src/contracts.
pub mod wire {
    include!(concat!(env!("OUT_DIR"), "/wire/mod.rs"));
}
