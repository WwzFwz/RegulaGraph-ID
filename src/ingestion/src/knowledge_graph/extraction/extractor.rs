//! Assembles and validates immutable EXTRACT artifacts against their source `DocumentBatch`.
//!
//! This boundary rejects hallucinated IDs, out-of-chunk spans, cross-corpus records, unsupported
//! assertions, model/prompt drift, and inconsistent partial-result accounting before persistence.
//! Validation is hash/set based and should remain linear in records plus reference edges; semantic
//! precision/recall and latency targets remain REQUIRED_UNMEASURED in `configs/benchmark-targets.yaml`.

use crate::domain::document_batch::{
    validate_document_batch, DocumentBatchConfig, DocumentBatchError,
};
use crate::domain::wire::{self, Limits};
use crate::wire::{common, documents, graph};
use protobuf::{EnumOrUnknown, MessageField};
use std::collections::{HashMap, HashSet};
use std::error::Error;
use std::fmt::{Display, Formatter};

const WIRE_SCHEMA_VERSION: u32 = 1;

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ExtractionBatchConfig {
    pub maximum_records: usize,
    pub maximum_reference_edges: usize,
}

impl Default for ExtractionBatchConfig {
    fn default() -> Self {
        Self {
            maximum_records: 1_000_000,
            maximum_reference_edges: 10_000_000,
        }
    }
}

