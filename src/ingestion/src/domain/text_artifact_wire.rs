//! Memproyeksikan keluaran parser dan normalizer menjadi `TextArtifact` C01 yang dapat dipublikasi.
//!
//! Peran dalam komponen:
//! Modul ini membangun mapping byte raw-normalized, status per halaman, dan parser manifest sambil
//! mengikat ketiganya pada `ArtifactRef` immutable. Penyimpanan byte dilakukan adapter storage;
//! modul ini hanya memeriksa bahwa hash, ukuran, media type, schema, dan identitas saling cocok.
//!
//! Kontrak integrasi dan perhatian implementasi:
//! Offset memakai byte UTF-8 start-inclusive/end-exclusive. Mapping harus diserialisasi dengan
//! `serialize_text_mapping()` sebelum disimpan, lalu reference hasil storage diberikan kembali ke
//! `project_text_artifact()`. Halaman OCR/sparse/failure tetap eksplisit dan tidak dianggap sukses.
//!
//! Benchmark dan gate penerimaan:
//! Ukur throughput serialisasi, bytes/span, peak RSS, dan waktu validasi bersama workload
//! `text_transform`. Target `configs/benchmark-targets.yaml` tetap REQUIRED_UNMEASURED; test unit
//! tidak membuktikan throughput corpus, kualitas OCR, atau parity lintas bahasa.
//!
//! Status: proyeksi TextMapping/TextArtifact C01 aktif; OCR, persistence orchestration, dan
//! publication DocumentBatch belum aktif.

use crate::document::normalization::text::{NormalizedText, TextNormalizerConfig};
use crate::document::parsing::pdf::{
    PdfDocumentStatus, PdfDocumentText, PdfPageResult, PdfPageStatus,
};
use crate::domain::wire::{self, Limits};
use crate::wire::{common, documents};
use protobuf::{EnumOrUnknown, Message, MessageField};
use sha2::{Digest, Sha256};
use std::error::Error;
use std::fmt::{Display, Formatter};

const WIRE_SCHEMA_VERSION: u32 = 1;
pub const MAPPING_MEDIA_TYPE: &str = "application/vnd.regulagraph.text-mapping+protobuf";
pub const TEXT_MEDIA_TYPE: &str = "text/plain;charset=utf-8";

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct TextArtifactWireConfig {
    pub corpus_id: String,
    pub text_artifact_id: String,
    pub software: String,
    pub build: String,
    pub maximum_pages: usize,
}

impl Default for TextArtifactWireConfig {
    fn default() -> Self {
        Self {
            corpus_id: "regulagraph-id".to_owned(),
            text_artifact_id: "text:unassigned".to_owned(),
            software: "regulagraph-ingestion".to_owned(),
            build: env!("CARGO_PKG_VERSION").to_owned(),
            maximum_pages: 100_000,
        }
    }
}

