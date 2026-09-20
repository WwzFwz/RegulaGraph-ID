//! Merakit record dokumen C01 menjadi `DocumentBatch` dengan reference closure tervalidasi.
//!
//! Peran dalam komponen:
//! Assembler ini adalah boundary terakhir sebelum batch diserialisasi sebagai artefak immutable. Ia
//! menggabungkan source, text artifact, struktur, versi ketentuan, chunk, metadata, dan dependency
//! manifest tanpa melakukan publication; coordinator Go tetap memvalidasi dan menerbitkan hasilnya.
//!
//! Kontrak integrasi dan perhatian implementasi:
//! Setiap record wajib berada pada corpus yang sama. Referensi harus ditutup oleh record lokal atau
//! dependency manifest, sedangkan parent/child structure wajib lokal dan acyclic. Completeness dihitung
//! dari page result dan validation issue, sehingga halaman gagal tidak dapat dipublikasikan sebagai
//! batch lengkap. Lookup memakai HashSet/HashMap agar validasi linear terhadap jumlah record dan edge.
//!
//! Benchmark dan gate penerimaan:
//! Ukur record/s, serialization bytes/record, peak RSS, p95/p99 assembly, reference-closure failures,
//! dan `INVARIANT.WIRE_PARITY`. Target `configs/benchmark-targets.yaml` tetap REQUIRED_UNMEASURED;
//! unit test tidak membuktikan throughput corpus atau publication lintas backend.
//!
//! Status: builder SourceBlob dan assembler DocumentBatch C01 aktif; version reconstruction, graph/index
//! output, worker transport, dan publication tetap belum aktif.

use crate::domain::wire::{self, Limits};
use crate::wire::{common, documents};
use protobuf::{EnumOrUnknown, MessageField};
use std::collections::{HashMap, HashSet};
use std::error::Error;
use std::fmt::{Display, Formatter};

const WIRE_SCHEMA_VERSION: u32 = 1;

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct DocumentBatchConfig {
    pub maximum_records: usize,
    pub maximum_reference_edges: usize,
}

impl Default for DocumentBatchConfig {
    fn default() -> Self {
        Self {
            maximum_records: 2_000_000,
            maximum_reference_edges: 20_000_000,
        }
    }
}