#[derive(Clone, Debug, Default)]
pub struct ExtractionBatchParts {
    pub batch_id: String,
    pub context: common::RequestContext,
    pub source_document_batch: common::ArtifactRef,
    pub mentions: Vec<graph::Mention>,
    pub assertions: Vec<graph::RelationAssertion>,
    pub supports: Vec<graph::SupportRecord>,
    pub issues: Vec<common::ValidationIssue>,
    pub dependencies: common::DependencyManifest,
    pub ontology_version: String,
    pub model_manifest: common::ModelManifest,
    pub prompt_hash: common::ContentHash,
    pub item_counts: common::Counts,
    pub token_usage: common::TokenUsage,
    pub durations: Vec<common::StageDuration>,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum ExtractionBatchError {
    InvalidConfig(&'static str),
    InvalidIdentity(&'static str),
    DuplicateRecordId(String),
    MissingReference { field: &'static str, id: String },
    InvalidReference { field: &'static str, id: String },
    RecordLimit { actual: usize, maximum: usize },
    ReferenceLimit { actual: usize, maximum: usize },
    Accounting(&'static str),
    DocumentBatch(DocumentBatchError),
    WireValidation(String),
}

impl Display for ExtractionBatchError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::InvalidConfig(field) => write!(formatter, "invalid extraction config: {field}"),
            Self::InvalidIdentity(field) => {
                write!(formatter, "invalid extraction identity: {field}")
            }
            Self::DuplicateRecordId(id) => {
                write!(formatter, "duplicate extraction record ID: {id}")
            }
            Self::MissingReference { field, id } => {
                write!(formatter, "missing extraction reference {field}: {id}")
            }
            Self::InvalidReference { field, id } => {
                write!(formatter, "invalid extraction reference {field}: {id}")
            }
            Self::RecordLimit { actual, maximum } => write!(
                formatter,
                "extraction records {actual} exceed limit {maximum}"
            ),
            Self::ReferenceLimit { actual, maximum } => write!(
                formatter,
                "extraction references {actual} exceed limit {maximum}"
            ),
            Self::Accounting(detail) => {
                write!(formatter, "invalid extraction accounting: {detail}")
            }
            Self::DocumentBatch(error) => {
                write!(formatter, "invalid source document batch: {error}")
            }
            Self::WireValidation(detail) => {
                write!(formatter, "extraction wire validation failed: {detail}")
            }
        }
    }
}

impl Error for ExtractionBatchError {}

impl From<DocumentBatchError> for ExtractionBatchError {
    fn from(value: DocumentBatchError) -> Self {
        Self::DocumentBatch(value)
    }
}

pub fn assemble_extraction_batch(
    parts: ExtractionBatchParts,
    source: &documents::DocumentBatch,
    config: &ExtractionBatchConfig,
) -> Result<graph::ExtractionBatch, ExtractionBatchError> {
    let completeness = completeness_for_counts(&parts.item_counts)?;
    let batch = graph::ExtractionBatch {
        meta: MessageField::some(common::RecordMeta {
            schema_version: WIRE_SCHEMA_VERSION,
            corpus_id: parts.context.corpus_id.clone(),
            record_id: parts.batch_id,
            ..Default::default()
        }),
        context: MessageField::some(parts.context),
        source_document_batch: MessageField::some(parts.source_document_batch),
        mentions: parts.mentions,
        assertions: parts.assertions,
        supports: parts.supports,
        issues: parts.issues,
        dependencies: MessageField::some(parts.dependencies),
        completeness: EnumOrUnknown::new(completeness),
        ontology_version: parts.ontology_version,
        model_manifest: MessageField::some(parts.model_manifest),
        prompt_hash: MessageField::some(parts.prompt_hash),
        item_counts: MessageField::some(parts.item_counts),
        token_usage: MessageField::some(parts.token_usage),
        durations: parts.durations,
        ..Default::default()
    };
    validate_extraction_batch(&batch, source, config)?;
    Ok(batch)
}

pub fn validate_extraction_batch(
    batch: &graph::ExtractionBatch,
    source: &documents::DocumentBatch,
    config: &ExtractionBatchConfig,
) -> Result<(), ExtractionBatchError> {
    validate_config(config)?;
    validate_document_batch(source, &DocumentBatchConfig::default())?;
    wire::validate(batch, Limits::default()).map_err(ExtractionBatchError::WireValidation)?;

    let meta = batch
        .meta
        .as_ref()
        .ok_or(ExtractionBatchError::InvalidIdentity("meta"))?;
    let context = batch
        .context
        .as_ref()
        .ok_or(ExtractionBatchError::InvalidIdentity("context"))?;
    let source_ref =
        batch
            .source_document_batch
            .as_ref()
            .ok_or(ExtractionBatchError::InvalidIdentity(
                "source_document_batch",
            ))?;
    let dependencies = batch
        .dependencies
        .as_ref()
        .ok_or(ExtractionBatchError::InvalidIdentity("dependencies"))?;
    let model = batch
        .model_manifest
        .as_ref()
        .ok_or(ExtractionBatchError::InvalidIdentity("model_manifest"))?;
    let prompt_hash = batch
        .prompt_hash
        .as_ref()
        .ok_or(ExtractionBatchError::InvalidIdentity("prompt_hash"))?;
    let counts = batch
        .item_counts
        .as_ref()
        .ok_or(ExtractionBatchError::InvalidIdentity("item_counts"))?;

    if meta.corpus_id != context.corpus_id || context.corpus_id != source.context.corpus_id {
        return Err(ExtractionBatchError::InvalidIdentity("corpus_id"));
    }
    if source_ref.artifact_id == meta.record_id {
        return Err(ExtractionBatchError::InvalidIdentity(
            "source_document_batch.artifact_id",
        ));
    }
    if batch.ontology_version.trim().is_empty() {
        return Err(ExtractionBatchError::InvalidIdentity("ontology_version"));
    }
    if model.task.enum_value() != Ok(common::ModelTask::MODEL_TASK_EXTRACT)
        || model.prompt_hash.as_ref() != Some(prompt_hash)
    {
        return Err(ExtractionBatchError::InvalidIdentity(
            "model_manifest task/prompt",
        ));
    }
    let producer =
        dependencies
            .producer_manifest
            .as_ref()
            .ok_or(ExtractionBatchError::InvalidIdentity(
                "dependencies.producer_manifest",
            ))?;
    if !producer.models.iter().any(|candidate| candidate == model)
        || !producer
            .prompt_hashes
            .iter()
            .any(|candidate| candidate == prompt_hash)
    {
        return Err(ExtractionBatchError::InvalidIdentity(
            "producer model/prompt identity",
        ));
    }
    if !dependencies.dependencies.iter().any(|dependency| {
        dependency.dependency_id == source_ref.artifact_id
            && dependency.fingerprint.as_ref() == source_ref.content_hash.as_ref()
    }) {
        return Err(ExtractionBatchError::MissingReference {
            field: "dependencies.source_document_batch",
            id: source_ref.artifact_id.clone(),
        });
    }

    let expected_completeness = completeness_for_counts(counts)?;
    if batch.completeness.enum_value() != Ok(expected_completeness) {
        return Err(ExtractionBatchError::Accounting(
            "completeness differs from item counts",
        ));
    }
    let has_error = batch
        .issues
        .iter()
        .any(|issue| issue.severity.enum_value() == Ok(common::Severity::SEVERITY_ERROR));
    if (counts.rejected > 0) != has_error {
        return Err(ExtractionBatchError::Accounting(
            "rejected items and error issues disagree",
        ));
    }

    let record_count = 1usize
        .checked_add(batch.mentions.len())
        .and_then(|count| count.checked_add(batch.assertions.len()))
        .and_then(|count| count.checked_add(batch.supports.len()))
        .and_then(|count| count.checked_add(batch.issues.len()))
        .ok_or(ExtractionBatchError::RecordLimit {
            actual: usize::MAX,
            maximum: config.maximum_records,
        })?;
    if record_count > config.maximum_records {
        return Err(ExtractionBatchError::RecordLimit {
            actual: record_count,
            maximum: config.maximum_records,
        });
    }

    let indexes = SourceIndexes::new(source)?;
    let mut record_ids = HashSet::with_capacity(record_count);
    let mut mention_ids = HashSet::with_capacity(batch.mentions.len());
    let mut assertion_ids = HashSet::with_capacity(batch.assertions.len());
    let mut support_counts: HashMap<&str, usize> = HashMap::new();
    let mut reference_edges = 0usize;

    for mention in &batch.mentions {
        let id = validate_record_meta(mention.meta.as_ref(), &context.corpus_id, &mut record_ids)?;
        mention_ids.insert(id.to_owned());
        if mention.extraction_manifest.as_ref() != Some(producer) {
            return Err(ExtractionBatchError::InvalidIdentity(
                "mention.extraction_manifest",
            ));
        }
        let span = mention
            .text_span
            .as_ref()
            .ok_or(ExtractionBatchError::InvalidReference {
                field: "mention.text_span",
                id: id.to_owned(),
            })?;
        indexes.validate_evidence_span(span, "mention.text_span", id)?;
        validate_source_refs(
            &indexes,
            span,
            &mention.source_refs,
            "mention.source_refs",
            id,
        )?;
        reference_edges = checked_add(reference_edges, 1 + mention.source_refs.len(), config)?;
    }

    for assertion in &batch.assertions {
        let id =
            validate_record_meta(assertion.meta.as_ref(), &context.corpus_id, &mut record_ids)?;
        assertion_ids.insert(id.to_owned());
        if assertion.ontology_version != batch.ontology_version {
            return Err(ExtractionBatchError::InvalidIdentity(
                "assertion.ontology_version",
            ));
        }
        reference_edges = checked_add(
            reference_edges,
            2 + assertion.qualifiers.len() + assertion.exception_refs.len(),
            config,
        )?;
    }
    for assertion in &batch.assertions {
        let id = assertion.meta.record_id.as_str();
        for endpoint in [&assertion.subject_id, &assertion.object_id] {
            if !mention_ids.contains(endpoint) {
                return Err(ExtractionBatchError::MissingReference {
                    field: "assertion.endpoint",
                    id: endpoint.clone(),
                });
            }
        }
        for qualifier in &assertion.qualifiers {
            if let Some(graph::qualifier::Value::CanonicalId(reference)) = qualifier.value.as_ref()
            {
                if !mention_ids.contains(reference) {
                    return Err(ExtractionBatchError::MissingReference {
                        field: "assertion.qualifier.canonical_id",
                        id: reference.clone(),
                    });
                }
            }
        }
        for reference in &assertion.exception_refs {
            if reference == id || !assertion_ids.contains(reference) {
                return Err(ExtractionBatchError::MissingReference {
                    field: "assertion.exception_refs",
                    id: reference.clone(),
                });
            }
        }
    }

    for support in &batch.supports {
        let id = validate_record_meta(support.meta.as_ref(), &context.corpus_id, &mut record_ids)?;
        if support.extraction_manifest.as_ref() != Some(producer) {
            return Err(ExtractionBatchError::InvalidIdentity(
                "support.extraction_manifest",
            ));
        }
        if !assertion_ids.contains(&support.assertion_id) {
            return Err(ExtractionBatchError::MissingReference {
                field: "support.assertion_id",
                id: support.assertion_id.clone(),
            });
        }
        if !matches!(
            support.review_state.enum_value(),
            Ok(common::ReviewState::REVIEW_STATE_UNREVIEWED)
                | Ok(common::ReviewState::REVIEW_STATE_QUARANTINED)
        ) {
            return Err(ExtractionBatchError::InvalidIdentity(
                "support.review_state",
            ));
        }
        let mut supported_refs = HashSet::new();
        for span in &support.evidence_spans {
            indexes.validate_evidence_span(span, "support.evidence_spans", id)?;
            for source_ref in &support.source_refs {
                if indexes.source_ref_supports_span(source_ref, span) {
                    supported_refs.insert(source_ref_key(source_ref));
                }
            }
        }
        let unique_refs: HashSet<_> = support.source_refs.iter().map(source_ref_key).collect();
        if unique_refs.len() != support.source_refs.len() || supported_refs != unique_refs {
            return Err(ExtractionBatchError::InvalidReference {
                field: "support.source_refs",
                id: id.to_owned(),
            });
        }
        *support_counts.entry(&support.assertion_id).or_default() += 1;
        reference_edges = checked_add(
            reference_edges,
            1 + support.evidence_spans.len() + support.source_refs.len(),
            config,
        )?;
    }
    for assertion_id in assertion_ids {
        if support_counts
            .get(assertion_id.as_str())
            .copied()
            .unwrap_or(0)
            == 0
        {
            return Err(ExtractionBatchError::MissingReference {
                field: "assertion.support",
                id: assertion_id,
            });
        }
    }

    let known_issue_records: HashSet<&str> = record_ids
        .iter()
        .map(String::as_str)
        .chain(
            source
                .chunks
                .iter()
                .map(|chunk| chunk.meta.record_id.as_str()),
        )
        .collect();
    for issue in &batch.issues {
        if !known_issue_records.contains(issue.record_id.as_str()) {
            return Err(ExtractionBatchError::MissingReference {
                field: "issue.record_id",
                id: issue.record_id.clone(),
            });
        }
        reference_edges = checked_add(reference_edges, issue.evidence_refs.len(), config)?;
    }
    Ok(())
}

fn validate_config(config: &ExtractionBatchConfig) -> Result<(), ExtractionBatchError> {
    if config.maximum_records == 0 {
        return Err(ExtractionBatchError::InvalidConfig("maximum_records"));
    }
    if config.maximum_reference_edges == 0 {
        return Err(ExtractionBatchError::InvalidConfig(
            "maximum_reference_edges",
        ));
    }
    Ok(())
}

fn completeness_for_counts(
    counts: &common::Counts,
) -> Result<common::Completeness, ExtractionBatchError> {
    if counts.expected == 0
        || counts.accepted > counts.expected
        || counts.rejected != counts.expected - counts.accepted
    {
        return Err(ExtractionBatchError::Accounting("invalid item counts"));
    }
    Ok(if counts.accepted == counts.expected {
        common::Completeness::COMPLETENESS_COMPLETE
    } else if counts.accepted == 0 {
        common::Completeness::COMPLETENESS_NONE
    } else {
        common::Completeness::COMPLETENESS_PARTIAL
    })
}

fn validate_record_meta<'a>(
    meta: Option<&'a common::RecordMeta>,
    corpus_id: &str,
    ids: &mut HashSet<String>,
) -> Result<&'a str, ExtractionBatchError> {
    let meta = meta.ok_or(ExtractionBatchError::InvalidIdentity("record.meta"))?;
    if meta.corpus_id != corpus_id || meta.visibility.is_some() {
        return Err(ExtractionBatchError::InvalidIdentity(
            "record corpus/visibility",
        ));
    }
    if !ids.insert(meta.record_id.clone()) {
        return Err(ExtractionBatchError::DuplicateRecordId(
            meta.record_id.clone(),
        ));
    }
    Ok(&meta.record_id)
}

fn checked_add(
    current: usize,
    increment: usize,
    config: &ExtractionBatchConfig,
) -> Result<usize, ExtractionBatchError> {
    let actual = current
        .checked_add(increment)
        .ok_or(ExtractionBatchError::ReferenceLimit {
            actual: usize::MAX,
            maximum: config.maximum_reference_edges,
        })?;
    if actual > config.maximum_reference_edges {
        return Err(ExtractionBatchError::ReferenceLimit {
            actual,
            maximum: config.maximum_reference_edges,
        });
    }
    Ok(actual)
}

fn validate_source_refs(
    indexes: &SourceIndexes<'_>,
    span: &common::TextSpan,
    refs: &[common::SourceVersionRef],
    field: &'static str,
    record_id: &str,
) -> Result<(), ExtractionBatchError> {
    let unique: HashSet<_> = refs.iter().map(source_ref_key).collect();
    if unique.len() != refs.len()
        || refs
            .iter()
            .any(|source_ref| !indexes.source_ref_supports_span(source_ref, span))
    {
        return Err(ExtractionBatchError::InvalidReference {
            field,
            id: record_id.to_owned(),
        });
    }
    Ok(())
}

fn source_ref_key(source_ref: &common::SourceVersionRef) -> (&str, &str, &str) {
    (
        source_ref.source_blob_id.as_str(),
        source_ref.provision_version_id.as_str(),
        source_ref.regulation_id.as_str(),
    )
}

struct VersionEvidence<'a> {
    regulation_id: &'a str,
    spans: Vec<(&'a common::TextSpan, &'a str)>,
}

