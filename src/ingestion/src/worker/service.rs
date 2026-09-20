//! Stateful Tonic service for bounded, fenced and cancellable ingestion batches.
//!
//! The service validates C01 with the descriptor-aware validator before execution, correlates one
//! active attempt per durable Go job, caches successful retry responses and never publishes outputs.
//! In-memory status is operational only; Go remains the source of durable job truth.

// Tonic's public service trait fixes `Status` as the error type; boxing it would break that contract.
#![allow(clippy::result_large_err)]

use super::transport;
use crate::domain::wire::{self, Limits};
use crate::wire::jobs;
use prost::Message as ProstMessage;
use protobuf::Message as ProtobufMessage;
use std::collections::HashMap;
use std::error::Error;
use std::fmt::{Display, Formatter};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};
use std::time::{SystemTime, UNIX_EPOCH};
use tokio::sync::Semaphore;
use tonic::{Code, Request, Response, Status};

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct WorkerServiceConfig {
    pub maximum_jobs: usize,
    pub maximum_concurrent_batches: usize,
}

impl Default for WorkerServiceConfig {
    fn default() -> Self {
        Self {
            maximum_jobs: 10_000,
            maximum_concurrent_batches: 2,
        }
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ProcessError {
    code: Code,
    message: String,
}

impl ProcessError {
    pub fn new(code: Code, message: impl Into<String>) -> Self {
        Self {
            code,
            message: message.into(),
        }
    }

    fn into_status(self) -> Status {
        Status::new(self.code, self.message)
    }
}

impl Display for ProcessError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        write!(formatter, "{}", self.message)
    }
}

impl Error for ProcessError {}

pub trait BatchProcessor: Send + Sync + 'static {
    fn process(
        &self,
        request: jobs::ProcessBatchRequest,
        cancelled: &AtomicBool,
        progress: &dyn Fn(jobs::JobStage, u64, u64),
    ) -> Result<jobs::ProcessBatchResponse, ProcessError>;
}

struct JobRecord {
    attempt: u32,
    fence: u64,
    stage: i32,
    completed_items: u64,
    total_items: u64,
    cancellation: Arc<AtomicBool>,
    finished: bool,
    response: Option<transport::ProcessBatchResponse>,
}

struct WorkerInner<P> {
    processor: Arc<P>,
    jobs: Mutex<HashMap<String, JobRecord>>,
    maximum_jobs: usize,
    concurrency: Arc<Semaphore>,
}

pub struct WorkerService<P> {
    inner: Arc<WorkerInner<P>>,
}

impl<P> Clone for WorkerService<P> {
    fn clone(&self) -> Self {
        Self {
            inner: Arc::clone(&self.inner),
        }
    }
}

impl<P: BatchProcessor> WorkerService<P> {
    pub fn new(processor: P, config: WorkerServiceConfig) -> Result<Self, ProcessError> {
        if config.maximum_jobs == 0 {
            return Err(ProcessError::new(
                Code::InvalidArgument,
                "maximum_jobs must be positive",
            ));
        }
        if config.maximum_concurrent_batches == 0 {
            return Err(ProcessError::new(
                Code::InvalidArgument,
                "maximum_concurrent_batches must be positive",
            ));
        }
        Ok(Self {
            inner: Arc::new(WorkerInner {
                processor: Arc::new(processor),
                jobs: Mutex::new(HashMap::new()),
                maximum_jobs: config.maximum_jobs,
                concurrency: Arc::new(Semaphore::new(config.maximum_concurrent_batches)),
            }),
        })
    }
}