#[derive(Clone, Debug, Default)]
pub struct DocumentBatchParts {
    pub batch_id: String,
    pub context: common::RequestContext,
    pub sources: Vec<documents::SourceBlob>,
    pub text_artifacts: Vec<documents::TextArtifact>,
    pub structures: Vec<documents::StructureNode>,
    pub provisions: Vec<documents::Provision>,
    pub versions: Vec<documents::ProvisionVersion>,
    pub chunks: Vec<documents::Chunk>,
    pub issues: Vec<common::ValidationIssue>,
    pub dependency_manifest: common::DependencyManifest,
    pub editions: Vec<documents::DocumentEdition>,
    pub regulations: Vec<documents::Regulation>,
    pub observations: Vec<documents::SourceObservation>,
    pub changes: Vec<documents::LegalChangeEvent>,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum DocumentBatchError {
    InvalidConfig(&'static str),
    InvalidIdentity(&'static str),
    DuplicateRecordId(String),
    MissingReference { field: &'static str, id: String },
    RecordLimit { actual: usize, maximum: usize },
    ReferenceLimit { actual: usize, maximum: usize },
    StructureCycle(String),
    WireValidation(String),
}

impl Display for DocumentBatchError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::InvalidConfig(field) => {
                write!(formatter, "invalid document batch config: {field}")
            }
            Self::InvalidIdentity(field) => {
                write!(formatter, "invalid document batch identity: {field}")
            }
            Self::DuplicateRecordId(id) => write!(formatter, "duplicate record ID: {id}"),
            Self::MissingReference { field, id } => {
                write!(formatter, "missing document batch reference {field}: {id}")
            }
            Self::RecordLimit { actual, maximum } => {
                write!(
                    formatter,
                    "document records {actual} exceed limit {maximum}"
                )
            }
            Self::ReferenceLimit { actual, maximum } => {
                write!(
                    formatter,
                    "document references {actual} exceed limit {maximum}"
                )
            }
            Self::StructureCycle(id) => write!(formatter, "structure parent cycle at {id}"),
            Self::WireValidation(detail) => write!(formatter, "wire validation failed: {detail}"),
        }
    }
}

impl Error for DocumentBatchError {}

pub fn build_source_blob(
    corpus_id: &str,
    source_blob_id: &str,
    artifact_ref: common::ArtifactRef,
) -> Result<documents::SourceBlob, DocumentBatchError> {
    if !valid_ascii_id(corpus_id) {
        return Err(DocumentBatchError::InvalidIdentity("corpus_id"));
    }
    if !valid_ascii_id(source_blob_id) {
        return Err(DocumentBatchError::InvalidIdentity("source_blob_id"));
    }
    validate_wire(&artifact_ref)?;
    let source = documents::SourceBlob {
        meta: MessageField::some(record_meta(corpus_id, source_blob_id)),
        raw_sha256: MessageField::some(artifact_ref.content_hash.clone().into_option().ok_or(
            DocumentBatchError::InvalidIdentity("source artifact content_hash"),
        )?),
        media_type: artifact_ref.media_type.clone(),
        byte_size: artifact_ref.byte_size,
        artifact_ref: MessageField::some(artifact_ref),
        ..Default::default()
    };
    validate_wire(&source)?;
    Ok(source)
}

pub fn assemble_document_batch(
    mut parts: DocumentBatchParts,
    config: &DocumentBatchConfig,
) -> Result<documents::DocumentBatch, DocumentBatchError> {
    validate_config(config)?;
    if !valid_ascii_id(&parts.batch_id) {
        return Err(DocumentBatchError::InvalidIdentity("batch_id"));
    }
    validate_wire(&parts.context)?;
    validate_wire(&parts.dependency_manifest)?;
    let corpus_id = parts.context.corpus_id.clone();
    if !valid_ascii_id(&corpus_id) {
        return Err(DocumentBatchError::InvalidIdentity("context.corpus_id"));
    }
    if parts.sources.is_empty() {
        return Err(DocumentBatchError::InvalidIdentity("sources"));
    }

    let total = record_count(&parts)?;
    if total > config.maximum_records {
        return Err(DocumentBatchError::RecordLimit {
            actual: total,
            maximum: config.maximum_records,
        });
    }

    validate_all_wire_records(&parts)?;
    let indexes = BatchIndexes::build(&parts, &corpus_id)?;
    validate_reference_closure(&parts, &indexes, config.maximum_reference_edges)?;
    let generated_issues = page_issues(&parts.text_artifacts);
    parts.issues.extend(generated_issues);
    let total_with_generated_issues = record_count(&parts)?;
    if total_with_generated_issues > config.maximum_records {
        return Err(DocumentBatchError::RecordLimit {
            actual: total_with_generated_issues,
            maximum: config.maximum_records,
        });
    }
    for issue in &parts.issues {
        validate_wire(issue)?;
    }
    let completeness = derive_completeness(&parts.text_artifacts, &parts.chunks, &parts.issues);

    let batch = documents::DocumentBatch {
        meta: MessageField::some(record_meta(&corpus_id, &parts.batch_id)),
        context: MessageField::some(parts.context),
        sources: parts.sources,
        text_artifacts: parts.text_artifacts,
        structures: parts.structures,
        provisions: parts.provisions,
        versions: parts.versions,
        chunks: parts.chunks,
        issues: parts.issues,
        dependency_manifest: MessageField::some(parts.dependency_manifest),
        completeness: EnumOrUnknown::new(completeness),
        editions: parts.editions,
        regulations: parts.regulations,
        observations: parts.observations,
        changes: parts.changes,
        ..Default::default()
    };
    validate_wire(&batch)?;
    Ok(batch)
}

struct BatchIndexes<'a> {
    local_ids: HashSet<&'a str>,
    dependency_ids: HashSet<&'a str>,
    source_ids: HashSet<&'a str>,
    text_ids: HashSet<&'a str>,
    structure_ids: HashSet<&'a str>,
    provision_ids: HashSet<&'a str>,
    version_ids: HashSet<&'a str>,
    artifact_ids: HashSet<&'a str>,
    structure_parents: HashMap<&'a str, Option<&'a str>>,
    structure_children: HashMap<&'a str, &'a [String]>,
}