struct SourceIndexes<'a> {
    chunks: &'a [documents::Chunk],
    text_sizes: HashMap<&'a str, u64>,
    versions: HashMap<&'a str, VersionEvidence<'a>>,
}

impl<'a> SourceIndexes<'a> {
    fn new(source: &'a documents::DocumentBatch) -> Result<Self, ExtractionBatchError> {
        let text_sources: HashMap<_, _> = source
            .text_artifacts
            .iter()
            .map(|artifact| {
                (
                    artifact.meta.record_id.as_str(),
                    (
                        artifact.source_blob_id.as_str(),
                        artifact.normalized_text_ref.byte_size,
                    ),
                )
            })
            .collect();
        let provision_regulations: HashMap<_, _> = source
            .provisions
            .iter()
            .map(|provision| {
                (
                    provision.meta.record_id.as_str(),
                    provision.regulation_id.as_str(),
                )
            })
            .collect();
        let mut versions = HashMap::with_capacity(source.versions.len());
        for version in &source.versions {
            let regulation_id = provision_regulations
                .get(version.provision_id.as_str())
                .copied()
                .ok_or_else(|| ExtractionBatchError::MissingReference {
                    field: "version.provision_id",
                    id: version.provision_id.clone(),
                })?;
            let mut spans = Vec::with_capacity(version.spans.len());
            for span in &version.spans {
                let source_blob_id = text_sources
                    .get(span.text_artifact_id.as_str())
                    .map(|entry| entry.0)
                    .ok_or_else(|| ExtractionBatchError::MissingReference {
                        field: "version.spans.text_artifact_id",
                        id: span.text_artifact_id.clone(),
                    })?;
                spans.push((span, source_blob_id));
            }
            versions.insert(
                version.meta.record_id.as_str(),
                VersionEvidence {
                    regulation_id,
                    spans,
                },
            );
        }
        Ok(Self {
            chunks: &source.chunks,
            text_sizes: text_sources
                .into_iter()
                .map(|(id, (_, size))| (id, size))
                .collect(),
            versions,
        })
    }