#[tonic::async_trait]
impl<P: BatchProcessor> transport::worker_server::Worker for WorkerService<P> {
    async fn process_batch(
        &self,
        request: Request<transport::ProcessBatchRequest>,
    ) -> Result<Response<transport::ProcessBatchResponse>, Status> {
        let transport_request = request.into_inner();
        let domain_request: jobs::ProcessBatchRequest = decode_domain(&transport_request)?;
        validate_process_request(&domain_request)?;
        let cancellation_delay = deadline_delay(&domain_request.context)?;

        let job_id = domain_request.job_id.clone();
        let attempt = domain_request.attempt;
        let fence = domain_request.lease.fence;
        let expected_request_id = domain_request.context.request_id.clone();
        let expected_corpus_id = domain_request.context.corpus_id.clone();
        let total_items = domain_request.sources.len() as u64;
        let cancellation = Arc::new(AtomicBool::new(false));
        {
            let mut jobs = self.lock_jobs()?;
            if let Some(record) = jobs.get(&job_id) {
                if record.attempt == attempt && record.fence == fence {
                    if let Some(response) = &record.response {
                        return Ok(Response::new(response.clone()));
                    }
                    if !record.finished {
                        return Err(Status::already_exists("worker attempt is already running"));
                    }
                }
                if attempt < record.attempt || attempt == record.attempt && fence <= record.fence {
                    return Err(Status::failed_precondition("stale worker attempt or fence"));
                }
                if !record.finished {
                    return Err(Status::failed_precondition(
                        "new worker attempt cannot replace a running attempt",
                    ));
                }
            } else if jobs.len() >= self.inner.maximum_jobs {
                let finished = jobs
                    .iter()
                    .find_map(|(key, record)| record.finished.then(|| key.clone()));
                if let Some(key) = finished {
                    jobs.remove(&key);
                } else {
                    return Err(Status::resource_exhausted("worker status registry is full"));
                }
            }
            jobs.insert(
                job_id.clone(),
                JobRecord {
                    attempt,
                    fence,
                    stage: jobs::JobStage::JOB_STAGE_PARSE as i32,
                    completed_items: 0,
                    total_items,
                    cancellation: Arc::clone(&cancellation),
                    finished: false,
                    response: None,
                },
            );
        }

        let deadline_cancellation = Arc::clone(&cancellation);
        let deadline_task = tokio::spawn(async move {
            tokio::time::sleep(cancellation_delay).await;
            deadline_cancellation.store(true, Ordering::Release);
        });

        let permit = Arc::clone(&self.inner.concurrency)
            .acquire_owned()
            .await
            .map_err(|_| Status::unavailable("worker is shutting down"))?;
        if cancellation.load(Ordering::Acquire) {
            self.finish_without_response(&job_id, attempt, fence)?;
            return Err(Status::cancelled(
                "worker attempt was cancelled before execution",
            ));
        }

        let processor = Arc::clone(&self.inner.processor);
        let inner = Arc::clone(&self.inner);
        let progress_job_id = job_id.clone();
        let cancellation_for_process = Arc::clone(&cancellation);
        let result = tokio::task::spawn_blocking(move || {
            let progress = |stage: jobs::JobStage, completed: u64, total: u64| {
                if let Ok(mut records) = inner.jobs.lock() {
                    if let Some(record) = records.get_mut(&progress_job_id) {
                        if record.attempt == attempt && record.fence == fence {
                            record.stage = stage as i32;
                            record.completed_items = completed;
                            record.total_items = total;
                        }
                    }
                }
            };
            processor.process(domain_request, &cancellation_for_process, &progress)
        })
        .await;
        deadline_task.abort();
        drop(permit);

        let domain_response = match result {
            Ok(Ok(response)) => response,
            Ok(Err(error)) => {
                self.finish_without_response(&job_id, attempt, fence)?;
                return Err(error.into_status());
            }
            Err(error) => {
                self.finish_without_response(&job_id, attempt, fence)?;
                return Err(Status::internal(format!("worker task failed: {error}")));
            }
        };
        validate_process_response(
            &domain_request_identity(
                &expected_request_id,
                &expected_corpus_id,
                &job_id,
                attempt,
                fence,
            ),
            &domain_response,
        )?;
        let transport_response: transport::ProcessBatchResponse =
            encode_transport(&domain_response)?;
        {
            let mut jobs = self.lock_jobs()?;
            let record = jobs
                .get_mut(&job_id)
                .ok_or_else(|| Status::internal("worker status record disappeared"))?;
            if record.attempt != attempt || record.fence != fence {
                return Err(Status::failed_precondition("worker result became stale"));
            }
            record.finished = true;
            record.completed_items = record.total_items;
            record.response = Some(transport_response.clone());
        }
        Ok(Response::new(transport_response))
    }

    async fn get_status(
        &self,
        request: Request<transport::WorkerStatusRequest>,
    ) -> Result<Response<transport::WorkerStatusResponse>, Status> {
        self.status(request.into_inner(), false)
    }

    async fn cancel(
        &self,
        request: Request<transport::WorkerStatusRequest>,
    ) -> Result<Response<transport::WorkerStatusResponse>, Status> {
        self.status(request.into_inner(), true)
    }
}