impl<'a> BatchIndexes<'a> {
    fn build(parts: &'a DocumentBatchParts, corpus_id: &str) -> Result<Self, DocumentBatchError> {
        let mut indexes = Self {
            local_ids: HashSet::new(),
            dependency_ids: parts
                .dependency_manifest
                .dependencies
                .iter()
                .map(|dependency| dependency.dependency_id.as_str())
                .collect(),
            source_ids: HashSet::new(),
            text_ids: HashSet::new(),
            structure_ids: HashSet::new(),
            provision_ids: HashSet::new(),
            version_ids: HashSet::new(),
            artifact_ids: HashSet::new(),
            structure_parents: HashMap::new(),
            structure_children: HashMap::new(),
        };
        if indexes.dependency_ids.len() != parts.dependency_manifest.dependencies.len() {
            return Err(DocumentBatchError::InvalidIdentity(
                "duplicate dependency_id",
            ));
        }
        indexes.insert(parts.batch_id.as_str())?;
        for source in &parts.sources {
            check_meta(&source.meta, corpus_id)?;
            let id = source.meta.record_id.as_str();
            indexes.insert(id)?;
            indexes.source_ids.insert(id);
            indexes
                .artifact_ids
                .insert(source.artifact_ref.artifact_id.as_str());
        }
        for artifact in &parts.text_artifacts {
            check_meta(&artifact.meta, corpus_id)?;
            let id = artifact.meta.record_id.as_str();
            indexes.insert(id)?;
            indexes.text_ids.insert(id);
            indexes
                .artifact_ids
                .insert(artifact.raw_text_ref.artifact_id.as_str());
            indexes
                .artifact_ids
                .insert(artifact.normalized_text_ref.artifact_id.as_str());
            indexes
                .artifact_ids
                .insert(artifact.mapping_ref.artifact_id.as_str());
        }
        for structure in &parts.structures {
            check_meta(&structure.meta, corpus_id)?;
            let id = structure.meta.record_id.as_str();
            indexes.insert(id)?;
            indexes.structure_ids.insert(id);
            indexes
                .structure_parents
                .insert(id, structure.parent_id.as_deref());
            indexes
                .structure_children
                .insert(id, &structure.ordered_children);
        }
        for provision in &parts.provisions {
            check_meta(&provision.meta, corpus_id)?;
            let id = provision.meta.record_id.as_str();
            indexes.insert(id)?;
            indexes.provision_ids.insert(id);
        }
        for version in &parts.versions {
            check_meta(&version.meta, corpus_id)?;
            let id = version.meta.record_id.as_str();
            indexes.insert(id)?;
            indexes.version_ids.insert(id);
        }
        for chunk in &parts.chunks {
            check_meta(&chunk.meta, corpus_id)?;
            indexes.insert(chunk.meta.record_id.as_str())?;
        }
        for edition in &parts.editions {
            check_meta(&edition.meta, corpus_id)?;
            indexes.insert(edition.meta.record_id.as_str())?;
        }
        for regulation in &parts.regulations {
            check_meta(&regulation.meta, corpus_id)?;
            indexes.insert(regulation.meta.record_id.as_str())?;
        }
        for observation in &parts.observations {
            check_meta(&observation.meta, corpus_id)?;
            indexes.insert(observation.meta.record_id.as_str())?;
        }
        for change in &parts.changes {
            check_meta(&change.meta, corpus_id)?;
            indexes.insert(change.meta.record_id.as_str())?;
        }
        Ok(indexes)
    }

    fn insert(&mut self, id: &'a str) -> Result<(), DocumentBatchError> {
        if !self.local_ids.insert(id) {
            return Err(DocumentBatchError::DuplicateRecordId(id.to_owned()));
        }
        Ok(())
    }

    fn known(&self, id: &str) -> bool {
        self.local_ids.contains(id) || self.dependency_ids.contains(id)
    }
}

