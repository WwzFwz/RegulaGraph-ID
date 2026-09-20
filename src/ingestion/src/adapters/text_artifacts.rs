//! Mengorkestrasi persistence raw text, normalized text, dan byte mapping untuk satu TextArtifact.
//!
//! Peran dalam komponen:
//! Adapter ini menjembatani hasil parser/normalizer dengan immutable artifact store dan proyeksi C01.
//! Ia mengembalikan descriptor lokal bersama `TextArtifact` tervalidasi untuk dirakit ke batch oleh
//! boundary worker; adapter ini tidak membuat publication marker atau mengubah snapshot.
//!
//! Kontrak integrasi dan perhatian implementasi:
//! Mapping diserialisasi sekali dengan schema C01, lalu ketiga payload disimpan sebelum message akhir
//! dibentuk. Kegagalan di tengah dapat meninggalkan object content-addressed yang belum direferensikan;
//! object tidak dihapus karena mungkin dipakai job konkuren dan publication tetap atomik di Go.
//!
//! Benchmark dan gate penerimaan:
//! Ukur bytes/s, p95/p99 persistence, fsync cost, peak RSS, jumlah dedup hit, dan orphan cleanup lag.
//! Target `configs/benchmark-targets.yaml` tetap REQUIRED_UNMEASURED; test filesystem sementara tidak
//! membuktikan durability atau throughput storage produksi.
//!
//! Status: persistence TextArtifact lokal aktif; object storage, lifecycle orphan, worker transport,
//! dan publication belum aktif.

use crate::adapters::storage::{ArtifactDescriptor, ArtifactStore, ArtifactStoreError};
use crate::document::normalization::text::{NormalizedText, TextNormalizerConfig};
use crate::document::parsing::pdf::PdfDocumentText;
use crate::domain::text_artifact_wire::{
    project_validated_text_artifact, serialize_validated_text_mapping, TextArtifactRefs,
    TextArtifactWireConfig, TextArtifactWireError, MAPPING_MEDIA_TYPE, TEXT_MEDIA_TYPE,
};
use crate::wire::documents;
use std::error::Error;
use std::fmt::{Display, Formatter};

const SCHEMA_VERSION: u32 = 1;

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct TextArtifactDescriptors {
    pub raw_text: ArtifactDescriptor,
    pub normalized_text: ArtifactDescriptor,
    pub mapping: ArtifactDescriptor,
}

#[derive(Clone, Debug)]
pub struct PersistedTextArtifact {
    pub artifact: documents::TextArtifact,
    pub descriptors: TextArtifactDescriptors,
}

#[derive(Debug, PartialEq, Eq)]
pub enum PersistTextArtifactError {
    Storage(ArtifactStoreError),
    Wire(TextArtifactWireError),
}

impl Display for PersistTextArtifactError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Storage(error) => write!(formatter, "text artifact storage failed: {error}"),
            Self::Wire(error) => write!(formatter, "text artifact projection failed: {error}"),
        }
    }
}

impl Error for PersistTextArtifactError {}

impl From<ArtifactStoreError> for PersistTextArtifactError {
    fn from(value: ArtifactStoreError) -> Self {
        Self::Storage(value)
    }
}

impl From<TextArtifactWireError> for PersistTextArtifactError {
    fn from(value: TextArtifactWireError) -> Self {
        Self::Wire(value)
    }
}

pub fn persist_text_artifact(
    store: &ArtifactStore,
    parsed: &PdfDocumentText,
    normalized: &NormalizedText,
    normalizer_config: &TextNormalizerConfig,
    wire_config: &TextArtifactWireConfig,
) -> Result<PersistedTextArtifact, PersistTextArtifactError> {
    normalized
        .validate_mapping(&parsed.raw_text, normalizer_config)
        .map_err(|error| {
            PersistTextArtifactError::Wire(TextArtifactWireError::Normalization(error.to_string()))
        })?;
    let mapping_bytes =
        serialize_validated_text_mapping(normalized, &wire_config.text_artifact_id)?;
    let raw_text = store.put_bytes(
        "raw-text",
        TEXT_MEDIA_TYPE,
        SCHEMA_VERSION,
        parsed.raw_text.as_bytes(),
    )?;
    let normalized_text = store.put_bytes(
        "normalized-text",
        TEXT_MEDIA_TYPE,
        SCHEMA_VERSION,
        normalized.text.as_bytes(),
    )?;
    let mapping = store.put_bytes(
        "text-mapping",
        MAPPING_MEDIA_TYPE,
        SCHEMA_VERSION,
        &mapping_bytes,
    )?;
    let references = TextArtifactRefs {
        raw_text: raw_text.to_wire_ref()?,
        normalized_text: normalized_text.to_wire_ref()?,
        mapping: mapping.to_wire_ref()?,
    };
    let artifact = project_validated_text_artifact(
        parsed,
        normalized,
        &mapping_bytes,
        &references,
        wire_config,
    )?;
    Ok(PersistedTextArtifact {
        artifact,
        descriptors: TextArtifactDescriptors {
            raw_text,
            normalized_text,
            mapping,
        },
    })
}
