//! Builds an additive GraphDelta from source-checked extraction and committed resolution.
//! Canonical registry rows, base snapshot, artifact hashes and receipt authority must be
//! authenticated by the coordinator before calling; this pure transform never allocates
//! registry identities or writes a backend. Every record gets the target visibility;
//! source supports, decisions and positive/negative dependencies remain auditable.
//! This entry point creates upserts only. Withdrawal/merge/split plans require a separate
//! before-image-aware path; no closures are inferred from absent records. Bound protobuf
//! bytes/items before cloning and measure assembly p95/p99/RSS and support preservation
//! against configs/benchmark-targets.yaml (REQUIRED_UNMEASURED).

use std::collections::{BTreeMap, BTreeSet};

use protobuf::{Message, MessageField};
use sha2::{Digest, Sha256};

use super::canonical::{assemble_canonical_relations, reject_unknown_fields};
use crate::domain::wire::{validate, Limits};
use crate::knowledge_graph::extraction::extractor::{
    validate_extraction_batch, ExtractionBatchConfig,
};
use crate::knowledge_graph::schema::Ontology;
use crate::wire::{common, documents, graph};

/// Internal borrowed inputs, not another wire contract. The registry artifact must
/// contain/authorize these exact entities at registry_revision, verified by Go.
pub struct DeltaInput<'a> {
    pub delta_id: &'a str,
    pub target_sequence: u64,
    pub source_documents: &'a documents::DocumentBatch,
    pub normalized_texts: &'a BTreeMap<String, Vec<u8>>,
    pub extraction: &'a graph::ExtractionBatch,
    pub extraction_ref: &'a common::ArtifactRef,
    pub resolution: &'a graph::ResolutionBatch,
    pub resolution_ref: &'a common::ArtifactRef,
    pub registry_ref: &'a common::ArtifactRef,
    pub registry_revision: u64,
    pub entities: &'a [graph::CanonicalEntity],
    pub producer: &'a common::ProducerManifest,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum DeltaError {
    InvalidInput,
    BudgetExceeded,
    InvalidSource,
    RegistryMismatch,
    DuplicateIdentity,
    DependencyConflict,
    Resolution,
}