impl<P: BatchProcessor> WorkerService<P> {
    fn status(
        &self,
        transport_request: transport::WorkerStatusRequest,
        cancel: bool,
    ) -> Result<Response<transport::WorkerStatusResponse>, Status> {
        let request: jobs::WorkerStatusRequest = decode_domain(&transport_request)?;
        wire::validate(&request, Limits::default()).map_err(invalid_wire)?;
        validate_deadline(&request.context)?;
        let jobs = self.lock_jobs()?;
        let record = jobs
            .get(&request.job_id)
            .ok_or_else(|| Status::not_found("worker job is not in the local registry"))?;
        if record.attempt != request.attempt || record.fence != request.fence {
            return Err(Status::failed_precondition("stale worker status request"));
        }
        if cancel {
            record.cancellation.store(true, Ordering::Release);
        }
        Ok(Response::new(transport::WorkerStatusResponse {
            job_id: request.job_id,
            attempt: record.attempt,
            fence: record.fence,
            stage: record.stage,
            completed_items: record.completed_items,
            total_items: record.total_items,
            cancellation_acknowledged: record.cancellation.load(Ordering::Acquire),
        }))
    }

    fn lock_jobs(&self) -> Result<std::sync::MutexGuard<'_, HashMap<String, JobRecord>>, Status> {
        self.inner
            .jobs
            .lock()
            .map_err(|_| Status::internal("worker status registry lock is poisoned"))
    }

    fn finish_without_response(
        &self,
        job_id: &str,
        attempt: u32,
        fence: u64,
    ) -> Result<(), Status> {
        let mut jobs = self.lock_jobs()?;
        if let Some(record) = jobs.get_mut(job_id) {
            if record.attempt == attempt && record.fence == fence {
                record.finished = true;
            }
        }
        Ok(())
    }
}

struct ResponseIdentity<'a> {
    request_id: &'a str,
    corpus_id: &'a str,
    job_id: &'a str,
    attempt: u32,
    fence: u64,
}

fn domain_request_identity<'a>(
    request_id: &'a str,
    corpus_id: &'a str,
    job_id: &'a str,
    attempt: u32,
    fence: u64,
) -> ResponseIdentity<'a> {
    ResponseIdentity {
        request_id,
        corpus_id,
        job_id,
        attempt,
        fence,
    }
}

fn validate_process_request(request: &jobs::ProcessBatchRequest) -> Result<(), Status> {
    wire::validate(request, Limits::default()).map_err(invalid_wire)?;
    validate_deadline(&request.context)?;
    if request.lease.expires_at.seconds < request.context.deadline.seconds
        || request.lease.expires_at.seconds == request.context.deadline.seconds
            && request.lease.expires_at.nanos < request.context.deadline.nanos
    {
        return Err(Status::failed_precondition(
            "lease expires before the request deadline",
        ));
    }
    Ok(())
}

fn validate_process_response(
    expected: &ResponseIdentity<'_>,
    response: &jobs::ProcessBatchResponse,
) -> Result<(), Status> {
    wire::validate(response, Limits::default()).map_err(invalid_wire)?;
    if response.request_id != expected.request_id
        || response.job_id != expected.job_id
        || response.attempt != expected.attempt
        || response.fence != expected.fence
    {
        return Err(Status::failed_precondition(
            "processor returned a stale or mismatched response",
        ));
    }
    if response.checkpoint.is_some()
        && (response.checkpoint.job_id != expected.job_id
            || response.checkpoint.fence != expected.fence
            || response.checkpoint.meta.corpus_id != expected.corpus_id)
    {
        return Err(Status::failed_precondition(
            "processor checkpoint context or fence is mismatched",
        ));
    }
    if response.status.enum_value()
        == Ok(crate::wire::common::CompletionStatus::COMPLETION_STATUS_SUCCEEDED)
        && (!response.errors.is_empty()
            || response.document_batch.is_none()
                && response.graph_delta.is_none()
                && response.index_batch.is_none())
    {
        return Err(Status::failed_precondition(
            "successful processor response requires output and no errors",
        ));
    }
    Ok(())
}

fn validate_deadline(
    context: &protobuf::MessageField<crate::wire::common::RequestContext>,
) -> Result<(), Status> {
    let context = context
        .as_ref()
        .ok_or_else(|| Status::invalid_argument("request context is required"))?;
    let deadline = context
        .deadline
        .as_ref()
        .ok_or_else(|| Status::invalid_argument("request context deadline is required"))?;
    let now = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_err(|_| Status::internal("system clock precedes Unix epoch"))?;
    if deadline.seconds < now.as_secs() as i64
        || deadline.seconds == now.as_secs() as i64 && deadline.nanos <= now.subsec_nanos() as i32
    {
        return Err(Status::deadline_exceeded("request deadline has elapsed"));
    }
    Ok(())
}

