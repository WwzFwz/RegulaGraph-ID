//! Assembles one coordinator-planned INDEX selection from verified artifacts.
//! The bounded preparation joins source/version facts once, pins dictionary lineage
//! and frozen statistics, and computes sparse vectors before native inference.
//! Finalization accepts all dense results or none; immutable output is not publication.
//! Measure verified I/O, rendering, inference, serialization, RSS and throughput
//! separately under configs/benchmark-targets.yaml (REQUIRED_UNMEASURED).

use super::{
    analyzer::analyze_document,
    dictionary_artifact::CheckedDictionary,
    inputs::RENDER_POLICY_VERSION,
    loading::VerifiedIndexInputs,
    reuse::validate_embedding_generation,
    statistics_artifact::{check_analyzer, check_statistics},
};
use crate::{
    adapters::{
        document_batches::load_document_batch,
        storage::{ArtifactDescriptor, ArtifactStore},
    },
    document::normalization::text::TextNormalizerConfig,
    domain::wire::{self, Limits},
    wire::{common, evidence, inference},
};
use protobuf::{MessageField, MessageFull};
use sha2::{Digest, Sha256};
use std::{
    collections::{HashMap, HashSet},
    sync::atomic::{AtomicBool, Ordering},
};

pub const PLAN_MEDIA: &str = "application/x-protobuf; message=regulagraph.v1.IndexBuildPlan";
pub const BATCH_MEDIA: &str = "application/x-protobuf; message=regulagraph.v1.IndexBatch";
const MAX_ARTIFACT_BYTES: u64 = 16 << 20;
const MAX_CHAIN: usize = 64;

pub fn load_typed<M: MessageFull>(
    store: &ArtifactStore,
    reference: &common::ArtifactRef,
    cancelled: &AtomicBool,
) -> Result<M, String> {
    check_cancel(cancelled)?;
    let expected = format!(
        "application/x-protobuf; message={}",
        M::descriptor().full_name()
    );
    if reference.media_type != expected || reference.byte_size > MAX_ARTIFACT_BYTES {
        return Err("INDEX artifact type or byte budget mismatch".into());
    }
    // C01 typed artifacts use logical record IDs; the Rust store's physical ID
    // convention is separate. Keep the caller's hash/key/size/type untouched.
    wire::validate(reference, Limits::default())?;
    let mut physical = reference.clone();
    physical.artifact_id = format!("artifact:index-input:{}", reference.content_hash.sha256);
    let descriptor = ArtifactDescriptor::from_wire_ref(&physical).map_err(|e| e.to_string())?;
    let raw = store
        .read_verified(&descriptor)
        .map_err(|e| e.to_string())?;
    check_cancel(cancelled)?;
    let limits = if M::descriptor().full_name() == "regulagraph.v1.IndexBatch" {
        index_limits()
    } else {
        Limits::default()
    };
    let decoded = wire::decode(&raw, &M::descriptor(), limits)?;
    decoded
        .downcast_box::<M>()
        .map_err(|_| "INDEX artifact descriptor mismatch".into())
        .map(|value| *value)
}

pub fn validate_plan(plan: &evidence::IndexBuildPlan) -> Result<(), String> {
    wire::validate(plan, Limits::default())?;
    let corpus = &plan.meta.corpus_id;
    if plan.meta.visibility.is_some()
        || plan.target_snapshot.corpus_id != *corpus
        || plan.source_snapshot.corpus_id != *corpus
        || plan.source_snapshot.sequence > plan.target_snapshot.sequence
        || (plan.source_snapshot.sequence == plan.target_snapshot.sequence
            && plan.source_snapshot != plan.target_snapshot)
        || plan.target_snapshot.representation_generation != plan.generation.meta.record_id
        || plan.lexical_input_policy != RENDER_POLICY_VERSION
        || plan.items.len() > 128
        || plan.document_batch.byte_size > Limits::default().max_bytes as u64
        || plan.dictionary_chain.len() > MAX_CHAIN
        || plan.dictionary_chain.last() != plan.generation.lexical_dictionary.as_ref()
        || plan.closures.len() > 128
    {
        return Err("INDEX plan scope, snapshot, policy or bound mismatch".into());
    }
    validate_embedding_generation(corpus, &plan.generation).map_err(|e| format!("{e:?}"))?;
    let mut remaining = 64u64 << 20;
    for reference in plan.dictionary_chain.iter().chain([
        &*plan.generation.lexical_analyzer,
        &*plan.generation.lexical_statistics,
    ]) {
        if reference.byte_size == 0
            || reference.byte_size > MAX_ARTIFACT_BYTES
            || reference.byte_size > remaining
        {
            return Err("INDEX generation exceeds per-artifact or aggregate byte budget".into());
        }
        remaining -= reference.byte_size;
    }
    let mut chunks = HashSet::new();
    let mut records = HashSet::new();
    for item in &plan.items {
        if !chunks.insert(&item.chunk_id) || !records.insert(&item.record_id) {
            return Err("duplicate INDEX chunk or record ID".into());
        }
    }
    for closure in &plan.closures {
        if !records.insert(&closure.record_id)
            || closure.to_seq != plan.target_snapshot.sequence
            || closure.expected_from_seq >= closure.to_seq
        {
            return Err("INDEX closure conflicts with target or upsert".into());
        }
    }
    Ok(())
}