fn validate_reference_closure(
    parts: &DocumentBatchParts,
    indexes: &BatchIndexes<'_>,
    maximum_edges: usize,
) -> Result<(), DocumentBatchError> {
    let mut edges = 0usize;
    let mut add_edge = |field: &'static str, id: &str, exists: bool| {
        edges = edges
            .checked_add(1)
            .ok_or(DocumentBatchError::ReferenceLimit {
                actual: usize::MAX,
                maximum: maximum_edges,
            })?;
        if edges > maximum_edges {
            return Err(DocumentBatchError::ReferenceLimit {
                actual: edges,
                maximum: maximum_edges,
            });
        }
        if !exists {
            return Err(DocumentBatchError::MissingReference {
                field,
                id: id.to_owned(),
            });
        }
        Ok(())
    };

    for artifact in &parts.text_artifacts {
        add_edge(
            "text_artifact.source_blob_id",
            &artifact.source_blob_id,
            indexes
                .source_ids
                .contains(artifact.source_blob_id.as_str())
                || indexes
                    .dependency_ids
                    .contains(artifact.source_blob_id.as_str()),
        )?;
        for page in &artifact.page_results {
            for span in &page.spans {
                add_edge(
                    "text_artifact.page_results.spans",
                    &span.text_artifact_id,
                    span.text_artifact_id == artifact.meta.record_id,
                )?;
            }
        }
    }
    for structure in &parts.structures {
        if let Some(parent) = structure.parent_id.as_deref() {
            add_edge(
                "structure.parent_id",
                parent,
                indexes.structure_ids.contains(parent),
            )?;
            let parent_lists_child =
                indexes
                    .structure_children
                    .get(parent)
                    .is_some_and(|children| {
                        children
                            .iter()
                            .any(|child| child == &structure.meta.record_id)
                    });
            add_edge("structure.parent_children", parent, parent_lists_child)?;
        }
        for child in &structure.ordered_children {
            add_edge(
                "structure.ordered_children",
                child,
                indexes.structure_ids.contains(child.as_str()),
            )?;
            let parent_matches = indexes
                .structure_parents
                .get(child.as_str())
                .copied()
                .flatten()
                == Some(structure.meta.record_id.as_str());
            add_edge("structure.child_parent", child, parent_matches)?;
        }
        for span in &structure.source_spans {
            add_edge(
                "structure.source_spans",
                &span.text_artifact_id,
                indexes.text_ids.contains(span.text_artifact_id.as_str()),
            )?;
        }
        for locator in &structure.page_locators {
            add_edge(
                "structure.page_locators",
                &locator.source_blob_id,
                indexes.source_ids.contains(locator.source_blob_id.as_str())
                    || indexes
                        .dependency_ids
                        .contains(locator.source_blob_id.as_str()),
            )?;
        }
    }
    validate_structure_cycles(indexes)?;
    for provision in &parts.provisions {
        add_edge(
            "provision.regulation_id",
            &provision.regulation_id,
            indexes.known(&provision.regulation_id),
        )?;
        if let Some(parent) = provision.parent_provision_id.as_deref() {
            add_edge(
                "provision.parent_provision_id",
                parent,
                indexes.known(parent),
            )?;
        }
        for lineage in &provision.lineage_refs {
            add_edge("provision.lineage_refs", lineage, indexes.known(lineage))?;
        }
    }
    for version in &parts.versions {
        add_edge(
            "version.provision_id",
            &version.provision_id,
            indexes
                .provision_ids
                .contains(version.provision_id.as_str())
                || indexes
                    .dependency_ids
                    .contains(version.provision_id.as_str()),
        )?;
        add_edge(
            "version.text_ref",
            &version.text_ref.artifact_id,
            indexes
                .artifact_ids
                .contains(version.text_ref.artifact_id.as_str())
                || indexes
                    .dependency_ids
                    .contains(version.text_ref.artifact_id.as_str()),
        )?;
        for span in &version.spans {
            add_edge(
                "version.spans",
                &span.text_artifact_id,
                indexes.text_ids.contains(span.text_artifact_id.as_str())
                    || indexes
                        .dependency_ids
                        .contains(span.text_artifact_id.as_str()),
            )?;
        }
        for event in &version.supporting_events {
            add_edge("version.supporting_events", event, indexes.known(event))?;
        }
    }
    for chunk in &parts.chunks {
        for version in &chunk.provision_version_refs {
            add_edge(
                "chunk.provision_version_refs",
                version,
                indexes.version_ids.contains(version.as_str())
                    || indexes.dependency_ids.contains(version.as_str()),
            )?;
        }
        add_edge(
            "chunk.text_span",
            &chunk.text_span.text_artifact_id,
            indexes
                .text_ids
                .contains(chunk.text_span.text_artifact_id.as_str()),
        )?;
        for structure in chunk
            .structure_node_refs
            .iter()
            .chain(chunk.parent_refs.iter())
        {
            add_edge(
                "chunk.structure_refs",
                structure,
                indexes.structure_ids.contains(structure.as_str()),
            )?;
        }
        for exception in &chunk.exception_refs {
            add_edge("chunk.exception_refs", exception, indexes.known(exception))?;
        }
    }
    for edition in &parts.editions {
        if let Some(regulation) = edition.regulation_id.as_deref() {
            add_edge(
                "edition.regulation_id",
                regulation,
                indexes.known(regulation),
            )?;
        }
        for source in &edition.source_refs {
            add_edge("edition.source_refs", source, indexes.known(source))?;
        }
    }
    for observation in &parts.observations {
        if let Some(source) = observation.source_blob_id.as_deref() {
            add_edge("observation.source_blob_id", source, indexes.known(source))?;
        }
    }
    for regulation in &parts.regulations {
        add_edge(
            "regulation.issuer_id",
            &regulation.issuer_id,
            indexes.known(&regulation.issuer_id),
        )?;
    }
    for change in &parts.changes {
        add_edge(
            "change.amending_source.source_blob_id",
            &change.amending_source.source_blob_id,
            indexes.known(&change.amending_source.source_blob_id),
        )?;
        add_edge(
            "change.amending_source.provision_version_id",
            &change.amending_source.provision_version_id,
            indexes.known(&change.amending_source.provision_version_id),
        )?;
        add_edge(
            "change.amending_source.regulation_id",
            &change.amending_source.regulation_id,
            indexes.known(&change.amending_source.regulation_id),
        )?;
        for provision in &change.affected_provisions {
            add_edge(
                "change.affected_provisions",
                provision,
                indexes.known(provision),
            )?;
        }
        for span in &change.replacement_spans {
            add_edge(
                "change.replacement_spans",
                &span.text_artifact_id,
                indexes.text_ids.contains(span.text_artifact_id.as_str())
                    || indexes
                        .dependency_ids
                        .contains(span.text_artifact_id.as_str()),
            )?;
        }
        for source in &change.supports.sources {
            add_edge(
                "change.supports.sources.source_blob_id",
                &source.source_blob_id,
                indexes.known(&source.source_blob_id),
            )?;
            add_edge(
                "change.supports.sources.provision_version_id",
                &source.provision_version_id,
                indexes.known(&source.provision_version_id),
            )?;
            add_edge(
                "change.supports.sources.regulation_id",
                &source.regulation_id,
                indexes.known(&source.regulation_id),
            )?;
        }
        for span in &change.supports.spans {
            add_edge(
                "change.supports.spans",
                &span.text_artifact_id,
                indexes.known(&span.text_artifact_id),
            )?;
        }
        for locator in &change.supports.locators {
            add_edge(
                "change.supports.locators",
                &locator.source_blob_id,
                indexes.known(&locator.source_blob_id),
            )?;
        }
    }
    Ok(())
}

