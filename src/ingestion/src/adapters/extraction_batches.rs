//! Persists validated `ExtractionBatch` messages as immutable content-addressed artifacts.
//!
//! Both write and verified read require the source `DocumentBatch`, so forged source/version/span
//! references cannot enter or leave the artifact boundary. Serialization benchmarks and durable
//! storage latency remain REQUIRED_UNMEASURED under `configs/benchmark-targets.yaml`.

use crate::adapters::storage::{ArtifactDescriptor, ArtifactStore, ArtifactStoreError};
use crate::knowledge_graph::extraction::extractor::{
    validate_extraction_batch, ExtractionBatchConfig, ExtractionBatchError,
};
use crate::wire::{common, documents, graph};
use protobuf::Message;
use std::error::Error;
use std::fmt::{Display, Formatter};

const SCHEMA_VERSION: u32 = 1;
pub const EXTRACTION_BATCH_MEDIA_TYPE: &str =
    "application/vnd.regulagraph.extraction-batch+protobuf";

#[derive(Clone, Debug, PartialEq)]
pub struct ExtractionBatchArtifact {
    pub descriptor: ArtifactDescriptor,
    pub reference: common::ArtifactRef,
}

#[derive(Debug, PartialEq, Eq)]
pub enum ExtractionBatchArtifactError {
    InvalidDescriptor(&'static str),
    Storage(ArtifactStoreError),
    Serialization(String),
    SemanticValidation(ExtractionBatchError),
}

impl Display for ExtractionBatchArtifactError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::InvalidDescriptor(field) => {
                write!(formatter, "invalid extraction batch descriptor: {field}")
            }
            Self::Storage(error) => write!(formatter, "extraction batch storage failed: {error}"),
            Self::Serialization(detail) => {
                write!(formatter, "extraction batch serialization failed: {detail}")
            }
            Self::SemanticValidation(error) => {
                write!(formatter, "extraction batch validation failed: {error}")
            }
        }
    }
}

impl Error for ExtractionBatchArtifactError {}

impl From<ArtifactStoreError> for ExtractionBatchArtifactError {
    fn from(value: ArtifactStoreError) -> Self {
        Self::Storage(value)
    }
}

impl From<ExtractionBatchError> for ExtractionBatchArtifactError {
    fn from(value: ExtractionBatchError) -> Self {
        Self::SemanticValidation(value)
    }
}

pub fn persist_extraction_batch(
    store: &ArtifactStore,
    batch: &graph::ExtractionBatch,
    source: &documents::DocumentBatch,
) -> Result<ExtractionBatchArtifact, ExtractionBatchArtifactError> {
    validate_extraction_batch(batch, source, &ExtractionBatchConfig::default())?;
    let bytes = batch
        .write_to_bytes()
        .map_err(|error| ExtractionBatchArtifactError::Serialization(error.to_string()))?;
    let descriptor = store.put_bytes(
        "extraction-batch",
        EXTRACTION_BATCH_MEDIA_TYPE,
        SCHEMA_VERSION,
        &bytes,
    )?;
    let reference = descriptor.to_wire_ref()?;
    Ok(ExtractionBatchArtifact {
        descriptor,
        reference,
    })
}

pub fn load_extraction_batch(
    store: &ArtifactStore,
    descriptor: &ArtifactDescriptor,
    source: &documents::DocumentBatch,
) -> Result<graph::ExtractionBatch, ExtractionBatchArtifactError> {
    if descriptor.media_type != EXTRACTION_BATCH_MEDIA_TYPE {
        return Err(ExtractionBatchArtifactError::InvalidDescriptor(
            "media_type",
        ));
    }
    if descriptor.schema_version != SCHEMA_VERSION {
        return Err(ExtractionBatchArtifactError::InvalidDescriptor(
            "schema_version",
        ));
    }
    let bytes = store.read_verified(descriptor)?;
    let batch = graph::ExtractionBatch::parse_from_bytes(&bytes)
        .map_err(|error| ExtractionBatchArtifactError::Serialization(error.to_string()))?;
    validate_extraction_batch(&batch, source, &ExtractionBatchConfig::default())?;
    Ok(batch)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::adapters::storage::ArtifactStoreConfig;
    use crate::knowledge_graph::extraction::extractor::{
        assemble_extraction_batch,
        tests::{fixture_parts, source_batch},
    };
    use std::fs;
    use std::path::PathBuf;
    use std::sync::atomic::{AtomicU64, Ordering};

    static TEST_SEQUENCE: AtomicU64 = AtomicU64::new(1);

    struct TestDir(PathBuf);

    impl TestDir {
        fn new() -> Self {
            let sequence = TEST_SEQUENCE.fetch_add(1, Ordering::Relaxed);
            let path = std::env::temp_dir().join(format!(
                "regulagraph-extraction-batch-test-{}-{sequence}",
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

    #[test]
    fn persists_loads_and_deduplicates_validated_extraction() {
        let root = TestDir::new();
        let store = ArtifactStore::open(
            &root.0,
            ArtifactStoreConfig {
                maximum_artifact_bytes: 1024 * 1024,
                sync_data: false,
            },
        )
        .expect("store opens");
        let source = source_batch();
        let batch =
            assemble_extraction_batch(fixture_parts(), &source, &ExtractionBatchConfig::default())
                .expect("fixture assembles");

        let first = persist_extraction_batch(&store, &batch, &source).expect("batch persists");
        let second = persist_extraction_batch(&store, &batch, &source).expect("retry deduplicates");
        assert_eq!(first, second);
        assert_eq!(first.reference.media_type, EXTRACTION_BATCH_MEDIA_TYPE);
        assert_eq!(
            load_extraction_batch(&store, &first.descriptor, &source).unwrap(),
            batch
        );

        let mut wrong_media = first.descriptor;
        wrong_media.media_type = "application/octet-stream".to_owned();
        assert_eq!(
            load_extraction_batch(&store, &wrong_media, &source).unwrap_err(),
            ExtractionBatchArtifactError::InvalidDescriptor("media_type")
        );
    }
}
