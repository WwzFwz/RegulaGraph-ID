//! Batch client for the Go semantic gateway used by Rust graph extraction.
//!
//! The adapter validates C01 before and after transport, applies the request deadline, preserves
//! one-to-one item correlation, and performs no implicit retry. Connections are opened explicitly
//! at runtime bootstrap and reused; queue/compute latency and throughput remain REQUIRED_UNMEASURED.

use crate::domain::wire::{self, Limits};
use crate::wire::{common, inference};
use crate::worker::transport;
use prost::Message as ProstMessage;
use protobuf::Message as ProtobufMessage;
use std::collections::HashSet;
use std::error::Error;
use std::fmt::{Display, Formatter};
use std::sync::atomic::{AtomicBool, Ordering};
use std::time::{Duration, SystemTime, UNIX_EPOCH};
use tonic::transport::{Channel, Endpoint};
use tonic::{Code, Request};

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct SemanticClientConfig {
    pub maximum_message_bytes: usize,
    pub connect_timeout: Duration,
}

impl Default for SemanticClientConfig {
    fn default() -> Self {
        Self {
            maximum_message_bytes: 16 << 20,
            connect_timeout: Duration::from_secs(5),
        }
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct SemanticClientError {
    code: Code,
    message: String,
}

impl SemanticClientError {
    fn new(code: Code, message: impl Into<String>) -> Self {
        Self {
            code,
            message: message.into(),
        }
    }

    pub fn code(&self) -> Code {
        self.code
    }
}

impl Display for SemanticClientError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        write!(formatter, "{}", self.message)
    }
}

impl Error for SemanticClientError {}

pub trait ExtractionInference: Send + Sync + 'static {
    fn extract_batch(
        &self,
        request: inference::ExtractBatchRequest,
        cancelled: &AtomicBool,
    ) -> Result<inference::ExtractBatchResponse, SemanticClientError>;
}

#[derive(Clone)]
pub struct TonicSemanticClient {
    client: transport::semantic_client::SemanticClient<Channel>,
    runtime: tokio::runtime::Handle,
}

impl TonicSemanticClient {
    pub async fn connect(
        endpoint: &str,
        config: SemanticClientConfig,
    ) -> Result<Self, SemanticClientError> {
        if config.maximum_message_bytes == 0 || config.connect_timeout.is_zero() {
            return Err(SemanticClientError::new(
                Code::InvalidArgument,
                "semantic client limits must be positive",
            ));
        }
        let endpoint = Endpoint::from_shared(endpoint.to_owned())
            .map_err(|error| SemanticClientError::new(Code::InvalidArgument, error.to_string()))?
            .connect_timeout(config.connect_timeout);
        let channel = endpoint
            .connect()
            .await
            .map_err(|error| SemanticClientError::new(Code::Unavailable, error.to_string()))?;
        let client = transport::semantic_client::SemanticClient::new(channel)
            .max_decoding_message_size(config.maximum_message_bytes)
            .max_encoding_message_size(config.maximum_message_bytes);
        Ok(Self {
            client,
            runtime: tokio::runtime::Handle::current(),
        })
    }
}

impl ExtractionInference for TonicSemanticClient {
    fn extract_batch(
        &self,
        request: inference::ExtractBatchRequest,
        cancelled: &AtomicBool,
    ) -> Result<inference::ExtractBatchResponse, SemanticClientError> {
        if cancelled.load(Ordering::Acquire) {
            return Err(SemanticClientError::new(
                Code::Cancelled,
                "semantic extraction cancelled before dispatch",
            ));
        }
        validate_extract_request(&request)?;
        let timeout = remaining_timeout(&request.batch.context.deadline)?;
        let expected_request_id = request.batch.context.request_id.clone();
        let expected_model = request.batch.model.clone();
        let expected_items: HashSet<String> = request
            .items
            .iter()
            .map(|item| item.item_id.clone())
            .collect();
        let transport_request: transport::ExtractBatchRequest = encode_transport(&request)?;
        let mut rpc_request = Request::new(transport_request);
        rpc_request.set_timeout(timeout);
        let mut client = self.client.clone();
        let response = self
            .runtime
            .block_on(async move { client.extract_batch(rpc_request).await })
            .map_err(|status| SemanticClientError::new(status.code(), status.message()))?
            .into_inner();
        if cancelled.load(Ordering::Acquire) {
            return Err(SemanticClientError::new(
                Code::Cancelled,
                "semantic extraction cancelled before result acceptance",
            ));
        }
        let response: inference::ExtractBatchResponse = decode_domain(&response)?;
        validate_extract_response(
            &response,
            &expected_request_id,
            &expected_model,
            &expected_items,
        )?;
        Ok(response)
    }
}