fn validate_structure_cycles(indexes: &BatchIndexes<'_>) -> Result<(), DocumentBatchError> {
    let mut resolved = HashSet::new();
    for start in &indexes.structure_ids {
        if resolved.contains(start) {
            continue;
        }
        let mut visiting = HashSet::new();
        let mut path = Vec::new();
        let mut current = Some(*start);
        while let Some(id) = current {
            if resolved.contains(id) {
                break;
            }
            if !visiting.insert(id) {
                return Err(DocumentBatchError::StructureCycle(id.to_owned()));
            }
            path.push(id);
            current = indexes.structure_parents.get(id).copied().flatten();
        }
        resolved.extend(path);
    }
    Ok(())
}

fn page_issues(text_artifacts: &[documents::TextArtifact]) -> Vec<common::ValidationIssue> {
    let succeeded = text_artifacts
        .iter()
        .flat_map(|artifact| &artifact.page_results)
        .filter(|page| {
            page.status.value() == common::CompletionStatus::COMPLETION_STATUS_SUCCEEDED as i32
        })
        .count();
    let severity = if succeeded == 0 {
        common::Severity::SEVERITY_ERROR
    } else {
        common::Severity::SEVERITY_WARNING
    };
    let mut issues = Vec::new();
    for artifact in text_artifacts {
        for (index, page) in artifact.page_results.iter().enumerate() {
            if page.status.value() != common::CompletionStatus::COMPLETION_STATUS_SUCCEEDED as i32 {
                issues.push(common::ValidationIssue {
                    code: "PAGE_EXTRACTION_INCOMPLETE".to_owned(),
                    severity: EnumOrUnknown::new(severity),
                    record_id: artifact.meta.record_id.clone(),
                    field_path: format!("page_results[{index}]"),
                    evidence_refs: page
                        .errors
                        .iter()
                        .filter_map(|error| error.item_id.clone())
                        .collect(),
                    disposition: "retain_partial_and_require_followup".to_owned(),
                    ..Default::default()
                });
            }
        }
    }
    issues
}

