//! Menyerialisasi dan menyimpan `DocumentBatch` sebagai artefak immutable yang dapat dikirim via RPC.
//!
//! Peran dalam komponen:
//! Adapter ini mengubah batch C01 tervalidasi menjadi satu payload protobuf content-addressed dan
//! mengembalikan descriptor lokal beserta `ArtifactRef`. Coordinator Go menerima reference tersebut,
//! membaca byte terverifikasi, lalu menjalankan commit/publication; Rust tidak menandai batch published.
//!
//! Kontrak integrasi dan perhatian implementasi:
//! Validasi wire dan invariant semantik DocumentBatch dilakukan sebelum serialisasi dan sesudah load.
//! Media type serta schema descriptor dibekukan pada boundary ini. Retry payload identik menghasilkan
//! descriptor identik; kegagalan write tidak menghasilkan response sukses dan tidak mengubah visibility.
//!
//! Benchmark dan gate penerimaan:
//! Ukur serialization/deserialization throughput, p95/p99 write/read, payload bytes/record, peak RSS,
//! dan dedup ratio pada batch resmi. Target `configs/benchmark-targets.yaml` tetap REQUIRED_UNMEASURED;
//! roundtrip unit tidak membuktikan throughput, durability, atau wire parity lintas bahasa.
//!
//! Status: persistence dan verified load DocumentBatch lokal aktif pada worker PARSE; publication Go
//! serta stage worker lanjutan belum terhubung.

