//! PARSE/STRUCTURE/CHUNK/EXTRACT processor backed by pinned native, tokenizer,
//! and semantic-model dependencies.
//!
//! STRUCTURE emits hierarchy, while CHUNK consumes a registry-bound immutable `DocumentBatch` and
//! emits source-mapped, parent-aware chunks. The processor never fabricates legal identities or
//! publication state; every node-to-version binding must already be explicit and unambiguous.
//! EXTRACT and the INDEX input helper share source/version/page projection.
//! Measure stage throughput, p95/p99, queue delay, and peak RSS against
//! configs/benchmark-targets.yaml (REQUIRED_UNMEASURED).

use super::service::{BatchProcessor, ProcessError};
use crate::adapters::document_batches::{load_document_batch, persist_document_batch};
use crate::adapters::extraction_batches::persist_extraction_batch;
use crate::adapters::inference::ExtractionInference;
use crate::adapters::storage::{ArtifactDescriptor, ArtifactStore};
use crate::adapters::text_artifacts::{load_normalized_text, persist_text_artifact};
use crate::document::chunking::builder::{build_bound_chunks_bounded, ChunkerConfig, TokenCounter};
use crate::document::chunking::structural::{
    parse_structure, StructureIdentity, StructureParserConfig,
};
use crate::document::normalization::text::{normalize_text, TextNormalizerConfig};
use crate::document::parsing::pdf::{PdfParseRequest, PdfParser};
use crate::domain::document_batch::{
    assemble_document_batch, build_source_blob, DocumentBatchConfig, DocumentBatchParts,
};
use crate::domain::document_wire::{
    project_bound_structure_and_chunks, project_structures, DocumentWireConfig,
};
use crate::domain::text_artifact_wire::TextArtifactWireConfig;
use crate::domain::wire::{self, Limits};
use crate::knowledge_graph::extraction::extractor::{
    assemble_extraction_batch, ExtractionBatchConfig, ExtractionBatchParts,
};
use crate::knowledge_graph::schema::Ontology;
use crate::wire::{common, inference, jobs};
use protobuf::{EnumOrUnknown, MessageField};
use sha2::{Digest, Sha256};
use std::collections::{HashMap, HashSet};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use tonic::Code;

#[derive(Clone, Debug)]
pub struct ParseBatchProcessorConfig {
    pub normalizer: TextNormalizerConfig,
    pub document_batch: DocumentBatchConfig,
    pub maximum_pages_per_document: usize,
    pub maximum_batch_pages: usize,
    pub maximum_sources: usize,
    pub maximum_input_bytes: u64,
    pub structure: StructureParserConfig,
    pub chunker: ChunkerConfig,
    pub document_wire: DocumentWireConfig,
}

#[derive(Clone, Debug)]
pub struct ExtractionRuntimeConfig {
    pub model: common::ModelManifest,
    pub ontology_version: String,
    pub ontology: Arc<Ontology>,
    pub output_schema: common::ArtifactRef,
    pub batch: ExtractionBatchConfig,
    pub maximum_items_per_rpc: usize,
    pub maximum_input_bytes_per_rpc: usize,
}

struct ExtractionRuntime {
    inference: Arc<dyn ExtractionInference>,
    config: ExtractionRuntimeConfig,
}

impl Default for ParseBatchProcessorConfig {
    fn default() -> Self {
        Self {
            normalizer: TextNormalizerConfig::default(),
            document_batch: DocumentBatchConfig::default(),
            maximum_pages_per_document: 50_000,
            maximum_batch_pages: 100_000,
            maximum_sources: 256,
            maximum_input_bytes: 2 * 1024 * 1024 * 1024,
            structure: StructureParserConfig::default(),
            chunker: ChunkerConfig::default(),
            document_wire: DocumentWireConfig::default(),
        }
    }
}

pub struct ParseBatchProcessor {
    store: ArtifactStore,
    parser: Option<PdfParser>,
    tokenizer: Arc<dyn TokenCounter>,
    config: ParseBatchProcessorConfig,
    extraction: Option<ExtractionRuntime>,
}

impl ParseBatchProcessor {
    pub fn new(
        store: ArtifactStore,
        parser: Option<PdfParser>,
        tokenizer: Arc<dyn TokenCounter>,
        config: ParseBatchProcessorConfig,
    ) -> Result<Self, ProcessError> {
        if tokenizer.tokenizer_id().trim().is_empty() {
            return Err(ProcessError::new(
                Code::InvalidArgument,
                "worker tokenizer identity is required",
            ));
        }
        if config.maximum_pages_per_document == 0
            || config.maximum_batch_pages == 0
            || config.maximum_sources == 0
            || config.maximum_input_bytes == 0
        {
            return Err(ProcessError::new(
                Code::InvalidArgument,
                "parse batch limits must be positive",
            ));
        }
        if config.maximum_batch_pages > config.document_batch.maximum_records {
            return Err(ProcessError::new(
                Code::InvalidArgument,
                "maximum_batch_pages cannot exceed maximum_records",
            ));
        }
        Ok(Self {
            store,
            parser,
            tokenizer,
            config,
            extraction: None,
        })
    }

    pub fn with_extraction(
        mut self,
        inference: Arc<dyn ExtractionInference>,
        config: ExtractionRuntimeConfig,
    ) -> Result<Self, ProcessError> {
        validate_extraction_runtime(&config)?;
        self.extraction = Some(ExtractionRuntime { inference, config });
        Ok(self)
    }
}

impl BatchProcessor for ParseBatchProcessor {
    fn process(
        &self,
        request: jobs::ProcessBatchRequest,
        cancelled: &AtomicBool,
        progress: &dyn Fn(jobs::JobStage, u64, u64),
    ) -> Result<jobs::ProcessBatchResponse, ProcessError> {
        if request.stages.len() == 1
            && request.stages[0].enum_value() == Ok(jobs::JobStage::JOB_STAGE_STRUCTURE)
        {
            return self.process_structure(request, cancelled, progress);
        }
        if request.stages.len() == 1
            && request.stages[0].enum_value() == Ok(jobs::JobStage::JOB_STAGE_CHUNK)
        {
            return self.process_chunk(request, cancelled, progress);
        }
        if request.stages.len() == 1
            && request.stages[0].enum_value() == Ok(jobs::JobStage::JOB_STAGE_EXTRACT)
        {
            return self.process_extract(request, cancelled, progress);
        }
        if request.stages.len() != 1
            || request.stages[0].enum_value() != Ok(jobs::JobStage::JOB_STAGE_PARSE)
        {
            return Err(ProcessError::new(
                Code::Unimplemented,
                "worker accepts exactly one PARSE, STRUCTURE, CHUNK, or EXTRACT stage",
            ));
        }
        let context = request
            .context
            .as_ref()
            .ok_or_else(|| ProcessError::new(Code::InvalidArgument, "request context is required"))?
            .clone();
        let _requested_manifest = request.manifest.as_ref().ok_or_else(|| {
            ProcessError::new(Code::InvalidArgument, "producer manifest is required")
        })?;
        preflight_sources(&request.sources, &self.config)?;
        let request_id = context.request_id.clone();
        let corpus_id = context.corpus_id.clone();
        preflight_observations(&request.observations, &request.sources, &corpus_id)?;
        let total = request.sources.len() as u64;
        let mut sources = Vec::with_capacity(request.sources.len());
        let mut text_artifacts = Vec::with_capacity(request.sources.len());
        let mut dependencies = Vec::with_capacity(request.sources.len());
        let mut observed_hashes = HashSet::with_capacity(request.sources.len());
        let mut observed_artifact_ids = HashSet::with_capacity(request.sources.len());
        let mut accumulated_pages = 0usize;

        for (index, source) in request.sources.iter().enumerate() {
            if cancelled.load(Ordering::Acquire) {
                return Err(ProcessError::new(
                    Code::Cancelled,
                    "batch cancellation acknowledged",
                ));
            }
            let source_hash = source
                .content_hash
                .as_ref()
                .ok_or_else(|| ProcessError::new(Code::InvalidArgument, "source hash is required"))?
                .sha256
                .clone();
            if !observed_artifact_ids.insert(source.artifact_id.clone()) {
                return Err(ProcessError::new(
                    Code::InvalidArgument,
                    "source artifact IDs must be unique within a batch",
                ));
            }
            let path = self
                .store
                .verified_input_path(source)
                .map_err(|error| ProcessError::new(Code::FailedPrecondition, error.to_string()))?;
            dependencies.push(common::Dependency {
                dependency_id: source.artifact_id.clone(),
                fingerprint: MessageField::some(common::ContentHash {
                    sha256: source_hash.clone(),
                    ..Default::default()
                }),
                ..Default::default()
            });
            if !observed_hashes.insert(source_hash.clone()) {
                progress(jobs::JobStage::JOB_STAGE_PARSE, (index + 1) as u64, total);
                continue;
            }
            let source_blob_id = format!("source-blob:{source_hash}");
            let text_artifact_id = format!("text:{source_hash}");
            let parsed = self
                .parser
                .as_ref()
                .ok_or_else(|| {
                    ProcessError::new(
                        Code::FailedPrecondition,
                        "PARSE capability is unavailable because PDFium is not configured",
                    )
                })?
                .parse_file(PdfParseRequest {
                    source_id: format!("source:{source_hash}"),
                    source_blob_id: source_blob_id.clone(),
                    expected_sha256: source_hash.clone(),
                    path,
                })
                .map_err(|error| ProcessError::new(Code::InvalidArgument, error.to_string()))?;
            accumulated_pages = accumulated_pages
                .checked_add(parsed.pages.len())
                .ok_or_else(|| ProcessError::new(Code::OutOfRange, "batch page count overflow"))?;
            if accumulated_pages > self.config.maximum_batch_pages {
                return Err(ProcessError::new(
                    Code::OutOfRange,
                    format!(
                        "batch page count {accumulated_pages} exceeds limit {}",
                        self.config.maximum_batch_pages
                    ),
                ));
            }
            let normalized = normalize_text(&parsed.raw_text, &self.config.normalizer)
                .map_err(|error| ProcessError::new(Code::InvalidArgument, error.to_string()))?;
            let persisted = persist_text_artifact(
                &self.store,
                &parsed,
                &normalized,
                &self.config.normalizer,
                &TextArtifactWireConfig {
                    corpus_id: corpus_id.clone(),
                    text_artifact_id,
                    software: "regulagraph-ingestion".to_owned(),
                    build: env!("CARGO_PKG_VERSION").to_owned(),
                    maximum_pages: self.config.maximum_pages_per_document,
                },
            )
            .map_err(|error| ProcessError::new(Code::Internal, error.to_string()))?;
            sources.push(
                build_source_blob(&corpus_id, &source_blob_id, source.clone())
                    .map_err(|error| ProcessError::new(Code::InvalidArgument, error.to_string()))?,
            );
            text_artifacts.push(persisted.artifact);
            progress(jobs::JobStage::JOB_STAGE_PARSE, (index + 1) as u64, total);
        }
        if sources.is_empty() {
            return Err(ProcessError::new(
                Code::InvalidArgument,
                "batch contains no unique source content",
            ));
        }

        let identity = response_identity(
            &request_id,
            &request.job_id,
            request.attempt,
            request.lease.fence,
        );
        let producer_manifest = runtime_manifest(&text_artifacts, &dependencies, &self.config)?;
        let checkpoint_manifest = producer_manifest.clone();
        let batch = assemble_document_batch(
            DocumentBatchParts {
                batch_id: format!("document-batch:{identity}"),
                context,
                sources,
                text_artifacts,
                observations: request.observations,
                dependency_manifest: common::DependencyManifest {
                    artifact_id: format!("dependency-manifest:{identity}"),
                    dependencies,
                    producer_manifest: MessageField::some(producer_manifest),
                    ..Default::default()
                },
                ..Default::default()
            },
            &self.config.document_batch,
        )
        .map_err(|error| ProcessError::new(Code::FailedPrecondition, error.to_string()))?;
        let complete =
            batch.completeness.enum_value() == Ok(common::Completeness::COMPLETENESS_COMPLETE);
        let artifact = persist_document_batch(&self.store, &batch)
            .map_err(|error| ProcessError::new(Code::Internal, error.to_string()))?;
        let document_batch_ref = artifact.reference;
        let document_batch_hash = document_batch_ref
            .content_hash
            .as_ref()
            .cloned()
            .ok_or_else(|| ProcessError::new(Code::Internal, "document batch hash is missing"))?;
        let completion = if complete {
            common::CompletionStatus::COMPLETION_STATUS_SUCCEEDED
        } else {
            common::CompletionStatus::COMPLETION_STATUS_FAILED
        };
        Ok(jobs::ProcessBatchResponse {
            request_id,
            job_id: request.job_id.clone(),
            attempt: request.attempt,
            fence: request.lease.fence,
            checkpoint: MessageField::some(jobs::Checkpoint {
                meta: MessageField::some(common::RecordMeta {
                    schema_version: 1,
                    corpus_id,
                    record_id: format!("checkpoint:{identity}"),
                    ..Default::default()
                }),
                job_id: request.job_id.clone(),
                stage: EnumOrUnknown::new(jobs::JobStage::JOB_STAGE_PARSE),
                completed_batch_keys: vec![document_batch_ref.artifact_id.clone()],
                artifact_hashes: vec![document_batch_hash],
                manifest: MessageField::some(checkpoint_manifest),
                fence: request.lease.fence,
                terminal_status: EnumOrUnknown::new(completion),
                ..Default::default()
            }),
            document_batch: MessageField::some(document_batch_ref),
            status: EnumOrUnknown::new(completion),
            errors: if complete {
                Vec::new()
            } else {
                vec![common::OperationError {
                    code: EnumOrUnknown::new(common::ErrorCode::ERROR_CODE_NOT_IMPLEMENTED),
                    safe_message: "parse output is incomplete and requires OCR or review"
                        .to_owned(),
                    stage: "parse".to_owned(),
                    retryable: false,
                    ..Default::default()
                }]
            },
            ..Default::default()
        })
    }
}

