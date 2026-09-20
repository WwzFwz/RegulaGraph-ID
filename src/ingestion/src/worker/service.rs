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
use sha2::{Digest, Sha256};
use std::collections::HashMap;
use std::error::Error;
use std::fmt::{Display, Formatter};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};
use std::time::{SystemTime, UNIX_EPOCH};
use tokio::sync::{oneshot, watch, Semaphore};
use tonic::metadata::MetadataMap;
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
    request_fingerprint: [u8; 32],
    corpus_id: String,
    auth_scope_ref: String,
    stage: i32,
    completed_items: u64,
    total_items: u64,
    cancellation: Arc<Cancellation>,
    finished: bool,
    response: Option<transport::ProcessBatchResponse>,
    failure: Option<StoredFailure>,
}

struct Cancellation {
    flag: AtomicBool,
    deadline_elapsed: AtomicBool,
    signal: watch::Sender<bool>,
}

impl Cancellation {
    fn new() -> Self {
        let (signal, _) = watch::channel(false);
        Self {
            flag: AtomicBool::new(false),
            deadline_elapsed: AtomicBool::new(false),
            signal,
        }
    }

    fn cancel(&self, deadline_elapsed: bool) {
        if deadline_elapsed {
            self.deadline_elapsed.store(true, Ordering::Release);
        }
        self.flag.store(true, Ordering::Release);
        self.signal.send_replace(true);
    }

    fn is_cancelled(&self) -> bool {
        self.flag.load(Ordering::Acquire)
    }

    fn deadline_elapsed(&self) -> bool {
        self.deadline_elapsed.load(Ordering::Acquire)
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
struct StoredFailure {
    code: Code,
    message: String,
}

impl StoredFailure {
    fn new(code: Code, message: impl Into<String>) -> Self {
        Self {
            code,
            message: message.into(),
        }
    }

    fn status(&self) -> Status {
        Status::new(self.code, self.message.clone())
    }
}

impl From<Status> for StoredFailure {
    fn from(status: Status) -> Self {
        Self::new(status.code(), status.message())
    }
}

type BatchOutcome = Result<transport::ProcessBatchResponse, StoredFailure>;

#[derive(Clone, Copy)]
struct BatchKey<'a> {
    job_id: &'a str,
    attempt: u32,
    fence: u64,
    request_fingerprint: [u8; 32],
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
        let received_at = tokio::time::Instant::now();
        let transport_timeout = grpc_timeout(request.metadata())?;
        let transport_deadline = match transport_timeout {
            Some(timeout) => Some(
                received_at
                    .checked_add(timeout)
                    .ok_or_else(|| Status::invalid_argument("grpc-timeout is out of range"))?,
            ),
            None => None,
        };
        let transport_request = request.into_inner();
        let domain_request: jobs::ProcessBatchRequest = decode_domain(&transport_request)?;
        validate_process_request(&domain_request)?;
        let body_timeout = deadline_delay(&domain_request.context)?;
        let body_deadline = tokio::time::Instant::now()
            .checked_add(body_timeout)
            .ok_or_else(|| Status::invalid_argument("request deadline is out of range"))?;
        let effective_deadline = transport_deadline
            .map(|deadline| deadline.min(body_deadline))
            .unwrap_or(body_deadline);
        let request_fingerprint = fingerprint_request(&domain_request)?;

        let job_id = domain_request.job_id.clone();
        let attempt = domain_request.attempt;
        let fence = domain_request.lease.fence;
        let corpus_id = domain_request.context.corpus_id.clone();
        let auth_scope_ref = domain_request.context.auth_scope_ref.clone();
        let total_items = domain_request.sources.len() as u64;
        let cancellation = Arc::new(Cancellation::new());
        {
            let mut jobs = self.lock_jobs()?;
            if let Some(record) = jobs.get(&job_id) {
                if record.corpus_id != corpus_id || record.auth_scope_ref != auth_scope_ref {
                    return Err(Status::permission_denied(
                        "worker job context does not match the registered corpus and scope",
                    ));
                }
                if record.attempt == attempt && record.fence == fence {
                    if record.request_fingerprint != request_fingerprint {
                        return Err(Status::failed_precondition(
                            "worker retry payload differs from the registered attempt",
                        ));
                    }
                    if let Some(response) = &record.response {
                        return Ok(Response::new(response.clone()));
                    }
                    if let Some(failure) = &record.failure {
                        return Err(failure.status());
                    }
                    if !record.finished {
                        return Err(Status::already_exists("worker attempt is already running"));
                    }
                    return Err(Status::internal(
                        "finished worker attempt has no terminal outcome",
                    ));
                }
                if attempt <= record.attempt || fence <= record.fence {
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
                    request_fingerprint,
                    corpus_id,
                    auth_scope_ref,
                    stage: jobs::JobStage::JOB_STAGE_PARSE as i32,
                    completed_items: 0,
                    total_items,
                    cancellation: Arc::clone(&cancellation),
                    finished: false,
                    response: None,
                    failure: None,
                },
            );
        }

        let inner = Arc::clone(&self.inner);
        let (sender, receiver) = oneshot::channel();
        tokio::spawn(execute_batch(
            inner,
            domain_request,
            Arc::clone(&cancellation),
            effective_deadline,
            request_fingerprint,
            sender,
        ));

        match receiver.await {
            Ok(Ok(response)) => Ok(Response::new(response)),
            Ok(Err(failure)) => Err(failure.status()),
            Err(_) => Err(Status::internal("worker execution task disappeared")),
        }
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
        let context = request
            .context
            .as_ref()
            .ok_or_else(|| Status::invalid_argument("request context is required"))?;
        if context.corpus_id != record.corpus_id || context.auth_scope_ref != record.auth_scope_ref
        {
            return Err(Status::permission_denied(
                "worker status context does not match the registered job",
            ));
        }
        if record.attempt != request.attempt || record.fence != request.fence {
            return Err(Status::failed_precondition("stale worker status request"));
        }
        if cancel && !record.finished {
            record.cancellation.cancel(false);
        }
        Ok(Response::new(transport::WorkerStatusResponse {
            job_id: request.job_id,
            attempt: record.attempt,
            fence: record.fence,
            stage: record.stage,
            completed_items: record.completed_items,
            total_items: record.total_items,
            cancellation_acknowledged: record.cancellation.is_cancelled(),
        }))
    }

