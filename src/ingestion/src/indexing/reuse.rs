//! Computes a versioned, corpus-scoped reuse key for identical document embeddings.
//!
//! The key binds actual rendered bytes, render policy, and every v1 ModelManifest
//! field that can change the vector. It allows vector reuse only; source refs,
//! legal filters, visibility and snapshot membership must be rebuilt and checked
//! for each IndexRecord. Unknown manifest fields fail closed until the key format
//! is reviewed. Measure reuse hit/miss and invalidation after model/policy updates;
//! required targets remain in configs/benchmark-targets.yaml (UNMEASURED).

use crate::indexing::inputs::{RenderedEmbeddingInput, RENDER_POLICY_VERSION};
use crate::wire::common::{self, ModelManifest};
use crate::wire::evidence;
use sha2::{Digest, Sha256};

const KEY_DOMAIN: &[u8] = b"regulagraph-embedding-reuse-v1\0";

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ReuseKeyError {
    InvalidCorpus,
    InvalidRenderedInput,
    InvalidGeneration,
    InvalidModel,
    UnknownModelField,
    FieldTooLarge,
}

/// Requires the generation to declare the same supported rendering policy.
/// The key omits generation ID and lexical statistics so identical dense
/// vectors can survive a compatible dictionary/statistics refresh.
pub fn embedding_reuse_key(
    corpus_id: &str,
    rendered: &RenderedEmbeddingInput,
    generation: &evidence::IndexGeneration,
) -> Result<String, ReuseKeyError> {
    if generation.meta.corpus_id != corpus_id
        || generation.embedding_input_policy != rendered.policy_version
        || generation.embedding_input_policy != RENDER_POLICY_VERSION
        || generation.filter_format.enum_value()
            != Ok(evidence::IndexFilterFormat::INDEX_FILTER_FORMAT_PAIRED_PROVISION_V1)
        || generation
            .special_fields
            .unknown_fields()
            .iter()
            .next()
            .is_some()
        || generation
            .meta
            .special_fields
            .unknown_fields()
            .iter()
            .next()
            .is_some()
    {
        return Err(ReuseKeyError::InvalidGeneration);
    }
    let model = generation
        .dense_manifest
        .as_ref()
        .ok_or(ReuseKeyError::InvalidGeneration)?;
    reuse_key_from_model(corpus_id, rendered, model)
}

/// Returns a stable SHA-256 hex digest; it is not an IndexRecord ID or proof of
/// authenticated source text. The caller first verifies the TextArtifact and
/// complete DocumentBatch.
fn reuse_key_from_model(
    corpus_id: &str,
    rendered: &RenderedEmbeddingInput,
    model: &ModelManifest,
) -> Result<String, ReuseKeyError> {
    if corpus_id.is_empty()
        || corpus_id.len() > 256
        || !corpus_id.bytes().all(|byte| (33..=126).contains(&byte))
    {
        return Err(ReuseKeyError::InvalidCorpus);
    }
    if rendered.chunk_id.is_empty()
        || rendered.text_artifact_id.is_empty()
        || rendered.text.is_empty()
        || rendered.policy_version != RENDER_POLICY_VERSION
        || hex_sha256(rendered.text.as_bytes()) != rendered.sha256
    {
        return Err(ReuseKeyError::InvalidRenderedInput);
    }
    if model
        .special_fields
        .unknown_fields()
        .iter()
        .next()
        .is_some()
        || model
            .weights_hash
            .as_ref()
            .is_some_and(|hash| hash.special_fields.unknown_fields().iter().next().is_some())
        || model
            .tokenizer_hash
            .as_ref()
            .is_some_and(|hash| hash.special_fields.unknown_fields().iter().next().is_some())
        || model
            .prompt_hash
            .as_ref()
            .is_some_and(|hash| hash.special_fields.unknown_fields().iter().next().is_some())
    {
        return Err(ReuseKeyError::UnknownModelField);
    }
    if model.model_id.is_empty()
        || model.version.is_empty()
        || model.task.enum_value() != Ok(common::ModelTask::MODEL_TASK_EMBED)
        || model.dimensions.is_none_or(|value| value == 0)
        || model.max_tokens == 0
        || model.precision.is_empty()
        || model.backend.is_empty()
        || !valid_hash(&model.weights_hash.sha256)
        || !valid_hash(&model.tokenizer_hash.sha256)
        || model
            .prompt_hash
            .as_ref()
            .is_some_and(|hash| !valid_hash(&hash.sha256))
    {
        return Err(ReuseKeyError::InvalidModel);
    }
    let mut hasher = Sha256::new();
    hasher.update(KEY_DOMAIN);
    add_field(&mut hasher, corpus_id.as_bytes())?;
    add_field(&mut hasher, rendered.policy_version.as_bytes())?;
    add_field(&mut hasher, rendered.text.as_bytes())?;
    add_field(&mut hasher, model.model_id.as_bytes())?;
    add_field(&mut hasher, model.version.as_bytes())?;
    add_field(&mut hasher, model.weights_hash.sha256.as_bytes())?;
    add_field(&mut hasher, model.tokenizer_hash.sha256.as_bytes())?;
    hasher.update(model.task.value().to_be_bytes());
    add_field(&mut hasher, model.pooling.as_bytes())?;
    add_field(&mut hasher, model.normalization.as_bytes())?;
    hasher.update(model.dimensions.unwrap().to_be_bytes());
    hasher.update(model.max_tokens.to_be_bytes());
    add_field(&mut hasher, model.precision.as_bytes())?;
    add_field(&mut hasher, model.backend.as_bytes())?;
    if let Some(hash) = model.prompt_hash.as_ref() {
        hasher.update([1]);
        add_field(&mut hasher, hash.sha256.as_bytes())?;
    } else {
        hasher.update([0]);
    }
    Ok(format!("{:x}", hasher.finalize()))
}