impl ParseBatchProcessor {
    fn process_structure(
        &self,
        request: jobs::ProcessBatchRequest,
        cancelled: &AtomicBool,
        progress: &dyn Fn(jobs::JobStage, u64, u64),
    ) -> Result<jobs::ProcessBatchResponse, ProcessError> {
        let context = request
            .context
            .as_ref()
            .ok_or_else(|| ProcessError::new(Code::InvalidArgument, "request context is required"))?
            .clone();
        if request.sources.len() != 1 {
            return Err(ProcessError::new(
                Code::InvalidArgument,
                "STRUCTURE requires exactly one PARSE DocumentBatch input",
            ));
        }
        let input_ref = &request.sources[0];
        let descriptor = ArtifactDescriptor::from_wire_ref(input_ref)
            .map_err(|error| ProcessError::new(Code::InvalidArgument, error.to_string()))?;
        let mut input = load_document_batch(&self.store, &descriptor)
            .map_err(|error| ProcessError::new(Code::FailedPrecondition, error.to_string()))?;
        if input.context.corpus_id != context.corpus_id {
            return Err(ProcessError::new(
                Code::FailedPrecondition,
                "STRUCTURE input corpus differs from request corpus",
            ));
        }
        if !input.structures.is_empty() || !input.chunks.is_empty() {
            return Err(ProcessError::new(
                Code::FailedPrecondition,
                "STRUCTURE input must be a PARSE batch without structure or chunks",
            ));
        }
        if input.completeness.enum_value() != Ok(common::Completeness::COMPLETENESS_COMPLETE) {
            return Err(ProcessError::new(
				Code::FailedPrecondition,
				"STRUCTURE requires a complete PARSE DocumentBatch; incomplete input remains in review",
			));
        }
        let total = input.text_artifacts.len() as u64;
        let mut structures = Vec::new();
        let fixed_records = [
            1usize,
            input.sources.len(),
            input.text_artifacts.len(),
            input.provisions.len(),
            input.versions.len(),
            input.chunks.len(),
            input.issues.len(),
            input.editions.len(),
            input.regulations.len(),
            input.observations.len(),
            input.changes.len(),
        ]
        .into_iter()
        .try_fold(0usize, |sum, count| sum.checked_add(count))
        .ok_or_else(|| ProcessError::new(Code::OutOfRange, "STRUCTURE record count overflow"))?;
        let structure_budget = self
            .config
            .document_batch
            .maximum_records
            .checked_sub(fixed_records)
            .ok_or_else(|| {
                ProcessError::new(
                    Code::OutOfRange,
                    "STRUCTURE input already exceeds the DocumentBatch record limit",
                )
            })?;
        for (index, artifact) in input.text_artifacts.iter().enumerate() {
            if cancelled.load(Ordering::Acquire) {
                return Err(ProcessError::new(
                    Code::Cancelled,
                    "batch cancellation acknowledged",
                ));
            }
            let normalized = load_normalized_text(&self.store, artifact, &self.config.normalizer)
                .map_err(|error| {
                ProcessError::new(Code::FailedPrecondition, error.to_string())
            })?;
            let remaining = structure_budget
                .checked_sub(structures.len())
                .ok_or_else(|| {
                    ProcessError::new(
                        Code::OutOfRange,
                        "STRUCTURE output exceeds the DocumentBatch record limit",
                    )
                })?;
            if remaining == 0 {
                return Err(ProcessError::new(
                    Code::OutOfRange,
                    "STRUCTURE output exhausts the DocumentBatch record limit",
                ));
            }
            let parser_config = StructureParserConfig {
                maximum_nodes: self.config.structure.maximum_nodes.min(remaining),
                ..self.config.structure.clone()
            };
            let tree = parse_structure(
                &normalized,
                &StructureIdentity {
                    source_blob_id: artifact.source_blob_id.clone(),
                    text_artifact_id: artifact.meta.record_id.clone(),
                },
                &parser_config,
            )
            .map_err(|error| ProcessError::new(Code::InvalidArgument, error.to_string()))?;
            let mut projected = project_structures(
                &tree,
                &DocumentWireConfig {
                    corpus_id: context.corpus_id.clone(),
                    ..self.config.document_wire.clone()
                },
            )
            .map_err(|error| ProcessError::new(Code::FailedPrecondition, error.to_string()))?;
            if projected.len() > remaining {
                return Err(ProcessError::new(
                    Code::OutOfRange,
                    "STRUCTURE projection exceeds the remaining DocumentBatch record limit",
                ));
            }
            structures.append(&mut projected);
            progress(
                jobs::JobStage::JOB_STAGE_STRUCTURE,
                (index + 1) as u64,
                total,
            );
        }
        if structures.is_empty() {
            return Err(ProcessError::new(
                Code::FailedPrecondition,
                "STRUCTURE input contains no text artifacts",
            ));
        }

        let request_id = context.request_id.clone();
        let identity = response_identity(
            &request_id,
            &request.job_id,
            request.attempt,
            request.lease.fence,
        );
        let manifest = structure_runtime_manifest(input_ref, &self.config)?;
        let lookup_scope_revisions = input
            .dependency_manifest
            .as_ref()
            .map(|manifest| manifest.lookup_scope_revisions.clone())
            .unwrap_or_default();
        let output = assemble_document_batch(
            DocumentBatchParts {
                batch_id: format!("document-batch:{identity}"),
                context,
                sources: std::mem::take(&mut input.sources),
                text_artifacts: std::mem::take(&mut input.text_artifacts),
                structures,
                provisions: std::mem::take(&mut input.provisions),
                versions: std::mem::take(&mut input.versions),
                chunks: Vec::new(),
                issues: std::mem::take(&mut input.issues),
                editions: std::mem::take(&mut input.editions),
                regulations: std::mem::take(&mut input.regulations),
                observations: std::mem::take(&mut input.observations),
                changes: std::mem::take(&mut input.changes),
                dependency_manifest: common::DependencyManifest {
                    artifact_id: format!("dependency-manifest:{identity}"),
                    dependencies: vec![common::Dependency {
                        dependency_id: input_ref.artifact_id.clone(),
                        fingerprint: input_ref.content_hash.clone(),
                        ..Default::default()
                    }],
                    producer_manifest: MessageField::some(manifest.clone()),
                    lookup_scope_revisions,
                    ..Default::default()
                },
            },
            &self.config.document_batch,
        )
        .map_err(|error| ProcessError::new(Code::FailedPrecondition, error.to_string()))?;
        let artifact = persist_document_batch(&self.store, &output)
            .map_err(|error| ProcessError::new(Code::Internal, error.to_string()))?;
        let document_batch_ref = artifact.reference;
        let document_batch_hash = document_batch_ref
            .content_hash
            .as_ref()
            .cloned()
            .ok_or_else(|| ProcessError::new(Code::Internal, "document batch hash is missing"))?;
        Ok(jobs::ProcessBatchResponse {
            request_id,
            job_id: request.job_id.clone(),
            attempt: request.attempt,
            fence: request.lease.fence,
            checkpoint: MessageField::some(jobs::Checkpoint {
                meta: MessageField::some(common::RecordMeta {
                    schema_version: 1,
                    corpus_id: output.context.corpus_id.clone(),
                    record_id: format!("checkpoint:{identity}"),
                    ..Default::default()
                }),
                job_id: request.job_id,
                stage: EnumOrUnknown::new(jobs::JobStage::JOB_STAGE_STRUCTURE),
                completed_batch_keys: vec![document_batch_ref.artifact_id.clone()],
                artifact_hashes: vec![document_batch_hash],
                manifest: MessageField::some(manifest),
                fence: request.lease.fence,
                terminal_status: EnumOrUnknown::new(
                    common::CompletionStatus::COMPLETION_STATUS_SUCCEEDED,
                ),
                ..Default::default()
            }),
            document_batch: MessageField::some(document_batch_ref),
            status: EnumOrUnknown::new(common::CompletionStatus::COMPLETION_STATUS_SUCCEEDED),
            ..Default::default()
        })
    }