fn derive_completeness(
    text_artifacts: &[documents::TextArtifact],
    chunks: &[documents::Chunk],
    issues: &[common::ValidationIssue],
) -> common::Completeness {
    let mut succeeded = 0usize;
    let mut failed = 0usize;
    for page in text_artifacts
        .iter()
        .flat_map(|artifact| &artifact.page_results)
    {
        if page.status.value() == common::CompletionStatus::COMPLETION_STATUS_SUCCEEDED as i32 {
            succeeded += 1;
        } else {
            failed += 1;
        }
    }
    if text_artifacts.is_empty() || (succeeded == 0 && chunks.is_empty()) {
        common::Completeness::COMPLETENESS_NONE
    } else if failed > 0
        || issues.iter().any(|issue| {
            matches!(
                issue.severity.enum_value(),
                Ok(common::Severity::SEVERITY_WARNING | common::Severity::SEVERITY_ERROR)
            )
        })
    {
        common::Completeness::COMPLETENESS_PARTIAL
    } else {
        common::Completeness::COMPLETENESS_COMPLETE
    }
}

fn record_count(parts: &DocumentBatchParts) -> Result<usize, DocumentBatchError> {
    [
        parts.sources.len(),
        parts.text_artifacts.len(),
        parts.structures.len(),
        parts.provisions.len(),
        parts.versions.len(),
        parts.chunks.len(),
        parts.issues.len(),
        parts.editions.len(),
        parts.regulations.len(),
        parts.observations.len(),
        parts.changes.len(),
    ]
    .into_iter()
    .try_fold(1usize, |total, count| total.checked_add(count))
    .ok_or(DocumentBatchError::RecordLimit {
        actual: usize::MAX,
        maximum: usize::MAX,
    })
}

fn validate_all_wire_records(parts: &DocumentBatchParts) -> Result<(), DocumentBatchError> {
    for message in parts
        .sources
        .iter()
        .map(|value| value as &dyn protobuf::MessageDyn)
        .chain(
            parts
                .text_artifacts
                .iter()
                .map(|value| value as &dyn protobuf::MessageDyn),
        )
        .chain(
            parts
                .structures
                .iter()
                .map(|value| value as &dyn protobuf::MessageDyn),
        )
        .chain(
            parts
                .provisions
                .iter()
                .map(|value| value as &dyn protobuf::MessageDyn),
        )
        .chain(
            parts
                .versions
                .iter()
                .map(|value| value as &dyn protobuf::MessageDyn),
        )
        .chain(
            parts
                .chunks
                .iter()
                .map(|value| value as &dyn protobuf::MessageDyn),
        )
        .chain(
            parts
                .issues
                .iter()
                .map(|value| value as &dyn protobuf::MessageDyn),
        )
        .chain(
            parts
                .editions
                .iter()
                .map(|value| value as &dyn protobuf::MessageDyn),
        )
        .chain(
            parts
                .regulations
                .iter()
                .map(|value| value as &dyn protobuf::MessageDyn),
        )
        .chain(
            parts
                .observations
                .iter()
                .map(|value| value as &dyn protobuf::MessageDyn),
        )
        .chain(
            parts
                .changes
                .iter()
                .map(|value| value as &dyn protobuf::MessageDyn),
        )
    {
        validate_wire(message)?;
    }
    Ok(())
}

fn check_meta(
    meta: &MessageField<common::RecordMeta>,
    corpus_id: &str,
) -> Result<(), DocumentBatchError> {
    let meta = meta
        .as_ref()
        .ok_or(DocumentBatchError::InvalidIdentity("record.meta"))?;
    if meta.corpus_id != corpus_id {
        return Err(DocumentBatchError::InvalidIdentity("record corpus_id"));
    }
    Ok(())
}

fn validate_config(config: &DocumentBatchConfig) -> Result<(), DocumentBatchError> {
    if config.maximum_records == 0 {
        return Err(DocumentBatchError::InvalidConfig("maximum_records"));
    }
    if config.maximum_reference_edges == 0 {
        return Err(DocumentBatchError::InvalidConfig("maximum_reference_edges"));
    }
    Ok(())
}

fn record_meta(corpus_id: &str, record_id: &str) -> common::RecordMeta {
    common::RecordMeta {
        schema_version: WIRE_SCHEMA_VERSION,
        corpus_id: corpus_id.to_owned(),
        record_id: record_id.to_owned(),
        ..Default::default()
    }
}

fn validate_wire(message: &dyn protobuf::MessageDyn) -> Result<(), DocumentBatchError> {
    wire::validate(message, Limits::default()).map_err(DocumentBatchError::WireValidation)
}

fn valid_ascii_id(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 256
        && value.bytes().all(|byte| {
            byte.is_ascii_alphanumeric() || matches!(byte, b':' | b'_' | b'-' | b'.' | b'/')
        })
}