pub struct PreparedBuild {
    batch: evidence::IndexBatch,
    items: Vec<inference::TextItem>,
}

impl PreparedBuild {
    pub fn items(&self) -> &[inference::TextItem] {
        &self.items
    }
    pub fn finish(
        mut self,
        dense: Vec<(String, evidence::DenseVector)>,
    ) -> Result<evidence::IndexBatch, String> {
        if dense.len() != self.batch.records.len() {
            return Err("partial dense INDEX response".into());
        }
        let mut by_id = HashMap::new();
        for (id, vector) in dense {
            wire::validate(&vector, Limits::default())?;
            let norm: f64 = vector.values.iter().map(|v| (*v as f64).powi(2)).sum();
            if vector.model_id != self.batch.generation.dense_manifest.model_id
                || vector.dimensions != self.batch.generation.dense_manifest.dimensions.unwrap_or(0)
                || vector.values.len() != vector.dimensions as usize
                || (norm - 1.0).abs() > 0.01
                || !norm.is_finite()
                || by_id.insert(id, vector).is_some()
            {
                return Err("invalid or duplicate dense INDEX vector".into());
            }
        }
        for record in &mut self.batch.records {
            record.dense_vector = MessageField::some(
                by_id
                    .remove(&record.chunk_id)
                    .ok_or("missing dense INDEX chunk")?,
            );
        }
        self.batch.operations_checksum = MessageField::some(common::ContentHash {
            sha256: operations_checksum(&self.batch)?,
            ..Default::default()
        });
        wire::validate(&self.batch, index_limits())?;
        Ok(self.batch)
    }
}