    fn process_chunk(
        &self,
        request: jobs::ProcessBatchRequest,
        cancelled: &AtomicBool,
        progress: &dyn Fn(jobs::JobStage, u64, u64),
    ) -> Result<jobs::ProcessBatchResponse, ProcessError> {
        let context = request
            .context
            .as_ref()
            .ok_or_else(|| ProcessError::new(Code::InvalidArgument, "request context is required"))?
            .clone();
        if request.sources.len() != 1 {
            return Err(ProcessError::new(
                Code::InvalidArgument,
                "CHUNK requires exactly one BIND DocumentBatch input",
            ));
        }
        let input_ref = &request.sources[0];
        let descriptor = ArtifactDescriptor::from_wire_ref(input_ref)
            .map_err(|error| ProcessError::new(Code::InvalidArgument, error.to_string()))?;
        let mut input = load_document_batch(&self.store, &descriptor)
            .map_err(|error| ProcessError::new(Code::FailedPrecondition, error.to_string()))?;
        if input.context.corpus_id != context.corpus_id {
            return Err(ProcessError::new(
                Code::FailedPrecondition,
                "CHUNK input corpus differs from request corpus",
            ));
        }
        if input.completeness.enum_value() != Ok(common::Completeness::COMPLETENESS_COMPLETE)
            || input.structures.is_empty()
            || input.provisions.is_empty()
            || input.versions.is_empty()
            || !input.chunks.is_empty()
        {
            return Err(ProcessError::new(
                Code::FailedPrecondition,
                "CHUNK requires a complete bound batch with structure/provision/version records and no chunks",
            ));
        }

        let text_artifacts: HashMap<&str, &crate::wire::documents::TextArtifact> = input
            .text_artifacts
            .iter()
            .map(|artifact| (artifact.meta.record_id.as_str(), artifact))
            .collect();
        if text_artifacts.len() != input.text_artifacts.len() {
            return Err(ProcessError::new(
                Code::FailedPrecondition,
                "CHUNK input contains duplicate text artifact IDs",
            ));
        }
        let nodes: HashMap<&str, &crate::wire::documents::StructureNode> = input
            .structures
            .iter()
            .map(|node| (node.meta.record_id.as_str(), node))
            .collect();
        let provisions: HashMap<&str, &crate::wire::documents::Provision> = input
            .provisions
            .iter()
            .map(|provision| (provision.meta.record_id.as_str(), provision))
            .collect();
        let regulation_ids: HashSet<&str> = input
            .regulations
            .iter()
            .map(|regulation| regulation.meta.record_id.as_str())
            .collect();
        if nodes.len() != input.structures.len()
            || provisions.len() != input.provisions.len()
            || regulation_ids.len() != input.regulations.len()
            || regulation_ids.is_empty()
        {
            return Err(ProcessError::new(
                Code::FailedPrecondition,
                "CHUNK input contains duplicate structure or provision IDs",
            ));
        }
        let mut path_cache = HashMap::with_capacity(input.structures.len());
        for node in &input.structures {
            structure_path(
                node.meta.record_id.as_str(),
                &nodes,
                &mut path_cache,
                &mut HashSet::new(),
            )?;
        }
        let mut nodes_by_binding = HashMap::with_capacity(input.structures.len());
        for node in &input.structures {
            let span_key = exact_span_key(&node.source_spans)?;
            let path = path_cache
                .get(node.meta.record_id.as_str())
                .ok_or_else(|| {
                    ProcessError::new(
                        Code::FailedPrecondition,
                        "structure path cache is incomplete",
                    )
                })?;
            let key = binding_key(&span_key, path);
            if nodes_by_binding
                .insert(key, node.meta.record_id.clone())
                .is_some()
            {
                return Err(ProcessError::new(
                    Code::FailedPrecondition,
                    "multiple structure nodes share one exact span and canonical path",
                ));
            }
        }
        let mut version_by_node = HashMap::with_capacity(input.versions.len());
        let mut provision_by_node = HashMap::with_capacity(input.versions.len());
        for version in &input.versions {
            let span_key = exact_span_key(&version.spans)?;
            let provision = provisions
                .get(version.provision_id.as_str())
                .ok_or_else(|| {
                    ProcessError::new(
                        Code::FailedPrecondition,
                        "provision version references an unknown provision",
                    )
                })?;
            if !regulation_ids.contains(provision.regulation_id.as_str()) {
                return Err(ProcessError::new(
                    Code::FailedPrecondition,
                    "provision references a regulation outside the bound batch",
                ));
            }
            let key = binding_key(&span_key, &provision.structural_path);
            let node_id = nodes_by_binding.get(&key).ok_or_else(|| {
                ProcessError::new(
                    Code::FailedPrecondition,
                    "provision version does not match one exact structure span and canonical path",
                )
            })?;
            let text_id = version.spans[0].text_artifact_id.as_str();
            let artifact = text_artifacts.get(text_id).ok_or_else(|| {
                ProcessError::new(
                    Code::FailedPrecondition,
                    "provision version references an unknown text artifact",
                )
            })?;
            if version.text_ref.as_ref() != artifact.normalized_text_ref.as_ref() {
                return Err(ProcessError::new(
                    Code::FailedPrecondition,
                    "provision version text_ref differs from its normalized text artifact",
                ));
            }
            if version_by_node
                .insert(node_id.clone(), version.meta.record_id.clone())
                .is_some()
            {
                return Err(ProcessError::new(
                    Code::FailedPrecondition,
                    "multiple provision versions bind one structure node",
                ));
            }
            provision_by_node.insert(node_id.clone(), provision.meta.record_id.clone());
        }
        if version_by_node.len() != input.structures.len() {
            return Err(ProcessError::new(
                Code::FailedPrecondition,
                "every structure node must have exactly one provision version before tokenization",
            ));
        }
        let mut regulation_by_text = HashMap::new();
        for node in &input.structures {
            let provision_id = provision_by_node.get(&node.meta.record_id).ok_or_else(|| {
                ProcessError::new(Code::FailedPrecondition, "structure has no bound provision")
            })?;
            let provision = provisions.get(provision_id.as_str()).ok_or_else(|| {
                ProcessError::new(Code::FailedPrecondition, "bound provision is missing")
            })?;
            let expected_parent = match node.parent_id.as_ref() {
                Some(parent) => Some(
                    provision_by_node
                        .get(parent)
                        .ok_or_else(|| {
                            ProcessError::new(
                                Code::FailedPrecondition,
                                "parent structure has no provision",
                            )
                        })?
                        .as_str(),
                ),
                None => None,
            };
            if provision.parent_provision_id.as_deref() != expected_parent {
                return Err(ProcessError::new(
                    Code::FailedPrecondition,
                    "provision hierarchy differs from the structure hierarchy",
                ));
            }
            let text_id = node.source_spans[0].text_artifact_id.as_str();
            if let Some(previous) =
                regulation_by_text.insert(text_id, provision.regulation_id.as_str())
            {
                if previous != provision.regulation_id.as_str() {
                    return Err(ProcessError::new(
                        Code::FailedPrecondition,
                        "one structured document is bound to multiple regulations",
                    ));
                }
            }
        }

        let fixed_records = document_record_count_without_chunks(&input)?;
        let chunk_budget = self
            .config
            .document_batch
            .maximum_records
            .checked_sub(fixed_records)
            .ok_or_else(|| {
                ProcessError::new(
                    Code::OutOfRange,
                    "CHUNK input already exceeds the DocumentBatch record limit",
                )
            })?;
        let total = input.text_artifacts.len() as u64;
        let mut chunks = Vec::new();
        for (index, artifact) in input.text_artifacts.iter().enumerate() {
            if cancelled.load(Ordering::Acquire) {
                return Err(ProcessError::new(
                    Code::Cancelled,
                    "batch cancellation acknowledged",
                ));
            }
            let normalized = load_normalized_text(&self.store, artifact, &self.config.normalizer)
                .map_err(|error| {
                ProcessError::new(Code::FailedPrecondition, error.to_string())
            })?;
            let tree = parse_structure(
                &normalized,
                &StructureIdentity {
                    source_blob_id: artifact.source_blob_id.clone(),
                    text_artifact_id: artifact.meta.record_id.clone(),
                },
                &self.config.structure,
            )
            .map_err(|error| ProcessError::new(Code::FailedPrecondition, error.to_string()))?;
            let projected_structures = project_structures(
                &tree,
                &DocumentWireConfig {
                    corpus_id: context.corpus_id.clone(),
                    ..self.config.document_wire.clone()
                },
            )
            .map_err(|error| ProcessError::new(Code::FailedPrecondition, error.to_string()))?;
            let bound_structures: Vec<_> = input
                .structures
                .iter()
                .filter(|node| {
                    node.source_spans
                        .first()
                        .is_some_and(|span| span.text_artifact_id == artifact.meta.record_id)
                })
                .cloned()
                .collect();
            if !same_structures(&projected_structures, &bound_structures) {
                return Err(ProcessError::new(
                    Code::FailedPrecondition,
                    "bound structure differs from deterministic reconstruction",
                ));
            }
            let bindings: HashMap<String, String> = tree
                .nodes
                .iter()
                .map(|node| {
                    version_by_node
                        .get(&node.id)
                        .cloned()
                        .map(|version| (node.id.clone(), version))
                        .ok_or_else(|| {
                            ProcessError::new(
                                Code::FailedPrecondition,
                                "reconstructed structure is missing a provision version binding",
                            )
                        })
                })
                .collect::<Result<_, _>>()?;
            let remaining = chunk_budget.checked_sub(chunks.len()).ok_or_else(|| {
                ProcessError::new(Code::OutOfRange, "CHUNK output exceeds the record limit")
            })?;
            if remaining == 0 {
                return Err(ProcessError::new(
                    Code::OutOfRange,
                    "CHUNK output exhausts the record limit",
                ));
            }
            let chunk_batch = build_bound_chunks_bounded(
                &normalized,
                &tree,
                &bindings,
                &self.config.chunker,
                remaining,
                self.tokenizer.as_ref(),
            )
            .map_err(|error| ProcessError::new(Code::FailedPrecondition, error.to_string()))?;
            let mut projected = project_bound_structure_and_chunks(
                &tree,
                &chunk_batch,
                &DocumentWireConfig {
                    corpus_id: context.corpus_id.clone(),
                    ..self.config.document_wire.clone()
                },
            )
            .map_err(|error| ProcessError::new(Code::FailedPrecondition, error.to_string()))?;
            if !same_structures(&projected.structures, &bound_structures) {
                return Err(ProcessError::new(
                    Code::FailedPrecondition,
                    "CHUNK projection changed bound structures",
                ));
            }
            if projected.chunks.len() > remaining {
                return Err(ProcessError::new(
                    Code::OutOfRange,
                    "CHUNK projection exceeds the remaining record limit",
                ));
            }
            chunks.append(&mut projected.chunks);
            progress(jobs::JobStage::JOB_STAGE_CHUNK, (index + 1) as u64, total);
        }
        if chunks.is_empty() {
            return Err(ProcessError::new(
                Code::FailedPrecondition,
                "CHUNK produced no searchable text",
            ));
        }

        let request_id = context.request_id.clone();
        let identity = response_identity(
            &request_id,
            &request.job_id,
            request.attempt,
            request.lease.fence,
        );
        let manifest = chunk_runtime_manifest(input_ref, &self.config, self.tokenizer.as_ref())?;
        let dependencies = derived_dependencies(&input, input_ref)?;
        let lookup_scope_revisions = input
            .dependency_manifest
            .as_ref()
            .map(|manifest| manifest.lookup_scope_revisions.clone())
            .unwrap_or_default();
        let output = assemble_document_batch(
            DocumentBatchParts {
                batch_id: format!("document-batch:{identity}"),
                context,
                sources: std::mem::take(&mut input.sources),
                text_artifacts: std::mem::take(&mut input.text_artifacts),
                structures: std::mem::take(&mut input.structures),
                provisions: std::mem::take(&mut input.provisions),
                versions: std::mem::take(&mut input.versions),
                chunks,
                issues: std::mem::take(&mut input.issues),
                editions: std::mem::take(&mut input.editions),
                regulations: std::mem::take(&mut input.regulations),
                observations: std::mem::take(&mut input.observations),
                changes: std::mem::take(&mut input.changes),
                dependency_manifest: common::DependencyManifest {
                    artifact_id: format!("dependency-manifest:{identity}"),
                    dependencies,
                    producer_manifest: MessageField::some(manifest.clone()),
                    lookup_scope_revisions,
                    ..Default::default()
                },
            },
            &self.config.document_batch,
        )
        .map_err(|error| ProcessError::new(Code::FailedPrecondition, error.to_string()))?;
        let persisted = persist_document_batch(&self.store, &output)
            .map_err(|error| ProcessError::new(Code::Internal, error.to_string()))?;
        let document_batch_ref = persisted.reference;
        let document_batch_hash = document_batch_ref
            .content_hash
            .as_ref()
            .cloned()
            .ok_or_else(|| ProcessError::new(Code::Internal, "document batch hash is missing"))?;
        Ok(jobs::ProcessBatchResponse {
            request_id,
            job_id: request.job_id.clone(),
            attempt: request.attempt,
            fence: request.lease.fence,
            checkpoint: MessageField::some(jobs::Checkpoint {
                meta: MessageField::some(common::RecordMeta {
                    schema_version: 1,
                    corpus_id: output.context.corpus_id.clone(),
                    record_id: format!("checkpoint:{identity}"),
                    ..Default::default()
                }),
                job_id: request.job_id,
                stage: EnumOrUnknown::new(jobs::JobStage::JOB_STAGE_CHUNK),
                completed_batch_keys: vec![document_batch_ref.artifact_id.clone()],
                artifact_hashes: vec![document_batch_hash],
                manifest: MessageField::some(manifest),
                fence: request.lease.fence,
                terminal_status: EnumOrUnknown::new(
                    common::CompletionStatus::COMPLETION_STATUS_SUCCEEDED,
                ),
                ..Default::default()
            }),
            document_batch: MessageField::some(document_batch_ref),
            status: EnumOrUnknown::new(common::CompletionStatus::COMPLETION_STATUS_SUCCEEDED),
            ..Default::default()
        })
    }