use crate::adapters::storage::{ArtifactDescriptor, ArtifactStore, ArtifactStoreError};
use crate::domain::document_batch::{
    validate_document_batch, DocumentBatchConfig, DocumentBatchError,
};
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
    SemanticValidation(DocumentBatchError),
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
            Self::SemanticValidation(error) => {
                write!(formatter, "document batch validation failed: {error}")
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

impl From<DocumentBatchError> for DocumentBatchArtifactError {
    fn from(value: DocumentBatchError) -> Self {
        Self::SemanticValidation(value)
    }
}

pub fn persist_document_batch(
    store: &ArtifactStore,
    batch: &documents::DocumentBatch,
) -> Result<DocumentBatchArtifact, DocumentBatchArtifactError> {
    validate_document_batch(batch, &DocumentBatchConfig::default())?;
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
    validate_document_batch(&batch, &DocumentBatchConfig::default())?;
    Ok(batch)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::adapters::storage::ArtifactStoreConfig;
    use crate::domain::document_batch::{
        assemble_document_batch, build_source_blob, DocumentBatchConfig, DocumentBatchParts,
    };
    use protobuf::well_known_types::timestamp::Timestamp;
    use protobuf::{EnumOrUnknown, MessageField};
    use std::fs;
    use std::path::PathBuf;
    use std::sync::atomic::{AtomicU64, Ordering};

    static TEST_SEQUENCE: AtomicU64 = AtomicU64::new(1);

    struct TestDir(PathBuf);

    impl TestDir {
        fn new() -> Self {
            let sequence = TEST_SEQUENCE.fetch_add(1, Ordering::Relaxed);
            let path = std::env::temp_dir().join(format!(
                "regulagraph-document-batch-test-{}-{sequence}",
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

    fn hash(character: char) -> String {
        character.to_string().repeat(64)
    }

    fn content_hash(character: char) -> common::ContentHash {
        common::ContentHash {
            sha256: hash(character),
            ..Default::default()
        }
    }

    fn producer() -> common::ProducerManifest {
        common::ProducerManifest {
            software: "regulagraph-ingestion".to_owned(),
            build: "test-build".to_owned(),
            schema_version: 1,
            config_hash: MessageField::some(content_hash('a')),
            ..Default::default()
        }
    }

    fn batch() -> documents::DocumentBatch {
        let source_hash = hash('b');
        let source_ref = common::ArtifactRef {
            artifact_id: "artifact:source:fixture".to_owned(),
            content_hash: MessageField::some(common::ContentHash {
                sha256: source_hash.clone(),
                ..Default::default()
            }),
            storage_key: format!(
                "sha256/{}/{}/{}.bin",
                &source_hash[..2],
                &source_hash[2..4],
                source_hash
            ),
            media_type: "application/pdf".to_owned(),
            byte_size: 100,
            schema_version: 1,
            ..Default::default()
        };
        let source = build_source_blob("regulagraph-id", "source-blob:fixture", source_ref)
            .expect("source fixture builds");
        assemble_document_batch(
            DocumentBatchParts {
                batch_id: "document-batch:persistence".to_owned(),
                context: common::RequestContext {
                    schema_version: 1,
                    request_id: "request:persistence".to_owned(),
                    trace_id: "trace:persistence".to_owned(),
                    corpus_id: "regulagraph-id".to_owned(),
                    deadline: MessageField::some(Timestamp {
                        seconds: 1_900_000_000,
                        nanos: 0,
                        ..Default::default()
                    }),
                    config_fingerprint: MessageField::some(content_hash('c')),
                    auth_scope_ref: "scope:ingestion".to_owned(),
                    ..Default::default()
                },
                sources: vec![source],
                dependency_manifest: common::DependencyManifest {
                    artifact_id: "dependency-manifest:persistence".to_owned(),
                    producer_manifest: MessageField::some(producer()),
                    ..Default::default()
                },
                ..Default::default()
            },
            &DocumentBatchConfig::default(),
        )
        .expect("batch fixture assembles")
    }

    fn store(root: &TestDir) -> ArtifactStore {
        ArtifactStore::open(
            &root.0,
            ArtifactStoreConfig {
                maximum_artifact_bytes: 1024 * 1024,
                sync_data: false,
            },
        )
        .expect("store opens")
    }

    #[test]
    fn persists_loads_and_deduplicates_document_batch() {
        let root = TestDir::new();
        let store = store(&root);
        let batch = batch();

        let first = persist_document_batch(&store, &batch).expect("batch persists");
        let second = persist_document_batch(&store, &batch).expect("retry deduplicates");
        assert_eq!(first, second);
        assert_eq!(first.reference, first.descriptor.to_wire_ref().unwrap());
        assert_eq!(first.reference.media_type, DOCUMENT_BATCH_MEDIA_TYPE);
        assert_eq!(
            load_document_batch(&store, &first.descriptor).unwrap(),
            batch
        );

        let files = fs::read_dir(root.0.join("sha256"))
            .expect("hash root exists")
            .count();
        assert_eq!(files, 1, "one first-level content shard is expected");
    }

    #[test]
    fn rejects_invalid_batch_descriptor_and_payload() {
        let root = TestDir::new();
        let store = store(&root);
        assert!(matches!(
            persist_document_batch(&store, &documents::DocumentBatch::default()),
            Err(DocumentBatchArtifactError::SemanticValidation(_))
        ));

        let persisted = persist_document_batch(&store, &batch()).unwrap();
        let mut wrong_media = persisted.descriptor.clone();
        wrong_media.media_type = "application/octet-stream".to_owned();
        assert_eq!(
            load_document_batch(&store, &wrong_media).unwrap_err(),
            DocumentBatchArtifactError::InvalidDescriptor("media_type")
        );

        let malformed = store
            .put_bytes("document-batch", DOCUMENT_BATCH_MEDIA_TYPE, 1, &[0xff])
            .unwrap();
        assert!(matches!(
            load_document_batch(&store, &malformed),
            Err(DocumentBatchArtifactError::Serialization(_))
        ));

        let mut semantically_invalid = batch();
        semantically_invalid.completeness =
            EnumOrUnknown::new(common::Completeness::COMPLETENESS_COMPLETE);
        assert!(matches!(
            persist_document_batch(&store, &semantically_invalid),
            Err(DocumentBatchArtifactError::SemanticValidation(_))
        ));
    }

    #[test]
    fn stored_batch_preserves_explicit_none_completeness() {
        let root = TestDir::new();
        let store = store(&root);
        let loaded = load_document_batch(
            &store,
            &persist_document_batch(&store, &batch()).unwrap().descriptor,
        )
        .unwrap();
        assert_eq!(
            loaded.completeness,
            EnumOrUnknown::new(common::Completeness::COMPLETENESS_NONE)
        );
    }
}