#[derive(Clone, Debug)]
pub struct TextArtifactRefs {
    pub raw_text: common::ArtifactRef,
    pub normalized_text: common::ArtifactRef,
    pub mapping: common::ArtifactRef,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum TextArtifactWireError {
    InvalidConfig(&'static str),
    InvalidDocument(&'static str),
    InvalidReference(&'static str),
    PageLimit { actual: usize, maximum: usize },
    OffsetOverflow,
    Normalization(String),
    Serialization(String),
    WireValidation(String),
}

impl Display for TextArtifactWireError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::InvalidConfig(field) => {
                write!(formatter, "invalid text artifact config: {field}")
            }
            Self::InvalidDocument(field) => write!(formatter, "invalid parsed document: {field}"),
            Self::InvalidReference(field) => {
                write!(formatter, "invalid artifact reference: {field}")
            }
            Self::PageLimit { actual, maximum } => {
                write!(formatter, "page count {actual} exceeds limit {maximum}")
            }
            Self::OffsetOverflow => write!(formatter, "text offset does not fit wire uint64"),
            Self::Normalization(detail) => {
                write!(formatter, "normalization validation failed: {detail}")
            }
            Self::Serialization(detail) => {
                write!(formatter, "mapping serialization failed: {detail}")
            }
            Self::WireValidation(detail) => write!(formatter, "wire validation failed: {detail}"),
        }
    }
}

impl Error for TextArtifactWireError {}

pub fn build_text_mapping(
    normalized: &NormalizedText,
    text_artifact_id: &str,
) -> Result<documents::TextMapping, TextArtifactWireError> {
    if !valid_ascii_id(text_artifact_id) {
        return Err(TextArtifactWireError::InvalidConfig("text_artifact_id"));
    }
    normalized
        .validate_integrity()
        .map_err(|error| TextArtifactWireError::Normalization(error.to_string()))?;
    let mut original_spans = Vec::with_capacity(normalized.mapping.len());
    let mut normalized_spans = Vec::with_capacity(normalized.mapping.len());
    for span in &normalized.mapping {
        original_spans.push(common::TextSpan {
            text_artifact_id: text_artifact_id.to_owned(),
            start_byte: span.raw_start_byte,
            end_byte: span.raw_end_byte,
            ..Default::default()
        });
        normalized_spans.push(common::TextSpan {
            text_artifact_id: text_artifact_id.to_owned(),
            start_byte: span.normalized_start_byte,
            end_byte: span.normalized_end_byte,
            ..Default::default()
        });
    }
    if normalized.mapping.is_empty() {
        original_spans.push(common::TextSpan {
            text_artifact_id: text_artifact_id.to_owned(),
            ..Default::default()
        });
        normalized_spans.push(common::TextSpan {
            text_artifact_id: text_artifact_id.to_owned(),
            ..Default::default()
        });
    }
    let mapping = documents::TextMapping {
        original_spans,
        normalized_spans,
        ..Default::default()
    };
    validate_wire(&mapping)?;
    Ok(mapping)
}

pub fn serialize_text_mapping(
    normalized: &NormalizedText,
    text_artifact_id: &str,
) -> Result<Vec<u8>, TextArtifactWireError> {
    build_text_mapping(normalized, text_artifact_id)?
        .write_to_bytes()
        .map_err(|error| TextArtifactWireError::Serialization(error.to_string()))
}

pub fn project_text_artifact(
    parsed: &PdfDocumentText,
    normalized: &NormalizedText,
    normalizer_config: &TextNormalizerConfig,
    references: &TextArtifactRefs,
    config: &TextArtifactWireConfig,
) -> Result<documents::TextArtifact, TextArtifactWireError> {
    validate_config(config)?;
    validate_parsed_document(parsed, config.maximum_pages)?;
    normalized
        .validate_mapping(&parsed.raw_text, normalizer_config)
        .map_err(|error| TextArtifactWireError::Normalization(error.to_string()))?;

    let mapping_bytes = serialize_text_mapping(normalized, &config.text_artifact_id)?;
    validate_reference(
        &references.raw_text,
        parsed.raw_text.as_bytes(),
        TEXT_MEDIA_TYPE,
        "raw_text_ref",
    )?;
    validate_reference(
        &references.normalized_text,
        normalized.text.as_bytes(),
        TEXT_MEDIA_TYPE,
        "normalized_text_ref",
    )?;
    validate_reference(
        &references.mapping,
        &mapping_bytes,
        MAPPING_MEDIA_TYPE,
        "mapping_ref",
    )?;

    let page_results = parsed
        .pages
        .iter()
        .map(|page| page_result(page, parsed.raw_text.len(), &config.text_artifact_id))
        .collect::<Result<Vec<_>, _>>()?;
    let artifact = documents::TextArtifact {
        meta: MessageField::some(record_meta(&config.corpus_id, &config.text_artifact_id)),
        source_blob_id: parsed.source_blob_id.clone(),
        parser_manifest: MessageField::some(parser_manifest(parsed, config)),
        raw_text_ref: MessageField::some(references.raw_text.clone()),
        normalized_text_ref: MessageField::some(references.normalized_text.clone()),
        mapping_ref: MessageField::some(references.mapping.clone()),
        page_results,
        ..Default::default()
    };
    validate_wire(&artifact)?;
    Ok(artifact)
}

fn validate_config(config: &TextArtifactWireConfig) -> Result<(), TextArtifactWireError> {
    if !valid_ascii_id(&config.corpus_id) {
        return Err(TextArtifactWireError::InvalidConfig("corpus_id"));
    }
    if !valid_ascii_id(&config.text_artifact_id) {
        return Err(TextArtifactWireError::InvalidConfig("text_artifact_id"));
    }
    if config.software.trim().is_empty() || config.software.len() > 256 {
        return Err(TextArtifactWireError::InvalidConfig("software"));
    }
    if config.build.trim().is_empty() || config.build.len() > 256 {
        return Err(TextArtifactWireError::InvalidConfig("build"));
    }
    if config.maximum_pages == 0 {
        return Err(TextArtifactWireError::InvalidConfig("maximum_pages"));
    }
    Ok(())
}

fn validate_parsed_document(
    parsed: &PdfDocumentText,
    maximum_pages: usize,
) -> Result<(), TextArtifactWireError> {
    if !valid_ascii_id(&parsed.source_id) {
        return Err(TextArtifactWireError::InvalidDocument("source_id"));
    }
    if !valid_ascii_id(&parsed.source_blob_id) {
        return Err(TextArtifactWireError::InvalidDocument("source_blob_id"));
    }
    if parsed.pages.is_empty() {
        return Err(TextArtifactWireError::InvalidDocument("pages"));
    }
    if parsed.pages.len() > maximum_pages {
        return Err(TextArtifactWireError::PageLimit {
            actual: parsed.pages.len(),
            maximum: maximum_pages,
        });
    }
    if parsed.manifest.schema_version != WIRE_SCHEMA_VERSION
        || !valid_sha256(&parsed.manifest.input_sha256)
        || !valid_sha256(&parsed.manifest.library_sha256)
        || !valid_sha256(&parsed.manifest.config_sha256)
        || parsed.manifest.engine.is_empty()
        || parsed.manifest.binding.is_empty()
        || parsed.manifest.binding_version.is_empty()
        || parsed.manifest.api_feature.is_empty()
        || parsed.manifest.declared_core_version.is_empty()
    {
        return Err(TextArtifactWireError::InvalidDocument("parser_manifest"));
    }
    let mut previous_end = 0u64;
    let raw_bytes = parsed.raw_text.as_bytes();
    for (index, page) in parsed.pages.iter().enumerate() {
        let expected_page = u32::try_from(index + 1)
            .map_err(|_| TextArtifactWireError::InvalidDocument("page_number"))?;
        let start = usize::try_from(page.start_byte)
            .map_err(|_| TextArtifactWireError::InvalidDocument("page_span"))?;
        let end = usize::try_from(page.end_byte)
            .map_err(|_| TextArtifactWireError::InvalidDocument("page_span"))?;
        let previous = usize::try_from(previous_end)
            .map_err(|_| TextArtifactWireError::InvalidDocument("page_span"))?;
        if page.page_number != expected_page
            || page.start_byte < previous_end
            || start > raw_bytes.len()
            || start > end
            || end > raw_bytes.len()
            || raw_bytes[previous..start]
                .iter()
                .any(|byte| *byte != b'\x0c')
        {
            return Err(TextArtifactWireError::InvalidDocument("page_sequence"));
        }
        previous_end = page.end_byte;
    }
    if usize::try_from(previous_end).ok() != Some(raw_bytes.len()) {
        return Err(TextArtifactWireError::InvalidDocument("page_coverage"));
    }
    for block in &parsed.blocks {
        let page_index = usize::try_from(block.page_number.saturating_sub(1))
            .map_err(|_| TextArtifactWireError::InvalidDocument("block_page"))?;
        let page = parsed
            .pages
            .get(page_index)
            .ok_or(TextArtifactWireError::InvalidDocument("block_page"))?;
        let bounds = block.bounding_box;
        if block.page_number == 0
            || page.page_number != block.page_number
            || block.start_byte > block.end_byte
            || block.start_byte < page.start_byte
            || block.end_byte > page.end_byte
            || ![bounds.x0, bounds.y0, bounds.x1, bounds.y1]
                .iter()
                .all(|value| value.is_finite() && (0.0..=1.0).contains(value))
            || bounds.x0 > bounds.x1
            || bounds.y0 > bounds.y1
        {
            return Err(TextArtifactWireError::InvalidDocument("block"));
        }
    }
    let expected_status = if parsed
        .pages
        .iter()
        .all(|page| matches!(page.status, PdfPageStatus::Failed { .. }))
    {
        PdfDocumentStatus::Failed
    } else if parsed
        .pages
        .iter()
        .any(|page| !matches!(page.status, PdfPageStatus::Text))
    {
        PdfDocumentStatus::Partial
    } else {
        PdfDocumentStatus::Complete
    };
    if parsed.status != expected_status {
        return Err(TextArtifactWireError::InvalidDocument("status"));
    }
    Ok(())
}

fn validate_reference(
    reference: &common::ArtifactRef,
    bytes: &[u8],
    expected_media_type: &str,
    field: &'static str,
) -> Result<(), TextArtifactWireError> {
    validate_wire(reference)?;
    let size = u64::try_from(bytes.len()).map_err(|_| TextArtifactWireError::OffsetOverflow)?;
    if reference.schema_version != WIRE_SCHEMA_VERSION
        || reference.media_type != expected_media_type
        || reference.byte_size != size
        || reference.content_hash.sha256 != sha256(bytes)
    {
        return Err(TextArtifactWireError::InvalidReference(field));
    }
    Ok(())
}

fn page_result(
    page: &PdfPageResult,
    raw_text_length: usize,
    text_artifact_id: &str,
) -> Result<documents::PageResult, TextArtifactWireError> {
    let raw_length =
        u64::try_from(raw_text_length).map_err(|_| TextArtifactWireError::OffsetOverflow)?;
    if page.page_number == 0 || page.start_byte > page.end_byte || page.end_byte > raw_length {
        return Err(TextArtifactWireError::InvalidDocument("page_span"));
    }
    let span = common::TextSpan {
        text_artifact_id: text_artifact_id.to_owned(),
        start_byte: page.start_byte,
        end_byte: page.end_byte,
        ..Default::default()
    };
    let (status, errors) = match &page.status {
        PdfPageStatus::Text => (
            common::CompletionStatus::COMPLETION_STATUS_SUCCEEDED,
            Vec::new(),
        ),
        PdfPageStatus::NeedsOcr => (
            common::CompletionStatus::COMPLETION_STATUS_FAILED,
            vec![operation_error(
                common::ErrorCode::ERROR_CODE_NOT_IMPLEMENTED,
                "page requires OCR",
                true,
                page.page_number,
            )],
        ),
        PdfPageStatus::Sparse => (
            common::CompletionStatus::COMPLETION_STATUS_FAILED,
            vec![operation_error(
                common::ErrorCode::ERROR_CODE_INVALID_ARGUMENT,
                "page text is below extraction threshold",
                false,
                page.page_number,
            )],
        ),
        PdfPageStatus::Failed { code, detail: _ } => (
            common::CompletionStatus::COMPLETION_STATUS_FAILED,
            vec![operation_error(
                common::ErrorCode::ERROR_CODE_INTERNAL,
                &format!("PDF extraction failed ({code})"),
                false,
                page.page_number,
            )],
        ),
    };
    let result = documents::PageResult {
        page_number: page.page_number,
        status: EnumOrUnknown::new(status),
        used_ocr: false,
        spans: vec![span],
        errors,
        ..Default::default()
    };
    validate_wire(&result)?;
    Ok(result)
}

fn operation_error(
    code: common::ErrorCode,
    message: &str,
    retryable: bool,
    page_number: u32,
) -> common::OperationError {
    common::OperationError {
        code: EnumOrUnknown::new(code),
        safe_message: message.to_owned(),
        stage: "pdf_text_extraction".to_owned(),
        retryable,
        item_id: Some(format!("page:{page_number}")),
        ..Default::default()
    }
}

fn parser_manifest(
    parsed: &PdfDocumentText,
    config: &TextArtifactWireConfig,
) -> common::ProducerManifest {
    let parser = &parsed.manifest;
    common::ProducerManifest {
        software: config.software.clone(),
        build: config.build.clone(),
        schema_version: WIRE_SCHEMA_VERSION,
        parser_version: Some(format!(
            "{}:{}:{}:{}:{}",
            parser.engine,
            parser.binding,
            parser.binding_version,
            parser.api_feature,
            parser.declared_core_version
        )),
        config_hash: MessageField::some(content_hash(&parser.config_sha256)),
        input_hashes: vec![
            content_hash(&parser.input_sha256),
            content_hash(&parser.library_sha256),
        ],
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

fn content_hash(value: &str) -> common::ContentHash {
    common::ContentHash {
        sha256: value.to_owned(),
        ..Default::default()
    }
}

fn sha256(bytes: &[u8]) -> String {
    format!("{:x}", Sha256::digest(bytes))
}

fn validate_wire(message: &dyn protobuf::MessageDyn) -> Result<(), TextArtifactWireError> {
    wire::validate(message, Limits::default()).map_err(TextArtifactWireError::WireValidation)
}

fn valid_ascii_id(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 256
        && value.bytes().all(|byte| {
            byte.is_ascii_alphanumeric() || matches!(byte, b':' | b'_' | b'-' | b'.' | b'/')
        })
}

fn valid_sha256(value: &str) -> bool {
    value.len() == 64
        && value
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::document::normalization::text::normalize_text;
    use crate::document::parsing::pdf::{NormalizedBoundingBox, PdfParserManifest, PdfTextBlock};

    fn parser_manifest() -> PdfParserManifest {
        PdfParserManifest {
            schema_version: 1,
            engine: "pdfium",
            binding: "pdfium-render",
            binding_version: "0.8.37",
            api_feature: "pdfium_latest",
            declared_core_version: "test-core-v1".to_owned(),
            library_sha256: sha256(b"pdfium library"),
            config_sha256: sha256(b"parser config"),
            input_sha256: sha256(b"source PDF"),
        }
    }

    fn parsed_document(second_status: PdfPageStatus) -> PdfDocumentText {
        let raw_text = "Pasal 1\r\nWajib.\x0cPasal 2\r\nScan".to_owned();
        let separator = raw_text.find('\x0c').expect("fixture has separator");
        let second_start = separator + 1;
        let status = if matches!(second_status, PdfPageStatus::Text) {
            PdfDocumentStatus::Complete
        } else {
            PdfDocumentStatus::Partial
        };
        PdfDocumentText {
            source_id: "source:fixture".to_owned(),
            source_blob_id: "source-blob:fixture".to_owned(),
            blocks: vec![
                PdfTextBlock {
                    page_number: 1,
                    start_byte: 0,
                    end_byte: separator as u64,
                    bounding_box: NormalizedBoundingBox {
                        x0: 0.1,
                        y0: 0.1,
                        x1: 0.9,
                        y1: 0.4,
                    },
                },
                PdfTextBlock {
                    page_number: 2,
                    start_byte: second_start as u64,
                    end_byte: raw_text.len() as u64,
                    bounding_box: NormalizedBoundingBox {
                        x0: 0.1,
                        y0: 0.1,
                        x1: 0.9,
                        y1: 0.4,
                    },
                },
            ],
            pages: vec![
                PdfPageResult {
                    page_number: 1,
                    start_byte: 0,
                    end_byte: separator as u64,
                    image_objects: 0,
                    elapsed_microseconds: 10,
                    status: PdfPageStatus::Text,
                },
                PdfPageResult {
                    page_number: 2,
                    start_byte: second_start as u64,
                    end_byte: raw_text.len() as u64,
                    image_objects: 1,
                    elapsed_microseconds: 20,
                    status: second_status,
                },
            ],
            raw_text,
            status,
            manifest: parser_manifest(),
        }
    }

    fn artifact_ref(namespace: &str, bytes: &[u8], media_type: &str) -> common::ArtifactRef {
        let hash = sha256(bytes);
        common::ArtifactRef {
            artifact_id: format!("artifact:{namespace}:{hash}"),
            content_hash: MessageField::some(content_hash(&hash)),
            storage_key: format!("sha256/{}/{}/{}.bin", &hash[..2], &hash[2..4], hash),
            media_type: media_type.to_owned(),
            byte_size: bytes.len() as u64,
            schema_version: 1,
            ..Default::default()
        }
    }

    fn fixture(
        second_status: PdfPageStatus,
    ) -> (
        PdfDocumentText,
        NormalizedText,
        TextNormalizerConfig,
        TextArtifactRefs,
        TextArtifactWireConfig,
    ) {
        let parsed = parsed_document(second_status);
        let normalizer_config = TextNormalizerConfig::default();
        let normalized = normalize_text(&parsed.raw_text, &normalizer_config).unwrap();
        let config = TextArtifactWireConfig {
            text_artifact_id: "text:fixture".to_owned(),
            ..Default::default()
        };
        let mapping = serialize_text_mapping(&normalized, &config.text_artifact_id).unwrap();
        let references = TextArtifactRefs {
            raw_text: artifact_ref("raw-text", parsed.raw_text.as_bytes(), TEXT_MEDIA_TYPE),
            normalized_text: artifact_ref(
                "normalized-text",
                normalized.text.as_bytes(),
                TEXT_MEDIA_TYPE,
            ),
            mapping: artifact_ref("text-mapping", &mapping, MAPPING_MEDIA_TYPE),
        };
        (parsed, normalized, normalizer_config, references, config)
    }

    #[test]
    fn projects_hash_bound_text_artifact_and_roundtrips() {
        let (parsed, normalized, normalizer_config, references, config) =
            fixture(PdfPageStatus::Text);

        let artifact = project_text_artifact(
            &parsed,
            &normalized,
            &normalizer_config,
            &references,
            &config,
        )
        .expect("text artifact projects");

        assert_eq!(artifact.meta.record_id, "text:fixture");
        assert_eq!(artifact.source_blob_id, parsed.source_blob_id);
        assert_eq!(artifact.raw_text_ref.as_ref(), Some(&references.raw_text));
        assert_eq!(
            artifact.normalized_text_ref.as_ref(),
            Some(&references.normalized_text)
        );
        assert_eq!(artifact.mapping_ref.as_ref(), Some(&references.mapping));
        assert_eq!(artifact.page_results.len(), 2);
        assert!(artifact.page_results.iter().all(|page| {
            page.status.value() == common::CompletionStatus::COMPLETION_STATUS_SUCCEEDED as i32
                && page.errors.is_empty()
        }));
        assert_eq!(artifact.parser_manifest.input_hashes.len(), 2);

        let encoded = artifact.write_to_bytes().expect("artifact serializes");
        let decoded = documents::TextArtifact::parse_from_bytes(&encoded).unwrap();
        assert_eq!(decoded, artifact);
        validate_wire(&decoded).expect("roundtrip remains valid");
    }

    #[test]
    fn keeps_ocr_requirement_explicit_without_exposing_native_detail() {
        let (parsed, normalized, normalizer_config, references, config) =
            fixture(PdfPageStatus::NeedsOcr);

        let artifact = project_text_artifact(
            &parsed,
            &normalized,
            &normalizer_config,
            &references,
            &config,
        )
        .expect("partial artifact projects");

        let page = &artifact.page_results[1];
        assert_eq!(
            page.status.value(),
            common::CompletionStatus::COMPLETION_STATUS_FAILED as i32
        );
        assert!(!page.used_ocr);
        assert_eq!(page.errors.len(), 1);
        assert_eq!(
            page.errors[0].code.value(),
            common::ErrorCode::ERROR_CODE_NOT_IMPLEMENTED as i32
        );
        assert!(page.errors[0].retryable);
        assert_eq!(page.errors[0].safe_message, "page requires OCR");
    }

    #[test]
    fn rejects_reference_page_status_and_block_corruption() {
        let (parsed, normalized, normalizer_config, mut references, config) =
            fixture(PdfPageStatus::Text);
        references.normalized_text.content_hash = MessageField::some(content_hash(&"0".repeat(64)));
        assert_eq!(
            project_text_artifact(
                &parsed,
                &normalized,
                &normalizer_config,
                &references,
                &config,
            )
            .unwrap_err(),
            TextArtifactWireError::InvalidReference("normalized_text_ref")
        );

        let (mut parsed, normalized, normalizer_config, references, config) =
            fixture(PdfPageStatus::Text);
        parsed.pages[1].page_number = 3;
        assert_eq!(
            project_text_artifact(
                &parsed,
                &normalized,
                &normalizer_config,
                &references,
                &config,
            )
            .unwrap_err(),
            TextArtifactWireError::InvalidDocument("page_sequence")
        );

        let (mut parsed, normalized, normalizer_config, references, config) =
            fixture(PdfPageStatus::NeedsOcr);
        parsed.status = PdfDocumentStatus::Complete;
        assert_eq!(
            project_text_artifact(
                &parsed,
                &normalized,
                &normalizer_config,
                &references,
                &config,
            )
            .unwrap_err(),
            TextArtifactWireError::InvalidDocument("status")
        );

        let (mut parsed, normalized, normalizer_config, references, config) =
            fixture(PdfPageStatus::Text);
        parsed.blocks[0].bounding_box.x1 = f32::INFINITY;
        assert_eq!(
            project_text_artifact(
                &parsed,
                &normalized,
                &normalizer_config,
                &references,
                &config,
            )
            .unwrap_err(),
            TextArtifactWireError::InvalidDocument("block")
        );
    }

    #[test]
    fn represents_empty_extracted_text_with_a_zero_length_mapping_pair() {
        let parsed = PdfDocumentText {
            source_id: "source:empty".to_owned(),
            source_blob_id: "source-blob:empty".to_owned(),
            raw_text: String::new(),
            blocks: Vec::new(),
            pages: vec![PdfPageResult {
                page_number: 1,
                start_byte: 0,
                end_byte: 0,
                image_objects: 1,
                elapsed_microseconds: 1,
                status: PdfPageStatus::NeedsOcr,
            }],
            status: PdfDocumentStatus::Partial,
            manifest: parser_manifest(),
        };
        let normalizer_config = TextNormalizerConfig::default();
        let normalized = normalize_text("", &normalizer_config).unwrap();
        let config = TextArtifactWireConfig {
            text_artifact_id: "text:empty".to_owned(),
            ..Default::default()
        };
        let mapping_message = build_text_mapping(&normalized, &config.text_artifact_id).unwrap();
        assert_eq!(mapping_message.original_spans.len(), 1);
        assert_eq!(mapping_message.normalized_spans.len(), 1);
        assert_eq!(mapping_message.original_spans[0].start_byte, 0);
        assert_eq!(mapping_message.original_spans[0].end_byte, 0);
        let mapping_bytes = mapping_message.write_to_bytes().unwrap();
        let references = TextArtifactRefs {
            raw_text: artifact_ref("raw-text", b"", TEXT_MEDIA_TYPE),
            normalized_text: artifact_ref("normalized-text", b"", TEXT_MEDIA_TYPE),
            mapping: artifact_ref("text-mapping", &mapping_bytes, MAPPING_MEDIA_TYPE),
        };

        let artifact = project_text_artifact(
            &parsed,
            &normalized,
            &normalizer_config,
            &references,
            &config,
        )
        .expect("empty OCR candidate remains representable");
        assert_eq!(artifact.page_results[0].spans[0].start_byte, 0);
        assert_eq!(artifact.page_results[0].spans[0].end_byte, 0);
    }
}