    fn validate_evidence_span(
        &self,
        span: &common::TextSpan,
        field: &'static str,
        record_id: &str,
    ) -> Result<(), ExtractionBatchError> {
        let valid_bounds = self
            .text_sizes
            .get(span.text_artifact_id.as_str())
            .is_some_and(|size| span.start_byte < span.end_byte && span.end_byte <= *size);
        let inside_chunk = self.chunks.iter().any(|chunk| {
            chunk.text_span.text_artifact_id == span.text_artifact_id
                && chunk.text_span.start_byte <= span.start_byte
                && chunk.text_span.end_byte >= span.end_byte
        });
        if !valid_bounds || !inside_chunk {
            return Err(ExtractionBatchError::InvalidReference {
                field,
                id: record_id.to_owned(),
            });
        }
        Ok(())
    }

    fn source_ref_supports_span(
        &self,
        source_ref: &common::SourceVersionRef,
        span: &common::TextSpan,
    ) -> bool {
        self.versions
            .get(source_ref.provision_version_id.as_str())
            .is_some_and(|version| {
                version.regulation_id == source_ref.regulation_id
                    && version.spans.iter().any(|(version_span, source_blob_id)| {
                        *source_blob_id == source_ref.source_blob_id
                            && version_span.text_artifact_id == span.text_artifact_id
                            && version_span.start_byte <= span.start_byte
                            && version_span.end_byte >= span.end_byte
                    })
            })
    }
}

