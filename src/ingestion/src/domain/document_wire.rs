//! Memproyeksikan hasil struktur dan chunk lokal ke generated wire contract C01.
//!
//! Peran dalam komponen:
//! Modul ini adalah boundary satu arah antara tipe transformasi Rust dan message protobuf
//! `StructureNode`/`Chunk`. Ia mempertahankan corpus, record ID, provision version, normalized byte
//! offsets, parent/exception refs, tokenizer usage, serta producer manifest tanpa mendefinisikan
//! schema publik kedua.
//!
//! Kontrak integrasi dan perhatian implementasi:
//! Input harus berasal dari `StructureTree` dan `ChunkBatch` yang sudah tervalidasi terhadap artefak
//! normalized yang sama. Konversi memakai checked integer conversion, membatasi jumlah record, dan
//! menjalankan validator descriptor C01 pada setiap message. Raw offsets tetap berada di mapping
//! artifact; `TextSpan` wire menunjuk offset normalized sesuai kontrak protobuf.
//!
//! Benchmark dan gate penerimaan:
//! Ukur serialization bytes/record, throughput batch, peak RSS, serta `INVARIANT.WIRE_PARITY` pada
//! fixture lintas Go/Rust/Python. Target configs/benchmark-targets.yaml tetap REQUIRED_UNMEASURED;
//! unit roundtrip tidak membuktikan throughput atau parity seluruh `DocumentBatch`.
//!
//! Status: proyeksi StructureNode/Chunk ke generated protobuf aktif; TextArtifact, DocumentBatch,
//! artifact persistence, dan worker transport belum aktif.

use crate::document::chunking::builder::ChunkBatch;
use crate::document::chunking::structural::{StructureKind, StructureTree};
use crate::domain::wire::{self, Limits};
use crate::wire::{common, documents};
use protobuf::{EnumOrUnknown, MessageField};
use std::collections::HashSet;
use std::error::Error;
use std::fmt::{Display, Formatter};

const WIRE_SCHEMA_VERSION: u32 = 1;

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct DocumentWireConfig {
    pub corpus_id: String,
    pub software: String,
    pub build: String,
    pub parser_version: String,
    pub chunker_version: String,
    pub maximum_records: usize,
}

impl Default for DocumentWireConfig {
    fn default() -> Self {
        Self {
            corpus_id: "regulagraph-id".to_owned(),
            software: "regulagraph-ingestion".to_owned(),
            build: env!("CARGO_PKG_VERSION").to_owned(),
            parser_version: "structure-v1".to_owned(),
            chunker_version: "chunker-v1".to_owned(),
            maximum_records: 2_000_000,
        }
    }
}

#[derive(Clone, Debug)]
pub struct DocumentWireProjection {
    pub structures: Vec<documents::StructureNode>,
    pub chunks: Vec<documents::Chunk>,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum DocumentWireError {
    InvalidConfig(&'static str),
    IdentityMismatch(&'static str),
    UnknownStructureReference(String),
    RecordLimit { actual: usize, maximum: usize },
    OffsetOverflow,
    WireValidation(String),
}

impl Display for DocumentWireError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::InvalidConfig(field) => {
                write!(formatter, "invalid document wire config: {field}")
            }
            Self::IdentityMismatch(field) => {
                write!(formatter, "document wire identity mismatch: {field}")
            }
            Self::UnknownStructureReference(id) => {
                write!(formatter, "unknown structure reference: {id}")
            }
            Self::RecordLimit { actual, maximum } => {
                write!(formatter, "wire records {actual} exceed limit {maximum}")
            }
            Self::OffsetOverflow => write!(formatter, "text offset does not fit wire uint64"),
            Self::WireValidation(error) => write!(formatter, "wire validation failed: {error}"),
        }
    }
}

impl Error for DocumentWireError {}

