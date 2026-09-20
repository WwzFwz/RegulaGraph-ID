//! PARSE/STRUCTURE batch processor backed by PDFium, conservative normalization and immutable artifacts.
//!
//! STRUCTURE consumes the immutable PARSE `DocumentBatch` and emits derived hierarchy only. Legal
//! provision/version binding remains a later registry-owned step; this processor never fabricates
//! provisions, canonical entities, or publication state.

use super::service::{BatchProcessor, ProcessError};
use crate::adapters::document_batches::{load_document_batch, persist_document_batch};
use crate::adapters::storage::{ArtifactDescriptor, ArtifactStore};
use crate::adapters::text_artifacts::{load_normalized_text, persist_text_artifact};
use crate::document::chunking::structural::{
    parse_structure, StructureIdentity, StructureParserConfig,
};
use crate::document::normalization::text::{normalize_text, TextNormalizerConfig};
use crate::document::parsing::pdf::{PdfParseRequest, PdfParser};
use crate::domain::document_batch::{
    assemble_document_batch, build_source_blob, DocumentBatchConfig, DocumentBatchParts,
};
use crate::domain::document_wire::{project_structures, DocumentWireConfig};
use crate::domain::text_artifact_wire::TextArtifactWireConfig;
use crate::wire::{common, jobs};
use protobuf::{EnumOrUnknown, MessageField};
use sha2::{Digest, Sha256};
use std::collections::HashSet;
use std::sync::atomic::{AtomicBool, Ordering};
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
    pub document_wire: DocumentWireConfig,
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
            document_wire: DocumentWireConfig::default(),
        }
    }
}

pub struct ParseBatchProcessor {
    store: ArtifactStore,
    parser: PdfParser,
    config: ParseBatchProcessorConfig,
}

impl ParseBatchProcessor {
    pub fn new(
        store: ArtifactStore,
        parser: PdfParser,
        config: ParseBatchProcessorConfig,
    ) -> Result<Self, ProcessError> {
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
            config,
        })
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
        if request.stages.len() != 1
            || request.stages[0].enum_value() != Ok(jobs::JobStage::JOB_STAGE_PARSE)
        {
            return Err(ProcessError::new(
                Code::Unimplemented,
                "worker accepts exactly one PARSE or STRUCTURE stage",
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
                ..Default::default()
            }),
            document_batch: MessageField::some(document_batch_ref),
            status: EnumOrUnknown::new(if complete {
                common::CompletionStatus::COMPLETION_STATUS_SUCCEEDED
            } else {
                common::CompletionStatus::COMPLETION_STATUS_FAILED
            }),
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
                ..Default::default()
            }),
            document_batch: MessageField::some(document_batch_ref),
            status: EnumOrUnknown::new(common::CompletionStatus::COMPLETION_STATUS_SUCCEEDED),
            ..Default::default()
        })
    }
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