#[cfg(test)]
pub(crate) mod tests {
    use super::*;
    use crate::domain::document_batch::{
        assemble_document_batch, build_source_blob, DocumentBatchParts,
    };
    use protobuf::well_known_types::timestamp::Timestamp;

    const CORPUS: &str = "regulagraph-id";

    fn hash(character: char) -> common::ContentHash {
        common::ContentHash {
            sha256: character.to_string().repeat(64),
            ..Default::default()
        }
    }

    fn artifact(id: &str, character: char, media_type: &str, size: u64) -> common::ArtifactRef {
        let digest = character.to_string().repeat(64);
        common::ArtifactRef {
            artifact_id: id.to_owned(),
            content_hash: MessageField::some(hash(character)),
            storage_key: format!("sha256/{}/{}/{}.bin", &digest[..2], &digest[2..4], digest),
            media_type: media_type.to_owned(),
            byte_size: size,
            schema_version: 1,
            ..Default::default()
        }
    }

    fn meta(id: &str) -> common::RecordMeta {
        common::RecordMeta {
            schema_version: 1,
            corpus_id: CORPUS.to_owned(),
            record_id: id.to_owned(),
            ..Default::default()
        }
    }

    fn context() -> common::RequestContext {
        common::RequestContext {
            schema_version: 1,
            request_id: "request:extract-fixture".to_owned(),
            trace_id: "trace:extract-fixture".to_owned(),
            corpus_id: CORPUS.to_owned(),
            deadline: MessageField::some(Timestamp {
                seconds: 1_900_000_000,
                ..Default::default()
            }),
            config_fingerprint: MessageField::some(hash('1')),
            auth_scope_ref: "scope:ingestion".to_owned(),
            ..Default::default()
        }
    }

