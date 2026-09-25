//! Builds one bounded, model-pinned dense document batch for X01.
//!
//! The caller supplies verified chunk text with explicit provenance; output preserves
//! input order and model identity. Any missing/error/truncated item fails the
//! entire batch. This module does not publish or substitute vectors. Measure
//! queue time, p95/p99, throughput, RSS, and recall against
//! configs/benchmark-targets.yaml (REQUIRED_UNMEASURED).

use crate::adapters::native_inference::NativeEmbeddingClient;
use crate::domain::wire::{self, Limits};
use crate::wire::{common, evidence, inference};
use protobuf::{EnumOrUnknown, MessageField};
use std::collections::{HashMap, HashSet};
use std::sync::atomic::{AtomicBool, Ordering};
use std::time::Duration;
use tonic::Status;

const MAX_ITEMS: usize = 128;
const MAX_TEXT_BYTES: usize = 2 * 1024 * 1024;

/// Embeds one batch; the caller advances its immutable input cursor only after
/// successful return. The externally pinned operation key identifies this batch.
pub async fn embed_document_batch(
    client: &NativeEmbeddingClient,
    context: common::RequestContext,
    model: common::ModelManifest,
    items: Vec<inference::TextItem>,
    operation_key: String,
    cancelled: &AtomicBool,
) -> Result<Vec<(String, evidence::DenseVector)>, Status> {
    if cancelled.load(Ordering::Acquire) {
        return Err(Status::cancelled("index embedding cancelled"));
    }
    if items.is_empty() || items.len() > MAX_ITEMS {
        return Err(Status::invalid_argument(
            "index embedding item limit exceeded",
        ));
    }
    let mut bytes = 0usize;
    let mut seen = HashSet::with_capacity(items.len());
    for item in &items {
        bytes = bytes
            .checked_add(item.text.len())
            .ok_or_else(|| Status::out_of_range("index embedding byte count overflow"))?;
        if item.item_id.is_empty()
            || item.text.is_empty()
            || !seen.insert(&item.item_id)
            || !item
                .provenance
                .as_ref()
                .is_some_and(|p| !p.sources.is_empty() && !p.spans.is_empty())
        {
            return Err(Status::invalid_argument(
                "index embedding requires unique sourced chunk text",
            ));
        }
    }
    if bytes > MAX_TEXT_BYTES {
        return Err(Status::resource_exhausted(
            "index embedding text limit exceeded",
        ));
    }
    let request = inference::EmbedBatchRequest {
        context: MessageField::some(context),
        model: MessageField::some(model),
        items,
        purpose: EnumOrUnknown::new(inference::EmbeddingPurpose::EMBEDDING_PURPOSE_DOCUMENT),
        operation_key,
        ..Default::default()
    };
    wire::validate(&request, Limits::default()).map_err(Status::invalid_argument)?;
    let response = tokio::select! {
        biased;
        _ = wait_for_cancellation(cancelled) => return Err(Status::cancelled("index embedding cancelled")),
        result = client.embed_batch(request.clone()) => result?,
    };
    if cancelled.load(Ordering::Acquire) {
        return Err(Status::cancelled("index embedding cancelled"));
    }
    materialize_dense_vectors(&request, &response)
}

async fn wait_for_cancellation(cancelled: &AtomicBool) {
    loop {
        if cancelled.load(Ordering::Acquire) {
            return;
        }
        tokio::time::sleep(Duration::from_millis(25)).await;
    }
}