    fn lock_jobs(&self) -> Result<std::sync::MutexGuard<'_, HashMap<String, JobRecord>>, Status> {
        self.inner
            .jobs
            .lock()
            .map_err(|_| Status::internal("worker status registry lock is poisoned"))
    }
}

async fn execute_batch<P: BatchProcessor>(
    inner: Arc<WorkerInner<P>>,
    domain_request: jobs::ProcessBatchRequest,
    cancellation: Arc<Cancellation>,
    effective_deadline: tokio::time::Instant,
    request_fingerprint: [u8; 32],
    sender: oneshot::Sender<BatchOutcome>,
) {
    let job_id = domain_request.job_id.clone();
    let attempt = domain_request.attempt;
    let fence = domain_request.lease.fence;
    let expected_request_id = domain_request.context.request_id.clone();
    let expected_corpus_id = domain_request.context.corpus_id.clone();
    let deadline_cancellation = Arc::clone(&cancellation);
    let deadline_task = tokio::spawn(async move {
        tokio::time::sleep_until(effective_deadline).await;
        deadline_cancellation.cancel(true);
    });

    let mut cancellation_receiver = cancellation.signal.subscribe();
    if cancellation.is_cancelled() {
        let failure = cancellation_failure(cancellation.deadline_elapsed());
        deadline_task.abort();
        complete_batch(
            &inner,
            BatchKey {
                job_id: &job_id,
                attempt,
                fence,
                request_fingerprint,
            },
            &cancellation,
            effective_deadline,
            Err(failure),
            sender,
        );
        return;
    }
    let permit = tokio::select! {
        biased;
        changed = cancellation_receiver.changed() => {
            let failure = if changed.is_ok() {
                cancellation_failure(cancellation.deadline_elapsed())
            } else {
                StoredFailure::new(Code::Unavailable, "worker cancellation channel closed")
            };
            deadline_task.abort();
            complete_batch(
                &inner,
                BatchKey {
                    job_id: &job_id,
                    attempt,
                    fence,
                    request_fingerprint,
                },
                &cancellation,
                effective_deadline,
                Err(failure),
                sender,
            );
            return;
        }
        permit = Arc::clone(&inner.concurrency).acquire_owned() => match permit {
            Ok(permit) => permit,
            Err(_) => {
                deadline_task.abort();
                complete_batch(
                    &inner,
                    BatchKey {
                        job_id: &job_id,
                        attempt,
                        fence,
                        request_fingerprint,
                    },
                    &cancellation,
                    effective_deadline,
                    Err(StoredFailure::new(Code::Unavailable, "worker is shutting down")),
                    sender,
                );
                return;
            }
        }
    };
    if cancellation.is_cancelled() {
        let failure = cancellation_failure(cancellation.deadline_elapsed());
        deadline_task.abort();
        drop(permit);
        complete_batch(
            &inner,
            BatchKey {
                job_id: &job_id,
                attempt,
                fence,
                request_fingerprint,
            },
            &cancellation,
            effective_deadline,
            Err(failure),
            sender,
        );
        return;
    }

    let processor = Arc::clone(&inner.processor);
    let progress_inner = Arc::clone(&inner);
    let progress_job_id = job_id.clone();
    let cancellation_for_process = Arc::clone(&cancellation);
    let result = tokio::task::spawn_blocking(move || {
        let progress = |stage: jobs::JobStage, completed: u64, total: u64| {
            if let Ok(mut records) = progress_inner.jobs.lock() {
                if let Some(record) = records.get_mut(&progress_job_id) {
                    if record.attempt == attempt
                        && record.fence == fence
                        && record.request_fingerprint == request_fingerprint
                    {
                        record.stage = stage as i32;
                        record.completed_items = completed;
                        record.total_items = total;
                    }
                }
            }
        };
        processor.process(domain_request, &cancellation_for_process.flag, &progress)
    })
    .await;
    drop(permit);

    let deadline_was_elapsed = cancellation.deadline_elapsed();
    let outcome = match result {
        Ok(Ok(_)) if cancellation.is_cancelled() => Err(cancellation_failure(deadline_was_elapsed)),
        Ok(Ok(response)) => validate_and_encode_response(
            &domain_request_identity(
                &expected_request_id,
                &expected_corpus_id,
                &job_id,
                attempt,
                fence,
            ),
            response,
        ),
        Ok(Err(error)) => {
            if error.code == Code::Cancelled && deadline_was_elapsed {
                Err(cancellation_failure(true))
            } else {
                Err(StoredFailure::new(error.code, error.message))
            }
        }
        Err(error) => Err(StoredFailure::new(
            Code::Internal,
            format!("worker task failed: {error}"),
        )),
    };
    complete_batch(
        &inner,
        BatchKey {
            job_id: &job_id,
            attempt,
            fence,
            request_fingerprint,
        },
        &cancellation,
        effective_deadline,
        outcome,
        sender,
    );
    deadline_task.abort();
}