    fn source_producer() -> common::ProducerManifest {
        common::ProducerManifest {
            software: "regulagraph-ingestion".to_owned(),
            build: "test".to_owned(),
            schema_version: 1,
            config_hash: MessageField::some(hash('2')),
            ..Default::default()
        }
    }

    fn unknown_date() -> common::DateAssertion {
        common::DateAssertion {
            knowledge: EnumOrUnknown::new(common::DateKnowledge::DATE_KNOWLEDGE_UNKNOWN),
            ..Default::default()
        }
    }

    pub(crate) fn source_batch() -> documents::DocumentBatch {
        let source_artifact = artifact("artifact:source:fixture", '3', "application/pdf", 1024);
        let source = build_source_blob(CORPUS, "source-blob:fixture", source_artifact.clone())
            .expect("source builds");
        let raw_ref = artifact("artifact:raw:fixture", '4', "text/plain;charset=utf-8", 31);
        let normalized_ref = artifact(
            "artifact:normalized:fixture",
            '5',
            "text/plain;charset=utf-8",
            31,
        );
        let mapping_ref = artifact(
            "artifact:mapping:fixture",
            '6',
            "application/vnd.regulagraph.text-mapping+protobuf",
            128,
        );
        let span = common::TextSpan {
            text_artifact_id: "text:fixture".to_owned(),
            start_byte: 0,
            end_byte: 31,
            ..Default::default()
        };
        assemble_document_batch(
            DocumentBatchParts {
                batch_id: "document-batch:fixture".to_owned(),
                context: context(),
                sources: vec![source],
                text_artifacts: vec![documents::TextArtifact {
                    meta: MessageField::some(meta("text:fixture")),
                    source_blob_id: "source-blob:fixture".to_owned(),
                    parser_manifest: MessageField::some(source_producer()),
                    raw_text_ref: MessageField::some(raw_ref),
                    normalized_text_ref: MessageField::some(normalized_ref.clone()),
                    mapping_ref: MessageField::some(mapping_ref),
                    page_results: vec![documents::PageResult {
                        page_number: 1,
                        status: EnumOrUnknown::new(
                            common::CompletionStatus::COMPLETION_STATUS_SUCCEEDED,
                        ),
                        spans: vec![span.clone()],
                        ..Default::default()
                    }],
                    normalizer_manifest: MessageField::some(source_producer()),
                    ..Default::default()
                }],
                structures: vec![documents::StructureNode {
                    meta: MessageField::some(meta("structure:fixture")),
                    kind: EnumOrUnknown::new(documents::StructureKind::STRUCTURE_KIND_DOCUMENT),
                    source_spans: vec![span.clone()],
                    ..Default::default()
                }],
                provisions: vec![documents::Provision {
                    meta: MessageField::some(meta("provision:fixture")),
                    regulation_id: "regulation:fixture".to_owned(),
                    structural_path: vec!["Pasal 1".to_owned()],
                    ..Default::default()
                }],
                versions: vec![documents::ProvisionVersion {
                    meta: MessageField::some(meta("version:fixture")),
                    provision_id: "provision:fixture".to_owned(),
                    text_ref: MessageField::some(normalized_ref),
                    spans: vec![span.clone()],
                    legal_interval: MessageField::some(common::LegalInterval {
                        start: MessageField::some(unknown_date()),
                        end: MessageField::some(unknown_date()),
                        ..Default::default()
                    }),
                    legal_status: EnumOrUnknown::new(documents::LegalStatus::LEGAL_STATUS_UNKNOWN),
                    review_state: EnumOrUnknown::new(common::ReviewState::REVIEW_STATE_UNREVIEWED),
                    ..Default::default()
                }],
                chunks: vec![documents::Chunk {
                    meta: MessageField::some(meta("chunk:fixture")),
                    provision_version_refs: vec!["version:fixture".to_owned()],
                    text_span: MessageField::some(span),
                    structure_node_refs: vec!["structure:fixture".to_owned()],
                    chunker_manifest: MessageField::some(source_producer()),
                    ..Default::default()
                }],
                dependency_manifest: common::DependencyManifest {
                    artifact_id: "dependency-manifest:source".to_owned(),
                    dependencies: vec![
                        common::Dependency {
                            dependency_id: "entity:issuer".to_owned(),
                            fingerprint: MessageField::some(hash('7')),
                            ..Default::default()
                        },
                        common::Dependency {
                            dependency_id: source_artifact.artifact_id,
                            fingerprint: source_artifact.content_hash,
                            ..Default::default()
                        },
                    ],
                    producer_manifest: MessageField::some(source_producer()),
                    ..Default::default()
                },
                regulations: vec![documents::Regulation {
                    meta: MessageField::some(meta("regulation:fixture")),
                    kind: "peraturan".to_owned(),
                    issuer_id: "entity:issuer".to_owned(),
                    jurisdiction: "ID".to_owned(),
                    official_number: "1".to_owned(),
                    year: 2026,
                    title: "Peraturan Fixture".to_owned(),
                    identity_status: EnumOrUnknown::new(
                        documents::IdentityStatus::IDENTITY_STATUS_VERIFIED,
                    ),
                    ..Default::default()
                }],
                ..Default::default()
            },
            &DocumentBatchConfig::default(),
        )
        .expect("source batch assembles")
    }

