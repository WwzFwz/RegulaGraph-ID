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
//! Status: persistence TextArtifact lokal aktif dan dipakai worker PARSE; object storage, lifecycle
//! orphan, serta publication belum aktif.

use crate::adapters::storage::{ArtifactDescriptor, ArtifactStore, ArtifactStoreError};
use crate::document::normalization::text::{
    config_fingerprint, normalize_text, NormalizedText, TextNormalizerConfig,
};
use crate::document::parsing::pdf::PdfDocumentText;
use crate::domain::text_artifact_wire::{
    project_validated_text_artifact, serialize_validated_text_mapping, TextArtifactRefs,
    TextArtifactWireConfig, TextArtifactWireError, MAPPING_MEDIA_TYPE, TEXT_MEDIA_TYPE,
    TEXT_NORMALIZER_SOFTWARE, TEXT_NORMALIZER_VERSION,
};
use crate::wire::documents;
use protobuf::Message;
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
    InvalidStoredArtifact(&'static str),
    InvalidUtf8(&'static str),
}

impl Display for PersistTextArtifactError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Storage(error) => write!(formatter, "text artifact storage failed: {error}"),
            Self::Wire(error) => write!(formatter, "text artifact projection failed: {error}"),
            Self::InvalidStoredArtifact(field) => {
                write!(formatter, "stored text artifact is inconsistent: {field}")
            }
            Self::InvalidUtf8(field) => {
                write!(formatter, "stored text artifact is not UTF-8: {field}")
            }
        }
    }
}