    fn process_extract(
        &self,
        request: jobs::ProcessBatchRequest,
        cancelled: &AtomicBool,
        progress: &dyn Fn(jobs::JobStage, u64, u64),
    ) -> Result<jobs::ProcessBatchResponse, ProcessError> {
        let runtime = self.extraction.as_ref().ok_or_else(|| {
            ProcessError::new(
                Code::FailedPrecondition,
                "EXTRACT runtime is not configured",
            )
        })?;
        if !request.manifest.as_ref().is_some_and(|manifest| {
            manifest
                .input_hashes
                .iter()
                .any(|hash| hash.sha256 == runtime.config.ontology.sha256())
        }) {
            return Err(ProcessError::new(
                Code::FailedPrecondition,
                "EXTRACT request did not pin ontology bytes",
            ));
        }
        let context = request
            .context
            .as_ref()
            .ok_or_else(|| ProcessError::new(Code::InvalidArgument, "request context is required"))?
            .clone();
        if request.sources.len() != 1 {
            return Err(ProcessError::new(
                Code::InvalidArgument,
                "EXTRACT requires exactly one CHUNK DocumentBatch input",
            ));
        }
        let input_ref = request.sources[0].clone();
        let descriptor = ArtifactDescriptor::from_wire_ref(&input_ref)
            .map_err(|error| ProcessError::new(Code::InvalidArgument, error.to_string()))?;
        let input = load_document_batch(&self.store, &descriptor)
            .map_err(|error| ProcessError::new(Code::FailedPrecondition, error.to_string()))?;
        if input.context.corpus_id != context.corpus_id {
            return Err(ProcessError::new(
                Code::FailedPrecondition,
                "EXTRACT input corpus differs from request corpus",
            ));
        }
        if input.completeness.enum_value() != Ok(common::Completeness::COMPLETENESS_COMPLETE)
            || input.chunks.is_empty()
        {
            return Err(ProcessError::new(
                Code::FailedPrecondition,
                "EXTRACT requires a complete CHUNK DocumentBatch with searchable chunks",
            ));
        }

        let items = build_extraction_items(&self.store, &input, &self.config, cancelled)?;
        let item_batches = partition_extraction_items(
            items,
            runtime.config.maximum_items_per_rpc,
            runtime.config.maximum_input_bytes_per_rpc,
        )?;
        let total_items: u64 = item_batches.iter().map(|batch| batch.len() as u64).sum();
        progress(jobs::JobStage::JOB_STAGE_EXTRACT, 0, total_items);

        let mut mentions = Vec::new();
        let mut assertions = Vec::new();
        let mut supports = Vec::new();
        let mut issues = Vec::new();
        let mut operation_errors = Vec::new();
        let mut accepted = 0u64;
        let mut completed = 0u64;
        let mut producer: Option<common::ProducerManifest> = None;
        let mut token_usage = common::TokenUsage::default();
        let mut durations = Vec::new();

        for (batch_index, items) in item_batches.into_iter().enumerate() {
            if cancelled.load(Ordering::Acquire) {
                return Err(ProcessError::new(
                    Code::Cancelled,
                    "batch cancellation acknowledged",
                ));
            }
            let operation_key =
                extraction_operation_key(&input_ref, &runtime.config, batch_index, &items)?;
            let response = runtime
                .inference
                .extract_batch(
                    inference::ExtractBatchRequest {
                        batch: MessageField::some(inference::SemanticBatchContext {
                            context: MessageField::some(context.clone()),
                            model: MessageField::some(runtime.config.model.clone()),
                            operation_key,
                            ontology_version: runtime.config.ontology_version.clone(),
                            output_schema: MessageField::some(runtime.config.output_schema.clone()),
                            ..Default::default()
                        }),
                        items,
                        ..Default::default()
                    },
                    cancelled,
                )
                .map_err(|error| ProcessError::new(error.code(), error.to_string()))?;
            let response_producer = response.producer_manifest.as_ref().ok_or_else(|| {
                ProcessError::new(
                    Code::FailedPrecondition,
                    "semantic response producer manifest is missing",
                )
            })?;
            if !response_producer
                .input_hashes
                .iter()
                .any(|hash| hash.sha256 == runtime.config.ontology.sha256())
            {
                return Err(ProcessError::new(
                    Code::FailedPrecondition,
                    "semantic producer did not pin ontology bytes",
                ));
            }
            if producer
                .as_ref()
                .is_some_and(|expected| expected != response_producer)
            {
                return Err(ProcessError::new(
                    Code::FailedPrecondition,
                    "semantic producer changed within one EXTRACT artifact",
                ));
            }
            producer.get_or_insert_with(|| response_producer.clone());
            let usage = response.usage.as_ref().ok_or_else(|| {
                ProcessError::new(Code::FailedPrecondition, "semantic token usage is missing")
            })?;
            if token_usage.tokenizer_id.is_empty() {
                token_usage.tokenizer_id = usage.tokenizer_id.clone();
            } else if token_usage.tokenizer_id != usage.tokenizer_id {
                return Err(ProcessError::new(
                    Code::FailedPrecondition,
                    "semantic tokenizer identity changed within one EXTRACT artifact",
                ));
            }
            token_usage.input_tokens = token_usage
                .input_tokens
                .checked_add(usage.input_tokens)
                .ok_or_else(|| ProcessError::new(Code::OutOfRange, "input token count overflow"))?;
            token_usage.output_tokens = token_usage
                .output_tokens
                .checked_add(usage.output_tokens)
                .ok_or_else(|| {
                    ProcessError::new(Code::OutOfRange, "output token count overflow")
                })?;
            durations.extend(response.durations);

            for result in response.results {
                let item_id = result.item_id.clone();
                match result.result {
                    Some(inference::extract_item_result::Result::Proposal(proposal))
                        if !proposal.issues.iter().any(|issue| {
                            issue.severity.enum_value() == Ok(common::Severity::SEVERITY_ERROR)
                        }) =>
                    {
                        accepted += 1;
                        mentions.extend(proposal.mentions);
                        assertions.extend(proposal.assertions);
                        supports.extend(proposal.supports);
                        issues.extend(proposal.issues);
                    }
                    Some(inference::extract_item_result::Result::Proposal(mut proposal)) => {
                        for issue in &mut proposal.issues {
                            issue.record_id = item_id.clone();
                            issue.evidence_refs.clear();
                        }
                        issues.extend(proposal.issues);
                        operation_errors.push(common::OperationError {
                            code: EnumOrUnknown::new(
                                common::ErrorCode::ERROR_CODE_INVALID_ARGUMENT,
                            ),
                            safe_message: "semantic proposal failed extraction validation"
                                .to_owned(),
                            stage: "extract".to_owned(),
                            retryable: false,
                            item_id: Some(item_id),
                            ..Default::default()
                        });
                    }
                    Some(inference::extract_item_result::Result::Error(mut error)) => {
                        error.item_id = Some(item_id.clone());
                        issues.push(extraction_error_issue(&item_id, &error));
                        operation_errors.push(error);
                    }
                    None => {
                        return Err(ProcessError::new(
                            Code::FailedPrecondition,
                            "semantic response item has no result",
                        ));
                    }
                }
                completed += 1;
                progress(jobs::JobStage::JOB_STAGE_EXTRACT, completed, total_items);
            }
        }

        let producer = producer.ok_or_else(|| {
            ProcessError::new(
                Code::FailedPrecondition,
                "semantic extraction produced no batch manifest",
            )
        })?;
        let prompt_hash = runtime
            .config
            .model
            .prompt_hash
            .as_ref()
            .cloned()
            .ok_or_else(|| {
                ProcessError::new(Code::InvalidArgument, "EXTRACT prompt hash is required")
            })?;
        let rejected = total_items.checked_sub(accepted).ok_or_else(|| {
            ProcessError::new(Code::Internal, "semantic item accounting underflow")
        })?;
        let request_id = context.request_id.clone();
        let identity = response_identity(
            &request_id,
            &request.job_id,
            request.attempt,
            request.lease.fence,
        );
        let output = assemble_extraction_batch(
            ExtractionBatchParts {
                batch_id: format!("extraction-batch:{identity}"),
                context,
                source_document_batch: input_ref.clone(),
                mentions,
                assertions,
                supports,
                issues,
                dependencies: common::DependencyManifest {
                    artifact_id: format!("dependency-manifest:{identity}"),
                    dependencies: vec![common::Dependency {
                        dependency_id: input_ref.artifact_id.clone(),
                        fingerprint: input_ref.content_hash.clone(),
                        ..Default::default()
                    }],
                    producer_manifest: MessageField::some(producer.clone()),
                    ..Default::default()
                },
                ontology_version: runtime.config.ontology_version.clone(),
                model_manifest: runtime.config.model.clone(),
                prompt_hash,
                item_counts: common::Counts {
                    expected: total_items,
                    accepted,
                    rejected,
                    ..Default::default()
                },
                token_usage,
                durations,
            },
            &input,
            &runtime.config.batch,
        )
        .map_err(|error| ProcessError::new(Code::FailedPrecondition, error.to_string()))?;
        runtime
            .config
            .ontology
            .validate_extraction_batch(&output)
            .map_err(|error| ProcessError::new(Code::FailedPrecondition, error))?;
        let persisted = persist_extraction_batch(&self.store, &output, &input)
            .map_err(|error| ProcessError::new(Code::Internal, error.to_string()))?;
        let output_ref = persisted.reference;
        let output_hash =
            output_ref.content_hash.as_ref().cloned().ok_or_else(|| {
                ProcessError::new(Code::Internal, "extraction batch hash is missing")
            })?;
        let completion = if rejected == 0 {
            common::CompletionStatus::COMPLETION_STATUS_SUCCEEDED
        } else {
            common::CompletionStatus::COMPLETION_STATUS_FAILED
        };
        Ok(jobs::ProcessBatchResponse {
            request_id,
            job_id: request.job_id.clone(),
            attempt: request.attempt,
            fence: request.lease.fence,
            checkpoint: MessageField::some(jobs::Checkpoint {
                meta: MessageField::some(common::RecordMeta {
                    schema_version: 1,
                    corpus_id: output.meta.corpus_id.clone(),
                    record_id: format!("checkpoint:{identity}"),
                    ..Default::default()
                }),
                job_id: request.job_id,
                stage: EnumOrUnknown::new(jobs::JobStage::JOB_STAGE_EXTRACT),
                completed_batch_keys: vec![output_ref.artifact_id.clone()],
                artifact_hashes: vec![output_hash],
                manifest: MessageField::some(producer),
                fence: request.lease.fence,
                terminal_status: EnumOrUnknown::new(completion),
                ..Default::default()
            }),
            status: EnumOrUnknown::new(completion),
            errors: operation_errors,
            extraction_batch: MessageField::some(output_ref),
            ..Default::default()
        })
    }
}

fn validate_extraction_runtime(config: &ExtractionRuntimeConfig) -> Result<(), ProcessError> {
    wire::validate(&config.model, Limits::default())
        .map_err(|error| ProcessError::new(Code::InvalidArgument, error))?;
    wire::validate(&config.output_schema, Limits::default())
        .map_err(|error| ProcessError::new(Code::InvalidArgument, error))?;
    if config.model.task.enum_value() != Ok(common::ModelTask::MODEL_TASK_EXTRACT)
        || config.model.prompt_hash.is_none()
    {
        return Err(ProcessError::new(
            Code::InvalidArgument,
            "EXTRACT runtime requires an EXTRACT model with prompt hash",
        ));
    }
    if config.ontology_version != config.ontology.version() {
        return Err(ProcessError::new(
            Code::InvalidArgument,
            "EXTRACT ontology version differs from pinned source",
        ));
    }
    if config.output_schema.media_type != "application/schema+json" {
        return Err(ProcessError::new(
            Code::InvalidArgument,
            "EXTRACT output schema must use application/schema+json",
        ));
    }
    if config.maximum_items_per_rpc == 0
        || config.maximum_input_bytes_per_rpc == 0
        || config.batch.maximum_records == 0
        || config.batch.maximum_reference_edges == 0
    {
        return Err(ProcessError::new(
            Code::InvalidArgument,
            "EXTRACT runtime limits must be positive",
        ));
    }
    Ok(())
}

fn build_extraction_items(
    store: &ArtifactStore,
    input: &crate::wire::documents::DocumentBatch,
    processor_config: &ParseBatchProcessorConfig,
    cancelled: &AtomicBool,
) -> Result<Vec<inference::TextItem>, ProcessError> {
    let provenance_index = crate::domain::chunk_provenance::ChunkProvenanceIndex::new(input)
        .map_err(|error| {
            ProcessError::new(
                Code::FailedPrecondition,
                format!("EXTRACT source closure: {error:?}"),
            )
        })?;
    let mut normalized_texts = HashMap::with_capacity(input.text_artifacts.len());
    for artifact in &input.text_artifacts {
        if cancelled.load(Ordering::Acquire) {
            return Err(ProcessError::new(
                Code::Cancelled,
                "batch cancellation acknowledged",
            ));
        }
        let normalized = load_normalized_text(store, artifact, &processor_config.normalizer)
            .map_err(|error| ProcessError::new(Code::FailedPrecondition, error.to_string()))?;
        normalized_texts.insert(artifact.meta.record_id.clone(), normalized.text);
    }
    let mut items = Vec::with_capacity(input.chunks.len());
    for chunk in &input.chunks {
        let span = chunk.text_span.as_ref().ok_or_else(|| {
            ProcessError::new(Code::FailedPrecondition, "chunk text span is missing")
        })?;
        let normalized = normalized_texts
            .get(&span.text_artifact_id)
            .ok_or_else(|| {
                ProcessError::new(
                    Code::FailedPrecondition,
                    "chunk references unavailable normalized text",
                )
            })?;
        wire::check_utf8_span(normalized.as_bytes(), span.start_byte, span.end_byte)
            .map_err(|error| ProcessError::new(Code::FailedPrecondition, error))?;
        if span.start_byte == span.end_byte {
            return Err(ProcessError::new(
                Code::FailedPrecondition,
                "EXTRACT refuses an empty chunk",
            ));
        }
        let text = normalized[span.start_byte as usize..span.end_byte as usize].to_owned();
        let provenance = provenance_index
            .project(&chunk.meta.record_id)
            .map_err(|error| {
                ProcessError::new(
                    Code::FailedPrecondition,
                    format!("EXTRACT source evidence: {error:?}"),
                )
            })?;
        items.push(inference::TextItem {
            item_id: chunk.meta.record_id.clone(),
            text,
            provenance: MessageField::some(provenance),
            ..Default::default()
        });
    }
    Ok(items)
}

