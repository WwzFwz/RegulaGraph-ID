//! Admits coordinator-pinned ASSEMBLE roles and exact canonical selections before
//! invoking delta assembly. Shared wire schemas are authoritative; caller verifies
//! artifact hashes, source checkpoint and live publication/registry authority.
//! Unknown stage fields, partial/reordered selections and role drift fail closed.
//! Work is bounded by wire bytes/items and uses one ordered set per selection;
//! measure p95/RSS under benchmark-targets.yaml (REQUIRED_UNMEASURED).

use std::collections::{BTreeMap, BTreeSet};

use super::canonical::reject_unknown_fields;
use super::delta::{assemble_graph_delta, DeltaError, DeltaInput};
use crate::domain::wire::{validate, Limits};
use crate::knowledge_graph::schema::Ontology;
use crate::wire::{common, documents, graph};

pub const REGISTRY_ENTITY_VIEW_MEDIA_TYPE: &str =
    "application/x-protobuf; message=regulagraph.v1.RegistryEntityView";
pub const GRAPH_ASSEMBLY_PLAN_MEDIA_TYPE: &str =
    "application/x-protobuf; message=regulagraph.v1.GraphAssemblyPlan";

pub fn validate_registry_entity_view(
    view: &graph::RegistryEntityView,
    ontology: &Ontology,
    limits: Limits,
) -> Result<(), DeltaError> {
    validate(view, limits).map_err(|_| DeltaError::InvalidInput)?;
    reject_unknown_fields(view, 0).map_err(|_| DeltaError::InvalidInput)?;
    if view.meta.schema_version != 1
        || view.meta.visibility.is_some()
        || view.producer_manifest.schema_version != 1
        || view.publication_fence > i64::MAX as u64
        || view.registry_revision > i64::MAX as u64
        || view.entities.len() != view.requested_ids.len()
        || view.requested_ids.windows(2).any(|pair| pair[0] >= pair[1])
    {
        return Err(DeltaError::RegistryMismatch);
    }
    for (id, entity) in view.requested_ids.iter().zip(&view.entities) {
        if entity.meta.record_id != *id
            || *id == view.meta.record_id
            || entity.meta.schema_version != 1
            || entity.meta.visibility.is_some()
            || entity.meta.corpus_id != view.meta.corpus_id
            || entity.registry_revision > view.registry_revision
            || !ontology.permits_entity_type(&entity.entity_type)
            || !matches!(
                entity.review_state.enum_value(),
                Ok(common::ReviewState::REVIEW_STATE_APPROVED
                    | common::ReviewState::REVIEW_STATE_UNREVIEWED)
            )
        {
            return Err(DeltaError::RegistryMismatch);
        }
    }
    Ok(())
}

pub fn validate_graph_assembly_plan(
    plan: &graph::GraphAssemblyPlan,
    limits: Limits,
) -> Result<(), DeltaError> {
    validate(plan, limits).map_err(|_| DeltaError::InvalidInput)?;
    reject_unknown_fields(plan, 0).map_err(|_| DeltaError::InvalidInput)?;
    let base = plan
        .context
        .snapshot_ref
        .as_ref()
        .ok_or(DeltaError::InvalidInput)?;
    if plan.meta.schema_version != 1
        || plan.context.schema_version != 1
        || plan.producer_manifest.schema_version != 1
        || plan.meta.visibility.is_some()
        || base.corpus_id != plan.meta.corpus_id
        || plan.context.corpus_id != plan.meta.corpus_id
        || plan.target_sequence <= base.sequence
        || plan.target_sequence > i64::MAX as u64
        || plan.publication_fence > i64::MAX as u64
        || plan.registry_revision > i64::MAX as u64
        || plan.output_artifact_id == plan.meta.record_id
        || !matches!(
            plan.document_batch.media_type.as_str(),
            "application/vnd.regulagraph.document-batch+protobuf"
                | "application/x-protobuf; message=regulagraph.v1.DocumentBatch"
        )
        || !matches!(
            plan.extraction_batch.media_type.as_str(),
            "application/vnd.regulagraph.extraction-batch+protobuf" | "application/x-protobuf"
        )
        || !matches!(
            plan.resolution_batch.media_type.as_str(),
            "application/x-protobuf"
                | "application/x-protobuf; message=regulagraph.v1.ResolutionBatch"
        )
        || plan.registry_view.media_type != REGISTRY_ENTITY_VIEW_MEDIA_TYPE
    {
        return Err(DeltaError::InvalidInput);
    }
    let mut seen = BTreeSet::from([
        plan.meta.record_id.as_str(),
        plan.output_artifact_id.as_str(),
    ]);
    let mut remaining = 64u64 << 20;
    for reference in [
        &plan.document_batch,
        &plan.extraction_batch,
        &plan.resolution_batch,
        &plan.registry_view,
    ] {
        if reference.schema_version != 1
            || reference.byte_size == 0
            || reference.byte_size > 16 << 20
            || reference.byte_size > remaining
            || !seen.insert(reference.artifact_id.as_str())
        {
            return Err(DeltaError::InvalidInput);
        }
        remaining -= reference.byte_size;
    }
    Ok(())
}

/// The typed payloads below must have been decoded from exactly the plan's refs.
/// This function binds their semantic contexts, not their original serialized hashes.
#[allow(clippy::too_many_arguments)]
pub fn assemble_planned_graph_delta(
    plan: &graph::GraphAssemblyPlan,
    view: &graph::RegistryEntityView,
    documents: &documents::DocumentBatch,
    extraction: &graph::ExtractionBatch,
    resolution: &graph::ResolutionBatch,
    texts: &BTreeMap<String, Vec<u8>>,
    ontology: &Ontology,
    limits: Limits,
) -> Result<graph::GraphDelta, DeltaError> {
    validate_graph_assembly_plan(plan, limits)?;
    validate_registry_entity_view(view, ontology, limits)?;
    // Bound the decision list before allocating its canonical selection set.
    validate(resolution, limits).map_err(|_| DeltaError::InvalidInput)?;
    if view.meta.record_id != plan.registry_view.artifact_id
        || view.meta.corpus_id != plan.meta.corpus_id
        || view.publication_id != plan.publication_id
        || view.publication_fence != plan.publication_fence
        || view.registry_revision != plan.registry_revision
        || plan.ontology_hash.as_ref() != Some(&ontology.content_hash())
        || extraction.source_document_batch != plan.document_batch
        || resolution.source_extraction_batch != plan.extraction_batch
        || extraction.context.snapshot_ref != plan.context.snapshot_ref
        || extraction.context.auth_scope_ref != plan.context.auth_scope_ref
        || extraction.context.config_fingerprint != plan.context.config_fingerprint
        || extraction.meta.corpus_id != plan.meta.corpus_id
    {
        return Err(DeltaError::InvalidInput);
    }
    let required: BTreeSet<_> = resolution
        .decisions
        .iter()
        .flat_map(|d| &d.assigned_canonical_ids)
        .collect();
    if !required.into_iter().eq(view.requested_ids.iter()) {
        return Err(DeltaError::RegistryMismatch);
    }
    assemble_graph_delta(
        DeltaInput {
            delta_id: &plan.output_artifact_id,
            target_sequence: plan.target_sequence,
            source_documents: documents,
            normalized_texts: texts,
            extraction,
            extraction_ref: &plan.extraction_batch,
            resolution,
            resolution_ref: &plan.resolution_batch,
            registry_ref: &plan.registry_view,
            registry_revision: plan.registry_revision,
            entities: &view.entities,
            producer: &plan.producer_manifest,
        },
        ontology,
        limits,
    )
}
