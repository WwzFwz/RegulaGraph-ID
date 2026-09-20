//! Deklarasi module tree untuk src/ingestion/src.
//!
//! Peran dalam komponen:
//! Kode library Rust untuk domain lokal, dokumen, graph engineering, persiapan indeks, dan adapter native.
//!
//! Integrasi dan perhatian performa:
//! lib.rs mengekspos module tree, proyeksi C01, penyimpanan artefak lokal, serta service worker
//! Tonic untuk batch parse; Go tetap menjadi pemilik job durable dan publication.
//!
//! Benchmark dan gate penerimaan:
//! Ukur p50/p95/p99, throughput, waktu antre, serta RSS/VRAM sesuai workload. Ambang wajib ada di configs/benchmark-targets.yaml (REQUIRED_UNMEASURED); ukur cold/warm terpisah dan pertahankan kualitas sumber/versi.
//!
//! Status: pipeline struktur/chunk I01, proyeksi TextArtifact/structure/chunk, assembler serta
//! persistence DocumentBatch C01, artifact store lokal, incremental change planner, selector timeline,
//! dan executable worker PARSE/STRUCTURE/CHUNK aktif; event extraction, graph, serta indexing belum aktif.

pub mod adapters;
pub mod document;
pub mod domain;
pub mod indexing;
pub mod knowledge_graph;
pub mod worker;

// Generated, unknown-field-preserving wire types. Authoritative schemas live in src/contracts.
pub mod wire {
    include!(concat!(env!("OUT_DIR"), "/wire/mod.rs"));
}