fn partition_extraction_items(
    items: Vec<inference::TextItem>,
    maximum_items: usize,
    maximum_bytes: usize,
) -> Result<Vec<Vec<inference::TextItem>>, ProcessError> {
    let mut batches = Vec::new();
    let mut current = Vec::new();
    let mut current_bytes = 0usize;
    for item in items {
        let item_bytes = item.text.len();
        if item_bytes > maximum_bytes {
            return Err(ProcessError::new(
                Code::ResourceExhausted,
                "one EXTRACT item exceeds the semantic RPC byte limit",
            ));
        }
        if !current.is_empty()
            && (current.len() == maximum_items
                || current_bytes
                    .checked_add(item_bytes)
                    .is_none_or(|size| size > maximum_bytes))
        {
            batches.push(std::mem::take(&mut current));
            current_bytes = 0;
        }
        current_bytes = current_bytes
            .checked_add(item_bytes)
            .ok_or_else(|| ProcessError::new(Code::OutOfRange, "EXTRACT byte count overflow"))?;
        current.push(item);
    }
    if !current.is_empty() {
        batches.push(current);
    }
    if batches.is_empty() {
        return Err(ProcessError::new(
            Code::FailedPrecondition,
            "EXTRACT has no semantic items",
        ));
    }
    Ok(batches)
}

fn extraction_operation_key(
    input: &common::ArtifactRef,
    config: &ExtractionRuntimeConfig,
    batch_index: usize,
    items: &[inference::TextItem],
) -> Result<String, ProcessError> {
    let mut hasher = Sha256::new();
    hasher.update(b"regulagraph-semantic-extract-v1\0");
    for hash in [
        input.content_hash.as_ref(),
        config.model.weights_hash.as_ref(),
        config.model.tokenizer_hash.as_ref(),
        config.model.prompt_hash.as_ref(),
        config.output_schema.content_hash.as_ref(),
    ] {
        hasher.update(
            hash.ok_or_else(|| {
                ProcessError::new(Code::InvalidArgument, "EXTRACT hash identity is missing")
            })?
            .sha256
            .as_bytes(),
        );
        hasher.update([0]);
    }
    hasher.update(config.ontology_version.as_bytes());
    hasher.update(config.ontology.sha256().as_bytes());
    hasher.update(batch_index.to_le_bytes());
    for item in items {
        hasher.update(item.item_id.as_bytes());
        hasher.update([0]);
        hasher.update(Sha256::digest(item.text.as_bytes()));
    }
    Ok(format!("semantic-extract:{:x}", hasher.finalize()))
}

fn extraction_error_issue(
    item_id: &str,
    error: &common::OperationError,
) -> common::ValidationIssue {
    common::ValidationIssue {
        code: format!("SEMANTIC_{:?}", error.code.enum_value().unwrap_or_default()),
        severity: EnumOrUnknown::new(common::Severity::SEVERITY_ERROR),
        record_id: item_id.to_owned(),
        field_path: "semantic.result".to_owned(),
        disposition: if error.retryable {
            "retry".to_owned()
        } else {
            "quarantine".to_owned()
        },
        ..Default::default()
    }
}

fn exact_span_key(spans: &[common::TextSpan]) -> Result<String, ProcessError> {
    if spans.is_empty() {
        return Err(ProcessError::new(
            Code::FailedPrecondition,
            "registry-bound record has no source spans",
        ));
    }
    let text_id = spans[0].text_artifact_id.as_str();
    if text_id.is_empty()
        || spans
            .iter()
            .any(|span| span.text_artifact_id != text_id || span.start_byte >= span.end_byte)
    {
        return Err(ProcessError::new(
            Code::FailedPrecondition,
            "registry-bound spans must be nonempty and belong to one text artifact",
        ));
    }
    let mut key = String::new();
    for span in spans {
        key.push_str(&format!(
            "{}:{}:{}:{};",
            span.text_artifact_id.len(),
            span.text_artifact_id,
            span.start_byte,
            span.end_byte
        ));
    }
    Ok(key)
}

fn binding_key(span_key: &str, path: &[String]) -> String {
    let mut key = String::with_capacity(span_key.len() + path.len() * 24);
    key.push_str(span_key);
    key.push('|');
    for component in path {
        let normalized = normalize_identity_text(component);
        key.push_str(&normalized.len().to_string());
        key.push(':');
        key.push_str(&normalized);
        key.push(';');
    }
    key
}

fn same_structures(
    left: &[crate::wire::documents::StructureNode],
    right: &[crate::wire::documents::StructureNode],
) -> bool {
    if left.len() != right.len() {
        return false;
    }
    let by_id: HashMap<&str, &crate::wire::documents::StructureNode> = right
        .iter()
        .map(|node| (node.meta.record_id.as_str(), node))
        .collect();
    by_id.len() == right.len()
        && left
            .iter()
            .all(|node| by_id.get(node.meta.record_id.as_str()) == Some(&node))
}

fn structure_path(
    node_id: &str,
    nodes: &HashMap<&str, &crate::wire::documents::StructureNode>,
    cache: &mut HashMap<String, Vec<String>>,
    visiting: &mut HashSet<String>,
) -> Result<Vec<String>, ProcessError> {
    if let Some(path) = cache.get(node_id) {
        return Ok(path.clone());
    }
    if visiting.len() >= 256 {
        return Err(ProcessError::new(
            Code::OutOfRange,
            "structure path exceeds the CHUNK depth limit",
        ));
    }
    let node = nodes.get(node_id).ok_or_else(|| {
        ProcessError::new(
            Code::FailedPrecondition,
            "structure path references an unknown node",
        )
    })?;
    if !visiting.insert(node_id.to_owned()) {
        return Err(ProcessError::new(
            Code::FailedPrecondition,
            "structure path contains a cycle",
        ));
    }
    let mut path = if let Some(parent) = node.parent_id.as_deref() {
        structure_path(parent, nodes, cache, visiting)?
    } else {
        Vec::new()
    };
    if path.len() >= 256 {
        return Err(ProcessError::new(
            Code::OutOfRange,
            "structure path exceeds the CHUNK depth limit",
        ));
    }
    let label = node.label.split_whitespace().collect::<Vec<_>>().join(" ");
    path.push(format!("{}:{label}", node.kind.value()));
    visiting.remove(node_id);
    cache.insert(node_id.to_owned(), path.clone());
    Ok(path)
}

fn normalize_identity_text(value: &str) -> String {
    value
        .split_whitespace()
        .collect::<Vec<_>>()
        .join(" ")
        .chars()
        .flat_map(char::to_lowercase)
        .collect()
}

fn document_record_count_without_chunks(
    batch: &crate::wire::documents::DocumentBatch,
) -> Result<usize, ProcessError> {
    [
        1usize,
        batch.sources.len(),
        batch.text_artifacts.len(),
        batch.structures.len(),
        batch.provisions.len(),
        batch.versions.len(),
        batch.issues.len(),
        batch.editions.len(),
        batch.regulations.len(),
        batch.observations.len(),
        batch.changes.len(),
    ]
    .into_iter()
    .try_fold(0usize, |sum, count| sum.checked_add(count))
    .ok_or_else(|| ProcessError::new(Code::OutOfRange, "CHUNK record count overflow"))
}

fn derived_dependencies(
    input: &crate::wire::documents::DocumentBatch,
    input_ref: &common::ArtifactRef,
) -> Result<Vec<common::Dependency>, ProcessError> {
    let input_hash = input_ref.content_hash.as_ref().ok_or_else(|| {
        ProcessError::new(Code::InvalidArgument, "derived input hash is required")
    })?;
    let mut by_id: HashMap<String, common::Dependency> = HashMap::new();
    if let Some(manifest) = input.dependency_manifest.as_ref() {
        for dependency in &manifest.dependencies {
            let fingerprint = dependency.fingerprint.as_ref().ok_or_else(|| {
                ProcessError::new(
                    Code::FailedPrecondition,
                    "inherited dependency has no fingerprint",
                )
            })?;
            if let Some(previous) = by_id.get(&dependency.dependency_id) {
                if previous.fingerprint.as_ref() != Some(fingerprint) {
                    return Err(ProcessError::new(
                        Code::FailedPrecondition,
                        "inherited dependency fingerprints conflict",
                    ));
                }
            } else {
                by_id.insert(dependency.dependency_id.clone(), dependency.clone());
            }
        }
    }
    let input_dependency = common::Dependency {
        dependency_id: input_ref.artifact_id.clone(),
        fingerprint: MessageField::some(input_hash.clone()),
        ..Default::default()
    };
    if let Some(previous) = by_id.get(&input_dependency.dependency_id) {
        if previous.fingerprint.as_ref() != input_dependency.fingerprint.as_ref() {
            return Err(ProcessError::new(
                Code::FailedPrecondition,
                "input artifact conflicts with an inherited dependency",
            ));
        }
    } else {
        by_id.insert(input_dependency.dependency_id.clone(), input_dependency);
    }
    let mut dependencies: Vec<_> = by_id.into_values().collect();
    dependencies.sort_by(|left, right| left.dependency_id.cmp(&right.dependency_id));
    Ok(dependencies)
}

fn chunk_runtime_manifest(
    input: &common::ArtifactRef,
    config: &ParseBatchProcessorConfig,
    tokenizer: &dyn TokenCounter,
) -> Result<common::ProducerManifest, ProcessError> {
    let input_hash =
        input.content_hash.as_ref().cloned().ok_or_else(|| {
            ProcessError::new(Code::InvalidArgument, "CHUNK input hash is required")
        })?;
    let mut hasher = Sha256::new();
    hasher.update(b"regulagraph-chunk-config-v2\0");
    hasher.update(config.chunker.maximum_chunk_bytes.to_le_bytes());
    hasher.update(config.chunker.maximum_chunk_tokens.to_le_bytes());
    hasher.update(config.chunker.minimum_split_bytes.to_le_bytes());
    hasher.update(config.chunker.overlap_bytes.to_le_bytes());
    hasher.update(config.chunker.maximum_chunks.to_le_bytes());
    hasher.update(config.chunker.maximum_parent_depth.to_le_bytes());
    hasher.update(config.document_wire.maximum_records.to_le_bytes());
    hasher.update(tokenizer.tokenizer_id().as_bytes());
    Ok(common::ProducerManifest {
        software: "regulagraph-ingestion".to_owned(),
        build: env!("CARGO_PKG_VERSION").to_owned(),
        schema_version: 1,
        parser_version: Some("structure-v1".to_owned()),
        chunker_version: Some("chunker-v2".to_owned()),
        config_hash: MessageField::some(common::ContentHash {
            sha256: format!("{:x}", hasher.finalize()),
            ..Default::default()
        }),
        input_hashes: vec![input_hash],
        ..Default::default()
    })
}

fn structure_runtime_manifest(
    input: &common::ArtifactRef,
    config: &ParseBatchProcessorConfig,
) -> Result<common::ProducerManifest, ProcessError> {
    let input_hash = input.content_hash.as_ref().cloned().ok_or_else(|| {
        ProcessError::new(Code::InvalidArgument, "STRUCTURE input hash is required")
    })?;
    let mut hasher = Sha256::new();
    hasher.update(b"regulagraph-structure-config-v1\0");
    hasher.update(config.structure.maximum_nodes.to_le_bytes());
    hasher.update(config.structure.maximum_heading_line_bytes.to_le_bytes());
    hasher.update([config.structure.recognize_list_items as u8]);
    hasher.update(config.document_wire.maximum_records.to_le_bytes());
    Ok(common::ProducerManifest {
        software: "regulagraph-ingestion".to_owned(),
        build: env!("CARGO_PKG_VERSION").to_owned(),
        schema_version: 1,
        parser_version: Some("structure-v1".to_owned()),
        config_hash: MessageField::some(common::ContentHash {
            sha256: format!("{:x}", hasher.finalize()),
            ..Default::default()
        }),
        input_hashes: vec![input_hash],
        ..Default::default()
    })
}

fn preflight_sources(
    sources: &[common::ArtifactRef],
    config: &ParseBatchProcessorConfig,
) -> Result<(), ProcessError> {
    if sources.len() > config.maximum_sources {
        return Err(ProcessError::new(
            Code::OutOfRange,
            format!(
                "source count {} exceeds batch limit {}",
                sources.len(),
                config.maximum_sources
            ),
        ));
    }
    if sources
        .iter()
        .any(|source| source.media_type != "application/pdf")
    {
        return Err(ProcessError::new(
            Code::InvalidArgument,
            "PARSE worker sources must use media_type application/pdf",
        ));
    }
    let total_bytes = sources.iter().try_fold(0u64, |total, source| {
        total
            .checked_add(source.byte_size)
            .ok_or_else(|| ProcessError::new(Code::OutOfRange, "batch input byte count overflow"))
    })?;
    if total_bytes > config.maximum_input_bytes {
        return Err(ProcessError::new(
            Code::OutOfRange,
            format!(
                "batch input bytes {total_bytes} exceed limit {}",
                config.maximum_input_bytes
            ),
        ));
    }
    Ok(())
}

