//! Executes one pinned ASSEMBLE plan using verified immutable artifacts and the
//! shared source-aware GraphDelta builder. No model call or backend publication
//! occurs here; Go authenticates committed resolution receipts and live fences.
//! Reads/decode are bounded before allocation; cancellation is checked between
//! reads and around assembly/write. A synchronous assembly call is bounded but
//! not preemptible internally. Measure queue/I/O/assembly p95/p99, RSS and cancel
//! lag under benchmark-targets.yaml; required production gates remain unmeasured.

use super::{processor::ParseBatchProcessor, service::ProcessError};
use crate::{
    adapters::storage::{ArtifactDescriptor, ArtifactStore},
    domain::wire::{self, Limits},
    knowledge_graph::{assembly::inputs, schema::Ontology},
    wire::{common, documents, graph, jobs},
};
use protobuf::{EnumOrUnknown, Message, MessageField, MessageFull};
use std::{
    collections::{BTreeMap, BTreeSet},
    sync::{
        atomic::{AtomicBool, Ordering},
        Arc,
    },
};
use tonic::Code;

pub const GRAPH_DELTA_MEDIA_TYPE: &str =
    "application/x-protobuf; message=regulagraph.v1.GraphDelta";

impl ParseBatchProcessor {
    pub fn with_assembly(mut self, ontology: Arc<Ontology>) -> Self {
        self.assembly_ontology = Some(ontology);
        self
    }

    pub(super) fn process_assembly(
        &self,
        request: jobs::ProcessBatchRequest,
        cancelled: &AtomicBool,
        progress: &dyn Fn(jobs::JobStage, u64, u64),
    ) -> Result<jobs::ProcessBatchResponse, ProcessError> {
        wire::validate(&request, Limits::default()).map_err(invalid)?;
        check_cancel(cancelled)?;
        let ontology = self
            .assembly_ontology
            .as_ref()
            .ok_or_else(|| failure("ASSEMBLE ontology runtime is not configured"))?;
        let reference = request
            .graph_assembly_plan
            .as_ref()
            .ok_or_else(|| invalid("ASSEMBLE plan required"))?;
        if reference.media_type != inputs::GRAPH_ASSEMBLY_PLAN_MEDIA_TYPE
            || request.index_build_plan.is_some()
            || request.registry.is_some()
            || !request.observations.is_empty()
        {
            return Err(invalid(
                "ASSEMBLE request has unrelated roles or incorrect plan type",
            ));
        }
        let mut remaining = Limits::default().max_bytes as u64;
        let plan: graph::GraphAssemblyPlan =
            read_message(&self.store, reference, &mut remaining, cancelled)?;
        inputs::validate_graph_assembly_plan(&plan, Limits::default())
            .map_err(|e| invalid(format!("ASSEMBLE plan: {e:?}")))?;
        let source_refs = vec![
            (*plan.document_batch).clone(),
            (*plan.extraction_batch).clone(),
            (*plan.resolution_batch).clone(),
            (*plan.registry_view).clone(),
        ];
        if plan.meta.record_id != reference.artifact_id
            || request.sources != source_refs
            || request.manifest != plan.producer_manifest
            || request.context.corpus_id != plan.meta.corpus_id
            || request.context.snapshot_ref != plan.context.snapshot_ref
            || request.context.auth_scope_ref != plan.context.auth_scope_ref
            || request.context.config_fingerprint != plan.context.config_fingerprint
            || plan.ontology_hash.as_ref() != Some(&ontology.content_hash())
        {
            return Err(invalid(
                "ASSEMBLE request roles, context or ontology differ from plan",
            ));
        }
        // Ref sizes have already been validated and bounded individually.
        if source_refs.iter().map(|r| r.byte_size).sum::<u64>() > remaining {
            return Err(invalid("ASSEMBLE aggregate input budget exceeded"));
        }
        progress(jobs::JobStage::JOB_STAGE_ASSEMBLE, 0, 4);
        let docs: documents::DocumentBatch =
            read_message(&self.store, &plan.document_batch, &mut remaining, cancelled)?;
        progress(jobs::JobStage::JOB_STAGE_ASSEMBLE, 1, 4);
        let extraction: graph::ExtractionBatch = read_message(
            &self.store,
            &plan.extraction_batch,
            &mut remaining,
            cancelled,
        )?;
        progress(jobs::JobStage::JOB_STAGE_ASSEMBLE, 2, 4);
        let resolution: graph::ResolutionBatch = read_message(
            &self.store,
            &plan.resolution_batch,
            &mut remaining,
            cancelled,
        )?;
        progress(jobs::JobStage::JOB_STAGE_ASSEMBLE, 3, 4);
        let view: graph::RegistryEntityView =
            read_message(&self.store, &plan.registry_view, &mut remaining, cancelled)?;
        let needed: BTreeSet<_> = extraction
            .mentions
            .iter()
            .map(|m| m.text_span.text_artifact_id.as_str())
            .chain(extraction.supports.iter().flat_map(|s| {
                s.evidence_spans
                    .iter()
                    .map(|span| span.text_artifact_id.as_str())
            }))
            .collect();
        let mut descriptors = BTreeMap::new();
        for text in &docs.text_artifacts {
            if descriptors
                .insert(text.meta.record_id.as_str(), text)
                .is_some()
            {
                return Err(invalid("ASSEMBLE duplicate text identity"));
            }
        }
        let mut texts = BTreeMap::new();
        for id in needed {
            let text = descriptors
                .get(id)
                .ok_or_else(|| invalid("ASSEMBLE source text missing"))?;
            if text.normalized_text_ref.media_type
                != crate::domain::text_artifact_wire::TEXT_MEDIA_TYPE
            {
                return Err(invalid("ASSEMBLE normalized text type mismatch"));
            }
            let raw = read_bytes(
                &self.store,
                &text.normalized_text_ref,
                &mut remaining,
                cancelled,
            )?;
            texts.insert(id.to_owned(), raw);
        }
        check_cancel(cancelled)?;
        let delta = inputs::assemble_planned_graph_delta(
            &plan,
            &view,
            &docs,
            &extraction,
            &resolution,
            &texts,
            ontology,
            Limits::default(),
        )
        .map_err(|e| failure(format!("ASSEMBLE source validation: {e:?}")))?;
        check_cancel(cancelled)?;
        let bytes = delta.write_to_bytes().map_err(|e| failure(e.to_string()))?;
        let output = self
            .store
            .put_bytes("graph-delta", GRAPH_DELTA_MEDIA_TYPE, 1, &bytes)
            .and_then(|d| d.to_wire_ref())
            .map_err(|e| failure(e.to_string()))?;
        check_cancel(cancelled)?;
        progress(jobs::JobStage::JOB_STAGE_ASSEMBLE, 4, 4);
        let response = jobs::ProcessBatchResponse {
            request_id: request.context.request_id.clone(),
            job_id: request.job_id.clone(),
            attempt: request.attempt,
            fence: request.lease.fence,
            status: EnumOrUnknown::new(common::CompletionStatus::COMPLETION_STATUS_SUCCEEDED),
            graph_delta: MessageField::some(output.clone()),
            checkpoint: MessageField::some(jobs::Checkpoint {
                meta: MessageField::some(common::RecordMeta {
                    schema_version: 1,
                    corpus_id: plan.meta.corpus_id.clone(),
                    record_id: format!("checkpoint:assemble:{}", reference.content_hash.sha256),
                    ..Default::default()
                }),
                job_id: request.job_id,
                stage: EnumOrUnknown::new(jobs::JobStage::JOB_STAGE_ASSEMBLE),
                completed_batch_keys: vec![output.artifact_id.clone()],
                artifact_hashes: vec![(*output.content_hash).clone()],
                manifest: plan.producer_manifest,
                fence: request.lease.fence,
                terminal_status: EnumOrUnknown::new(
                    common::CompletionStatus::COMPLETION_STATUS_SUCCEEDED,
                ),
                ..Default::default()
            }),
            ..Default::default()
        };
        wire::validate(&response, Limits::default()).map_err(invalid)?;
        Ok(response)
    }
}

