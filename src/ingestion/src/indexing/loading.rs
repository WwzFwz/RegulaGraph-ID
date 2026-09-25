//! Loads selected X01 chunk inputs from an authenticated DocumentBatch and TextArtifacts.
//!
//! Construction validates a complete batch object once; its caller must first
//! authenticate the batch bytes with `load_document_batch` and pin its corpus/job.
//! Each bounded selection reads
//! only referenced normalized text through the artifact store's verified path,
//! then renders exact UTF-8 spans and parent labels. Source/version metadata is
//! not inferred from embedding text; later IndexRecord assembly must project it
//! from this same batch. Measure verified I/O, normalization, RSS, and per-batch
//! throughput against configs/benchmark-targets.yaml (REQUIRED_UNMEASURED).
//! Cancellation is checked around, but cannot interrupt, one artifact read and
//! renormalization; the 2 MiB output cap is not a cap on verified I/O/RSS.

use crate::adapters::storage::ArtifactStore;
use crate::adapters::text_artifacts::{load_normalized_text, PersistTextArtifactError};
use crate::document::normalization::text::TextNormalizerConfig;
use crate::domain::document_batch::{
    validate_document_batch, DocumentBatchConfig, DocumentBatchError,
};
use crate::indexing::inputs::{EmbeddingInputRenderer, RenderError, RenderedEmbeddingInput};
use crate::wire::{common, documents};
use std::collections::{HashMap, HashSet};
use std::sync::atomic::{AtomicBool, Ordering};

const MAX_ITEMS: usize = 128;
const MAX_ITEM_BYTES: usize = 1024 * 1024;
const MAX_BATCH_BYTES: usize = 2 * 1024 * 1024;

#[derive(Debug)]
pub enum LoadIndexInputsError {
    Batch(DocumentBatchError),
    Artifact(PersistTextArtifactError),
    Render(RenderError),
    IncompleteBatch,
    InvalidSelection,
    MissingChunk,
    MissingTextArtifact,
    TooLarge,
    Cancelled,
}

/// Holds the validated batch and O(1) chunk/structure lookups across multiple
/// selections, avoiding a complete closure scan for each native inference RPC.
pub struct VerifiedIndexInputs<'a> {
    batch: &'a documents::DocumentBatch,
    chunks: HashMap<&'a str, &'a documents::Chunk>,
    renderer: EmbeddingInputRenderer<'a>,
}

impl<'a> VerifiedIndexInputs<'a> {
    pub fn new(batch: &'a documents::DocumentBatch) -> Result<Self, LoadIndexInputsError> {
        validate_document_batch(batch, &DocumentBatchConfig::default())
            .map_err(LoadIndexInputsError::Batch)?;
        if batch.completeness.enum_value() != Ok(common::Completeness::COMPLETENESS_COMPLETE)
            || batch.chunks.is_empty()
        {
            return Err(LoadIndexInputsError::IncompleteBatch);
        }
        let renderer =
            EmbeddingInputRenderer::new(&batch.structures).map_err(LoadIndexInputsError::Render)?;
        let chunks = batch
            .chunks
            .iter()
            .map(|chunk| (chunk.meta.record_id.as_str(), chunk))
            .collect();
        Ok(Self {
            batch,
            chunks,
            renderer,
        })
    }

    /// Returns all selected items in caller order or an error without a partial
    /// result. The caller advances its cursor only after successful return.
    pub fn load_selected(
        &self,
        store: &ArtifactStore,
        normalizer: &TextNormalizerConfig,
        chunk_ids: &[String],
        maximum_item_bytes: usize,
        cancelled: &AtomicBool,
    ) -> Result<Vec<RenderedEmbeddingInput>, LoadIndexInputsError> {
        if chunk_ids.is_empty()
            || chunk_ids.len() > MAX_ITEMS
            || maximum_item_bytes == 0
            || maximum_item_bytes > MAX_ITEM_BYTES
        {
            return Err(LoadIndexInputsError::InvalidSelection);
        }
        if cancelled.load(Ordering::Acquire) {
            return Err(LoadIndexInputsError::Cancelled);
        }
        let mut seen = HashSet::with_capacity(chunk_ids.len());
        let mut grouped: HashMap<&str, Vec<(usize, &documents::Chunk)>> = HashMap::new();
        for (position, chunk_id) in chunk_ids.iter().enumerate() {
            if !seen.insert(chunk_id.as_str()) {
                return Err(LoadIndexInputsError::InvalidSelection);
            }
            let chunk = self
                .chunks
                .get(chunk_id.as_str())
                .ok_or(LoadIndexInputsError::MissingChunk)?;
            let span = chunk
                .text_span
                .as_ref()
                .ok_or(LoadIndexInputsError::Render(RenderError::InvalidSpan))?;
            grouped
                .entry(span.text_artifact_id.as_str())
                .or_default()
                .push((position, chunk));
        }
        let mut output = vec![None; chunk_ids.len()];
        let mut total_bytes = 0usize;
        for artifact in &self.batch.text_artifacts {
            let Some(items) = grouped.remove(artifact.meta.record_id.as_str()) else {
                continue;
            };
            if cancelled.load(Ordering::Acquire) {
                return Err(LoadIndexInputsError::Cancelled);
            }
            let normalized = load_normalized_text(store, artifact, normalizer)
                .map_err(LoadIndexInputsError::Artifact)?;
            for (position, chunk) in items {
                if cancelled.load(Ordering::Acquire) {
                    return Err(LoadIndexInputsError::Cancelled);
                }
                let rendered = self
                    .renderer
                    .render_embedding_input(
                        chunk,
                        &artifact.meta.record_id,
                        &normalized.text,
                        maximum_item_bytes,
                    )
                    .map_err(LoadIndexInputsError::Render)?;
                total_bytes = total_bytes
                    .checked_add(rendered.text.len())
                    .ok_or(LoadIndexInputsError::TooLarge)?;
                if total_bytes > MAX_BATCH_BYTES {
                    return Err(LoadIndexInputsError::TooLarge);
                }
                output[position] = Some(rendered);
            }
        }
        if !grouped.is_empty() {
            return Err(LoadIndexInputsError::MissingTextArtifact);
        }
        output
            .into_iter()
            .map(|item| item.ok_or(LoadIndexInputsError::MissingChunk))
            .collect()
    }
}