fn add_field(hasher: &mut Sha256, value: &[u8]) -> Result<(), ReuseKeyError> {
    let length = u32::try_from(value.len()).map_err(|_| ReuseKeyError::FieldTooLarge)?;
    hasher.update(length.to_be_bytes());
    hasher.update(value);
    Ok(())
}

fn valid_hash(value: &str) -> bool {
    value.len() == 64
        && value
            .bytes()
            .all(|byte| byte.is_ascii_hexdigit() && !byte.is_ascii_uppercase())
}

fn hex_sha256(bytes: &[u8]) -> String {
    format!("{:x}", Sha256::digest(bytes))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::wire::common::ContentHash;
    use protobuf::{EnumOrUnknown, MessageField};

    fn fixture() -> (RenderedEmbeddingInput, ModelManifest) {
        let input = RenderedEmbeddingInput {
            chunk_id: "chunk:one".into(),
            text_artifact_id: "text:one".into(),
            policy_version: RENDER_POLICY_VERSION,
            text: "Konteks: Pasal 1\nTeks: Wajib melapor.".into(),
            sha256: hex_sha256(b"Konteks: Pasal 1\nTeks: Wajib melapor."),
        };
        let model = ModelManifest {
            model_id: "bge-m3".into(),
            version: "1".into(),
            weights_hash: MessageField::some(ContentHash {
                sha256: "a".repeat(64),
                ..Default::default()
            }),
            tokenizer_hash: MessageField::some(ContentHash {
                sha256: "b".repeat(64),
                ..Default::default()
            }),
            task: EnumOrUnknown::new(common::ModelTask::MODEL_TASK_EMBED),
            pooling: "cls".into(),
            normalization: "l2".into(),
            dimensions: Some(1024),
            max_tokens: 8192,
            precision: "fp16".into(),
            backend: "onnxruntime".into(),
            ..Default::default()
        };
        (input, model)
    }

    #[test]
    fn identical_input_and_manifest_reuse_across_chunk_metadata() {
        let (input, model) = fixture();
        let first = reuse_key_from_model("corpus:one", &input, &model).unwrap();
        let mut same_bytes = input.clone();
        same_bytes.chunk_id = "chunk:other".into();
        same_bytes.text_artifact_id = "text:other".into();
        assert_eq!(
            reuse_key_from_model("corpus:one", &same_bytes, &model).unwrap(),
            first
        );
        assert_eq!(first.len(), 64);
    }

    #[test]
    fn generation_policy_is_required_without_binding_lexical_refresh_to_dense_key() {
        let (input, model) = fixture();
        let mut generation = evidence::IndexGeneration {
            meta: MessageField::some(common::RecordMeta {
                schema_version: 1,
                corpus_id: "corpus:one".into(),
                record_id: "generation:one".into(),
                ..Default::default()
            }),
            dense_manifest: MessageField::some(model),
            filter_format: EnumOrUnknown::new(
                evidence::IndexFilterFormat::INDEX_FILTER_FORMAT_PAIRED_PROVISION_V1,
            ),
            embedding_input_policy: RENDER_POLICY_VERSION.into(),
            ..Default::default()
        };
        let base = embedding_reuse_key("corpus:one", &input, &generation).unwrap();
        generation.meta.as_mut().unwrap().record_id = "generation:lexical-refresh".into();
        assert_eq!(
            embedding_reuse_key("corpus:one", &input, &generation).unwrap(),
            base
        );
        generation.embedding_input_policy = "parent-labels-v1".into();
        assert_eq!(
            embedding_reuse_key("corpus:one", &input, &generation),
            Err(ReuseKeyError::InvalidGeneration)
        );
        generation.embedding_input_policy = RENDER_POLICY_VERSION.into();
        generation.meta.as_mut().unwrap().corpus_id = "corpus:other".into();
        assert_eq!(
            embedding_reuse_key("corpus:one", &input, &generation),
            Err(ReuseKeyError::InvalidGeneration)
        );
    }

    #[test]
    fn corpus_text_and_every_model_dimension_invalidate() {
        let (input, model) = fixture();
        let base = reuse_key_from_model("corpus:one", &input, &model).unwrap();
        assert_ne!(
            reuse_key_from_model("corpus:two", &input, &model).unwrap(),
            base
        );
        let mut changed_text = input.clone();
        changed_text.text.push('!');
        changed_text.sha256 = hex_sha256(changed_text.text.as_bytes());
        assert_ne!(
            reuse_key_from_model("corpus:one", &changed_text, &model).unwrap(),
            base
        );
        let mut variants = Vec::new();
        let mut change = model.clone();
        change.model_id.push('2');
        variants.push(change);
        let mut change = model.clone();
        change.version.push('2');
        variants.push(change);
        let mut change = model.clone();
        change.weights_hash.as_mut().unwrap().sha256 = "c".repeat(64);
        variants.push(change);
        let mut change = model.clone();
        change.tokenizer_hash.as_mut().unwrap().sha256 = "c".repeat(64);
        variants.push(change);
        let mut change = model.clone();
        change.pooling.push('2');
        variants.push(change);
        let mut change = model.clone();
        change.normalization.push('2');
        variants.push(change);
        let mut change = model.clone();
        change.dimensions = Some(512);
        variants.push(change);
        let mut change = model.clone();
        change.max_tokens = 4096;
        variants.push(change);
        let mut change = model.clone();
        change.precision.push('2');
        variants.push(change);
        let mut change = model.clone();
        change.backend.push('2');
        variants.push(change);
        let mut change = model.clone();
        change.prompt_hash = MessageField::some(ContentHash {
            sha256: "d".repeat(64),
            ..Default::default()
        });
        variants.push(change);
        for variant in variants {
            assert_ne!(
                reuse_key_from_model("corpus:one", &input, &variant).unwrap(),
                base
            );
        }
    }

    #[test]
    fn rejects_tampered_rendered_bytes_or_unsupported_model() {
        let (input, model) = fixture();
        let mut tampered = input.clone();
        tampered.text.push('!');
        assert_eq!(
            reuse_key_from_model("corpus:one", &tampered, &model),
            Err(ReuseKeyError::InvalidRenderedInput)
        );
        let mut wrong_policy = input.clone();
        wrong_policy.policy_version = "other";
        assert_eq!(
            reuse_key_from_model("corpus:one", &wrong_policy, &model),
            Err(ReuseKeyError::InvalidRenderedInput)
        );
        let mut wrong_task = model.clone();
        wrong_task.task = EnumOrUnknown::new(common::ModelTask::MODEL_TASK_RERANK);
        assert_eq!(
            reuse_key_from_model("corpus:one", &input, &wrong_task),
            Err(ReuseKeyError::InvalidModel)
        );

        let mut future_model = model.clone();
        future_model
            .special_fields
            .mut_unknown_fields()
            .add_varint(100, 1);
        assert_eq!(
            reuse_key_from_model("corpus:one", &input, &future_model),
            Err(ReuseKeyError::UnknownModelField)
        );
        let mut future_hash = model;
        future_hash
            .weights_hash
            .as_mut()
            .unwrap()
            .special_fields
            .mut_unknown_fields()
            .add_varint(100, 1);
        assert_eq!(
            reuse_key_from_model("corpus:one", &input, &future_hash),
            Err(ReuseKeyError::UnknownModelField)
        );
    }
}
