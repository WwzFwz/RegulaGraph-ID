//! Projects one chunk's versioned source evidence from a complete DocumentBatch.
//!
//! EXTRACT and INDEX must agree on source blob, provision version, regulation,
//! primary UTF-8 span, and page locators. This index validates a batch once and
//! builds O(1) lookup maps, then refuses unbound or uncovered chunk versions.
//! The caller authenticates DocumentBatch bytes and pins corpus/job before
//! construction; this helper does not infer legal effectiveness or publish data.
//! Measure build RSS and per-chunk p95 against configs/benchmark-targets.yaml
//! (REQUIRED_UNMEASURED).

use crate::domain::document_batch::{
    validate_document_batch, DocumentBatchConfig, DocumentBatchError,
};
use crate::wire::{common, documents};
use std::collections::{HashMap, HashSet};

#[derive(Debug)]
pub enum ChunkProvenanceError {
    Batch(DocumentBatchError),
    IncompleteBatch,
    MissingChunk,
    InvalidSpan,
    MissingTextArtifact,
    MissingVersion,
    MissingProvision,
    VersionTextMismatch,
    ForeignVersionSpan,
    UncoveredVersion,
    MissingStructure,
    ForeignStructure,
    InvalidParentChain,
    ForeignLocator,
}

pub struct ChunkProvenanceIndex<'a> {
    chunks: HashMap<&'a str, &'a documents::Chunk>,
    versions: HashMap<&'a str, &'a documents::ProvisionVersion>,
    provisions: HashMap<&'a str, &'a documents::Provision>,
    artifacts: HashMap<&'a str, &'a documents::TextArtifact>,
    structures: HashMap<&'a str, &'a documents::StructureNode>,
}

impl<'a> ChunkProvenanceIndex<'a> {
    pub fn new(batch: &'a documents::DocumentBatch) -> Result<Self, ChunkProvenanceError> {
        validate_document_batch(batch, &DocumentBatchConfig::default())
            .map_err(ChunkProvenanceError::Batch)?;
        if batch.completeness.enum_value() != Ok(common::Completeness::COMPLETENESS_COMPLETE)
            || batch.chunks.is_empty()
        {
            return Err(ChunkProvenanceError::IncompleteBatch);
        }
        Ok(Self {
            chunks: batch
                .chunks
                .iter()
                .map(|item| (item.meta.record_id.as_str(), item))
                .collect(),
            versions: batch
                .versions
                .iter()
                .map(|item| (item.meta.record_id.as_str(), item))
                .collect(),
            provisions: batch
                .provisions
                .iter()
                .map(|item| (item.meta.record_id.as_str(), item))
                .collect(),
            artifacts: batch
                .text_artifacts
                .iter()
                .map(|item| (item.meta.record_id.as_str(), item))
                .collect(),
            structures: batch
                .structures
                .iter()
                .map(|item| (item.meta.record_id.as_str(), item))
                .collect(),
        })
    }

