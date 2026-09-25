//! Async batch embedding client for the pinned C++ service, consumed by indexing.
//! Reuses one explicit Tonic channel, preserves C01 identities and partial errors,
//! and rejects model/result drift. Dropping the future cancels transport; no retries.
//! Bounded 128-item/4-MiB batches include up to 4096 scalars per embedding in validation.
//! Queue-inclusive throughput and parity gates remain configs/benchmark-targets.yaml.
use crate::{
    domain::wire::{self, Limits},
    wire::{common, inference},
    worker::transport,
};
use prost::Message as ProstMessage;
use protobuf::Message as DomainMessage;
use std::{
    collections::HashSet,
    time::{Duration, SystemTime, UNIX_EPOCH},
};
use tonic::{
    transport::{Channel, Endpoint},
    Request, Status,
};

const MAX_BYTES: usize = 4 * 1024 * 1024;

#[derive(Clone)]
pub struct NativeEmbeddingClient {
    client: transport::inference_client::InferenceClient<Channel>,
    model: common::ModelManifest,
}

impl NativeEmbeddingClient {
    /// Connect explicitly at bootstrap. Current native executable accepts loopback only.
    pub async fn connect(address: String, model: common::ModelManifest) -> Result<Self, Status> {
        validate_model(&model)?;
        let endpoint = Endpoint::from_shared(address)
            .map_err(|_| Status::invalid_argument("invalid native endpoint"))?;
        let uri = endpoint.uri();
        let loopback = uri
            .host()
            .and_then(|h| h.trim_matches(['[', ']']).parse::<std::net::IpAddr>().ok())
            .is_some_and(|ip| ip.is_loopback());
        if uri.scheme_str() != Some("http") || !loopback {
            return Err(Status::invalid_argument(
                "native endpoint must be loopback HTTP",
            ));
        }
        let channel = endpoint
            .connect_timeout(Duration::from_secs(5))
            .connect()
            .await
            .map_err(|_| Status::unavailable("native inference connection failed"))?;
        Ok(Self {
            client: transport::inference_client::InferenceClient::new(channel)
                .max_decoding_message_size(MAX_BYTES)
                .max_encoding_message_size(MAX_BYTES),
            model,
        })
    }

    pub async fn check_ready(&self, context: common::RequestContext) -> Result<(), Status> {
        let request = inference::CapabilitiesRequest {
            context: protobuf::MessageField::some(context),
            ..Default::default()
        };
        wire::validate(&request, request_limits()).map_err(Status::invalid_argument)?;
        let mut rpc = Request::new(to_transport::<transport::CapabilitiesRequest, _>(&request)?);
        rpc.set_timeout(timeout(&request.context)?);
        let result = self
            .client
            .clone()
            .get_capabilities(rpc)
            .await?
            .into_inner();
        let result: inference::CapabilitiesResponse = to_domain(&result)?;
        wire::validate(&result, request_limits()).map_err(Status::failed_precondition)?;
        let matches: Vec<_> = result
            .models
            .iter()
            .filter(|cap| cap.model.as_ref() == Some(&self.model))
            .collect();
        if !result.ready
            || matches.len() != 1
            || !matches[0].ready
            || matches[0].limits.max_items < 128
            || matches[0].limits.max_bytes < MAX_BYTES as u64
            || matches[0].limits.max_tokens < self.model.max_tokens
            || !result.supported_tasks.contains(&self.model.task)
        {
            return Err(Status::failed_precondition(
                "native embedding model/limits not ready",
            ));
        }
        Ok(())
    }

    pub async fn embed_batch(
        &self,
        request: inference::EmbedBatchRequest,
    ) -> Result<inference::EmbedBatchResponse, Status> {
        wire::validate(&request, request_limits()).map_err(Status::invalid_argument)?;
        if request.model.as_ref() != Some(&self.model)
            || request.items.is_empty()
            || request.items.len() > 128
        {
            return Err(Status::invalid_argument(
                "native embedding model/batch mismatch",
            ));
        }
        let ids: HashSet<_> = request.items.iter().map(|item| &item.item_id).collect();
        if ids.len() != request.items.len() {
            return Err(Status::invalid_argument("duplicate embedding IDs"));
        }
        let mut rpc = Request::new(to_transport::<transport::EmbedBatchRequest, _>(&request)?);
        rpc.set_timeout(timeout(&request.context)?);
        let result = self.client.clone().embed_batch(rpc).await?.into_inner();
        let result: inference::EmbedBatchResponse = to_domain(&result)?;
        validate_response(&request, &result)?;
        Ok(result)
    }
}