/// Loads and independently verifies a persisted normalized artifact before structure detection.
pub fn load_normalized_text(
    store: &ArtifactStore,
    artifact: &documents::TextArtifact,
    config: &TextNormalizerConfig,
) -> Result<NormalizedText, PersistTextArtifactError> {
    let raw_ref =
        artifact
            .raw_text_ref
            .as_ref()
            .ok_or(PersistTextArtifactError::InvalidStoredArtifact(
                "raw_text_ref",
            ))?;
    let normalized_ref = artifact.normalized_text_ref.as_ref().ok_or(
        PersistTextArtifactError::InvalidStoredArtifact("normalized_text_ref"),
    )?;
    let mapping_ref =
        artifact
            .mapping_ref
            .as_ref()
            .ok_or(PersistTextArtifactError::InvalidStoredArtifact(
                "mapping_ref",
            ))?;
    let manifest = artifact.normalizer_manifest.as_ref().ok_or(
        PersistTextArtifactError::InvalidStoredArtifact("normalizer_manifest"),
    )?;
    if raw_ref.media_type != TEXT_MEDIA_TYPE
        || normalized_ref.media_type != TEXT_MEDIA_TYPE
        || mapping_ref.media_type != MAPPING_MEDIA_TYPE
        || raw_ref.schema_version != SCHEMA_VERSION
        || normalized_ref.schema_version != SCHEMA_VERSION
        || mapping_ref.schema_version != SCHEMA_VERSION
    {
        return Err(PersistTextArtifactError::InvalidStoredArtifact(
            "artifact media type or schema version",
        ));
    }
    if manifest.software != TEXT_NORMALIZER_SOFTWARE
        || manifest.schema_version != SCHEMA_VERSION
        || manifest.parser_version.as_deref() != Some(TEXT_NORMALIZER_VERSION)
        || manifest.config_hash.sha256 != config_fingerprint(config)
        || manifest.input_hashes.len() != 2
        || manifest.input_hashes[0].sha256 != raw_ref.content_hash.sha256
        || manifest.input_hashes[1].sha256 != normalized_ref.content_hash.sha256
    {
        return Err(PersistTextArtifactError::InvalidStoredArtifact(
            "normalizer_manifest",
        ));
    }
    let raw = store.read_verified(&ArtifactDescriptor::from_wire_ref(raw_ref)?)?;
    let stored_normalized =
        store.read_verified(&ArtifactDescriptor::from_wire_ref(normalized_ref)?)?;
    let stored_mapping = store.read_verified(&ArtifactDescriptor::from_wire_ref(mapping_ref)?)?;
    let raw = std::str::from_utf8(&raw)
        .map_err(|_| PersistTextArtifactError::InvalidUtf8("raw_text_ref"))?;
    let normalized_text = std::str::from_utf8(&stored_normalized)
        .map_err(|_| PersistTextArtifactError::InvalidUtf8("normalized_text_ref"))?;
    let normalized = normalize_text(raw, config).map_err(|error| {
        PersistTextArtifactError::Wire(TextArtifactWireError::Normalization(error.to_string()))
    })?;
    if normalized.text != normalized_text
        || serialize_validated_text_mapping(&normalized, &artifact.meta.record_id)?
            != stored_mapping
        || documents::TextMapping::parse_from_bytes(&stored_mapping).is_err()
    {
        return Err(PersistTextArtifactError::InvalidStoredArtifact(
            "normalized text or mapping",
        ));
    }
    Ok(normalized)
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

#[cfg(test)]
mod tests {
    use super::*;
    use crate::document::normalization::text::normalize_text;
    use crate::document::parsing::pdf::{
        NormalizedBoundingBox, PdfDocumentStatus, PdfPageResult, PdfPageStatus, PdfParserManifest,
        PdfTextBlock,
    };
    use sha2::{Digest, Sha256};
    use std::fs;
    use std::path::{Path, PathBuf};
    use std::sync::atomic::{AtomicU64, Ordering};

    static TEST_SEQUENCE: AtomicU64 = AtomicU64::new(1);

    struct TestDir(PathBuf);

    impl TestDir {
        fn new() -> Self {
            let sequence = TEST_SEQUENCE.fetch_add(1, Ordering::Relaxed);
            let path = std::env::temp_dir().join(format!(
                "regulagraph-text-artifact-test-{}-{sequence}",
                std::process::id()
            ));
            fs::create_dir_all(&path).expect("test root is created");
            Self(path)
        }
    }

    impl Drop for TestDir {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.0);
        }
    }

    fn hash(bytes: &[u8]) -> String {
        format!("{:x}", Sha256::digest(bytes))
    }

    fn parsed() -> PdfDocumentText {
        let raw_text = "Pasal 1\r\nSetiap orang wajib patuh.".to_owned();
        PdfDocumentText {
            source_id: "source:persistence-fixture".to_owned(),
            source_blob_id: "source-blob:persistence-fixture".to_owned(),
            blocks: vec![PdfTextBlock {
                page_number: 1,
                start_byte: 0,
                end_byte: raw_text.len() as u64,
                bounding_box: NormalizedBoundingBox {
                    x0: 0.1,
                    y0: 0.1,
                    x1: 0.9,
                    y1: 0.4,
                },
            }],
            pages: vec![PdfPageResult {
                page_number: 1,
                start_byte: 0,
                end_byte: raw_text.len() as u64,
                image_objects: 0,
                elapsed_microseconds: 10,
                status: PdfPageStatus::Text,
            }],
            raw_text,
            status: PdfDocumentStatus::Complete,
            manifest: PdfParserManifest {
                schema_version: 1,
                engine: "pdfium",
                binding: "pdfium-render",
                binding_version: "0.8.37",
                api_feature: "pdfium_latest",
                declared_core_version: "test-core-v1".to_owned(),
                library_sha256: hash(b"library"),
                config_sha256: hash(b"parser-config"),
                input_sha256: hash(b"source-pdf"),
            },
        }
    }

    fn store(root: &Path, maximum_artifact_bytes: usize) -> ArtifactStore {
        ArtifactStore::open(
            root,
            crate::adapters::storage::ArtifactStoreConfig {
                maximum_artifact_bytes,
                sync_data: false,
            },
        )
        .expect("artifact store opens")
    }

    fn list_files(path: &Path, output: &mut Vec<PathBuf>) {
        for entry in fs::read_dir(path).expect("directory is readable") {
            let path = entry.expect("entry is readable").path();
            if path.is_dir() {
                list_files(&path, output);
            } else {
                output.push(path);
            }
        }
    }

    #[test]
    fn persists_three_hash_bound_payloads_and_deduplicates_retry() {
        let root = TestDir::new();
        let store = store(&root.0, 1024 * 1024);
        let parsed = parsed();
        let normalizer_config = TextNormalizerConfig::default();
        let normalized = normalize_text(&parsed.raw_text, &normalizer_config).unwrap();
        let wire_config = TextArtifactWireConfig {
            text_artifact_id: "text:persistence-fixture".to_owned(),
            ..Default::default()
        };

        let first = persist_text_artifact(
            &store,
            &parsed,
            &normalized,
            &normalizer_config,
            &wire_config,
        )
        .expect("first persistence succeeds");
        let second = persist_text_artifact(
            &store,
            &parsed,
            &normalized,
            &normalizer_config,
            &wire_config,
        )
        .expect("idempotent retry succeeds");

        assert_eq!(first.descriptors, second.descriptors);
        assert_eq!(first.artifact, second.artifact);
        assert_eq!(
            store.read_verified(&first.descriptors.raw_text).unwrap(),
            parsed.raw_text.as_bytes()
        );
        assert_eq!(
            store
                .read_verified(&first.descriptors.normalized_text)
                .unwrap(),
            normalized.text.as_bytes()
        );
        assert_eq!(
            store.read_verified(&first.descriptors.mapping).unwrap(),
            serialize_validated_text_mapping(&normalized, &wire_config.text_artifact_id).unwrap()
        );
        assert_eq!(
            first.artifact.raw_text_ref.as_ref(),
            Some(&first.descriptors.raw_text.to_wire_ref().unwrap())
        );
        let loaded = load_normalized_text(&store, &first.artifact, &normalizer_config)
            .expect("persisted normalization reloads with manifest and mapping verification");
        assert_eq!(loaded, normalized);

        let mut wrong_config = normalizer_config.clone();
        wrong_config.remove_soft_hyphen = false;
        assert!(matches!(
            load_normalized_text(&store, &first.artifact, &wrong_config),
            Err(PersistTextArtifactError::InvalidStoredArtifact(
                "normalizer_manifest"
            ))
        ));
        let mut incompatible = first.artifact.clone();
        let incompatible_manifest = incompatible
            .normalizer_manifest
            .as_mut()
            .expect("fixture has normalizer manifest");
        incompatible_manifest.software = "unrelated-producer".to_owned();
        incompatible_manifest.parser_version = Some("nonexistent-normalizer-v999".to_owned());
        assert!(matches!(
            load_normalized_text(&store, &incompatible, &normalizer_config),
            Err(PersistTextArtifactError::InvalidStoredArtifact(
                "normalizer_manifest"
            ))
        ));
        let mut wrong_media_type = first.artifact.clone();
        wrong_media_type.raw_text_ref.as_mut().unwrap().media_type = "application/pdf".to_owned();
        assert!(matches!(
            load_normalized_text(&store, &wrong_media_type, &normalizer_config),
            Err(PersistTextArtifactError::InvalidStoredArtifact(
                "artifact media type or schema version"
            ))
        ));
        let mut files = Vec::new();
        list_files(&root.0, &mut files);
        assert_eq!(files.len(), 3);
    }

    #[test]
    fn rejects_invalid_normalization_before_any_storage_write() {
        let root = TestDir::new();
        let store = store(&root.0, 1024 * 1024);
        let parsed = parsed();
        let normalizer_config = TextNormalizerConfig::default();
        let mut normalized = normalize_text(&parsed.raw_text, &normalizer_config).unwrap();
        normalized.text.push('x');

        assert!(matches!(
            persist_text_artifact(
                &store,
                &parsed,
                &normalized,
                &normalizer_config,
                &TextArtifactWireConfig::default(),
            ),
            Err(PersistTextArtifactError::Wire(
                TextArtifactWireError::Normalization(_)
            ))
        ));
        let mut files = Vec::new();
        list_files(&root.0, &mut files);
        assert!(files.is_empty());
    }

    #[test]
    fn mapping_write_failure_never_returns_a_publishable_text_artifact() {
        let root = TestDir::new();
        let parsed = parsed();
        let normalizer_config = TextNormalizerConfig::default();
        let normalized = normalize_text(&parsed.raw_text, &normalizer_config).unwrap();
        let payload_limit = parsed.raw_text.len().max(normalized.text.len());
        let store = store(&root.0, payload_limit);

        assert!(matches!(
            persist_text_artifact(
                &store,
                &parsed,
                &normalized,
                &normalizer_config,
                &TextArtifactWireConfig::default(),
            ),
            Err(PersistTextArtifactError::Storage(
                ArtifactStoreError::ArtifactTooLarge { .. }
            ))
        ));
        let mut files = Vec::new();
        list_files(&root.0, &mut files);
        assert_eq!(files.len(), 2);
    }
}