    fn model(prompt: &common::ContentHash) -> common::ModelManifest {
        common::ModelManifest {
            model_id: "model:extract-fixture".to_owned(),
            version: "v1".to_owned(),
            weights_hash: MessageField::some(hash('8')),
            tokenizer_hash: MessageField::some(hash('9')),
            task: EnumOrUnknown::new(common::ModelTask::MODEL_TASK_EXTRACT),
            max_tokens: 4096,
            precision: "provider".to_owned(),
            backend: "fixture".to_owned(),
            prompt_hash: MessageField::some(prompt.clone()),
            ..Default::default()
        }
    }

    fn source_ref() -> common::SourceVersionRef {
        common::SourceVersionRef {
            source_blob_id: "source-blob:fixture".to_owned(),
            provision_version_id: "version:fixture".to_owned(),
            regulation_id: "regulation:fixture".to_owned(),
            ..Default::default()
        }
    }

    fn span(start: u64, end: u64) -> common::TextSpan {
        common::TextSpan {
            text_artifact_id: "text:fixture".to_owned(),
            start_byte: start,
            end_byte: end,
            ..Default::default()
        }
    }

    pub(crate) fn fixture_parts() -> ExtractionBatchParts {
        let prompt = hash('a');
        let model = model(&prompt);
        let producer = common::ProducerManifest {
            software: "regulagraph-ingestion".to_owned(),
            build: "test".to_owned(),
            schema_version: 1,
            models: vec![model.clone()],
            prompt_hashes: vec![prompt.clone()],
            config_hash: MessageField::some(hash('b')),
            ..Default::default()
        };
        let source_document = artifact(
            "artifact:document-batch:fixture",
            'c',
            "application/vnd.regulagraph.document-batch+protobuf",
            4096,
        );
        let mention = |id: &str, start: u64, end: u64, surface: &str| graph::Mention {
            meta: MessageField::some(meta(id)),
            text_span: MessageField::some(span(start, end)),
            source_refs: vec![source_ref()],
            surface_form: surface.to_owned(),
            candidate_type: "legal_concept".to_owned(),
            extraction_manifest: MessageField::some(producer.clone()),
            ..Default::default()
        };
        ExtractionBatchParts {
            batch_id: "extraction-batch:fixture".to_owned(),
            context: context(),
            source_document_batch: source_document.clone(),
            mentions: vec![
                mention("mention:subject", 0, 6, "Pasal"),
                mention("mention:object", 7, 12, "wajib"),
            ],
            assertions: vec![graph::RelationAssertion {
                meta: MessageField::some(meta("assertion:fixture")),
                subject_id: "mention:subject".to_owned(),
                predicate_id: "requires".to_owned(),
                object_id: "mention:object".to_owned(),
                temporal_scope: MessageField::some(common::TemporalScope {
                    mode: EnumOrUnknown::new(common::TemporalMode::TEMPORAL_MODE_CURRENT),
                    unresolved_policy: EnumOrUnknown::new(
                        common::UnresolvedPolicy::UNRESOLVED_POLICY_REQUIRE_REVIEW,
                    ),
                    ..Default::default()
                }),
                origin: EnumOrUnknown::new(graph::AssertionOrigin::ASSERTION_ORIGIN_EXPLICIT),
                ontology_version: "ontology:test-v1".to_owned(),
                ..Default::default()
            }],
            supports: vec![graph::SupportRecord {
                meta: MessageField::some(meta("support:fixture")),
                assertion_id: "assertion:fixture".to_owned(),
                evidence_spans: vec![span(0, 12)],
                source_refs: vec![source_ref()],
                extraction_manifest: MessageField::some(producer.clone()),
                independent_source_group: "source-group:fixture".to_owned(),
                review_state: EnumOrUnknown::new(common::ReviewState::REVIEW_STATE_UNREVIEWED),
                ..Default::default()
            }],
            dependencies: common::DependencyManifest {
                artifact_id: "dependency-manifest:extract".to_owned(),
                dependencies: vec![common::Dependency {
                    dependency_id: source_document.artifact_id,
                    fingerprint: source_document.content_hash,
                    ..Default::default()
                }],
                producer_manifest: MessageField::some(producer),
                ..Default::default()
            },
            ontology_version: "ontology:test-v1".to_owned(),
            model_manifest: model,
            prompt_hash: prompt,
            item_counts: common::Counts {
                expected: 1,
                accepted: 1,
                ..Default::default()
            },
            token_usage: common::TokenUsage {
                input_tokens: 30,
                output_tokens: 10,
                tokenizer_id: "tokenizer:fixture".to_owned(),
                ..Default::default()
            },
            durations: vec![common::StageDuration {
                stage: "semantic_extract".to_owned(),
                duration_ns: 10,
                ..Default::default()
            }],
            ..Default::default()
        }
    }

