//! Parse-stage batch processor backed by PDFium, conservative normalization and immutable artifacts.
//!
//! Legal structure/version stages are rejected until their required identities are explicit in the
//! request contract. This processor never fabricates provisions, canonical entities or publication state.

use super::service::{BatchProcessor, ProcessError};
use crate::adapters::document_batches::persist_document_batch;
use crate::adapters::storage::ArtifactStore;
use crate::adapters::text_artifacts::persist_text_artifact;
use crate::document::normalization::text::{normalize_text, TextNormalizerConfig};
use crate::document::parsing::pdf::{PdfParseRequest, PdfParser};
use crate::domain::document_batch::{
    assemble_document_batch, build_source_blob, DocumentBatchConfig, DocumentBatchParts,
};
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
        if request.stages.len() != 1
            || request.stages[0].enum_value() != Ok(jobs::JobStage::JOB_STAGE_PARSE)
        {
            return Err(ProcessError::new(
                Code::Unimplemented,
                "worker currently accepts exactly the PARSE stage; legal identities are required before later stages",
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
                .ok_or_else(|| {
                    ProcessError::new(Code::ResourceExhausted, "batch page count overflow")
                })?;
            if accumulated_pages > self.config.maximum_batch_pages {
                return Err(ProcessError::new(
                    Code::ResourceExhausted,
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
        Ok(jobs::ProcessBatchResponse {
            request_id,
            job_id: request.job_id,
            attempt: request.attempt,
            fence: request.lease.fence,
            document_batch: MessageField::some(artifact.reference),
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

fn preflight_sources(
    sources: &[common::ArtifactRef],
    config: &ParseBatchProcessorConfig,
) -> Result<(), ProcessError> {
    if sources.len() > config.maximum_sources {
        return Err(ProcessError::new(
            Code::ResourceExhausted,
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
        total.checked_add(source.byte_size).ok_or_else(|| {
            ProcessError::new(Code::ResourceExhausted, "batch input byte count overflow")
        })
    })?;
    if total_bytes > config.maximum_input_bytes {
        return Err(ProcessError::new(
            Code::ResourceExhausted,
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