/// Converts a one-to-one native response into ordered, all-or-nothing vectors.
fn materialize_dense_vectors(
    request: &inference::EmbedBatchRequest,
    response: &inference::EmbedBatchResponse,
) -> Result<Vec<(String, evidence::DenseVector)>, Status> {
    let reject = || Status::failed_precondition("incomplete or drifted index embedding batch");
    if response.request_id != request.context.request_id
        || response.model != request.model
        || response.results.len() != request.items.len()
        || request.purpose.enum_value()
            != Ok(inference::EmbeddingPurpose::EMBEDDING_PURPOSE_DOCUMENT)
    {
        return Err(reject());
    }
    let model = request.model.as_ref().ok_or_else(reject)?;
    let dimensions = model.dimensions.ok_or_else(reject)?;
    let mut results = HashMap::with_capacity(response.results.len());
    for result in &response.results {
        let embedding = match &result.result {
            Some(inference::embedding_result::Result::Embedding(value)) => value,
            Some(inference::embedding_result::Result::Error(error)) => {
                return Err(item_error_status(error));
            }
            _ => return Err(reject()),
        };
        let truncation = embedding.truncation.as_ref().ok_or_else(reject)?;
        let norm = embedding
            .values
            .iter()
            .map(|v| f64::from(*v) * f64::from(*v))
            .sum::<f64>();
        if embedding.values.len() != dimensions as usize
            || embedding.input_tokens == 0
            || embedding.input_tokens > model.max_tokens
            || truncation.truncated
            || truncation.original_tokens != embedding.input_tokens as u64
            || truncation.retained_tokens != embedding.input_tokens as u64
            || !norm.is_finite()
            || (norm - 1.0).abs() > 0.01
            || results.insert(result.item_id.as_str(), embedding).is_some()
        {
            return Err(reject());
        }
    }
    let mut output = Vec::with_capacity(request.items.len());
    for item in &request.items {
        let embedding = results.remove(item.item_id.as_str()).ok_or_else(reject)?;
        output.push((
            item.item_id.clone(),
            evidence::DenseVector {
                values: embedding.values.clone(),
                dimensions,
                model_id: model.model_id.clone(),
                ..Default::default()
            },
        ));
    }
    if !results.is_empty() {
        return Err(reject());
    }
    Ok(output)
}

