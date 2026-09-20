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
    pub maximum_pages: usize,
}

impl Default for ParseBatchProcessorConfig {
    fn default() -> Self {
        Self {
            normalizer: TextNormalizerConfig::default(),
            document_batch: DocumentBatchConfig::default(),
            maximum_pages: 100_000,
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
        if config.maximum_pages == 0 {
            return Err(ProcessError::new(
                Code::InvalidArgument,
                "maximum_pages must be positive",
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
        let manifest = request
            .manifest
            .as_ref()
            .ok_or_else(|| {
                ProcessError::new(Code::InvalidArgument, "producer manifest is required")
            })?
            .clone();
        let request_id = context.request_id.clone();
        let corpus_id = context.corpus_id.clone();
        let total = request.sources.len() as u64;
        let mut sources = Vec::with_capacity(request.sources.len());
        let mut text_artifacts = Vec::with_capacity(request.sources.len());
        let mut dependencies = Vec::with_capacity(request.sources.len());
        let mut observed_hashes = HashSet::with_capacity(request.sources.len());
        let mut observed_artifact_ids = HashSet::with_capacity(request.sources.len());

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
            if !observed_hashes.insert(source_hash.clone()) {
                progress(jobs::JobStage::JOB_STAGE_PARSE, (index + 1) as u64, total);
                continue;
            }
            let path = self
                .store
                .verified_input_path(source)
                .map_err(|error| ProcessError::new(Code::FailedPrecondition, error.to_string()))?;
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
                    software: manifest.software.clone(),
                    build: manifest.build.clone(),
                    maximum_pages: self.config.maximum_pages,
                },
            )
            .map_err(|error| ProcessError::new(Code::Internal, error.to_string()))?;
            sources.push(
                build_source_blob(&corpus_id, &source_blob_id, source.clone())
                    .map_err(|error| ProcessError::new(Code::InvalidArgument, error.to_string()))?,
            );
            text_artifacts.push(persisted.artifact);
            dependencies.push(common::Dependency {
                dependency_id: source.artifact_id.clone(),
                fingerprint: MessageField::some(common::ContentHash {
                    sha256: source_hash,
                    ..Default::default()
                }),
                ..Default::default()
            });
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
        let batch = assemble_document_batch(
            DocumentBatchParts {
                batch_id: format!("document-batch:{identity}"),
                context,
                sources,
                text_artifacts,
                dependency_manifest: common::DependencyManifest {
                    artifact_id: format!("dependency-manifest:{identity}"),
                    dependencies,
                    producer_manifest: MessageField::some(manifest),
                    ..Default::default()
                },
                ..Default::default()
            },
            &self.config.document_batch,
        )
        .map_err(|error| ProcessError::new(Code::FailedPrecondition, error.to_string()))?;
        let artifact = persist_document_batch(&self.store, &batch)
            .map_err(|error| ProcessError::new(Code::Internal, error.to_string()))?;
        Ok(jobs::ProcessBatchResponse {
            request_id,
            job_id: request.job_id,
            attempt: request.attempt,
            fence: request.lease.fence,
            document_batch: MessageField::some(artifact.reference),
            status: EnumOrUnknown::new(common::CompletionStatus::COMPLETION_STATUS_SUCCEEDED),
            ..Default::default()
        })
    }
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