    /// Builds evidence only for the canonical chunk object inside this batch.
    pub fn project(&self, chunk_id: &str) -> Result<common::Provenance, ChunkProvenanceError> {
        let chunk = self
            .chunks
            .get(chunk_id)
            .ok_or(ChunkProvenanceError::MissingChunk)?;
        let span = chunk
            .text_span
            .as_ref()
            .ok_or(ChunkProvenanceError::InvalidSpan)?;
        if span.start_byte >= span.end_byte || span.text_artifact_id.is_empty() {
            return Err(ChunkProvenanceError::InvalidSpan);
        }
        let artifact = self
            .artifacts
            .get(span.text_artifact_id.as_str())
            .ok_or(ChunkProvenanceError::MissingTextArtifact)?;
        let source_blob_id = artifact.source_blob_id.as_str();
        let mut sources = Vec::with_capacity(chunk.provision_version_refs.len());
        let mut seen = HashSet::with_capacity(chunk.provision_version_refs.len());
        for version_id in &chunk.provision_version_refs {
            let version = self
                .versions
                .get(version_id.as_str())
                .ok_or(ChunkProvenanceError::MissingVersion)?;
            if version.text_ref.as_ref() != artifact.normalized_text_ref.as_ref() {
                return Err(ChunkProvenanceError::VersionTextMismatch);
            }
            if version.spans.is_empty()
                || version
                    .spans
                    .iter()
                    .any(|version_span| version_span.text_artifact_id != span.text_artifact_id)
            {
                return Err(ChunkProvenanceError::ForeignVersionSpan);
            }
            if !version.spans.iter().any(|version_span| {
                version_span.text_artifact_id == span.text_artifact_id
                    && version_span.start_byte <= span.start_byte
                    && version_span.end_byte >= span.end_byte
            }) {
                return Err(ChunkProvenanceError::UncoveredVersion);
            }
            let provision = self
                .provisions
                .get(version.provision_id.as_str())
                .ok_or(ChunkProvenanceError::MissingProvision)?;
            let key = (
                source_blob_id,
                version_id.as_str(),
                provision.regulation_id.as_str(),
            );
            if seen.insert(key) {
                sources.push(common::SourceVersionRef {
                    source_blob_id: source_blob_id.to_owned(),
                    provision_version_id: version_id.clone(),
                    regulation_id: provision.regulation_id.clone(),
                    ..Default::default()
                });
            }
        }
        if sources.is_empty() {
            return Err(ChunkProvenanceError::MissingVersion);
        }
        if chunk.structure_node_refs.len() != 1 {
            return Err(ChunkProvenanceError::InvalidParentChain);
        }
        let direct = self
            .structures
            .get(chunk.structure_node_refs[0].as_str())
            .ok_or(ChunkProvenanceError::MissingStructure)?;
        if direct.source_spans.is_empty()
            || direct
                .source_spans
                .iter()
                .any(|source| source.text_artifact_id != span.text_artifact_id)
            || !direct.source_spans.iter().any(|source| {
                source.start_byte <= span.start_byte && source.end_byte >= span.end_byte
            })
        {
            return Err(ChunkProvenanceError::ForeignStructure);
        }
        let mut locators_by_page: HashMap<u32, Vec<common::PageLocator>> = HashMap::new();
        for locator in &direct.page_locators {
            let page_index = usize::try_from(locator.page_number.saturating_sub(1))
                .map_err(|_| ChunkProvenanceError::ForeignLocator)?;
            if locator.source_blob_id != source_blob_id
                || locator.page_number == 0
                || artifact.page_results.get(page_index).is_none_or(|page| {
                    page.page_number != locator.page_number
                        || page.status.enum_value()
                            != Ok(common::CompletionStatus::COMPLETION_STATUS_SUCCEEDED)
                })
            {
                return Err(ChunkProvenanceError::ForeignLocator);
            }
            locators_by_page
                .entry(locator.page_number)
                .or_default()
                .push(locator.clone());
        }
        let mut locators = Vec::new();
        let first_page = artifact.page_results.partition_point(|page| {
            page.spans
                .last()
                .is_some_and(|page_span| page_span.end_byte <= span.start_byte)
        });
        for page in &artifact.page_results[first_page..] {
            if page
                .spans
                .first()
                .is_some_and(|page_span| page_span.start_byte >= span.end_byte)
            {
                break;
            }
            if !page.spans.iter().any(|page_span| {
                page_span.text_artifact_id == span.text_artifact_id
                    && page_span.start_byte < span.end_byte
                    && page_span.end_byte > span.start_byte
            }) {
                continue;
            }
            if page.status.enum_value() != Ok(common::CompletionStatus::COMPLETION_STATUS_SUCCEEDED)
            {
                return Err(ChunkProvenanceError::ForeignLocator);
            }
            if let Some(proven_locators) = locators_by_page.remove(&page.page_number) {
                locators.extend(proven_locators);
            } else {
                locators.push(common::PageLocator {
                    source_blob_id: source_blob_id.to_owned(),
                    page_number: page.page_number,
                    ..Default::default()
                });
            }
        }
        if locators.is_empty() {
            return Err(ChunkProvenanceError::ForeignLocator);
        }
        let mut expected_reverse = Vec::new();
        let mut current = *direct;
        let mut visited = HashSet::new();
        loop {
            if !visited.insert(current.meta.record_id.as_str()) {
                return Err(ChunkProvenanceError::InvalidParentChain);
            }
            let Some(parent_id) = current.parent_id.as_deref() else {
                break;
            };
            let parent = self
                .structures
                .get(parent_id)
                .ok_or(ChunkProvenanceError::MissingStructure)?;
            if parent.source_spans.is_empty()
                || parent
                    .source_spans
                    .iter()
                    .any(|source| source.text_artifact_id != span.text_artifact_id)
            {
                return Err(ChunkProvenanceError::ForeignStructure);
            }
            match parent.kind.enum_value() {
                Ok(documents::StructureKind::STRUCTURE_KIND_DOCUMENT) => {}
                Ok(_) => expected_reverse.push(parent_id),
                Err(_) => return Err(ChunkProvenanceError::InvalidParentChain),
            }
            current = parent;
        }
        expected_reverse.reverse();
        if chunk
            .parent_refs
            .iter()
            .map(String::as_str)
            .collect::<Vec<_>>()
            != expected_reverse
        {
            return Err(ChunkProvenanceError::InvalidParentChain);
        }
        Ok(common::Provenance {
            sources,
            spans: vec![span.clone()],
            locators,
            ..Default::default()
        })
    }
}
