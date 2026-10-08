//! Prepares the complete selected CHUNK population for initial BM25 statistics.
//! Reads authenticated source/text artifacts and reuses the exact INDEX renderer
//! and analyzer. No model call or term-ID allocation occurs here; Go owns source
//! checkpoint/snapshot authorization, dictionary allocation and publication.
//! All chunks are counted once, including zero-token documents. Duplicate IDs,
//! scope drift, corrupt input, cancellation and resource overflow return no partial
//! population. Measure source/render I/O, term memory, throughput and score parity
//! under configs/benchmark-targets.yaml; required quality/performance is unmeasured.

use super::{
    analyzer::{analyze_document, ANALYZER_VERSION},
    dictionary_artifact::CheckedDictionary,
    inputs::RENDER_POLICY_VERSION,
    lexical::Bm25Statistics,
    loading::VerifiedIndexInputs,
};
use crate::{
    adapters::{
        document_batches::load_document_batch,
        storage::{ArtifactDescriptor, ArtifactStore},
    },
    document::normalization::text::TextNormalizerConfig,
    domain::wire::{self, Limits},
    wire::{common, evidence},
};
use std::{
    collections::HashSet,
    sync::atomic::{AtomicBool, Ordering},
};

/// Operational memory/work limits, not benchmark target changes. Exceeding a
/// limit rejects the entire population; callers must not silently sample it.
#[derive(Clone, Copy)]
pub struct PopulationLimits {
    pub maximum_sources: usize,
    pub maximum_chunks: usize,
    pub maximum_source_bytes: u64,
    pub maximum_text_read_bytes: u64,
    pub maximum_tokens: u64,
    pub maximum_term_bytes: u64,
    pub chunks_per_selection: usize,
}

impl Default for PopulationLimits {
    fn default() -> Self {
        Self {
            maximum_sources: 256,
            maximum_chunks: 32768,
            maximum_source_bytes: 64 << 20,
            maximum_text_read_bytes: 512 << 20,
            maximum_tokens: 2_000_000,
            maximum_term_bytes: 64 << 20,
            chunks_per_selection: 128,
        }
    }
}

/// Sealed preparation can freeze only the snapshot and rendering policy whose
/// actual inputs it analyzed. Source refs remain available for dependency writes.
pub struct PreparedLexicalPopulation {
    snapshot: common::SnapshotRef,
    sources: Vec<common::ArtifactRef>,
    statistics: Bm25Statistics,
}

impl PreparedLexicalPopulation {
    pub fn sources(&self) -> &[common::ArtifactRef] {
        &self.sources
    }
    pub fn document_count(&self) -> usize {
        self.statistics.document_count()
    }
    pub fn total_tokens(&self) -> u64 {
        self.statistics.total_tokens()
    }
    /// Sorted distinct terms for the Go registry allocator; this never invents IDs.
    pub fn terms(&self) -> impl Iterator<Item = &str> {
        self.statistics.terms()
    }

    pub fn freeze(
        &self,
        meta: &common::RecordMeta,
        dictionary: &CheckedDictionary,
        k1: f64,
        b: f64,
    ) -> Result<evidence::LexicalStatisticsArtifact, String> {
        let mut result = self.statistics.freeze_artifact(
            meta,
            &self.snapshot,
            dictionary,
            k1,
            b,
            Limits::default(),
        )?;
        result.input_policy = RENDER_POLICY_VERSION.into();
        wire::validate(&result, Limits::default())?;
        Ok(result)
    }
}

