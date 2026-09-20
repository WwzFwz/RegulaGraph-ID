//! Menyerialisasi dan menyimpan `DocumentBatch` sebagai artefak immutable yang dapat dikirim via RPC.
//!
//! Peran dalam komponen:
//! Adapter ini mengubah batch C01 tervalidasi menjadi satu payload protobuf content-addressed dan
//! mengembalikan descriptor lokal beserta `ArtifactRef`. Coordinator Go menerima reference tersebut,
//! membaca byte terverifikasi, lalu menjalankan commit/publication; Rust tidak menandai batch published.
//!
//! Kontrak integrasi dan perhatian implementasi:
//! Validasi C01 dilakukan sebelum serialisasi dan sesudah load. Media type serta schema descriptor
//! dibekukan pada boundary ini. Retry payload identik menghasilkan descriptor identik; kegagalan write
//! tidak menghasilkan response sukses dan tidak mengubah visibility snapshot.
//!
//! Benchmark dan gate penerimaan:
//! Ukur serialization/deserialization throughput, p95/p99 write/read, payload bytes/record, peak RSS,
//! dan dedup ratio pada batch resmi. Target `configs/benchmark-targets.yaml` tetap REQUIRED_UNMEASURED;
//! roundtrip unit tidak membuktikan throughput, durability, atau wire parity lintas bahasa.
//!
//! Status: persistence dan verified load DocumentBatch lokal aktif; worker RPC serta publication Go
//! belum terhubung.

use crate::adapters::storage::{ArtifactDescriptor, ArtifactStore, ArtifactStoreError};
use crate::domain::wire::{self, Limits};
use crate::wire::{common, documents};
use protobuf::Message;
use std::error::Error;
use std::fmt::{Display, Formatter};

const SCHEMA_VERSION: u32 = 1;
pub const DOCUMENT_BATCH_MEDIA_TYPE: &str = "application/vnd.regulagraph.document-batch+protobuf";

#[derive(Clone, Debug, PartialEq)]
pub struct DocumentBatchArtifact {
    pub descriptor: ArtifactDescriptor,
    pub reference: common::ArtifactRef,
}

#[derive(Debug, PartialEq, Eq)]
pub enum DocumentBatchArtifactError {
    InvalidDescriptor(&'static str),
    Storage(ArtifactStoreError),
    Serialization(String),
    WireValidation(String),
}

impl Display for DocumentBatchArtifactError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::InvalidDescriptor(field) => {
                write!(formatter, "invalid document batch descriptor: {field}")
            }
            Self::Storage(error) => write!(formatter, "document batch storage failed: {error}"),
            Self::Serialization(detail) => {
                write!(formatter, "document batch serialization failed: {detail}")
            }
            Self::WireValidation(detail) => {
                write!(formatter, "document batch wire validation failed: {detail}")
            }
        }
    }
}

impl Error for DocumentBatchArtifactError {}

impl From<ArtifactStoreError> for DocumentBatchArtifactError {
    fn from(value: ArtifactStoreError) -> Self {
        Self::Storage(value)
    }
}

pub fn persist_document_batch(
    store: &ArtifactStore,
    batch: &documents::DocumentBatch,
) -> Result<DocumentBatchArtifact, DocumentBatchArtifactError> {
    validate_wire(batch)?;
    let bytes = batch
        .write_to_bytes()
        .map_err(|error| DocumentBatchArtifactError::Serialization(error.to_string()))?;
    let descriptor = store.put_bytes(
        "document-batch",
        DOCUMENT_BATCH_MEDIA_TYPE,
        SCHEMA_VERSION,
        &bytes,
    )?;
    let reference = descriptor.to_wire_ref()?;
    Ok(DocumentBatchArtifact {
        descriptor,
        reference,
    })
}

pub fn load_document_batch(
    store: &ArtifactStore,
    descriptor: &ArtifactDescriptor,
) -> Result<documents::DocumentBatch, DocumentBatchArtifactError> {
    if descriptor.media_type != DOCUMENT_BATCH_MEDIA_TYPE {
        return Err(DocumentBatchArtifactError::InvalidDescriptor("media_type"));
    }
    if descriptor.schema_version != SCHEMA_VERSION {
        return Err(DocumentBatchArtifactError::InvalidDescriptor(
            "schema_version",
        ));
    }
    let bytes = store.read_verified(descriptor)?;
    let batch = documents::DocumentBatch::parse_from_bytes(&bytes)
        .map_err(|error| DocumentBatchArtifactError::Serialization(error.to_string()))?;
    validate_wire(&batch)?;
    Ok(batch)
}

fn validate_wire(message: &dyn protobuf::MessageDyn) -> Result<(), DocumentBatchArtifactError> {
    wire::validate(message, Limits::default()).map_err(DocumentBatchArtifactError::WireValidation)
}