fn deadline_delay(
    context: &protobuf::MessageField<crate::wire::common::RequestContext>,
) -> Result<std::time::Duration, Status> {
    let deadline = context
        .as_ref()
        .and_then(|value| value.deadline.as_ref())
        .ok_or_else(|| Status::invalid_argument("request context deadline is required"))?;
    let seconds = u64::try_from(deadline.seconds)
        .map_err(|_| Status::invalid_argument("request deadline precedes Unix epoch"))?;
    let nanos = u32::try_from(deadline.nanos)
        .map_err(|_| Status::invalid_argument("request deadline nanos are invalid"))?;
    let target = UNIX_EPOCH + std::time::Duration::new(seconds, nanos);
    target
        .duration_since(SystemTime::now())
        .map_err(|_| Status::deadline_exceeded("request deadline has elapsed"))
}

fn decode_domain<T, D>(message: &T) -> Result<D, Status>
where
    T: ProstMessage,
    D: ProtobufMessage + Default,
{
    D::parse_from_bytes(&message.encode_to_vec())
        .map_err(|error| Status::invalid_argument(format!("decode C01 request: {error}")))
}

fn encode_transport<T, D>(message: &D) -> Result<T, Status>
where
    T: ProstMessage + Default,
    D: ProtobufMessage,
{
    let bytes = message
        .write_to_bytes()
        .map_err(|error| Status::internal(format!("encode C01 response: {error}")))?;
    T::decode(bytes.as_slice())
        .map_err(|error| Status::internal(format!("decode transport response: {error}")))
}