pub fn prepare_population(
    store: &ArtifactStore,
    source_refs: &[common::ArtifactRef],
    snapshot: &common::SnapshotRef,
    auth_scope: &str,
    normalizer: &TextNormalizerConfig,
    limits: PopulationLimits,
    cancelled: &AtomicBool,
) -> Result<PreparedLexicalPopulation, String> {
    wire::validate(snapshot, Limits::default())?;
    if auth_scope.is_empty()
        || source_refs.is_empty()
        || source_refs.len() > limits.maximum_sources
        || limits.maximum_sources == 0
        || limits.maximum_sources > 256
        || limits.maximum_chunks == 0
        || limits.maximum_chunks > 32768
        || limits.maximum_source_bytes == 0
        || limits.maximum_source_bytes > 64 << 20
        || limits.maximum_text_read_bytes == 0
        || limits.maximum_text_read_bytes > 512 << 20
        || limits.maximum_tokens == 0
        || limits.maximum_tokens > 2_000_000
        || limits.maximum_term_bytes == 0
        || limits.maximum_term_bytes > 64 << 20
        || !(1..=128).contains(&limits.chunks_per_selection)
    {
        return Err("invalid or exceeded lexical population limits/scope".into());
    }
    let check_cancel = || {
        if cancelled.load(Ordering::Acquire) {
            Err("lexical population cancelled".to_owned())
        } else {
            Ok(())
        }
    };
    check_cancel()?;
    let mut sources = source_refs.to_vec();
    sources.sort_by(|a, b| a.artifact_id.cmp(&b.artifact_id));
    let mut source_bytes = 0u64;
    let mut hashes = HashSet::new();
    for (i, reference) in sources.iter().enumerate() {
        wire::validate(reference, Limits::default())?;
        if reference.byte_size == 0
            || reference.byte_size > 16 << 20
            || i > 0 && sources[i - 1].artifact_id == reference.artifact_id
            || !hashes.insert(reference.content_hash.sha256.as_str())
        {
            return Err("duplicate source or oversized lexical population artifact".into());
        }
        source_bytes = source_bytes
            .checked_add(reference.byte_size)
            .ok_or("source byte overflow")?;
        if source_bytes > limits.maximum_source_bytes {
            return Err("lexical source byte budget exceeded".into());
        }
    }
    let mut statistics = Bm25Statistics::new(ANALYZER_VERSION).map_err(|e| format!("{e:?}"))?;
    let mut chunk_ids = HashSet::new();
    let mut term_bytes = 0u64;
    let mut text_read_bytes = 0u64;
    for reference in &sources {
        check_cancel()?;
        let source = load_document_batch(
            store,
            &ArtifactDescriptor::from_wire_ref(reference).map_err(|e| e.to_string())?,
        )
        .map_err(|e| e.to_string())?;
        wire::validate(&source, Limits::default())?;
        if source.meta.corpus_id != snapshot.corpus_id
            || source.context.snapshot_ref.as_ref() != Some(snapshot)
            || source.context.auth_scope_ref != auth_scope
            || source.chunks.is_empty()
        {
            return Err("lexical source snapshot, scope or chunk inventory mismatch".into());
        }
        let inputs = VerifiedIndexInputs::new(&source).map_err(|e| format!("{e:?}"))?;
        let mut selection = Vec::with_capacity(source.chunks.len());
        for chunk in &source.chunks {
            if chunk_ids.len() == limits.maximum_chunks
                || !chunk_ids.insert(chunk.meta.record_id.clone())
            {
                return Err("duplicate chunk or lexical population chunk budget exceeded".into());
            }
            selection.push(chunk.meta.record_id.clone());
        }
        selection.sort();
        let chunk_text: std::collections::HashMap<_, _> = source
            .chunks
            .iter()
            .map(|c| {
                (
                    c.meta.record_id.as_str(),
                    c.text_span.text_artifact_id.as_str(),
                )
            })
            .collect();
        let text_sizes: std::collections::HashMap<_, _> = source
            .text_artifacts
            .iter()
            .map(|a| {
                let mut bytes = 0u64;
                for r in [&a.raw_text_ref, &a.normalized_text_ref, &a.mapping_ref] {
                    if r.byte_size > 16 << 20 {
                        return Err("population text artifact exceeds 16 MiB");
                    }
                    bytes = bytes
                        .checked_add(r.byte_size)
                        .ok_or("text byte count overflow")?;
                }
                Ok((a.meta.record_id.as_str(), bytes))
            })
            .collect::<Result<_, &str>>()?;
        for page in selection.chunks(limits.chunks_per_selection) {
            check_cancel()?;
            // Charge actual loader reads, including rereads across pages. This
            // bounds I/O rather than pretending rendered bytes bound peak RSS.
            let mut page_text = HashSet::new();
            for id in page {
                let text = chunk_text.get(id.as_str()).ok_or("missing chunk text")?;
                if page_text.insert(*text) {
                    text_read_bytes = text_read_bytes
                        .checked_add(*text_sizes.get(text).ok_or("missing text artifact")?)
                        .ok_or("text read budget overflow")?;
                }
            }
            if text_read_bytes > limits.maximum_text_read_bytes {
                return Err("lexical population text read budget exceeded".into());
            }
            let rendered = inputs
                .load_selected(store, normalizer, page, 1 << 20, cancelled)
                .map_err(|e| format!("{e:?}"))?;
            for item in rendered {
                check_cancel()?;
                let terms = analyze_document(&item.text).map_err(|e| format!("{e:?}"))?;
                if statistics
                    .total_tokens()
                    .checked_add(terms.len() as u64)
                    .ok_or("token count overflow")?
                    > limits.maximum_tokens
                {
                    return Err("lexical token budget exceeded".into());
                }
                for term in &terms {
                    term_bytes = term_bytes
                        .checked_add(term.len() as u64)
                        .ok_or("term byte overflow")?;
                }
                if term_bytes > limits.maximum_term_bytes {
                    return Err("lexical term byte budget exceeded".into());
                }
                statistics
                    .upsert(
                        ANALYZER_VERSION,
                        &item.chunk_id,
                        terms.iter().map(String::as_str),
                    )
                    .map_err(|e| format!("{e:?}"))?;
            }
        }
    }
    check_cancel()?;
    Ok(PreparedLexicalPopulation {
        snapshot: snapshot.clone(),
        sources,
        statistics,
    })
}