pub fn project_structure_and_chunks(
    tree: &StructureTree,
    batch: &ChunkBatch,
    config: &DocumentWireConfig,
) -> Result<DocumentWireProjection, DocumentWireError> {
    validate_config(config)?;
    if batch.normalized_sha256 != tree.normalized_sha256 {
        return Err(DocumentWireError::IdentityMismatch("normalized_sha256"));
    }
    let total =
        tree.nodes
            .len()
            .checked_add(batch.chunks.len())
            .ok_or(DocumentWireError::RecordLimit {
                actual: usize::MAX,
                maximum: config.maximum_records,
            })?;
    if total > config.maximum_records {
        return Err(DocumentWireError::RecordLimit {
            actual: total,
            maximum: config.maximum_records,
        });
    }
    let structure_ids: HashSet<&str> = tree.nodes.iter().map(|node| node.id.as_str()).collect();
    let mut structures = Vec::with_capacity(tree.nodes.len());
    for node in &tree.nodes {
        let message = documents::StructureNode {
            meta: MessageField::some(record_meta(&config.corpus_id, &node.id)),
            kind: EnumOrUnknown::new(wire_structure_kind(node.kind)),
            label: node.label.clone(),
            parent_id: node.parent_id.clone(),
            ordered_children: node.ordered_children.clone(),
            source_spans: vec![text_span(
                &tree.text_artifact_id,
                node.normalized_span.start,
                node.normalized_span.end,
            )?],
            ..Default::default()
        };
        validate_wire(&message)?;
        structures.push(message);
    }

    let manifest = producer_manifest(tree, batch, config);
    validate_wire(&manifest)?;
    let mut chunks = Vec::with_capacity(batch.chunks.len());
    for chunk in &batch.chunks {
        if chunk.source_blob_id != tree.source_blob_id {
            return Err(DocumentWireError::IdentityMismatch("source_blob_id"));
        }
        if chunk.text_artifact_id != tree.text_artifact_id {
            return Err(DocumentWireError::IdentityMismatch("text_artifact_id"));
        }
        if chunk.chunker_config_sha256 != batch.chunker_config_sha256 {
            return Err(DocumentWireError::IdentityMismatch("chunker_config_sha256"));
        }
        if chunk.provision_version_refs != [tree.provision_version_id.as_str()] {
            return Err(DocumentWireError::IdentityMismatch(
                "provision_version_refs",
            ));
        }
        for structure_id in chunk
            .structure_node_refs
            .iter()
            .chain(chunk.parent_refs.iter())
        {
            if !structure_ids.contains(structure_id.as_str()) {
                return Err(DocumentWireError::UnknownStructureReference(
                    structure_id.clone(),
                ));
            }
        }
        let message = documents::Chunk {
            meta: MessageField::some(record_meta(&config.corpus_id, &chunk.id)),
            provision_version_refs: chunk.provision_version_refs.clone(),
            text_span: MessageField::some(text_span(
                &tree.text_artifact_id,
                chunk.text_span.normalized.start,
                chunk.text_span.normalized.end,
            )?),
            structure_node_refs: chunk.structure_node_refs.clone(),
            parent_refs: chunk.parent_refs.clone(),
            exception_refs: chunk.exception_refs.clone(),
            chunker_manifest: MessageField::some(manifest.clone()),
            token_counts: chunk
                .token_counts
                .iter()
                .map(|usage| common::TokenUsage {
                    input_tokens: u64::from(usage.tokens),
                    output_tokens: 0,
                    tokenizer_id: usage.tokenizer_id.clone(),
                    ..Default::default()
                })
                .collect(),
            ..Default::default()
        };
        validate_wire(&message)?;
        chunks.push(message);
    }

    Ok(DocumentWireProjection { structures, chunks })
}

fn producer_manifest(
    tree: &StructureTree,
    batch: &ChunkBatch,
    config: &DocumentWireConfig,
) -> common::ProducerManifest {
    common::ProducerManifest {
        software: config.software.clone(),
        build: config.build.clone(),
        schema_version: WIRE_SCHEMA_VERSION,
        parser_version: Some(config.parser_version.clone()),
        chunker_version: Some(config.chunker_version.clone()),
        config_hash: MessageField::some(content_hash(&batch.chunker_config_sha256)),
        input_hashes: vec![content_hash(&tree.normalized_sha256)],
        ..Default::default()
    }
}

fn record_meta(corpus_id: &str, record_id: &str) -> common::RecordMeta {
    common::RecordMeta {
        schema_version: WIRE_SCHEMA_VERSION,
        corpus_id: corpus_id.to_owned(),
        record_id: record_id.to_owned(),
        ..Default::default()
    }
}

fn content_hash(sha256: &str) -> common::ContentHash {
    common::ContentHash {
        sha256: sha256.to_owned(),
        ..Default::default()
    }
}

fn text_span(
    text_artifact_id: &str,
    start: usize,
    end: usize,
) -> Result<common::TextSpan, DocumentWireError> {
    Ok(common::TextSpan {
        text_artifact_id: text_artifact_id.to_owned(),
        start_byte: u64::try_from(start).map_err(|_| DocumentWireError::OffsetOverflow)?,
        end_byte: u64::try_from(end).map_err(|_| DocumentWireError::OffsetOverflow)?,
        ..Default::default()
    })
}

fn wire_structure_kind(kind: StructureKind) -> documents::StructureKind {
    match kind {
        StructureKind::Document => documents::StructureKind::STRUCTURE_KIND_DOCUMENT,
        StructureKind::Chapter => documents::StructureKind::STRUCTURE_KIND_CHAPTER,
        StructureKind::Part => documents::StructureKind::STRUCTURE_KIND_PART,
        StructureKind::Article => documents::StructureKind::STRUCTURE_KIND_ARTICLE,
        StructureKind::Paragraph => documents::StructureKind::STRUCTURE_KIND_PARAGRAPH,
        StructureKind::Item => documents::StructureKind::STRUCTURE_KIND_ITEM,
        StructureKind::Annex => documents::StructureKind::STRUCTURE_KIND_ANNEX,
        StructureKind::Explanation => documents::StructureKind::STRUCTURE_KIND_EXPLANATION,
    }
}

fn validate_config(config: &DocumentWireConfig) -> Result<(), DocumentWireError> {
    if !valid_ascii_id(&config.corpus_id) {
        return Err(DocumentWireError::InvalidConfig("corpus_id"));
    }
    for (field, value) in [
        ("software", config.software.as_str()),
        ("build", config.build.as_str()),
        ("parser_version", config.parser_version.as_str()),
        ("chunker_version", config.chunker_version.as_str()),
    ] {
        if value.trim().is_empty() || value.len() > 256 {
            return Err(DocumentWireError::InvalidConfig(field));
        }
    }
    if config.maximum_records == 0 {
        return Err(DocumentWireError::InvalidConfig("maximum_records"));
    }
    Ok(())
}

fn validate_wire(message: &dyn protobuf::MessageDyn) -> Result<(), DocumentWireError> {
    wire::validate(message, Limits::default()).map_err(DocumentWireError::WireValidation)
}

fn valid_ascii_id(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 256
        && value.bytes().all(|byte| {
            byte.is_ascii_alphanumeric() || matches!(byte, b':' | b'_' | b'-' | b'.' | b'/')
        })
}
