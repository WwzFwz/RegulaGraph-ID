//! Native embedding regressions: scalar budgets, correlation and real opt-in C++ RPC.
//! Deterministic fixtures verify boundaries; actual BGE parity is a separate evidence run.
use super::*;
use protobuf::{EnumOrUnknown, MessageField};

fn hash(c: char) -> MessageField<common::ContentHash> {
    MessageField::some(common::ContentHash {
        sha256: c.to_string().repeat(64),
        ..Default::default()
    })
}
fn model() -> common::ModelManifest {
    common::ModelManifest {
        model_id: "fixture:embed".into(),
        version: "v1".into(),
        weights_hash: hash('a'),
        tokenizer_hash: hash('b'),
        dimensions: Some(1024),
        task: EnumOrUnknown::new(common::ModelTask::MODEL_TASK_EMBED),
        max_tokens: 8192,
        precision: "fp32".into(),
        backend: "onnxruntime:1.22.0:cpu".into(),
        pooling: "cls".into(),
        normalization: "l2".into(),
        ..Default::default()
    }
}
fn context() -> common::RequestContext {
    let until = SystemTime::now().duration_since(UNIX_EPOCH).unwrap() + Duration::from_secs(60);
    common::RequestContext {
        schema_version: 1,
        request_id: "request:native".into(),
        trace_id: "trace:native".into(),
        corpus_id: "corpus:test".into(),
        auth_scope_ref: "test:local".into(),
        config_fingerprint: hash('c'),
        deadline: MessageField::some(protobuf::well_known_types::timestamp::Timestamp {
            seconds: until.as_secs() as i64,
            nanos: until.subsec_nanos() as i32,
            ..Default::default()
        }),
        ..Default::default()
    }
}
fn request(count: usize) -> inference::EmbedBatchRequest {
    inference::EmbedBatchRequest {
        context: MessageField::some(context()),
        model: MessageField::some(model()),
        purpose: EnumOrUnknown::new(inference::EmbeddingPurpose::EMBEDDING_PURPOSE_DOCUMENT),
        operation_key: "operation:test".into(),
        items: (0..count)
            .map(|i| inference::TextItem {
                item_id: format!("item:{i}"),
                text: "Pasal 1 izin usaha".into(),
                ..Default::default()
            })
            .collect(),
        ..Default::default()
    }
}
fn response(req: &inference::EmbedBatchRequest) -> inference::EmbedBatchResponse {
    let mut values = vec![0.0; req.model.dimensions.unwrap() as usize];
    values[0] = 1.0;
    inference::EmbedBatchResponse {
        request_id: req.context.request_id.clone(),
        model: req.model.clone(),
        results: req
            .items
            .iter()
            .map(|i| inference::EmbeddingResult {
                item_id: i.item_id.clone(),
                result: Some(inference::embedding_result::Result::Embedding(
                    inference::Embedding {
                        values: values.clone(),
                        input_tokens: 5,
                        truncation: MessageField::some(common::TruncationInfo {
                            original_tokens: 5,
                            retained_tokens: 5,
                            ..Default::default()
                        }),
                        ..Default::default()
                    },
                )),
                ..Default::default()
            })
            .collect(),
        ..Default::default()
    }
}

#[test]
fn validates_full_batch_and_rejects_duplicate_missing_nonfinite_and_drift() {
    let req = request(128);
    let good = response(&req);
    validate_model(&model()).unwrap();
    validate_response(&req, &good).unwrap();
    let mut bad = good.clone();
    bad.results[1] = bad.results[0].clone();
    assert!(validate_response(&req, &bad).is_err());
    let mut bad = good.clone();
    bad.results.pop();
    assert!(validate_response(&req, &bad).is_err());
    let mut bad = good.clone();
    bad.model.as_mut().unwrap().version = "changed".into();
    assert!(validate_response(&req, &bad).is_err());
    let mut bad = good.clone();
    if let Some(inference::embedding_result::Result::Embedding(e)) = &mut bad.results[0].result {
        e.values[0] = f32::NAN;
    }
    assert!(validate_response(&req, &bad).is_err());
}

#[tokio::test]
#[ignore = "requires explicit real native endpoint and binary C01 model manifest"]
async fn real_native_embedding_transport() {
    let address = std::env::var("REGULAGRAPH_TEST_NATIVE_ENDPOINT").unwrap();
    let bytes =
        std::fs::read(std::env::var("REGULAGRAPH_TEST_NATIVE_EMBED_BINARY").unwrap()).unwrap();
    let manifest = common::ModelManifest::parse_from_bytes(&bytes).unwrap();
    let client = NativeEmbeddingClient::connect(format!("http://{address}"), manifest.clone())
        .await
        .unwrap();
    client.check_ready(context()).await.unwrap();
    let mut req = request(2);
    req.model = MessageField::some(manifest);
    let result = client.embed_batch(req.clone()).await.unwrap();
    assert!(result.results.iter().all(|i| matches!(
        i.result,
        Some(inference::embedding_result::Result::Embedding(_))
    )));
    req.items[0].text = "izin ".repeat(8193);
    let result = client.embed_batch(req).await.unwrap();
    assert!(matches!(
        result.results[0].result,
        Some(inference::embedding_result::Result::Error(_))
    ));
    assert!(matches!(
        result.results[1].result,
        Some(inference::embedding_result::Result::Embedding(_))
    ));
}