/// Produces a closed, source-scoped upsert batch. Empty extraction is allowed when
/// its source/counts are complete. Alias/profile generation and incremental closure
/// are separate stages and are not fabricated here. No caller validation-report is trusted.
pub fn assemble_graph_delta(
    input: DeltaInput<'_>,
    ontology: &Ontology,
    limits: Limits,
) -> Result<graph::GraphDelta, DeltaError> {
    use DeltaError as E;
    let corpus = input.extraction.meta.corpus_id.as_str();
    let base = input
        .extraction
        .context
        .snapshot_ref
        .as_ref()
        .ok_or(E::InvalidInput)?;
    if limits.max_items == 0 || limits.max_bytes == 0 || limits.max_depth == 0 {
        return Err(E::BudgetExceeded);
    }
    if input.delta_id.is_empty() || input.delta_id.len() > 256 {
        return Err(E::InvalidInput);
    }
    let mut bytes = 0u64;
    for size in [
        input.source_documents.compute_size(),
        input.extraction.compute_size(),
        input.resolution.compute_size(),
        input.producer.compute_size(),
        input.extraction_ref.compute_size(),
        input.resolution_ref.compute_size(),
        input.registry_ref.compute_size(),
    ]
    .into_iter()
    .chain(input.entities.iter().map(Message::compute_size))
    .chain(
        input
            .normalized_texts
            .values()
            .map(|text| text.len() as u64),
    ) {
        bytes = bytes.checked_add(size).ok_or(E::BudgetExceeded)?;
        if bytes > limits.max_bytes as u64 {
            return Err(E::BudgetExceeded);
        }
    }
    if input.entities.len() > limits.max_items || input.normalized_texts.len() > limits.max_items {
        return Err(E::BudgetExceeded);
    }
    if input.target_sequence <= base.sequence
        || input.target_sequence > i64::MAX as u64
        || input.registry_revision == 0
        || input.registry_revision > i64::MAX as u64
        || input.registry_revision != input.resolution.registry_revision
        || input.source_documents.context.snapshot_ref.as_ref() != Some(base)
        || input.source_documents.context.auth_scope_ref != input.extraction.context.auth_scope_ref
        || input.source_documents.context.config_fingerprint
            != input.extraction.context.config_fingerprint
        || input.producer.schema_version != 1
    {
        return Err(E::InvalidInput);
    }
    if input.source_documents.completeness.enum_value()
        != Ok(common::Completeness::COMPLETENESS_COMPLETE)
    {
        return Err(E::InvalidSource);
    }
    let input_ids: BTreeSet<_> = [
        input.extraction_ref.artifact_id.as_str(),
        input.resolution_ref.artifact_id.as_str(),
        input.registry_ref.artifact_id.as_str(),
        input.extraction.source_document_batch.artifact_id.as_str(),
    ]
    .into_iter()
    .collect();
    if input_ids.len() != 4 {
        return Err(E::InvalidInput);
    }
    for reference in [
        input.extraction_ref,
        input.resolution_ref,
        input.registry_ref,
    ] {
        validate(reference, limits).map_err(|_| E::InvalidInput)?;
        reject_unknown_fields(reference, 0).map_err(|_| E::InvalidInput)?;
        if reference.schema_version != 1 {
            return Err(E::InvalidInput);
        }
    }
    validate(input.producer, limits).map_err(|_| E::InvalidInput)?;
    reject_unknown_fields(input.producer, 0).map_err(|_| E::InvalidInput)?;
    reject_unknown_fields(input.source_documents, 0).map_err(|_| E::InvalidInput)?;
    validate_extraction_batch(
        input.extraction,
        input.source_documents,
        &ExtractionBatchConfig {
            maximum_records: limits.max_items,
            maximum_reference_edges: limits.max_items,
        },
    )
    .map_err(|_| E::InvalidSource)?;
    validate_source_texts(&input)?;

    let mut entities = BTreeMap::new();
    for entity in input.entities {
        validate(entity, limits).map_err(|_| E::RegistryMismatch)?;
        reject_unknown_fields(entity, 0).map_err(|_| E::InvalidInput)?;
        if entity.meta.schema_version != 1
            || entity.meta.corpus_id != corpus
            || entity.meta.visibility.is_some()
            || !ontology.permits_entity_type(&entity.entity_type)
            || entity.registry_revision > input.registry_revision
            || !matches!(
                entity.review_state.enum_value(),
                Ok(common::ReviewState::REVIEW_STATE_APPROVED
                    | common::ReviewState::REVIEW_STATE_UNREVIEWED)
            )
        {
            return Err(E::RegistryMismatch);
        }
        if entities
            .insert(entity.meta.record_id.as_str(), entity)
            .is_some()
        {
            return Err(E::DuplicateIdentity);
        }
    }
    let canonical_ids: BTreeSet<_> = entities.keys().map(|id| (*id).to_owned()).collect();
    let canonical = assemble_canonical_relations(
        input.extraction,
        input.resolution,
        input.extraction_ref,
        ontology,
        &canonical_ids,
        limits.max_items,
        limits.max_bytes as u64,
    )
    .map_err(|_| E::Resolution)?;
    // A valid mention ontology type alone does not prove that the committed target
    // has that type. Recheck assignments against actual revision-pinned entity rows.
    let mention_types: BTreeMap<_, _> = input
        .extraction
        .mentions
        .iter()
        .map(|m| (m.meta.record_id.as_str(), m.candidate_type.as_str()))
        .collect();
    let proposals: BTreeMap<_, _> = input
        .resolution
        .proposals
        .iter()
        .map(|p| (p.meta.record_id.as_str(), p))
        .collect();
    for decision in &input.resolution.decisions {
        if decision.assigned_canonical_ids.is_empty() {
            continue;
        }
        let entity = entities
            .get(decision.assigned_canonical_ids[0].as_str())
            .ok_or(E::RegistryMismatch)?;
        let proposal = proposals
            .get(decision.proposal_id.as_str())
            .ok_or(E::RegistryMismatch)?;
        if proposal
            .mention_ids
            .iter()
            .any(|id| mention_types.get(id.as_str()).copied() != Some(entity.entity_type.as_str()))
        {
            return Err(E::RegistryMismatch);
        }
    }

    let mut dependencies = BTreeMap::<String, common::ContentHash>::new();
    let mut lookups = BTreeMap::<String, common::LookupScopeRevision>::new();
    for inherited in [
        &input.extraction.dependencies,
        &input.resolution.dependencies,
    ] {
        for dep in &inherited.dependencies {
            insert_dependency(&mut dependencies, &dep.dependency_id, &dep.fingerprint)?;
        }
        for scope in &inherited.lookup_scope_revisions {
            if scope.revision > input.registry_revision {
                return Err(E::DependencyConflict);
            }
            if let Some(previous) = lookups.insert(scope.scope_id.clone(), scope.clone()) {
                if previous != *scope {
                    return Err(E::DependencyConflict);
                }
            }
        }
    }
    for reference in [
        input.extraction_ref,
        input.resolution_ref,
        input.registry_ref,
        &input.extraction.source_document_batch,
    ] {
        insert_dependency(
            &mut dependencies,
            &reference.artifact_id,
            &reference.content_hash,
        )?;
    }
    insert_dependency(
        &mut dependencies,
        &format!("ontology:{}", ontology.version()),
        &ontology.content_hash(),
    )?;
    if dependencies.contains_key(input.delta_id) {
        return Err(E::DependencyConflict);
    }
    let mut delta = graph::GraphDelta {
        meta: MessageField::some(common::RecordMeta {
            schema_version: 1,
            corpus_id: corpus.to_owned(),
            record_id: input.delta_id.to_owned(),
            ..Default::default()
        }),
        base_snapshot: MessageField::some(base.clone()),
        registry_revision: input.registry_revision,
        entities: entities.into_values().cloned().collect(),
        mentions: canonical.relations.mentions,
        assertions: canonical.relations.assertions,
        supports: canonical.relations.supports,
        decisions: input.resolution.decisions.clone(),
        ontology_version: ontology.version().to_owned(),
        dependencies: MessageField::some(common::DependencyManifest {
            artifact_id: input.delta_id.to_owned(),
            producer_manifest: MessageField::some(input.producer.clone()),
            dependencies: dependencies
                .into_iter()
                .map(|(dependency_id, fingerprint)| common::Dependency {
                    dependency_id,
                    fingerprint: MessageField::some(fingerprint),
                    ..Default::default()
                })
                .collect(),
            lookup_scope_revisions: lookups.into_values().collect(),
            ..Default::default()
        }),
        validation_report: MessageField::some(common::ValidationReport::new()),
        ..Default::default()
    };
    delta
        .decisions
        .sort_by(|a, b| a.meta.record_id.cmp(&b.meta.record_id));
    let mut ids = BTreeSet::from([input.delta_id.to_owned()]);
    for meta in delta
        .entities
        .iter_mut()
        .map(|r| &mut r.meta)
        .chain(delta.mentions.iter_mut().map(|r| &mut r.meta))
        .chain(delta.assertions.iter_mut().map(|r| &mut r.meta))
        .chain(delta.supports.iter_mut().map(|r| &mut r.meta))
        .chain(delta.decisions.iter_mut().map(|r| &mut r.meta))
    {
        let meta = meta.as_mut().ok_or(E::InvalidInput)?;
        if meta.visibility.is_some() || !ids.insert(meta.record_id.clone()) {
            return Err(E::DuplicateIdentity);
        }
        meta.visibility = MessageField::some(common::Visibility {
            from_seq: input.target_sequence,
            ..Default::default()
        });
    }
    delta.validation_report = MessageField::some(common::ValidationReport {
        checked_records: ids.len() as u64,
        valid: true,
        ..Default::default()
    });
    if delta.compute_size() > limits.max_bytes as u64 {
        return Err(E::BudgetExceeded);
    }
    validate(&delta, limits).map_err(|_| E::InvalidInput)?;
    Ok(delta)
}