pub fn prepare(
    store: &ArtifactStore,
    plan: &evidence::IndexBuildPlan,
    plan_ref: &common::ArtifactRef,
    context: &common::RequestContext,
    normalizer: &TextNormalizerConfig,
    cancelled: &AtomicBool,
) -> Result<PreparedBuild, String> {
    validate_plan(plan)?;
    wire::validate(context, Limits::default())?;
    if context.corpus_id != plan.meta.corpus_id
        || context.snapshot_ref != plan.target_snapshot
        || plan.meta.record_id != plan_ref.artifact_id
    {
        return Err("INDEX context differs from target snapshot".into());
    }
    // Re-read the immutable plan: callers cannot substitute a different object under its hash.
    if load_typed::<evidence::IndexBuildPlan>(store, plan_ref, cancelled)? != *plan {
        return Err("INDEX plan bytes differ from supplied object".into());
    }
    let source = load_document_batch(
        store,
        &ArtifactDescriptor::from_wire_ref(&plan.document_batch).map_err(|e| e.to_string())?,
    )
    .map_err(|e| e.to_string())?;
    // Match Go source-view admission before rendering or any native call. Large
    // corpora require dependency-complete source partitions, never silent drops.
    wire::validate(&source, Limits::default())?;
    if source.meta.corpus_id != plan.meta.corpus_id
        || source.context.snapshot_ref != plan.source_snapshot
    {
        return Err("INDEX source snapshot or corpus mismatch".into());
    }
    let inputs = VerifiedIndexInputs::new(&source).map_err(|e| format!("{e:?}"))?;
    let analyzer: evidence::LexicalAnalyzerArtifact =
        load_typed(store, &plan.generation.lexical_analyzer, cancelled)?;
    check_analyzer(&analyzer, &plan.meta.corpus_id, Limits::default())?;
    if analyzer.meta.record_id != plan.generation.lexical_analyzer.artifact_id {
        return Err("analyzer artifact identity mismatch".into());
    }
    let statistics: evidence::LexicalStatisticsArtifact =
        load_typed(store, &plan.generation.lexical_statistics, cancelled)?;
    if statistics.input_policy != plan.lexical_input_policy
        || statistics.meta.record_id != plan.generation.lexical_statistics.artifact_id
        || statistics.population_snapshot.sequence > plan.target_snapshot.sequence
        || (statistics.population_snapshot.sequence == plan.target_snapshot.sequence
            && statistics.population_snapshot != plan.target_snapshot)
    {
        return Err("statistics identity or population snapshot mismatch".into());
    }
    let mut parent = None;
    let mut frozen = None;
    let mut artifact_ids = HashSet::new();
    for reference in &plan.dictionary_chain {
        let artifact: evidence::LexicalDictionaryArtifact =
            load_typed(store, reference, cancelled)?;
        if artifact.meta.record_id != reference.artifact_id
            || !artifact_ids.insert(&reference.artifact_id)
        {
            return Err("dictionary artifact identity mismatch or duplicate".into());
        }
        let checked = CheckedDictionary::from_artifact(
            &artifact,
            &plan.meta.corpus_id,
            parent.as_ref(),
            Limits::default(),
        )?;
        if checked.registry_revision() == statistics.dictionary_registry_revision {
            frozen = Some(check_statistics(&statistics, &checked, Limits::default())?);
        }
        parent = Some(checked);
    }
    let dictionary = parent.ok_or("missing dictionary")?;
    let frozen = frozen.ok_or("statistics base not in checked dictionary chain")?;
    let selected: Vec<_> = plan.items.iter().map(|i| i.chunk_id.clone()).collect();
    let prepared = inputs
        .prepare_selected(
            store,
            normalizer,
            &selected,
            1 << 20,
            &plan.generation,
            cancelled,
        )
        .map_err(|e| format!("{e:?}"))?;
    let versions: HashMap<_, _> = source
        .versions
        .iter()
        .map(|v| (v.meta.record_id.as_str(), v))
        .collect();
    let regulations: HashMap<_, _> = source
        .regulations
        .iter()
        .map(|v| (v.meta.record_id.as_str(), v))
        .collect();
    let provisions: HashMap<_, _> = source
        .provisions
        .iter()
        .map(|v| (v.meta.record_id.as_str(), v))
        .collect();
    let texts: HashMap<_, _> = source
        .text_artifacts
        .iter()
        .map(|v| (v.meta.record_id.as_str(), v))
        .collect();
    let blobs: HashMap<_, _> = source
        .sources
        .iter()
        .map(|v| (v.meta.record_id.as_str(), v))
        .collect();
    let chunks: HashMap<_, _> = source
        .chunks
        .iter()
        .map(|v| (v.meta.record_id.as_str(), v))
        .collect();
    let visibility = common::Visibility {
        from_seq: plan.target_snapshot.sequence,
        ..Default::default()
    };
    let dependency = common::Dependency {
        dependency_id: plan_ref.artifact_id.clone(),
        fingerprint: plan_ref.content_hash.clone(),
        ..Default::default()
    };
    let deps = |id: &str| common::DependencyManifest {
        artifact_id: id.into(),
        dependencies: vec![dependency.clone()],
        producer_manifest: plan.producer.clone(),
        ..Default::default()
    };
    let mut records = Vec::with_capacity(prepared.len());
    let mut items = Vec::with_capacity(prepared.len());
    for (selection, input) in plan.items.iter().zip(prepared) {
        check_cancel(cancelled)?;
        let chunk = chunks
            .get(selection.chunk_id.as_str())
            .ok_or("missing source chunk")?;
        visible(&chunk.meta, visibility.from_seq)?;
        let text = texts
            .get(chunk.text_span.text_artifact_id.as_str())
            .ok_or("missing chunk text")?;
        visible(&text.meta, visibility.from_seq)?;
        let mut filters = Vec::new();
        for reference in &input.native_item.provenance.sources {
            let version = versions
                .get(reference.provision_version_id.as_str())
                .ok_or("missing source version")?;
            let regulation = regulations
                .get(reference.regulation_id.as_str())
                .ok_or("missing source regulation")?;
            let provision = provisions
                .get(version.provision_id.as_str())
                .ok_or("missing source provision")?;
            let blob = blobs
                .get(reference.source_blob_id.as_str())
                .ok_or("missing source blob")?;
            for meta in [&version.meta, &regulation.meta, &provision.meta, &blob.meta] {
                visible(meta, visibility.from_seq)?;
            }
            if matches!(
                version.review_state.enum_value(),
                Ok(common::ReviewState::REVIEW_STATE_REJECTED
                    | common::ReviewState::REVIEW_STATE_QUARANTINED)
            ) {
                return Err("version is rejected or quarantined".into());
            }
            filters.push(evidence::IndexProvisionFilter {
                provision_version_id: reference.provision_version_id.clone(),
                regulation_id: reference.regulation_id.clone(),
                source_blob_id: reference.source_blob_id.clone(),
                legal_interval: version.legal_interval.clone(),
                legal_status: version.legal_status,
                jurisdiction: regulation.jurisdiction.clone(),
                ..Default::default()
            });
        }
        let terms = analyze_document(&input.native_item.text).map_err(|e| format!("{e:?}"))?;
        let sparse = frozen
            .encode_document(dictionary.dictionary(), terms.iter().map(String::as_str))
            .map_err(|e| format!("{e:?}"))?;
        if sparse.indices.is_empty() {
            return Err("INDEX selection contains a zero-term document; no silent drop".into());
        }
        records.push(evidence::IndexRecord {
            meta: MessageField::some(common::RecordMeta {
                schema_version: 1,
                corpus_id: plan.meta.corpus_id.clone(),
                record_id: selection.record_id.clone(),
                visibility: MessageField::some(visibility.clone()),
                ..Default::default()
            }),
            generation_id: plan.generation.meta.record_id.clone(),
            chunk_id: selection.chunk_id.clone(),
            provision_version_refs: chunk.provision_version_refs.clone(),
            sparse_vector: MessageField::some(sparse),
            filter_metadata: MessageField::some(evidence::FilterMetadata {
                visibility: MessageField::some(visibility.clone()),
                provision_filters: filters,
                ..Default::default()
            }),
            dependencies: MessageField::some(deps(&selection.record_id)),
            ..Default::default()
        });
        items.push(input.native_item);
    }
    let count = records.len() as u64;
    Ok(PreparedBuild {
        items,
        batch: evidence::IndexBatch {
            meta: MessageField::some(common::RecordMeta {
                schema_version: 1,
                corpus_id: plan.meta.corpus_id.clone(),
                record_id: plan.output_batch_id.clone(),
                ..Default::default()
            }),
            context: MessageField::some(context.clone()),
            generation: plan.generation.clone(),
            records,
            closures: plan.closures.clone(),
            counts: MessageField::some(common::Counts {
                expected: count,
                accepted: count,
                ..Default::default()
            }),
            dependencies: MessageField::some(deps(&plan.output_batch_id)),
            build_plan: MessageField::some(plan_ref.clone()),
            ..Default::default()
        },
    })
}

