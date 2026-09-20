//! Mendefinisikan representasi lokal chunk yang mempertahankan provenance lintas runtime.
//!
//! Peran dalam komponen:
//! `ChunkView` menjadi hasil teruji dari structural chunker sebelum dikonversi ke message `Chunk`
//! pada src/contracts. Tipe ini mengikat teks retrieval ke source blob, text artifact, provision
//! version, struktur, konteks induk, fingerprint config, dan offset raw/normalized.
//!
//! Kontrak integrasi dan perhatian implementasi:
//! Offset adalah byte UTF-8 start-inclusive/end-exclusive. Referensi wajib berupa ASCII ID unik;
//! token count selalu menyebut tokenizer yang dipakai. Konversi wire dilakukan di boundary worker,
//! bukan dengan mendefinisikan schema publik kedua di modul ini.
//!
//! Benchmark dan gate penerimaan:
//! Validasi ini mendukung `INVARIANT.SOURCE_MAPPING` dan `CHUNKING.THROUGHPUT` pada
//! configs/benchmark-targets.yaml. Hindari menyalin teks ketika hanya metadata yang diperlukan.
//! Target tetap REQUIRED_UNMEASURED hingga workload dan gold set resmi dijalankan.
//!
//! Status: representasi chunk lokal dan validator aktif; konversi wire/publikasi belum aktif.

use crate::document::normalization::text::{NormalizeError, NormalizedText};
use sha2::{Digest, Sha256};
use std::collections::HashSet;
use std::error::Error;
use std::fmt::{Display, Formatter};
use std::ops::Range;