fn item_error_status(error: &common::OperationError) -> Status {
    use common::ErrorCode;
    let message = "native index embedding item failed";
    match error.code.enum_value() {
        Ok(ErrorCode::ERROR_CODE_RESOURCE_EXHAUSTED) => Status::resource_exhausted(message),
        Ok(ErrorCode::ERROR_CODE_UNAVAILABLE) => Status::unavailable(message),
        Ok(ErrorCode::ERROR_CODE_DEADLINE_EXCEEDED) => Status::deadline_exceeded(message),
        Ok(ErrorCode::ERROR_CODE_CANCELLED) => Status::cancelled(message),
        Ok(ErrorCode::ERROR_CODE_INVALID_ARGUMENT) => Status::invalid_argument(message),
        Ok(ErrorCode::ERROR_CODE_TOO_LARGE) => Status::invalid_argument(message),
        Ok(ErrorCode::ERROR_CODE_INTERNAL) => Status::internal(message),
        _ => Status::failed_precondition(message),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::wire::inference::embedding_result::Result as ItemResult;

    fn fixture() -> (inference::EmbedBatchRequest, inference::EmbedBatchResponse) {
        let model = common::ModelManifest {
            model_id: "model:embed".into(),
            dimensions: Some(2),
            max_tokens: 32,
            ..Default::default()
        };
        let request = inference::EmbedBatchRequest {
            context: MessageField::some(common::RequestContext {
                request_id: "request:1".into(),
                ..Default::default()
            }),
            model: MessageField::some(model.clone()),
            items: vec![
                inference::TextItem {
                    item_id: "chunk:a".into(),
                    text: "pasal satu".into(),
                    ..Default::default()
                },
                inference::TextItem {
                    item_id: "chunk:b".into(),
                    text: "pasal dua".into(),
                    ..Default::default()
                },
            ],
            purpose: EnumOrUnknown::new(inference::EmbeddingPurpose::EMBEDDING_PURPOSE_DOCUMENT),
            ..Default::default()
        };
        let item = |id: &str, values| inference::EmbeddingResult {
            item_id: id.into(),
            result: Some(ItemResult::Embedding(inference::Embedding {
                values,
                input_tokens: 2,
                truncation: MessageField::some(common::TruncationInfo {
                    original_tokens: 2,
                    retained_tokens: 2,
                    ..Default::default()
                }),
                ..Default::default()
            })),
            ..Default::default()
        };
        let response = inference::EmbedBatchResponse {
            request_id: "request:1".into(),
            model: MessageField::some(model),
            results: vec![
                item("chunk:b", vec![0.0, 1.0]),
                item("chunk:a", vec![1.0, 0.0]),
            ],
            ..Default::default()
        };
        (request, response)
    }

    #[test]
    fn restores_input_order_and_model_binding() {
        let (request, response) = fixture();
        let output = materialize_dense_vectors(&request, &response).unwrap();
        assert_eq!(output[0].0, "chunk:a");
        assert_eq!(output[0].1.values, vec![1.0, 0.0]);
        assert_eq!(output[1].1.model_id, "model:embed");
    }

    #[test]
    fn rejects_partial_drifted_or_invalid_result() {
        let (request, response) = fixture();
        let mut cases = Vec::new();
        let mut missing = response.clone();
        missing.results.pop();
        cases.push(missing);
        let mut duplicate = response.clone();
        duplicate.results[0].item_id = "chunk:a".into();
        cases.push(duplicate);
        let mut drift = response.clone();
        drift.model.as_mut().unwrap().model_id = "other".into();
        cases.push(drift);
        let mut zero = response.clone();
        if let Some(ItemResult::Embedding(ref mut e)) = zero.results[0].result {
            e.values = vec![0.0, 0.0];
        }
        cases.push(zero);
        let mut truncated = response.clone();
        if let Some(ItemResult::Embedding(ref mut e)) = truncated.results[0].result {
            e.truncation.as_mut().unwrap().truncated = true;
        }
        cases.push(truncated);
        for result in cases {
            assert!(materialize_dense_vectors(&request, &result).is_err());
        }
    }

    #[test]
    fn preserves_native_retry_and_deadline_classification() {
        let (request, response) = fixture();
        for (code, expected) in [
            (
                common::ErrorCode::ERROR_CODE_RESOURCE_EXHAUSTED,
                tonic::Code::ResourceExhausted,
            ),
            (
                common::ErrorCode::ERROR_CODE_UNAVAILABLE,
                tonic::Code::Unavailable,
            ),
            (
                common::ErrorCode::ERROR_CODE_DEADLINE_EXCEEDED,
                tonic::Code::DeadlineExceeded,
            ),
            (
                common::ErrorCode::ERROR_CODE_TOO_LARGE,
                tonic::Code::InvalidArgument,
            ),
        ] {
            let mut result = response.clone();
            let retryable = code != common::ErrorCode::ERROR_CODE_TOO_LARGE;
            result.results[0].result = Some(ItemResult::Error(common::OperationError {
                code: EnumOrUnknown::new(code),
                retryable,
                item_id: Some("chunk:b".into()),
                ..Default::default()
            }));
            assert_eq!(
                materialize_dense_vectors(&request, &result)
                    .unwrap_err()
                    .code(),
                expected
            );
        }
    }

    #[tokio::test]
    async fn cancellation_wakes_while_rpc_would_still_be_running() {
        let flag = AtomicBool::new(false);
        let slow = tokio::time::sleep(Duration::from_secs(5));
        tokio::pin!(slow);
        let cancel = async {
            tokio::time::sleep(Duration::from_millis(5)).await;
            flag.store(true, Ordering::Release);
        };
        let ((), ()) = tokio::join!(cancel, async {
            tokio::select! {
                _ = wait_for_cancellation(&flag) => (),
                _ = &mut slow => panic!("cancellation lost to slow inference"),
            }
        });
    }
}