fn visible(meta: &common::RecordMeta, sequence: u64) -> Result<(), String> {
    if let Some(v) = meta.visibility.as_ref() {
        // Output is open-ended. Closed input cannot back an open-ended record.
        if v.from_seq > sequence || v.to_seq.is_some() {
            return Err("INDEX source visibility cannot contain output".into());
        }
    }
    Ok(()) // Missing visibility still requires coordinator membership proof before publication.
}
pub fn check_cancel(cancelled: &AtomicBool) -> Result<(), String> {
    if cancelled.load(Ordering::Acquire) {
        Err("INDEX cancelled".into())
    } else {
        Ok(())
    }
}
pub fn index_limits() -> Limits {
    Limits {
        max_items: 1_000_000,
        ..Limits::default()
    }
}

/// v1 commitment: domain, plan byte SHA-256 (length-prefixed UTF-8), record count,
/// each ordered record ID/chunk ID, dense length/IEEE754 f32 bits, sparse length and
/// (u32 ID, f32 bits). All integers little endian; counts/string lengths u64.
/// Plan bytes bind source/generation/closures/producer. Go MUST also validate source
/// projections and exact plan correspondence; this digest alone is not admission.
pub fn operations_checksum(batch: &evidence::IndexBatch) -> Result<String, String> {
    let mut h = Sha256::new();
    h.update(b"regulagraph-index-plan-v1\0");
    let put = |h: &mut Sha256, value: &str| {
        h.update((value.len() as u64).to_le_bytes());
        h.update(value.as_bytes());
    };
    let plan = batch
        .build_plan
        .as_ref()
        .ok_or("INDEX checksum requires build plan")?;
    put(&mut h, &plan.content_hash.sha256);
    h.update((batch.records.len() as u64).to_le_bytes());
    for record in &batch.records {
        put(&mut h, &record.meta.record_id);
        put(&mut h, &record.chunk_id);
        let dense = record.dense_vector.as_ref().ok_or("missing dense vector")?;
        h.update((dense.values.len() as u64).to_le_bytes());
        for value in &dense.values {
            h.update(value.to_bits().to_le_bytes());
        }
        let sparse = record
            .sparse_vector
            .as_ref()
            .ok_or("missing sparse vector")?;
        if sparse.indices.len() != sparse.values.len() {
            return Err("sparse cardinality mismatch".into());
        }
        h.update((sparse.indices.len() as u64).to_le_bytes());
        for (id, value) in sparse.indices.iter().zip(&sparse.values) {
            h.update(id.to_le_bytes());
            h.update(value.to_bits().to_le_bytes());
        }
    }
    Ok(format!("{:x}", h.finalize()))
}