pub fn validate_extract_request(
    request: &inference::ExtractBatchRequest,
) -> Result<(), SemanticClientError> {
    wire::validate(request, Limits::default())
        .map_err(|detail| SemanticClientError::new(Code::InvalidArgument, detail))?;
    let batch = request
        .batch
        .as_ref()
        .ok_or_else(|| SemanticClientError::new(Code::InvalidArgument, "batch is required"))?;
    if batch.model.task.enum_value() != Ok(common::ModelTask::MODEL_TASK_EXTRACT) {
        return Err(SemanticClientError::new(
            Code::InvalidArgument,
            "semantic extraction requires an EXTRACT model",
        ));
    }
    let item_ids: HashSet<_> = request.items.iter().map(|item| &item.item_id).collect();
    if item_ids.len() != request.items.len() {
        return Err(SemanticClientError::new(
            Code::InvalidArgument,
            "semantic extraction item IDs must be unique",
        ));
    }
    remaining_timeout(&batch.context.deadline)?;
    Ok(())
}

pub fn validate_extract_response(
    response: &inference::ExtractBatchResponse,
    expected_request_id: &str,
    expected_model: &common::ModelManifest,
    expected_items: &HashSet<String>,
) -> Result<(), SemanticClientError> {
    wire::validate(response, Limits::default())
        .map_err(|detail| SemanticClientError::new(Code::FailedPrecondition, detail))?;
    if response.request_id != expected_request_id || response.model.as_ref() != Some(expected_model)
    {
        return Err(SemanticClientError::new(
            Code::FailedPrecondition,
            "semantic extraction response context/model mismatch",
        ));
    }
    let producer = response.producer_manifest.as_ref().ok_or_else(|| {
        SemanticClientError::new(
            Code::FailedPrecondition,
            "semantic extraction response producer manifest is missing",
        )
    })?;
    if !producer.models.iter().any(|model| model == expected_model)
        || expected_model.prompt_hash.as_ref().is_none_or(|prompt| {
            !producer
                .prompt_hashes
                .iter()
                .any(|candidate| candidate == prompt)
        })
    {
        return Err(SemanticClientError::new(
            Code::FailedPrecondition,
            "semantic extraction producer does not bind the model and prompt",
        ));
    }
    let mut missing = expected_items.clone();
    for result in &response.results {
        if !missing.remove(&result.item_id) {
            return Err(SemanticClientError::new(
                Code::FailedPrecondition,
                "semantic extraction returned an unexpected or duplicate item",
            ));
        }
        if result.result.is_none() {
            return Err(SemanticClientError::new(
                Code::FailedPrecondition,
                "semantic extraction result has neither proposal nor error",
            ));
        }
    }
    if !missing.is_empty() {
        return Err(SemanticClientError::new(
            Code::FailedPrecondition,
            "semantic extraction response is missing items",
        ));
    }
    Ok(())
}

fn remaining_timeout(
    deadline: &protobuf::MessageField<protobuf::well_known_types::timestamp::Timestamp>,
) -> Result<Duration, SemanticClientError> {
    let deadline = deadline.as_ref().ok_or_else(|| {
        SemanticClientError::new(Code::InvalidArgument, "request deadline is required")
    })?;
    if deadline.seconds < 0 || deadline.nanos < 0 {
        return Err(SemanticClientError::new(
            Code::InvalidArgument,
            "request deadline is outside the supported runtime range",
        ));
    }
    let deadline = UNIX_EPOCH
        .checked_add(Duration::new(
            deadline.seconds as u64,
            deadline.nanos as u32,
        ))
        .ok_or_else(|| {
            SemanticClientError::new(Code::InvalidArgument, "request deadline overflows")
        })?;
    deadline.duration_since(SystemTime::now()).map_err(|_| {
        SemanticClientError::new(Code::DeadlineExceeded, "request deadline has elapsed")
    })
}

fn encode_transport<T, D>(message: &D) -> Result<T, SemanticClientError>
where
    T: ProstMessage + Default,
    D: ProtobufMessage,
{
    let bytes = message
        .write_to_bytes()
        .map_err(|error| SemanticClientError::new(Code::Internal, error.to_string()))?;
    T::decode(bytes.as_slice())
        .map_err(|error| SemanticClientError::new(Code::Internal, error.to_string()))
}

fn decode_domain<T, D>(message: &T) -> Result<D, SemanticClientError>
where
    T: ProstMessage,
    D: ProtobufMessage + Default,
{
    D::parse_from_bytes(&message.encode_to_vec())
        .map_err(|error| SemanticClientError::new(Code::FailedPrecondition, error.to_string()))
}

#[cfg(test)]
mod tests {
    use super::*;
    use protobuf::well_known_types::timestamp::Timestamp;
    use protobuf::{EnumOrUnknown, MessageField};

    fn hash(character: char) -> common::ContentHash {
        common::ContentHash {
            sha256: character.to_string().repeat(64),
            ..Default::default()
        }
    }