fn request_limits() -> Limits {
    Limits {
        max_bytes: MAX_BYTES,
        ..Limits::default()
    }
}
fn validate_model(model: &common::ModelManifest) -> Result<(), Status> {
    wire::validate(model, request_limits()).map_err(Status::invalid_argument)?;
    if model.task.enum_value() != Ok(common::ModelTask::MODEL_TASK_EMBED)
        || model.dimensions.is_none_or(|d| d == 0 || d > 4096)
        || model.max_tokens > 8192
        || model.pooling != "cls"
        || model.normalization != "l2"
    {
        return Err(Status::invalid_argument(
            "unsupported native embedding profile",
        ));
    }
    Ok(())
}

fn timeout(context: &protobuf::MessageField<common::RequestContext>) -> Result<Duration, Status> {
    let deadline = context
        .deadline
        .as_ref()
        .ok_or_else(|| Status::invalid_argument("deadline required"))?;
    if deadline.seconds < 0 || !(0..1_000_000_000).contains(&deadline.nanos) {
        return Err(Status::invalid_argument("invalid deadline"));
    }
    let target = UNIX_EPOCH
        .checked_add(Duration::new(
            deadline.seconds as u64,
            deadline.nanos as u32,
        ))
        .ok_or_else(|| Status::invalid_argument("deadline overflow"))?;
    let left = target
        .duration_since(SystemTime::now())
        .map_err(|_| Status::deadline_exceeded("deadline elapsed"))?;
    if left > Duration::from_secs(1800) {
        return Err(Status::invalid_argument("deadline exceeds 30 minutes"));
    }
    Ok(left)
}

fn to_transport<T: ProstMessage + Default, D: DomainMessage>(value: &D) -> Result<T, Status> {
    let bytes = value
        .write_to_bytes()
        .map_err(|_| Status::internal("wire encoding failed"))?;
    T::decode(bytes.as_slice()).map_err(|_| Status::internal("transport decoding failed"))
}
fn to_domain<T: ProstMessage, D: DomainMessage>(value: &T) -> Result<D, Status> {
    D::parse_from_bytes(&value.encode_to_vec())
        .map_err(|_| Status::failed_precondition("native response decoding failed"))
}

fn validate_response(
    request: &inference::EmbedBatchRequest,
    result: &inference::EmbedBatchResponse,
) -> Result<(), Status> {
    wire::validate(
        result,
        Limits {
            max_items: 128 * 4096 + 100000,
            ..request_limits()
        },
    )
    .map_err(Status::failed_precondition)?;
    let reject = || Status::failed_precondition("native result correlation/tensor mismatch");
    if result.request_id != request.context.request_id || result.model != request.model {
        return Err(reject());
    }
    let mut remaining: HashSet<_> = request.items.iter().map(|item| &item.item_id).collect();
    for item in &result.results {
        if !remaining.remove(&item.item_id) {
            return Err(reject());
        }
        match &item.result {
            Some(inference::embedding_result::Result::Embedding(embedding)) => {
                let trunc = &embedding.truncation;
                if embedding.values.len() != request.model.dimensions.unwrap_or(0) as usize
                    || embedding.input_tokens == 0
                    || embedding.input_tokens > request.model.max_tokens
                    || trunc.is_none()
                    || trunc.truncated
                    || trunc.original_tokens != embedding.input_tokens as u64
                    || trunc.retained_tokens != embedding.input_tokens as u64
                    || embedding.values.iter().any(|x| !x.is_finite())
                    || (embedding
                        .values
                        .iter()
                        .map(|x| (*x as f64).powi(2))
                        .sum::<f64>()
                        - 1.0)
                        .abs()
                        > 0.01
                {
                    return Err(reject());
                }
            }
            Some(inference::embedding_result::Result::Error(error)) => {
                if error.item_id.as_ref() != Some(&item.item_id) {
                    return Err(reject());
                }
            }
            None => return Err(reject()),
        }
    }
    if !remaining.is_empty() {
        return Err(reject());
    }
    Ok(())
}

#[cfg(test)]
#[path = "native_inference_tests.rs"]
mod tests;