// Source metadata establishes blob/regulation/version/span ownership; verified UTF-8
// bytes additionally establish actual bounds, code-point boundaries and mention text.
fn validate_source_texts(input: &DeltaInput<'_>) -> Result<(), DeltaError> {
    let spans: Vec<_> = input
        .extraction
        .mentions
        .iter()
        .map(|m| &*m.text_span)
        .chain(
            input
                .extraction
                .supports
                .iter()
                .flat_map(|s| &s.evidence_spans),
        )
        .collect();
    let needed: BTreeSet<_> = spans.iter().map(|s| s.text_artifact_id.as_str()).collect();
    if needed.len() != input.normalized_texts.len() {
        return Err(DeltaError::InvalidSource);
    }
    let artifacts: BTreeMap<_, _> = input
        .source_documents
        .text_artifacts
        .iter()
        .map(|a| (a.meta.record_id.as_str(), a))
        .collect();
    let mut texts = BTreeMap::new();
    for id in needed {
        let artifact = artifacts.get(id).ok_or(DeltaError::InvalidSource)?;
        let raw = input
            .normalized_texts
            .get(id)
            .ok_or(DeltaError::InvalidSource)?;
        if raw.len() as u64 != artifact.normalized_text_ref.byte_size
            || format!("{:x}", Sha256::digest(raw))
                != artifact.normalized_text_ref.content_hash.sha256
        {
            return Err(DeltaError::InvalidSource);
        }
        texts.insert(
            id,
            std::str::from_utf8(raw).map_err(|_| DeltaError::InvalidSource)?,
        );
    }
    for span in spans {
        let text = texts
            .get(span.text_artifact_id.as_str())
            .ok_or(DeltaError::InvalidSource)?;
        let start = usize::try_from(span.start_byte).map_err(|_| DeltaError::InvalidSource)?;
        let end = usize::try_from(span.end_byte).map_err(|_| DeltaError::InvalidSource)?;
        if text.get(start..end).is_none() {
            return Err(DeltaError::InvalidSource);
        }
    }
    for mention in &input.extraction.mentions {
        let raw = &input.normalized_texts[&mention.text_span.text_artifact_id];
        if raw[mention.text_span.start_byte as usize..mention.text_span.end_byte as usize]
            != *mention.surface_form.as_bytes()
        {
            return Err(DeltaError::InvalidSource);
        }
    }
    Ok(())
}

fn insert_dependency(
    target: &mut BTreeMap<String, common::ContentHash>,
    id: &str,
    fingerprint: &common::ContentHash,
) -> Result<(), DeltaError> {
    if let Some(previous) = target.insert(id.to_owned(), fingerprint.clone()) {
        if previous != *fingerprint {
            return Err(DeltaError::DependencyConflict);
        }
    }
    Ok(())
}