fn preflight_observations(
    observations: &[crate::wire::documents::SourceObservation],
    sources: &[common::ArtifactRef],
    corpus_id: &str,
) -> Result<(), ProcessError> {
    let mut source_blob_ids = HashSet::with_capacity(sources.len());
    let mut source_artifact_ids = HashSet::with_capacity(sources.len());
    for source in sources {
        if !source_artifact_ids.insert(source.artifact_id.as_str()) {
            return Err(ProcessError::new(
                Code::InvalidArgument,
                "source artifact IDs must be unique within a batch",
            ));
        }
        let source_hash = source
            .content_hash
            .as_ref()
            .ok_or_else(|| ProcessError::new(Code::InvalidArgument, "source hash is required"))?;
        source_blob_ids.insert(format!("source-blob:{}", source_hash.sha256));
    }

    let mut observation_ids = HashSet::with_capacity(observations.len());
    for observation in observations {
        if !observation_ids.insert(observation.meta.record_id.as_str()) {
            return Err(ProcessError::new(
                Code::InvalidArgument,
                "source observation IDs must be unique within a batch",
            ));
        }
        if observation.meta.corpus_id != corpus_id {
            return Err(ProcessError::new(
                Code::InvalidArgument,
                "source observation corpus differs from request corpus",
            ));
        }
        if observation
            .source_blob_id
            .as_ref()
            .is_some_and(|source_id| !source_blob_ids.contains(source_id))
        {
            return Err(ProcessError::new(
                Code::InvalidArgument,
                "source observation references bytes outside the request",
            ));
        }
    }
    Ok(())
}

fn runtime_manifest(
    text_artifacts: &[crate::wire::documents::TextArtifact],
    dependencies: &[common::Dependency],
    config: &ParseBatchProcessorConfig,
) -> Result<common::ProducerManifest, ProcessError> {
    let parser_manifest = text_artifacts
        .first()
        .and_then(|artifact| artifact.parser_manifest.as_ref())
        .ok_or_else(|| ProcessError::new(Code::Internal, "parser manifest is unavailable"))?;
    let parser_config = parser_manifest
        .config_hash
        .as_ref()
        .ok_or_else(|| ProcessError::new(Code::Internal, "parser config hash is unavailable"))?;
    let pdfium_library = parser_manifest
        .input_hashes
        .get(1)
        .ok_or_else(|| ProcessError::new(Code::Internal, "PDFium library hash is unavailable"))?;
    let mut hasher = Sha256::new();
    hasher.update(b"regulagraph-parse-batch-config-v1\0");
    hasher.update(
        parser_manifest
            .parser_version
            .as_deref()
            .unwrap_or_default()
            .as_bytes(),
    );
    hasher.update([0]);
    hasher.update(parser_config.sha256.as_bytes());
    hasher.update(pdfium_library.sha256.as_bytes());
    hasher.update(config.normalizer.maximum_input_bytes.to_le_bytes());
    hasher.update(config.normalizer.maximum_mapping_spans.to_le_bytes());
    hasher.update([config.normalizer.collapse_horizontal_whitespace as u8]);
    hasher.update([config.normalizer.expand_unicode_ligatures as u8]);
    hasher.update([config.normalizer.remove_soft_hyphen as u8]);
    hasher.update(config.document_batch.maximum_records.to_le_bytes());
    hasher.update(config.document_batch.maximum_reference_edges.to_le_bytes());
    hasher.update(config.maximum_pages_per_document.to_le_bytes());
    hasher.update(config.maximum_batch_pages.to_le_bytes());
    hasher.update(config.maximum_sources.to_le_bytes());
    hasher.update(config.maximum_input_bytes.to_le_bytes());
    let mut input_hashes: Vec<_> = dependencies
        .iter()
        .filter_map(|dependency| dependency.fingerprint.as_ref().cloned())
        .collect();
    input_hashes.push(pdfium_library.clone());
    Ok(common::ProducerManifest {
        software: "regulagraph-ingestion".to_owned(),
        build: env!("CARGO_PKG_VERSION").to_owned(),
        schema_version: 1,
        parser_version: parser_manifest.parser_version.clone(),
        config_hash: MessageField::some(common::ContentHash {
            sha256: format!("{:x}", hasher.finalize()),
            ..Default::default()
        }),
        input_hashes,
        ..Default::default()
    })
}

