//! Executes INDEX inside the existing bounded worker admission/fence lifecycle.
//! A persistent native client and Tokio handle are injected at bootstrap; no model
//! is loaded per request. Blocking artifact work stays on the worker blocking pool.
//! Emits an immutable IndexBatch/checkpoint only after complete vector validation.
//! Go still verifies registry authority, source membership and publication receipts.
//! Measure queue/read/build/inference/write time, cancellation delay and RSS under
//! configs/benchmark-targets.yaml; production acceptance remains unmeasured.

use super::{processor::ParseBatchProcessor, service::ProcessError};
use crate::{
    adapters::native_inference::NativeEmbeddingClient,
    domain::wire::{self, Limits},
    indexing::{build, dense::embed_document_batch},
    wire::{common, evidence, inference, jobs},
};
use protobuf::{EnumOrUnknown, Message, MessageField};
use std::sync::{
    atomic::{AtomicBool, Ordering},
    Arc,
};
use tonic::Code;

pub trait IndexEmbedding: Send + Sync {
    fn model(&self) -> &common::ModelManifest;
    fn embed(
        &self,
        context: common::RequestContext,
        items: Vec<inference::TextItem>,
        operation: String,
        cancelled: &AtomicBool,
    ) -> Result<Vec<(String, evidence::DenseVector)>, ProcessError>;
}

pub struct NativeIndexEmbedding {
    client: NativeEmbeddingClient,
    model: common::ModelManifest,
    runtime: tokio::runtime::Handle,
}
impl NativeIndexEmbedding {
    /// Call from bootstrap on a live Tokio runtime; process() runs on spawn_blocking.
    pub async fn connect(
        endpoint: String,
        model: common::ModelManifest,
    ) -> Result<Self, tonic::Status> {
        let client = NativeEmbeddingClient::connect(endpoint, model.clone()).await?;
        Ok(Self {
            client,
            model,
            runtime: tokio::runtime::Handle::current(),
        })
    }
}
impl IndexEmbedding for NativeIndexEmbedding {
    fn model(&self) -> &common::ModelManifest {
        &self.model
    }
    fn embed(
        &self,
        context: common::RequestContext,
        items: Vec<inference::TextItem>,
        operation: String,
        cancelled: &AtomicBool,
    ) -> Result<Vec<(String, evidence::DenseVector)>, ProcessError> {
        self.runtime
            .block_on(embed_document_batch(
                &self.client,
                context,
                self.model.clone(),
                items,
                operation,
                cancelled,
            ))
            .map_err(|e| ProcessError::new(e.code(), e.message()))
    }
}

impl ParseBatchProcessor {
    pub fn with_indexing(
        mut self,
        embedding: Arc<dyn IndexEmbedding>,
    ) -> Result<Self, ProcessError> {
        wire::validate(embedding.model(), Limits::default()).map_err(invalid)?;
        if embedding.model().task.enum_value() != Ok(common::ModelTask::MODEL_TASK_EMBED) {
            return Err(invalid("INDEX requires EMBED model"));
        }
        self.indexing = Some(embedding);
        Ok(self)
    }

    pub(super) fn process_index(
        &self,
        request: jobs::ProcessBatchRequest,
        cancelled: &AtomicBool,
        progress: &dyn Fn(jobs::JobStage, u64, u64),
    ) -> Result<jobs::ProcessBatchResponse, ProcessError> {
        wire::validate(&request, Limits::default()).map_err(invalid)?;
        let embedding = self.indexing.as_ref().ok_or_else(|| {
            ProcessError::new(
                Code::FailedPrecondition,
                "INDEX native runtime is not configured",
            )
        })?;
        let reference = request
            .index_build_plan
            .as_ref()
            .ok_or_else(|| invalid("INDEX build plan is required"))?;
        let plan: evidence::IndexBuildPlan = build::load_typed(&self.store, reference, cancelled)
            .map_err(|e| failure(e, cancelled))?;
        if request.sources != vec![(*plan.document_batch).clone()]
            || request.manifest != plan.producer
            || plan.generation.dense_manifest.as_ref() != Some(embedding.model())
        {
            return Err(invalid("INDEX source/producer/model differs from plan"));
        }
        progress(jobs::JobStage::JOB_STAGE_INDEX, 0, plan.items.len() as u64);
        let prepared = build::prepare(
            &self.store,
            &plan,
            reference,
            &request.context,
            &self.config.normalizer,
            cancelled,
        )
        .map_err(|e| failure(e, cancelled))?;
        let dense = embedding.embed(
            (*request.context).clone(),
            prepared.items().to_vec(),
            format!("index:{}", reference.content_hash.sha256),
            cancelled,
        )?;
        let batch = prepared.finish(dense).map_err(|e| failure(e, cancelled))?;
        build::check_cancel(cancelled).map_err(|e| failure(e, cancelled))?;
        let bytes = batch
            .write_to_bytes()
            .map_err(|e| ProcessError::new(Code::Internal, e.to_string()))?;
        let output = self
            .store
            .put_bytes("index-batch", build::BATCH_MEDIA, 1, &bytes)
            .and_then(|d| d.to_wire_ref())
            .map_err(|e| ProcessError::new(Code::Internal, e.to_string()))?;
        build::check_cancel(cancelled).map_err(|e| failure(e, cancelled))?;
        progress(
            jobs::JobStage::JOB_STAGE_INDEX,
            plan.items.len() as u64,
            plan.items.len() as u64,
        );
        let response = jobs::ProcessBatchResponse {
            request_id: request.context.request_id.clone(),
            job_id: request.job_id.clone(),
            attempt: request.attempt,
            fence: request.lease.fence,
            status: EnumOrUnknown::new(common::CompletionStatus::COMPLETION_STATUS_SUCCEEDED),
            index_batch: MessageField::some(output.clone()),
            checkpoint: MessageField::some(jobs::Checkpoint {
                meta: MessageField::some(common::RecordMeta {
                    schema_version: 1,
                    corpus_id: plan.meta.corpus_id.clone(),
                    record_id: format!("checkpoint:index:{}", reference.content_hash.sha256),
                    ..Default::default()
                }),
                job_id: request.job_id,
                stage: EnumOrUnknown::new(jobs::JobStage::JOB_STAGE_INDEX),
                completed_batch_keys: vec![output.artifact_id.clone()],
                artifact_hashes: vec![(*output.content_hash).clone()],
                manifest: plan.producer,
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
fn invalid(message: impl Into<String>) -> ProcessError {
    ProcessError::new(Code::InvalidArgument, message)
}
fn failure(message: String, cancelled: &AtomicBool) -> ProcessError {
    ProcessError::new(
        if cancelled.load(Ordering::Acquire) {
            Code::Cancelled
        } else {
            Code::FailedPrecondition
        },
        message,
    )
}