pub const CHUNK_SCHEMA_VERSION: u32 = 1;

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct SourceMappedSpan {
    pub normalized: Range<usize>,
    pub raw: Range<usize>,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct TokenCount {
    pub tokenizer_id: String,
    pub tokens: u32,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ChunkView {
    pub schema_version: u32,
    pub id: String,
    pub source_blob_id: String,
    pub text_artifact_id: String,
    pub provision_version_refs: Vec<String>,
    pub structure_node_refs: Vec<String>,
    pub parent_refs: Vec<String>,
    pub exception_refs: Vec<String>,
    pub text_span: SourceMappedSpan,
    pub text_sha256: String,
    pub chunker_config_sha256: String,
    pub token_counts: Vec<TokenCount>,
}

impl ChunkView {
    pub fn validate(
        &self,
        normalized: &NormalizedText,
        known_structure_ids: &HashSet<&str>,
    ) -> Result<(), ChunkValidationError> {
        if self.schema_version != CHUNK_SCHEMA_VERSION {
            return Err(ChunkValidationError::SchemaMismatch {
                actual: self.schema_version,
            });
        }
        for (field, value) in [
            ("id", self.id.as_str()),
            ("source_blob_id", self.source_blob_id.as_str()),
            ("text_artifact_id", self.text_artifact_id.as_str()),
            ("chunker_config_sha256", self.chunker_config_sha256.as_str()),
        ] {
            if !valid_ascii_id(value) {
                return Err(ChunkValidationError::InvalidId(field));
            }
        }
        validate_id_list("provision_version_refs", &self.provision_version_refs, true)?;
        validate_id_list("structure_node_refs", &self.structure_node_refs, true)?;
        validate_id_list("parent_refs", &self.parent_refs, false)?;
        validate_id_list("exception_refs", &self.exception_refs, false)?;
        if self
            .structure_node_refs
            .iter()
            .chain(self.parent_refs.iter())
            .any(|id| !known_structure_ids.contains(id.as_str()))
        {
            return Err(ChunkValidationError::UnknownStructureReference);
        }
        validate_span(normalized, &self.text_span)?;
        let expected_raw = normalized
            .raw_cover(self.text_span.normalized.clone())
            .map_err(ChunkValidationError::SourceMapping)?;
        if expected_raw != self.text_span.raw {
            return Err(ChunkValidationError::SourceSpanMismatch);
        }
        let text = &normalized.text[self.text_span.normalized.clone()];
        if !is_sha256(&self.text_sha256) || sha256(text.as_bytes()) != self.text_sha256 {
            return Err(ChunkValidationError::TextHashMismatch);
        }
        if !is_sha256(&self.chunker_config_sha256) {
            return Err(ChunkValidationError::InvalidHash("chunker_config_sha256"));
        }
        let mut tokenizer_ids = HashSet::with_capacity(self.token_counts.len());
        for count in &self.token_counts {
            if !valid_ascii_id(&count.tokenizer_id)
                || count.tokens == 0
                || !tokenizer_ids.insert(count.tokenizer_id.as_str())
            {
                return Err(ChunkValidationError::InvalidTokenCount);
            }
        }
        Ok(())
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum ChunkValidationError {
    SchemaMismatch { actual: u32 },
    InvalidId(&'static str),
    InvalidHash(&'static str),
    MissingReference(&'static str),
    DuplicateReference(&'static str),
    UnknownStructureReference,
    InvalidSpan,
    SourceSpanMismatch,
    SourceMapping(NormalizeError),
    TextHashMismatch,
    InvalidTokenCount,
}

impl Display for ChunkValidationError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::SchemaMismatch { actual } => {
                write!(formatter, "chunk schema {actual} is unsupported")
            }
            Self::InvalidId(field) => write!(formatter, "invalid chunk ID field: {field}"),
            Self::InvalidHash(field) => write!(formatter, "invalid chunk hash field: {field}"),
            Self::MissingReference(field) => write!(formatter, "missing chunk reference: {field}"),
            Self::DuplicateReference(field) => {
                write!(formatter, "duplicate chunk reference: {field}")
            }
            Self::UnknownStructureReference => write!(formatter, "unknown structure reference"),
            Self::InvalidSpan => write!(formatter, "invalid chunk UTF-8 span"),
            Self::SourceSpanMismatch => write!(formatter, "chunk raw span does not match mapping"),
            Self::SourceMapping(error) => write!(formatter, "chunk source mapping failed: {error}"),
            Self::TextHashMismatch => write!(formatter, "chunk text hash mismatch"),
            Self::InvalidTokenCount => write!(formatter, "invalid or duplicate chunk token count"),
        }
    }
}

impl Error for ChunkValidationError {}

pub fn text_sha256(text: &str) -> String {
    sha256(text.as_bytes())
}

fn validate_span(
    normalized: &NormalizedText,
    span: &SourceMappedSpan,
) -> Result<(), ChunkValidationError> {
    if span.normalized.is_empty()
        || span.normalized.end > normalized.text.len()
        || !normalized.text.is_char_boundary(span.normalized.start)
        || !normalized.text.is_char_boundary(span.normalized.end)
        || span.raw.is_empty()
    {
        return Err(ChunkValidationError::InvalidSpan);
    }
    Ok(())
}

fn validate_id_list(
    field: &'static str,
    values: &[String],
    required: bool,
) -> Result<(), ChunkValidationError> {
    if required && values.is_empty() {
        return Err(ChunkValidationError::MissingReference(field));
    }
    let mut unique = HashSet::with_capacity(values.len());
    for value in values {
        if !valid_ascii_id(value) {
            return Err(ChunkValidationError::InvalidId(field));
        }
        if !unique.insert(value.as_str()) {
            return Err(ChunkValidationError::DuplicateReference(field));
        }
    }
    Ok(())
}

fn valid_ascii_id(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 256
        && value.bytes().all(|byte| {
            byte.is_ascii_alphanumeric() || matches!(byte, b':' | b'_' | b'-' | b'.' | b'/')
        })
}

fn is_sha256(value: &str) -> bool {
    value.len() == 64
        && value
            .bytes()
            .all(|byte| byte.is_ascii_hexdigit() && !byte.is_ascii_uppercase())
}

fn sha256(bytes: &[u8]) -> String {
    format!("{:x}", Sha256::digest(bytes))
}