    fn model() -> common::ModelManifest {
        common::ModelManifest {
            model_id: "model:extract".to_owned(),
            version: "v1".to_owned(),
            weights_hash: MessageField::some(hash('a')),
            tokenizer_hash: MessageField::some(hash('b')),
            task: EnumOrUnknown::new(common::ModelTask::MODEL_TASK_EXTRACT),
            max_tokens: 4096,
            precision: "provider".to_owned(),
            backend: "fixture".to_owned(),
            prompt_hash: MessageField::some(hash('c')),
            ..Default::default()
        }
    }

    fn request() -> inference::ExtractBatchRequest {
        inference::ExtractBatchRequest {
            batch: MessageField::some(inference::SemanticBatchContext {
                context: MessageField::some(common::RequestContext {
                    schema_version: 1,
                    request_id: "request:extract".to_owned(),
                    trace_id: "trace:extract".to_owned(),
                    corpus_id: "regulagraph-id".to_owned(),
                    deadline: MessageField::some(Timestamp {
                        seconds: 4_102_444_800,
                        ..Default::default()
                    }),
                    config_fingerprint: MessageField::some(hash('d')),
                    auth_scope_ref: "scope:ingestion".to_owned(),
                    ..Default::default()
                }),
                model: MessageField::some(model()),
                operation_key: "operation:extract".to_owned(),
                ontology_version: "ontology:v1".to_owned(),
                output_schema: MessageField::some(common::ArtifactRef {
                    artifact_id: "artifact:schema:extract".to_owned(),
                    content_hash: MessageField::some(hash('e')),
                    storage_key: format!("sha256/ee/{}/{}.bin", "e".repeat(2), "e".repeat(64)),
                    media_type: "application/schema+json".to_owned(),
                    byte_size: 1,
                    schema_version: 1,
                    ..Default::default()
                }),
                ..Default::default()
            }),
            items: vec![
                inference::TextItem {
                    item_id: "chunk:a".to_owned(),
                    text: "Pasal 1".to_owned(),
                    ..Default::default()
                },
                inference::TextItem {
                    item_id: "chunk:b".to_owned(),
                    text: "Pasal 2".to_owned(),
                    ..Default::default()
                },
            ],
            ..Default::default()
        }
    }

    fn response_fixture(
        request: &inference::ExtractBatchRequest,
    ) -> inference::ExtractBatchResponse {
        let model = request.batch.model.as_ref().unwrap().clone();
        inference::ExtractBatchResponse {
            request_id: request.batch.context.request_id.clone(),
            results: request
                .items
                .iter()
                .rev()
                .map(|item| inference::ExtractItemResult {
                    item_id: item.item_id.clone(),
                    result: Some(inference::extract_item_result::Result::Proposal(
                        inference::ExtractionProposal::default(),
                    )),
                    ..Default::default()
                })
                .collect(),
            model: request.batch.model.clone(),
            usage: MessageField::some(common::TokenUsage {
                tokenizer_id: "tokenizer:fixture".to_owned(),
                ..Default::default()
            }),
            producer_manifest: MessageField::some(common::ProducerManifest {
                software: "semantic-gateway".to_owned(),
                build: "test".to_owned(),
                schema_version: 1,
                models: vec![model.clone()],
                prompt_hashes: vec![model.prompt_hash.as_ref().unwrap().clone()],
                config_hash: MessageField::some(hash('f')),
                ..Default::default()
            }),
            ..Default::default()
        }
    }

    #[test]
    fn accepts_reordered_one_to_one_results() {
        let request = request();
        validate_extract_request(&request).unwrap();
        let expected: HashSet<_> = request
            .items
            .iter()
            .map(|item| item.item_id.clone())
            .collect();
        validate_extract_response(
            &response_fixture(&request),
            &request.batch.context.request_id,
            &request.batch.model,
            &expected,
        )
        .unwrap();
    }

    #[test]
    fn rejects_duplicate_missing_and_model_drift() {
        let request = request();
        let expected: HashSet<_> = request
            .items
            .iter()
            .map(|item| item.item_id.clone())
            .collect();
        let mut response = response_fixture(&request);
        response.results[1].item_id = response.results[0].item_id.clone();
        assert_eq!(
            validate_extract_response(
                &response,
                &request.batch.context.request_id,
                &request.batch.model,
                &expected,
            )
            .unwrap_err()
            .code(),
            Code::FailedPrecondition
        );
        let mut response = response_fixture(&request);
        response.results.pop();
        assert_eq!(
            validate_extract_response(
                &response,
                &request.batch.context.request_id,
                &request.batch.model,
                &expected,
            )
            .unwrap_err()
            .code(),
            Code::FailedPrecondition
        );
        let mut response = response_fixture(&request);
        response.model.as_mut().unwrap().version = "drift".to_owned();
        assert_eq!(
            validate_extract_response(
                &response,
                &request.batch.context.request_id,
                &request.batch.model,
                &expected,
            )
            .unwrap_err()
            .code(),
            Code::FailedPrecondition
        );
    }
}