fn response_identity(request_id: &str, job_id: &str, attempt: u32, fence: u64) -> String {
    let mut hasher = Sha256::new();
    hasher.update(request_id.as_bytes());
    hasher.update([0]);
    hasher.update(job_id.as_bytes());
    hasher.update(attempt.to_le_bytes());
    hasher.update(fence.to_le_bytes());
    format!("{:x}", hasher.finalize())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::adapters::storage::ArtifactStoreConfig;
    use crate::document::normalization::text::normalize_text;
    use crate::document::parsing::pdf::{
        NormalizedBoundingBox, PdfDocumentStatus, PdfDocumentText, PdfPageResult, PdfPageStatus,
        PdfParserManifest, PdfTextBlock,
    };
    use crate::domain::document_batch::build_source_blob;
    use crate::domain::text_artifact_wire::TextArtifactWireConfig;
    use crate::wire::documents;
    use protobuf::well_known_types::timestamp::Timestamp;
    use std::fs;
    use std::path::PathBuf;
    use std::sync::atomic::{AtomicU64, AtomicUsize};

    static TEST_SEQUENCE: AtomicU64 = AtomicU64::new(1);

    struct Words;

    impl TokenCounter for Words {
        fn tokenizer_id(&self) -> &str {
            "test:chunk-worker-words-v1"
        }

        fn count_tokens(&self, text: &str) -> Result<u32, String> {
            u32::try_from(text.split_whitespace().count()).map_err(|error| error.to_string())
        }
    }

    struct EmptyExtraction {
        calls: Arc<AtomicUsize>,
        reject_first_call: bool,
        invalid_typed_output: bool,
    }

    impl ExtractionInference for EmptyExtraction {
        fn extract_batch(
            &self,
            request: inference::ExtractBatchRequest,
            _cancelled: &AtomicBool,
        ) -> Result<inference::ExtractBatchResponse, crate::adapters::inference::SemanticClientError>
        {
            let call_index = self.calls.fetch_add(1, Ordering::AcqRel);
            let model = request.batch.model.as_ref().unwrap().clone();
            let producer = common::ProducerManifest {
                software: "semantic-fixture".to_owned(),
                build: "v1".to_owned(),
                schema_version: 1,
                models: vec![model.clone()],
                prompt_hashes: vec![model.prompt_hash.as_ref().unwrap().clone()],
                config_hash: MessageField::some(hash('f')),
                input_hashes: vec![test_ontology().content_hash()],
                ..Default::default()
            };
            let corpus_id = request.batch.context.corpus_id.clone();
            let ontology_version = request.batch.ontology_version.clone();
            Ok(inference::ExtractBatchResponse {
                request_id: request.batch.context.request_id.clone(),
                results: request
                    .items
                    .into_iter()
                    .enumerate()
                    .map(|(item_index, item)| inference::ExtractItemResult {
                        item_id: item.item_id.clone(),
                        result: Some(
                            if self.reject_first_call && call_index == 0 && item_index == 0 {
                                inference::extract_item_result::Result::Error(
                                    common::OperationError {
                                        code: EnumOrUnknown::new(
                                            common::ErrorCode::ERROR_CODE_INVALID_ARGUMENT,
                                        ),
                                        safe_message: "fixture rejection".to_owned(),
                                        stage: "extract".to_owned(),
                                        retryable: false,
                                        item_id: Some(item.item_id),
                                        ..Default::default()
                                    },
                                )
                            } else if self.invalid_typed_output {
                                inference::extract_item_result::Result::Proposal(
                                    invalid_ontology_proposal(
                                        &item,
                                        &producer,
                                        &corpus_id,
                                        &ontology_version,
                                    ),
                                )
                            } else {
                                inference::extract_item_result::Result::Proposal(
                                    inference::ExtractionProposal::default(),
                                )
                            },
                        ),
                        ..Default::default()
                    })
                    .collect(),
                model: MessageField::some(model.clone()),
                usage: MessageField::some(common::TokenUsage {
                    input_tokens: 10,
                    output_tokens: 1,
                    tokenizer_id: "tokenizer:semantic-fixture".to_owned(),
                    ..Default::default()
                }),
                durations: vec![common::StageDuration {
                    stage: "semantic_extract".to_owned(),
                    duration_ns: 10,
                    ..Default::default()
                }],
                producer_manifest: MessageField::some(producer),
                ..Default::default()
            })
        }
    }

    fn invalid_ontology_proposal(
        item: &inference::TextItem,
        producer: &common::ProducerManifest,
        corpus_id: &str,
        ontology_version: &str,
    ) -> inference::ExtractionProposal {
        use crate::wire::graph;
        let provenance = item.provenance.as_ref().unwrap();
        let span = provenance.spans[0].clone();
        let source_refs = provenance.sources.clone();
        let identity = format!("{:x}", Sha256::digest(item.item_id.as_bytes()));
        let subject_id = format!("mention:{identity}:subject");
        let object_id = format!("mention:{identity}:object");
        let assertion_id = format!("assertion:{identity}");
        let make_mention = |id: &str| graph::Mention {
            meta: MessageField::some(meta(corpus_id, id)),
            text_span: MessageField::some(span.clone()),
            source_refs: source_refs.clone(),
            surface_form: item.text.clone(),
            candidate_type: "legal_concept".to_owned(),
            extraction_manifest: MessageField::some(producer.clone()),
            ..Default::default()
        };
        inference::ExtractionProposal {
            mentions: vec![make_mention(&subject_id), make_mention(&object_id)],
            assertions: vec![graph::RelationAssertion {
                meta: MessageField::some(meta(corpus_id, &assertion_id)),
                subject_id,
                object_id,
                predicate_id: "unknown_predicate".to_owned(),
                temporal_scope: MessageField::some(common::TemporalScope {
                    mode: EnumOrUnknown::new(common::TemporalMode::TEMPORAL_MODE_CURRENT),
                    unresolved_policy: EnumOrUnknown::new(
                        common::UnresolvedPolicy::UNRESOLVED_POLICY_REQUIRE_REVIEW,
                    ),
                    ..Default::default()
                }),
                origin: EnumOrUnknown::new(graph::AssertionOrigin::ASSERTION_ORIGIN_EXPLICIT),
                ontology_version: ontology_version.to_owned(),
                ..Default::default()
            }],
            supports: vec![graph::SupportRecord {
                meta: MessageField::some(meta(corpus_id, &format!("support:{identity}"))),
                assertion_id,
                evidence_spans: vec![span],
                source_refs,
                extraction_manifest: MessageField::some(producer.clone()),
                independent_source_group: provenance.sources[0].source_blob_id.clone(),
                review_state: EnumOrUnknown::new(common::ReviewState::REVIEW_STATE_UNREVIEWED),
                ..Default::default()
            }],
            ..Default::default()
        }
    }

    struct TestDir(PathBuf);

    impl TestDir {
        fn new() -> Self {
            let sequence = TEST_SEQUENCE.fetch_add(1, Ordering::Relaxed);
            let path = std::env::temp_dir().join(format!(
                "regulagraph-chunk-worker-test-{}-{sequence}",
                std::process::id()
            ));
            fs::create_dir_all(&path).expect("test root");
            Self(path)
        }
    }

    impl Drop for TestDir {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.0);
        }
    }

    #[test]
    fn chunk_stage_requires_exact_bindings_and_persists_parent_aware_output() {
        let root = TestDir::new();
        let store = ArtifactStore::open(
            &root.0,
            ArtifactStoreConfig {
                maximum_artifact_bytes: 16 * 1024 * 1024,
                sync_data: false,
            },
        )
        .unwrap();
        let source_bytes = b"%PDF-registry-bound-fixture";
        let source_ref = store
            .put_bytes("source-pdf", "application/pdf", 1, source_bytes)
            .unwrap()
            .to_wire_ref()
            .unwrap();
        let source_id = "source-blob:fixture";
        let source = build_source_blob("corpus:fixture", source_id, source_ref).unwrap();
        let raw_text = "BAB I\r\nPasal 1\r\n(1) Setiap orang wajib memelihara data yang akurat dan lengkap.\r\n";
        let parsed = PdfDocumentText {
            source_id: "source:fixture".to_owned(),
            source_blob_id: source_id.to_owned(),
            raw_text: raw_text.to_owned(),
            blocks: vec![PdfTextBlock {
                page_number: 1,
                start_byte: 0,
                end_byte: raw_text.len() as u64,
                bounding_box: NormalizedBoundingBox {
                    x0: 0.0,
                    y0: 0.0,
                    x1: 1.0,
                    y1: 1.0,
                },
            }],
            pages: vec![PdfPageResult {
                page_number: 1,
                start_byte: 0,
                end_byte: raw_text.len() as u64,
                image_objects: 0,
                elapsed_microseconds: 1,
                status: PdfPageStatus::Text,
            }],
            status: PdfDocumentStatus::Complete,
            manifest: PdfParserManifest {
                schema_version: 1,
                engine: "pdfium",
                binding: "pdfium-render",
                binding_version: "fixture",
                api_feature: "fixture",
                declared_core_version: "fixture".to_owned(),
                library_sha256: "1".repeat(64),
                config_sha256: "2".repeat(64),
                input_sha256: format!("{:x}", Sha256::digest(source_bytes)),
            },
        };
        let normalizer = TextNormalizerConfig::default();
        let normalized = normalize_text(raw_text, &normalizer).unwrap();
        let persisted_text = persist_text_artifact(
            &store,
            &parsed,
            &normalized,
            &normalizer,
            &TextArtifactWireConfig {
                corpus_id: "corpus:fixture".to_owned(),
                text_artifact_id: "text:fixture".to_owned(),
                ..Default::default()
            },
        )
        .unwrap();
        let tree = parse_structure(
            &normalized,
            &StructureIdentity {
                source_blob_id: source_id.to_owned(),
                text_artifact_id: "text:fixture".to_owned(),
            },
            &StructureParserConfig::default(),
        )
        .unwrap();
        let structures = project_structures(
            &tree,
            &DocumentWireConfig {
                corpus_id: "corpus:fixture".to_owned(),
                ..Default::default()
            },
        )
        .unwrap();
        let provision_ids: HashMap<_, _> = structures
            .iter()
            .enumerate()
            .map(|(index, node)| {
                (
                    node.meta.record_id.clone(),
                    format!("provision:{}", index + 1),
                )
            })
            .collect();
        let structure_index: HashMap<_, _> = structures
            .iter()
            .map(|node| (node.meta.record_id.as_str(), node))
            .collect();
        let mut fixture_paths = HashMap::new();
        for node in &structures {
            structure_path(
                node.meta.record_id.as_str(),
                &structure_index,
                &mut fixture_paths,
                &mut HashSet::new(),
            )
            .unwrap();
        }
        let provisions: Vec<_> = structures
            .iter()
            .map(|node| documents::Provision {
                meta: MessageField::some(meta(
                    "corpus:fixture",
                    &provision_ids[&node.meta.record_id],
                )),
                regulation_id: "regulation:fixture".to_owned(),
                structural_path: fixture_paths[&node.meta.record_id].clone(),
                parent_provision_id: node
                    .parent_id
                    .as_ref()
                    .map(|parent| provision_ids[parent].clone()),
                ..Default::default()
            })
            .collect();
        let versions: Vec<_> = structures
            .iter()
            .enumerate()
            .map(|(index, node)| documents::ProvisionVersion {
                meta: MessageField::some(meta("corpus:fixture", &format!("version:{}", index + 1))),
                provision_id: provision_ids[&node.meta.record_id].clone(),
                text_ref: persisted_text.artifact.normalized_text_ref.clone(),
                spans: node.source_spans.clone(),
                legal_interval: MessageField::some(common::LegalInterval {
                    start: MessageField::some(unknown_date()),
                    end: MessageField::some(unknown_date()),
                    ..Default::default()
                }),
                legal_status: EnumOrUnknown::new(documents::LegalStatus::LEGAL_STATUS_UNKNOWN),
                review_state: EnumOrUnknown::new(common::ReviewState::REVIEW_STATE_UNREVIEWED),
                ..Default::default()
            })
            .collect();
        let context = request_context("bind:fixture");
        let bound = assemble_document_batch(
            DocumentBatchParts {
                batch_id: "document-batch:bound-fixture".to_owned(),
                context: context.clone(),
                sources: vec![source],
                text_artifacts: vec![persisted_text.artifact],
                structures,
                provisions,
                versions,
                regulations: vec![documents::Regulation {
                    meta: MessageField::some(meta("corpus:fixture", "regulation:fixture")),
                    kind: "peraturan".to_owned(),
                    issuer_id: "organization:fixture".to_owned(),
                    jurisdiction: "ID".to_owned(),
                    official_number: "1".to_owned(),
                    year: 2026,
                    title: "Peraturan Uji".to_owned(),
                    identity_status: EnumOrUnknown::new(
                        documents::IdentityStatus::IDENTITY_STATUS_UNRESOLVED,
                    ),
                    ..Default::default()
                }],
                dependency_manifest: common::DependencyManifest {
                    artifact_id: "dependency-manifest:bound-fixture".to_owned(),
                    dependencies: vec![common::Dependency {
                        dependency_id: "organization:fixture".to_owned(),
                        fingerprint: MessageField::some(hash('a')),
                        ..Default::default()
                    }],
                    producer_manifest: MessageField::some(producer()),
                    ..Default::default()
                },
                ..Default::default()
            },
            &DocumentBatchConfig::default(),
        )
        .unwrap();
        let bound_ref = persist_document_batch(&store, &bound).unwrap().reference;
        let processor = ParseBatchProcessor::new(
            store,
            None,
            Arc::new(Words),
            ParseBatchProcessorConfig {
                chunker: ChunkerConfig {
                    maximum_chunk_bytes: 96,
                    maximum_chunk_tokens: 4,
                    minimum_split_bytes: 16,
                    overlap_bytes: 8,
                    maximum_chunks: 100,
                    maximum_parent_depth: 16,
                },
                document_wire: DocumentWireConfig {
                    corpus_id: "corpus:fixture".to_owned(),
                    ..Default::default()
                },
                ..Default::default()
            },
        )
        .unwrap();
        let response = processor
            .process(
                jobs::ProcessBatchRequest {
                    context: MessageField::some(request_context("chunk:fixture")),
                    job_id: "job:fixture".to_owned(),
                    attempt: 1,
                    lease: MessageField::some(jobs::Lease {
                        owner_id: "worker:fixture".to_owned(),
                        fence: 7,
                        expires_at: MessageField::some(Timestamp {
                            seconds: 2_000_000_100,
                            ..Default::default()
                        }),
                        ..Default::default()
                    }),
                    sources: vec![bound_ref],
                    manifest: MessageField::some(producer()),
                    stages: vec![EnumOrUnknown::new(jobs::JobStage::JOB_STAGE_CHUNK)],
                    ..Default::default()
                },
                &AtomicBool::new(false),
                &|_, _, _| {},
            )
            .unwrap();
        assert_eq!(
            response.status.enum_value(),
            Ok(common::CompletionStatus::COMPLETION_STATUS_SUCCEEDED)
        );
        let descriptor =
            ArtifactDescriptor::from_wire_ref(response.document_batch.as_ref().unwrap()).unwrap();
        let output = load_document_batch(&processor.store, &descriptor).unwrap();
        assert!(!output.chunks.is_empty());
        assert!(output.chunks.iter().all(|chunk| {
            chunk.provision_version_refs.len() == 1
                && chunk.structure_node_refs.len() == 1
                && chunk.token_counts.len() == 1
                && chunk.token_counts[0].input_tokens <= 4
        }));
        assert!(output
            .dependency_manifest
            .dependencies
            .iter()
            .any(|dependency| dependency.dependency_id == "organization:fixture"));

        let index_inputs = crate::indexing::loading::VerifiedIndexInputs::new(&output)
            .expect("complete CHUNK batch passes index preflight");
        let selected = vec![output.chunks[0].meta.record_id.clone()];
        let rendered = index_inputs
            .load_selected(
                &processor.store,
                &normalizer,
                &selected,
                1024,
                &AtomicBool::new(false),
            )
            .expect("index input reads verified text and source-bound chunk");
        assert_eq!(rendered.len(), 1);
        assert_eq!(rendered[0].chunk_id, selected[0]);
        let primary_span = output.chunks[0].text_span.as_ref().unwrap();
        let primary =
            &normalized.text[primary_span.start_byte as usize..primary_span.end_byte as usize];
        assert!(rendered[0].text.ends_with(primary));
        assert!(output.chunks.len() > 1);
        let reversed = vec![
            output.chunks.last().unwrap().meta.record_id.clone(),
            selected[0].clone(),
        ];
        let selected_in_order = index_inputs
            .load_selected(
                &processor.store,
                &normalizer,
                &reversed,
                1024,
                &AtomicBool::new(false),
            )
            .unwrap();
        assert_eq!(selected_in_order[0].chunk_id, reversed[0]);
        assert_eq!(selected_in_order[1].chunk_id, reversed[1]);
        assert!(index_inputs
            .load_selected(
                &processor.store,
                &normalizer,
                &[selected[0].clone(), selected[0].clone()],
                1024,
                &AtomicBool::new(false),
            )
            .is_err());
        assert!(index_inputs
            .load_selected(
                &processor.store,
                &normalizer,
                &["chunk:missing".to_owned()],
                1024,
                &AtomicBool::new(false),
            )
            .is_err());
        assert!(matches!(
            index_inputs.load_selected(
                &processor.store,
                &normalizer,
                &selected,
                1024,
                &AtomicBool::new(true),
            ),
            Err(crate::indexing::loading::LoadIndexInputsError::Cancelled)
        ));
        let mut wrong_normalizer = normalizer.clone();
        wrong_normalizer.remove_soft_hyphen = !wrong_normalizer.remove_soft_hyphen;
        assert!(index_inputs
            .load_selected(
                &processor.store,
                &wrong_normalizer,
                &selected,
                1024,
                &AtomicBool::new(false),
            )
            .is_err());

        let index_generation = crate::wire::evidence::IndexGeneration {
            meta: MessageField::some(meta("corpus:fixture", "generation:fixture")),
            dense_manifest: MessageField::some(common::ModelManifest {
                model_id: "model:embed-fixture".to_owned(),
                version: "v1".to_owned(),
                weights_hash: MessageField::some(hash('8')),
                tokenizer_hash: MessageField::some(hash('9')),
                task: EnumOrUnknown::new(common::ModelTask::MODEL_TASK_EMBED),
                dimensions: Some(2),
                max_tokens: 4096,
                precision: "fp32".to_owned(),
                backend: "fixture".to_owned(),
                ..Default::default()
            }),
            filter_format: EnumOrUnknown::new(
                crate::wire::evidence::IndexFilterFormat::INDEX_FILTER_FORMAT_PAIRED_PROVISION_V1,
            ),
            embedding_input_policy: crate::indexing::inputs::RENDER_POLICY_VERSION.to_owned(),
            ..Default::default()
        };
        let prepared = index_inputs
            .prepare_selected(
                &processor.store,
                &normalizer,
                &selected,
                1024,
                &index_generation,
                &AtomicBool::new(false),
            )
            .expect("index native item keeps source/version evidence");
        assert_eq!(prepared.len(), 1);
        assert_eq!(prepared[0].native_item.item_id, selected[0]);
        assert_eq!(prepared[0].reuse_key.len(), 64);
        assert_eq!(prepared[0].input_sha256, rendered[0].sha256);
        let source = &prepared[0].native_item.provenance.sources[0];
        assert_eq!(source.source_blob_id, source_id);
        assert_eq!(source.regulation_id, "regulation:fixture");
        assert_eq!(
            source.provision_version_id,
            output.chunks[0].provision_version_refs[0]
        );
        let extraction_items = build_extraction_items(
            &processor.store,
            &output,
            &processor.config,
            &AtomicBool::new(false),
        )
        .expect("EXTRACT projects the same source evidence");
        assert_eq!(
            extraction_items[0].provenance,
            prepared[0].native_item.provenance
        );
        let mut uncovered = output.clone();
        let chunk_end = uncovered.chunks[0].text_span.end_byte;
        let version_id = uncovered.chunks[0].provision_version_refs[0].clone();
        let version = uncovered
            .versions
            .iter_mut()
            .find(|version| version.meta.record_id == version_id)
            .unwrap();
        let version_span = version
            .spans
            .iter_mut()
            .find(|span| span.text_artifact_id == uncovered.chunks[0].text_span.text_artifact_id)
            .unwrap();
        assert!(chunk_end < version_span.end_byte);
        version_span.start_byte = chunk_end;
        let uncovered_index =
            crate::domain::chunk_provenance::ChunkProvenanceIndex::new(&uncovered)
                .expect("batch reference closure alone does not prove version coverage");
        assert!(matches!(
            uncovered_index.project(&selected[0]),
            Err(crate::domain::chunk_provenance::ChunkProvenanceError::UncoveredVersion)
        ));
        let mut wrong_text_ref = output.clone();
        let artifact_id = wrong_text_ref.chunks[0].text_span.text_artifact_id.clone();
        let raw_ref = wrong_text_ref
            .text_artifacts
            .iter()
            .find(|artifact| artifact.meta.record_id == artifact_id)
            .unwrap()
            .raw_text_ref
            .clone();
        wrong_text_ref
            .versions
            .iter_mut()
            .find(|version| version.meta.record_id == version_id)
            .unwrap()
            .text_ref = raw_ref;
        let wrong_text_index =
            crate::domain::chunk_provenance::ChunkProvenanceIndex::new(&wrong_text_ref)
                .expect("known artifact reference is not a normalized-text binding");
        assert!(matches!(
            wrong_text_index.project(&selected[0]),
            Err(crate::domain::chunk_provenance::ChunkProvenanceError::VersionTextMismatch)
        ));
        let mut mixed_version = output.clone();
        let mut other_artifact = mixed_version.text_artifacts[0].clone();
        other_artifact.meta.as_mut().unwrap().record_id = "text:other-source".to_owned();
        for page in &mut other_artifact.page_results {
            for span in &mut page.spans {
                span.text_artifact_id = other_artifact.meta.record_id.clone();
            }
        }
        mixed_version.text_artifacts.push(other_artifact);
        let version = mixed_version
            .versions
            .iter_mut()
            .find(|version| version.meta.record_id == version_id)
            .unwrap();
        let mut foreign_span = version.spans[0].clone();
        foreign_span.text_artifact_id = "text:other-source".to_owned();
        version.spans.insert(0, foreign_span);
        let mixed_index =
            crate::domain::chunk_provenance::ChunkProvenanceIndex::new(&mixed_version)
                .expect("reference closure alone permits a mixed-artifact version span");
        assert!(matches!(
            mixed_index.project(&selected[0]),
            Err(crate::domain::chunk_provenance::ChunkProvenanceError::ForeignVersionSpan)
        ));
        let mut wrong_structure = output.clone();
        let structure_id = wrong_structure.chunks[0].structure_node_refs[0].clone();
        let owner = wrong_structure
            .structures
            .iter_mut()
            .find(|node| node.meta.record_id == structure_id)
            .unwrap();
        owner.source_spans[0].start_byte = chunk_end;
        let wrong_structure_index =
            crate::domain::chunk_provenance::ChunkProvenanceIndex::new(&wrong_structure)
                .expect("reference closure does not prove owning-node coverage");
        assert!(matches!(
            wrong_structure_index.project(&selected[0]),
            Err(crate::domain::chunk_provenance::ChunkProvenanceError::ForeignStructure)
        ));
        let mut wrong_page = output.clone();
        let owner = wrong_page
            .structures
            .iter_mut()
            .find(|node| node.meta.record_id == structure_id)
            .unwrap();
        owner.page_locators.push(common::PageLocator {
            source_blob_id: source_id.to_owned(),
            page_number: 9999,
            ..Default::default()
        });
        let wrong_page_index =
            crate::domain::chunk_provenance::ChunkProvenanceIndex::new(&wrong_page)
                .expect("reference closure does not prove locator page membership");
        assert!(matches!(
            wrong_page_index.project(&selected[0]),
            Err(crate::domain::chunk_provenance::ChunkProvenanceError::ForeignLocator)
        ));
        let mut old_generation = index_generation.clone();
        old_generation.embedding_input_policy = "parent-labels-v1".to_owned();
        assert!(matches!(
            index_inputs.prepare_selected(
                &processor.store,
                &normalizer,
                &selected,
                1024,
                &old_generation,
                &AtomicBool::new(true),
            ),
            Err(crate::indexing::loading::LoadIndexInputsError::Cancelled)
        ));
        assert!(matches!(
            index_inputs.prepare_selected(
                &processor.store,
                &wrong_normalizer,
                &selected,
                1024,
                &old_generation,
                &AtomicBool::new(false),
            ),
            Err(crate::indexing::loading::LoadIndexInputsError::Reuse(
                crate::indexing::reuse::ReuseKeyError::InvalidGeneration
            ))
        ));
        let mut wrong_model = index_generation.clone();
        wrong_model.dense_manifest.as_mut().unwrap().task =
            EnumOrUnknown::new(common::ModelTask::MODEL_TASK_RERANK);
        assert!(matches!(
            index_inputs.prepare_selected(
                &processor.store,
                &wrong_normalizer,
                &selected,
                1024,
                &wrong_model,
                &AtomicBool::new(false),
            ),
            Err(crate::indexing::loading::LoadIndexInputsError::Reuse(
                crate::indexing::reuse::ReuseKeyError::InvalidModel
            ))
        ));

        let calls = Arc::new(AtomicUsize::new(0));
        let prompt_hash = hash('d');
        let model = common::ModelManifest {
            model_id: "model:extract-fixture".to_owned(),
            version: "v1".to_owned(),
            weights_hash: MessageField::some(hash('8')),
            tokenizer_hash: MessageField::some(hash('9')),
            task: EnumOrUnknown::new(common::ModelTask::MODEL_TASK_EXTRACT),
            max_tokens: 4096,
            precision: "provider".to_owned(),
            backend: "fixture".to_owned(),
            prompt_hash: MessageField::some(prompt_hash),
            ..Default::default()
        };
        let extraction_config = ExtractionRuntimeConfig {
            model,
            ontology_version: "id-regulation-ontology-v1".to_owned(),
            ontology: Arc::new(test_ontology()),
            output_schema: common::ArtifactRef {
                artifact_id: "artifact:schema:extract".to_owned(),
                content_hash: MessageField::some(hash('e')),
                storage_key: format!("sha256/ee/ee/{}.bin", "e".repeat(64)),
                media_type: "application/schema+json".to_owned(),
                byte_size: 1,
                schema_version: 1,
                ..Default::default()
            },
            batch: ExtractionBatchConfig::default(),
            maximum_items_per_rpc: 1,
            maximum_input_bytes_per_rpc: 1024,
        };
        let processor = processor
            .with_extraction(
                Arc::new(EmptyExtraction {
                    calls: Arc::clone(&calls),
                    reject_first_call: false,
                    invalid_typed_output: false,
                }),
                extraction_config.clone(),
            )
            .unwrap();
        let extract_response = processor
            .process(
                jobs::ProcessBatchRequest {
                    context: MessageField::some(request_context("extract:fixture")),
                    job_id: "job:extract-fixture".to_owned(),
                    attempt: 1,
                    lease: MessageField::some(jobs::Lease {
                        owner_id: "worker:fixture".to_owned(),
                        fence: 8,
                        expires_at: MessageField::some(Timestamp {
                            seconds: 2_000_000_100,
                            ..Default::default()
                        }),
                        ..Default::default()
                    }),
                    sources: vec![response.document_batch.as_ref().unwrap().clone()],
                    manifest: MessageField::some(producer()),
                    stages: vec![EnumOrUnknown::new(jobs::JobStage::JOB_STAGE_EXTRACT)],
                    ..Default::default()
                },
                &AtomicBool::new(false),
                &|_, _, _| {},
            )
            .unwrap();
        assert_eq!(
            extract_response.status.enum_value(),
            Ok(common::CompletionStatus::COMPLETION_STATUS_SUCCEEDED)
        );
        assert_eq!(calls.load(Ordering::Acquire), output.chunks.len());
        let extract_descriptor =
            ArtifactDescriptor::from_wire_ref(extract_response.extraction_batch.as_ref().unwrap())
                .unwrap();
        let extracted = crate::adapters::extraction_batches::load_extraction_batch(
            &processor.store,
            &extract_descriptor,
            &output,
        )
        .unwrap();
        assert_eq!(extracted.item_counts.expected, output.chunks.len() as u64);
        assert_eq!(extracted.item_counts.accepted, output.chunks.len() as u64);
        assert!(extracted.mentions.is_empty());

        let rejected_calls = Arc::new(AtomicUsize::new(0));
        let processor = processor
            .with_extraction(
                Arc::new(EmptyExtraction {
                    calls: Arc::clone(&rejected_calls),
                    reject_first_call: true,
                    invalid_typed_output: false,
                }),
                extraction_config.clone(),
            )
            .unwrap();
        let rejected_response = processor
            .process(
                jobs::ProcessBatchRequest {
                    context: MessageField::some(request_context("extract:partial-fixture")),
                    job_id: "job:extract-partial-fixture".to_owned(),
                    attempt: 1,
                    lease: MessageField::some(jobs::Lease {
                        owner_id: "worker:fixture".to_owned(),
                        fence: 9,
                        expires_at: MessageField::some(Timestamp {
                            seconds: 2_000_000_100,
                            ..Default::default()
                        }),
                        ..Default::default()
                    }),
                    sources: vec![response.document_batch.as_ref().unwrap().clone()],
                    manifest: MessageField::some(producer()),
                    stages: vec![EnumOrUnknown::new(jobs::JobStage::JOB_STAGE_EXTRACT)],
                    ..Default::default()
                },
                &AtomicBool::new(false),
                &|_, _, _| {},
            )
            .unwrap();
        assert_eq!(
            rejected_response.status.enum_value(),
            Ok(common::CompletionStatus::COMPLETION_STATUS_FAILED)
        );
        assert_eq!(rejected_response.errors.len(), 1);
        let rejected_descriptor =
            ArtifactDescriptor::from_wire_ref(rejected_response.extraction_batch.as_ref().unwrap())
                .unwrap();
        let rejected = crate::adapters::extraction_batches::load_extraction_batch(
            &processor.store,
            &rejected_descriptor,
            &output,
        )
        .unwrap();
        assert_eq!(rejected.item_counts.rejected, 1);
        assert_eq!(rejected.issues.len(), 1);

        let malicious_calls = Arc::new(AtomicUsize::new(0));
        let processor = processor
            .with_extraction(
                Arc::new(EmptyExtraction {
                    calls: Arc::clone(&malicious_calls),
                    reject_first_call: false,
                    invalid_typed_output: true,
                }),
                extraction_config,
            )
            .unwrap();
        let error = processor
            .process(
                jobs::ProcessBatchRequest {
                    context: MessageField::some(request_context("extract:invalid-ontology")),
                    job_id: "job:extract-invalid-ontology".to_owned(),
                    attempt: 1,
                    lease: MessageField::some(jobs::Lease {
                        owner_id: "worker:fixture".to_owned(),
                        fence: 10,
                        expires_at: MessageField::some(Timestamp {
                            seconds: 2_000_000_100,
                            ..Default::default()
                        }),
                        ..Default::default()
                    }),
                    sources: vec![response.document_batch.as_ref().unwrap().clone()],
                    manifest: MessageField::some(producer()),
                    stages: vec![EnumOrUnknown::new(jobs::JobStage::JOB_STAGE_EXTRACT)],
                    ..Default::default()
                },
                &AtomicBool::new(false),
                &|_, _, _| {},
            )
            .expect_err("invalid ontology proposal should never be persisted");
        assert_eq!(error.code(), Code::FailedPrecondition);
        assert!(
            error.to_string().contains("unknown predicate"),
            "unexpected rejection: {error}"
        );
        assert!(malicious_calls.load(Ordering::Acquire) > 0);
    }

    fn meta(corpus_id: &str, record_id: &str) -> common::RecordMeta {
        common::RecordMeta {
            schema_version: 1,
            corpus_id: corpus_id.to_owned(),
            record_id: record_id.to_owned(),
            ..Default::default()
        }
    }

    fn hash(character: char) -> common::ContentHash {
        common::ContentHash {
            sha256: character.to_string().repeat(64),
            ..Default::default()
        }
    }

    fn test_ontology() -> Ontology {
        Ontology::parse_jsonc(include_bytes!(concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/../../configs/ontology-v1.jsonc"
        )))
        .unwrap()
    }

    fn producer() -> common::ProducerManifest {
        common::ProducerManifest {
            software: "fixture".to_owned(),
            build: "fixture".to_owned(),
            schema_version: 1,
            config_hash: MessageField::some(hash('b')),
            input_hashes: vec![test_ontology().content_hash()],
            ..Default::default()
        }
    }

    fn request_context(request_id: &str) -> common::RequestContext {
        common::RequestContext {
            schema_version: 1,
            request_id: request_id.to_owned(),
            trace_id: request_id.to_owned(),
            corpus_id: "corpus:fixture".to_owned(),
            deadline: MessageField::some(Timestamp {
                seconds: 2_000_000_000,
                ..Default::default()
            }),
            config_fingerprint: MessageField::some(hash('c')),
            auth_scope_ref: "scope:fixture".to_owned(),
            ..Default::default()
        }
    }

    fn unknown_date() -> common::DateAssertion {
        common::DateAssertion {
            knowledge: EnumOrUnknown::new(common::DateKnowledge::DATE_KNOWLEDGE_UNKNOWN),
            ..Default::default()
        }
    }
}