fn validate_and_encode_response(
    expected: &ResponseIdentity<'_>,
    response: jobs::ProcessBatchResponse,
) -> BatchOutcome {
    validate_process_response(expected, &response).map_err(StoredFailure::from)?;
    encode_transport(&response).map_err(StoredFailure::from)
}

fn cancellation_failure(deadline_elapsed: bool) -> StoredFailure {
    if deadline_elapsed {
        StoredFailure::new(Code::DeadlineExceeded, "worker attempt deadline elapsed")
    } else {
        StoredFailure::new(Code::Cancelled, "worker attempt was cancelled")
    }
}

fn complete_batch<P: BatchProcessor>(
    inner: &WorkerInner<P>,
    key: BatchKey<'_>,
    cancellation: &Cancellation,
    effective_deadline: tokio::time::Instant,
    mut outcome: BatchOutcome,
    sender: oneshot::Sender<BatchOutcome>,
) {
    match inner.jobs.lock() {
        Ok(mut jobs) => match jobs.get_mut(key.job_id) {
            Some(record)
                if record.attempt == key.attempt
                    && record.fence == key.fence
                    && record.request_fingerprint == key.request_fingerprint =>
            {
                if tokio::time::Instant::now() >= effective_deadline {
                    cancellation.cancel(true);
                }
                if outcome.is_ok() && cancellation.is_cancelled() {
                    outcome = Err(cancellation_failure(cancellation.deadline_elapsed()));
                }
                record.finished = true;
                match &outcome {
                    Ok(response) => {
                        record.completed_items = record.total_items;
                        record.response = Some(response.clone());
                        record.failure = None;
                    }
                    Err(failure) => {
                        record.response = None;
                        record.failure = Some(failure.clone());
                    }
                }
            }
            _ => {
                outcome = Err(StoredFailure::new(
                    Code::FailedPrecondition,
                    "worker result became stale",
                ));
            }
        },
        Err(_) => {
            outcome = Err(StoredFailure::new(
                Code::Internal,
                "worker status registry lock is poisoned",
            ));
        }
    }
    let _ = sender.send(outcome);
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

fn grpc_timeout(metadata: &MetadataMap) -> Result<Option<std::time::Duration>, Status> {
    let Some(value) = metadata.get("grpc-timeout") else {
        return Ok(None);
    };
    let raw = value
        .to_str()
        .map_err(|_| Status::invalid_argument("grpc-timeout is not ASCII"))?;
    if !(2..=9).contains(&raw.len()) {
        return Err(Status::invalid_argument("grpc-timeout has invalid length"));
    }
    let (digits, unit) = raw.split_at(raw.len() - 1);
    if digits.is_empty() || digits.len() > 8 || !digits.bytes().all(|byte| byte.is_ascii_digit()) {
        return Err(Status::invalid_argument("grpc-timeout has invalid digits"));
    }
    let amount = digits
        .parse::<u64>()
        .map_err(|_| Status::invalid_argument("grpc-timeout is out of range"))?;
    let duration = match unit {
        "H" => std::time::Duration::from_secs(
            amount
                .checked_mul(60 * 60)
                .ok_or_else(|| Status::invalid_argument("grpc-timeout is out of range"))?,
        ),
        "M" => std::time::Duration::from_secs(
            amount
                .checked_mul(60)
                .ok_or_else(|| Status::invalid_argument("grpc-timeout is out of range"))?,
        ),
        "S" => std::time::Duration::from_secs(amount),
        "m" => std::time::Duration::from_millis(amount),
        "u" => std::time::Duration::from_micros(amount),
        "n" => std::time::Duration::from_nanos(amount),
        _ => return Err(Status::invalid_argument("grpc-timeout has invalid unit")),
    };
    Ok(Some(duration))
}

fn fingerprint_request(request: &jobs::ProcessBatchRequest) -> Result<[u8; 32], Status> {
    let bytes = request
        .write_to_bytes()
        .map_err(|error| Status::internal(format!("encode request fingerprint: {error}")))?;
    Ok(Sha256::digest(bytes).into())
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
        invalid_response: bool,
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
                request_id: if self.invalid_response {
                    "wrong-request".to_owned()
                } else {
                    request.context.request_id.clone()
                },
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
                invalid_response: false,
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
    async fn binds_cached_result_to_the_exact_request_payload() {
        let calls = Arc::new(AtomicUsize::new(0));
        let service = WorkerService::new(
            EchoProcessor {
                calls: Arc::clone(&calls),
                wait_for_cancel: false,
                invalid_response: false,
            },
            WorkerServiceConfig::default(),
        )
        .unwrap();
        let request = transport_request(60);
        service
            .process_batch(Request::new(request.clone()))
            .await
            .unwrap();

        let mut changed = request;
        changed.context.as_mut().unwrap().trace_id = "different-trace".to_owned();
        let error = service
            .process_batch(Request::new(changed))
            .await
            .unwrap_err();
        assert_eq!(error.code(), Code::FailedPrecondition);
        assert_eq!(calls.load(Ordering::Acquire), 1);
    }

    #[tokio::test]
    async fn caches_invalid_processor_response_as_terminal_failure() {
        let calls = Arc::new(AtomicUsize::new(0));
        let service = WorkerService::new(
            EchoProcessor {
                calls: Arc::clone(&calls),
                wait_for_cancel: false,
                invalid_response: true,
            },
            WorkerServiceConfig::default(),
        )
        .unwrap();
        let request = transport_request(60);
        let first = service
            .process_batch(Request::new(request.clone()))
            .await
            .unwrap_err();
        let second = service
            .process_batch(Request::new(request))
            .await
            .unwrap_err();
        assert_eq!(first.code(), Code::FailedPrecondition);
        assert_eq!(second.code(), first.code());
        assert_eq!(calls.load(Ordering::Acquire), 1);
    }

    #[tokio::test]
    async fn rejects_elapsed_body_deadline_before_processor() {
        let calls = Arc::new(AtomicUsize::new(0));
        let service = WorkerService::new(
            EchoProcessor {
                calls: Arc::clone(&calls),
                wait_for_cancel: false,
                invalid_response: false,
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
                invalid_response: false,
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