fn read_message<M: MessageFull>(
    store: &ArtifactStore,
    reference: &common::ArtifactRef,
    remaining: &mut u64,
    cancelled: &AtomicBool,
) -> Result<M, ProcessError> {
    let raw = read_bytes(store, reference, remaining, cancelled)?;
    let decoded = wire::decode(&raw, &M::descriptor(), Limits::default()).map_err(invalid)?;
    decoded
        .downcast_box::<M>()
        .map(|v| *v)
        .map_err(|_| invalid("ASSEMBLE descriptor mismatch"))
}
fn read_bytes(
    store: &ArtifactStore,
    reference: &common::ArtifactRef,
    remaining: &mut u64,
    cancelled: &AtomicBool,
) -> Result<Vec<u8>, ProcessError> {
    check_cancel(cancelled)?;
    wire::validate(reference, Limits::default()).map_err(invalid)?;
    if reference.schema_version != 1
        || reference.byte_size > *remaining
        || reference.byte_size > Limits::default().max_bytes as u64
    {
        return Err(invalid("ASSEMBLE artifact schema or byte budget mismatch"));
    }
    *remaining -= reference.byte_size;
    // Logical message IDs differ from the store's physical artifact ID convention.
    // Hash, size, storage key and media are preserved and verified by ArtifactStore.
    let mut physical = reference.clone();
    physical.artifact_id = format!("artifact:assembly-input:{}", reference.content_hash.sha256);
    let descriptor =
        ArtifactDescriptor::from_wire_ref(&physical).map_err(|e| invalid(e.to_string()))?;
    let bytes = store
        .read_verified(&descriptor)
        .map_err(|e| failure(e.to_string()))?;
    check_cancel(cancelled)?;
    Ok(bytes)
}
fn check_cancel(cancelled: &AtomicBool) -> Result<(), ProcessError> {
    if cancelled.load(Ordering::Acquire) {
        Err(ProcessError::new(Code::Cancelled, "ASSEMBLE cancelled"))
    } else {
        Ok(())
    }
}
fn invalid(message: impl Into<String>) -> ProcessError {
    ProcessError::new(Code::InvalidArgument, message)
}
fn failure(message: impl Into<String>) -> ProcessError {
    ProcessError::new(Code::FailedPrecondition, message)
}