    #[test]
    fn assembles_source_bound_extraction() {
        let source = source_batch();
        let batch =
            assemble_extraction_batch(fixture_parts(), &source, &ExtractionBatchConfig::default())
                .expect("valid extraction assembles");
        assert_eq!(
            batch.completeness.enum_value(),
            Ok(common::Completeness::COMPLETENESS_COMPLETE)
        );
        assert_eq!(batch.mentions.len(), 2);
        assert_eq!(batch.item_counts.accepted, 1);
    }

    #[test]
    fn rejects_hallucinated_endpoint_and_out_of_chunk_span() {
        let source = source_batch();
        let mut parts = fixture_parts();
        parts.assertions[0].object_id = "mention:hallucinated".to_owned();
        assert!(matches!(
            assemble_extraction_batch(parts, &source, &ExtractionBatchConfig::default()),
            Err(ExtractionBatchError::MissingReference {
                field: "assertion.endpoint",
                ..
            })
        ));

        let mut parts = fixture_parts();
        parts.mentions[0].text_span.as_mut().unwrap().end_byte = 32;
        assert!(matches!(
            assemble_extraction_batch(parts, &source, &ExtractionBatchConfig::default()),
            Err(ExtractionBatchError::InvalidReference {
                field: "mention.text_span",
                ..
            })
        ));
    }

    #[test]
    fn rejects_unsupported_assertion_and_prompt_drift() {
        let source = source_batch();
        let mut parts = fixture_parts();
        parts.supports.clear();
        assert_eq!(
            assemble_extraction_batch(parts, &source, &ExtractionBatchConfig::default())
                .unwrap_err(),
            ExtractionBatchError::MissingReference {
                field: "assertion.support",
                id: "assertion:fixture".to_owned(),
            }
        );

        let mut parts = fixture_parts();
        parts.prompt_hash = hash('d');
        assert_eq!(
            assemble_extraction_batch(parts, &source, &ExtractionBatchConfig::default())
                .unwrap_err(),
            ExtractionBatchError::InvalidIdentity("model_manifest task/prompt")
        );
    }

    #[test]
    fn derives_partial_only_with_explicit_error_issue() {
        let source = source_batch();
        let mut parts = fixture_parts();
        parts.item_counts = common::Counts {
            expected: 2,
            accepted: 1,
            rejected: 1,
            ..Default::default()
        };
        assert_eq!(
            assemble_extraction_batch(parts.clone(), &source, &ExtractionBatchConfig::default())
                .unwrap_err(),
            ExtractionBatchError::Accounting("rejected items and error issues disagree")
        );
        parts.issues.push(common::ValidationIssue {
            code: "SEMANTIC_ITEM_REJECTED".to_owned(),
            severity: EnumOrUnknown::new(common::Severity::SEVERITY_ERROR),
            record_id: "chunk:fixture".to_owned(),
            field_path: "semantic.result".to_owned(),
            disposition: "quarantine".to_owned(),
            ..Default::default()
        });
        let batch = assemble_extraction_batch(parts, &source, &ExtractionBatchConfig::default())
            .expect("explicit partial result assembles");
        assert_eq!(
            batch.completeness.enum_value(),
            Ok(common::Completeness::COMPLETENESS_PARTIAL)
        );
    }
}