fn invalid_wire(error: String) -> Status {
    Status::invalid_argument(format!("C01 validation failed: {error}"))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::wire::common;
    use protobuf::well_known_types::timestamp::Timestamp;
    use protobuf::{EnumOrUnknown, MessageField};
    use std::sync::atomic::AtomicUsize;
    use std::time::Duration;
    use transport::worker_server::Worker;

    struct EchoProcessor {
        calls: Arc<AtomicUsize>,
        wait_for_cancel: bool,
    }

    impl BatchProcessor for EchoProcessor {
        fn process(
            &self,
            request: jobs::ProcessBatchRequest,
            cancelled: &AtomicBool,
            progress: &dyn Fn(jobs::JobStage, u64, u64),
        ) -> Result<jobs::ProcessBatchResponse, ProcessError> {
            self.calls.fetch_add(1, Ordering::AcqRel);
            if self.wait_for_cancel {
                while !cancelled.load(Ordering::Acquire) {
                    std::thread::sleep(Duration::from_millis(2));
                }
                return Err(ProcessError::new(Code::Cancelled, "cancelled by test"));
            }
            progress(jobs::JobStage::JOB_STAGE_PARSE, 1, 1);
            Ok(jobs::ProcessBatchResponse {
                request_id: request.context.request_id.clone(),
                job_id: request.job_id,
                attempt: request.attempt,
                fence: request.lease.fence,
                document_batch: MessageField::some(artifact("output-1", "outputs/batch.pb")),
                status: EnumOrUnknown::new(common::CompletionStatus::COMPLETION_STATUS_SUCCEEDED),
                ..Default::default()
            })
        }
    }

    #[tokio::test]
    async fn caches_completed_response_and_rejects_stale_fence() {
        let calls = Arc::new(AtomicUsize::new(0));
        let service = WorkerService::new(
            EchoProcessor {
                calls: Arc::clone(&calls),
                wait_for_cancel: false,
            },
            WorkerServiceConfig::default(),
        )
        .unwrap();
        let request = transport_request(60);

        let first = service
            .process_batch(Request::new(request.clone()))
            .await
            .unwrap()
            .into_inner();
        let second = service
            .process_batch(Request::new(request.clone()))
            .await
            .unwrap()
            .into_inner();
        assert_eq!(first, second);
        assert_eq!(calls.load(Ordering::Acquire), 1);

        let mut stale = request;
        stale.attempt = 0;
        let error = service
            .process_batch(Request::new(stale))
            .await
            .unwrap_err();
        assert!(matches!(
            error.code(),
            Code::InvalidArgument | Code::FailedPrecondition
        ));
    }

    #[tokio::test]
    async fn rejects_elapsed_body_deadline_before_processor() {
        let calls = Arc::new(AtomicUsize::new(0));
        let service = WorkerService::new(
            EchoProcessor {
                calls: Arc::clone(&calls),
                wait_for_cancel: false,
            },
            WorkerServiceConfig::default(),
        )
        .unwrap();
        let error = service
            .process_batch(Request::new(transport_request(-1)))
            .await
            .unwrap_err();
        assert_eq!(error.code(), Code::DeadlineExceeded);
        assert_eq!(calls.load(Ordering::Acquire), 0);
    }

    #[tokio::test]
    async fn cancel_sets_acknowledgement_and_stops_running_processor() {
        let service = WorkerService::new(
            EchoProcessor {
                calls: Arc::new(AtomicUsize::new(0)),
                wait_for_cancel: true,
            },
            WorkerServiceConfig::default(),
        )
        .unwrap();
        let process_service = service.clone();
        let request = transport_request(60);
        let status = status_request(&request);
        let running =
            tokio::spawn(async move { process_service.process_batch(Request::new(request)).await });

        let mut observed = None;
        for _ in 0..100 {
            match service.get_status(Request::new(status.clone())).await {
                Ok(response) => {
                    observed = Some(response.into_inner());
                    break;
                }
                Err(error) if error.code() == Code::NotFound => tokio::task::yield_now().await,
                Err(error) => panic!("unexpected status error: {error}"),
            }
        }
        assert!(observed.is_some(), "running job became visible");
        let cancelled = service
            .cancel(Request::new(status))
            .await
            .unwrap()
            .into_inner();
        assert!(cancelled.cancellation_acknowledged);
        let error = running.await.unwrap().unwrap_err();
        assert_eq!(error.code(), Code::Cancelled);
    }

    fn transport_request(deadline_seconds: i64) -> transport::ProcessBatchRequest {
        let domain = domain_request(deadline_seconds);
        encode_transport(&domain).unwrap()
    }

    fn status_request(request: &transport::ProcessBatchRequest) -> transport::WorkerStatusRequest {
        transport::WorkerStatusRequest {
            context: request.context.clone(),
            job_id: request.job_id.clone(),
            attempt: request.attempt,
            fence: request.lease.as_ref().unwrap().fence,
        }
    }

    fn domain_request(deadline_seconds: i64) -> jobs::ProcessBatchRequest {
        let now = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_secs() as i64;
        let deadline = now + deadline_seconds;
        jobs::ProcessBatchRequest {
            context: MessageField::some(common::RequestContext {
                schema_version: 1,
                request_id: "request-1".to_owned(),
                trace_id: "trace-1".to_owned(),
                corpus_id: "corpus-1".to_owned(),
                deadline: MessageField::some(Timestamp {
                    seconds: deadline,
                    ..Default::default()
                }),
                config_fingerprint: MessageField::some(hash('a')),
                auth_scope_ref: "scope-1".to_owned(),
                ..Default::default()
            }),
            job_id: "job-1".to_owned(),
            attempt: 1,
            lease: MessageField::some(jobs::Lease {
                owner_id: "worker-1".to_owned(),
                fence: 7,
                expires_at: MessageField::some(Timestamp {
                    seconds: deadline + 60,
                    ..Default::default()
                }),
                ..Default::default()
            }),
            sources: vec![artifact("source-1", "inputs/source.pdf")],
            manifest: MessageField::some(common::ProducerManifest {
                software: "test".to_owned(),
                build: "test".to_owned(),
                schema_version: 1,
                config_hash: MessageField::some(hash('b')),
                ..Default::default()
            }),
            stages: vec![EnumOrUnknown::new(jobs::JobStage::JOB_STAGE_PARSE)],
            ..Default::default()
        }
    }

    fn artifact(id: &str, key: &str) -> common::ArtifactRef {
        common::ArtifactRef {
            artifact_id: id.to_owned(),
            content_hash: MessageField::some(hash('c')),
            storage_key: key.to_owned(),
            media_type: "application/pdf".to_owned(),
            byte_size: 1,
            schema_version: 1,
            ..Default::default()
        }
    }

    fn hash(character: char) -> common::ContentHash {
        common::ContentHash {
            sha256: character.to_string().repeat(64),
            ..Default::default()
        }
    }
}
