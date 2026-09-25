//! Menyusun rencana perubahan node/edge dari entitas canonical dan relasi berbukti.
//!
//! Peran dalam komponen:
//! Menghubungkan hasil graph engineering dengan adapter persistensi.
//!
//! Kontrak integrasi dan perhatian implementasi:
//! Deduplikasi tidak menghapus ragam predicate atau sumber; retry idempotent; provenance dari sumber yang dihapus ditarik tanpa menghapus bukti lain.
//!
//! Benchmark dan gate penerimaan:
//! [GRAPH] Ukur validitas endpoint dan provenance, ketepatan predicate/arah, kelengkapan jalur bukti, waktu assembly/traversal, dan penggunaan memori. Gate: graph yang dipublikasikan tidak memiliki endpoint/bukti wajib yang hilang. Connectivity adalah diagnosis, bukan target memaksa satu komponen.
//!
//! [UPDATE] Gate: sumber tak berubah tidak diekstraksi ulang; update identik idempotent; dependensi terdampak diinvalidasi; bukti yang masih didukung sumber lain tetap tersedia. Uji pemulihan kegagalan parsial dan snapshot konsisten. Ukur waktu/biaya update terhadap full rebuild serta freshness lag.
//! Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
//! Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//!
//! Status: penggantian endpoint LINK/CREATE aktif sebagai library prapublikasi;
//! ID assertion/support masih ID ekstraksi, belum dedup ke identitas assertion
//! canonical. GraphDelta, closure, writer Neo4j, dan publication belum tersambung.
//!
//! Keluaran saat ini hanya ResolvedRelations prapublikasi. GraphDelta batch kelak
//! diserahkan ke coordinator Go, yang menggunakan adapter Neo4j untuk commit.
//! Worker tidak membuka transaksi Neo4j sendiri.
//! Rekomendasi implementasi berikutnya: rakit GraphDelta immutable dari hasil ini
//! bersama dependency manifest/closure berversi, lalu validasi seluruh batch.
//! Bukti verifikasi: Test duplicate extraction and withdrawing one of several sources; no dangling edges or deletion of shared support.
//! Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

use std::collections::{BTreeMap, BTreeSet};

use crate::domain::wire::{validate, Limits};
use crate::knowledge_graph::schema::Ontology;
use crate::wire::{common, graph};

/// Relasi sementara tetap memisahkan assertion dari seluruh support asli. ID
/// assertion/support masih ID ekstraksi dan tidak boleh dipublikasikan sampai
/// dedup canonical assertion key, remap support, dan validasi GraphDelta selesai.
/// Worker harus memeriksa `ReadVerified`, ontology, dan receipt registry committed;
/// set ID canonical sendiri bukan bukti keputusan sah atau revision historis.
#[derive(Debug, Clone)]
pub struct ResolvedRelations {
    pub assertions: Vec<graph::RelationAssertion>,
    pub supports: Vec<graph::SupportRecord>,
    pub mentions: Vec<graph::Mention>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum AssemblyError {
    InvalidInput,
    TooManyItems,
    DuplicateID,
    UnsupportedAction,
    UnresolvedMention,
    MissingCanonical,
    MissingSupport,
}

/// Resolve provisional mention IDs using committed decisions, retaining every
/// support. The canonical set must come from the committed registry revision
/// of `resolution`; the caller binds source bytes and decisions to verified
/// artifact hashes and Go's committed receipt before calling this function.
/// This pure stage is O(n log n); it cannot publish a GraphDelta by itself.
pub fn materialize_resolved_relations(
    source: &graph::ExtractionBatch,
    resolution: &graph::ResolutionBatch,
    source_ref: &common::ArtifactRef,
    ontology: &Ontology,
    canonical_ids: &BTreeSet<String>,
    maximum_items: usize,
) -> Result<ResolvedRelations, AssemblyError> {
    if maximum_items == 0
        || source.mentions.len() > maximum_items
        || source.assertions.len() > maximum_items
        || source.supports.len() > maximum_items
        || resolution.proposals.len() > maximum_items
        || resolution.decisions.len() > maximum_items
    {
        return Err(AssemblyError::TooManyItems);
    }
    if source.meta.schema_version != 1
        || resolution.meta.schema_version != 1
        || source.context.schema_version != 1
        || resolution.context.schema_version != 1
        || source.context.snapshot_ref.is_none()
        || resolution.context.snapshot_ref.is_none()
        || source.meta.corpus_id.is_empty()
        || source.meta.corpus_id != resolution.meta.corpus_id
        || source.context.corpus_id != source.meta.corpus_id
        || resolution.context.corpus_id != source.meta.corpus_id
        || source.context.snapshot_ref != resolution.context.snapshot_ref
        || source.context.config_fingerprint != resolution.context.config_fingerprint
        || source.context.auth_scope_ref != resolution.context.auth_scope_ref
        || resolution.source_extraction_batch.as_ref() != Some(source_ref)
        || source_ref.artifact_id.is_empty()
        || source_ref.content_hash.is_none()
        || source_ref.storage_key.is_empty()
        || source_ref.schema_version != 1
        || source.ontology_version.is_empty()
        || source.ontology_version != resolution.ontology_version
        || source.ontology_version != ontology.version()
        || resolution.registry_revision == 0
        || source.completeness.enum_value() != Ok(common::Completeness::COMPLETENESS_COMPLETE)
        || resolution.completeness.enum_value() != Ok(common::Completeness::COMPLETENESS_COMPLETE)
    {
        return Err(AssemblyError::InvalidInput);
    }
    validate(source_ref, Limits::default()).map_err(|_| AssemblyError::InvalidInput)?;
    validate(source, Limits::default()).map_err(|_| AssemblyError::InvalidInput)?;
    validate(resolution, Limits::default()).map_err(|_| AssemblyError::InvalidInput)?;
    ontology
        .validate_extraction_batch(source)
        .map_err(|_| AssemblyError::InvalidInput)?;

    let mut record_ids = BTreeSet::from([
        source.meta.record_id.clone(),
        resolution.meta.record_id.clone(),
    ]);
    if record_ids.len() != 2 || record_ids.contains("") {
        return Err(AssemblyError::DuplicateID);
    }
    let mut mention_ids = BTreeSet::new();
    for mention in &source.mentions {
        if mention.meta.record_id.is_empty()
            || mention.meta.corpus_id != source.meta.corpus_id
            || mention.meta.schema_version != 1
            || mention.text_span.is_none()
            || !valid_span(&mention.text_span)
            || mention.source_refs.is_empty()
            || mention
                .source_refs
                .iter()
                .any(|reference| !valid_source_ref(reference))
            || !mention_ids.insert(mention.meta.record_id.clone())
            || !record_ids.insert(mention.meta.record_id.clone())
        {
            return Err(AssemblyError::DuplicateID);
        }
    }
    let mut proposals = BTreeMap::new();
    for proposal in &resolution.proposals {
        if proposal.meta.record_id.is_empty()
            || proposal.meta.corpus_id != source.meta.corpus_id
            || proposal.meta.schema_version != 1
            || proposal.mention_ids.is_empty()
            || proposal.expected_registry_revision == 0
            || proposal.expected_registry_revision > resolution.registry_revision
            || !record_ids.insert(proposal.meta.record_id.clone())
            || proposals
                .insert(proposal.meta.record_id.as_str(), proposal)
                .is_some()
        {
            return Err(AssemblyError::DuplicateID);
        }
    }
    if resolution.decisions.len() != proposals.len() {
        return Err(AssemblyError::InvalidInput);
    }
    let mut seen_decisions = BTreeSet::new();
    let mut assigned = BTreeMap::new();
    for decision in &resolution.decisions {
        if decision.meta.record_id.is_empty()
            || decision.meta.corpus_id != source.meta.corpus_id
            || decision.meta.schema_version != 1
            || decision.registry_revision == 0
            || decision.registry_revision > resolution.registry_revision
            || !record_ids.insert(decision.meta.record_id.clone())
            || !seen_decisions.insert(decision.proposal_id.as_str())
        {
            return Err(AssemblyError::DuplicateID);
        }
        let proposal = proposals
            .get(decision.proposal_id.as_str())
            .ok_or(AssemblyError::InvalidInput)?;
        if decision.registry_revision < proposal.expected_registry_revision {
            return Err(AssemblyError::InvalidInput);
        }
        if !matches!(
            decision.action.enum_value(),
            Ok(graph::ResolutionAction::RESOLUTION_ACTION_LINK
                | graph::ResolutionAction::RESOLUTION_ACTION_CREATE)
        ) || decision.action != proposal.action
            || decision.assigned_canonical_ids.len() != 1
        {
            return Err(AssemblyError::UnsupportedAction);
        }
        let canonical = &decision.assigned_canonical_ids[0];
        match decision.action.enum_value() {
            Ok(graph::ResolutionAction::RESOLUTION_ACTION_LINK)
                if proposal.candidate_ids.len() != 1 || proposal.candidate_ids[0] != *canonical =>
            {
                return Err(AssemblyError::InvalidInput);
            }
            Ok(graph::ResolutionAction::RESOLUTION_ACTION_CREATE)
                if !proposal.candidate_ids.is_empty()
                    || proposal.proposed_identity_keys.is_empty() =>
            {
                return Err(AssemblyError::InvalidInput);
            }
            _ => {}
        }
        if !canonical_ids.contains(canonical) {
            return Err(AssemblyError::MissingCanonical);
        }
        for mention_id in &proposal.mention_ids {
            if !mention_ids.contains(mention_id)
                || assigned
                    .insert(mention_id.as_str(), canonical.as_str())
                    .is_some()
            {
                return Err(AssemblyError::DuplicateID);
            }
        }
    }
    if assigned.len() != mention_ids.len() {
        return Err(AssemblyError::UnresolvedMention);
    }

    let resolve_endpoint = |id: &str| -> Result<String, AssemblyError> {
        let canonical = assigned.get(id).copied().unwrap_or(id);
        if !canonical_ids.contains(canonical) {
            return Err(AssemblyError::MissingCanonical);
        }
        Ok(canonical.to_owned())
    };
    let mut assertion_ids = BTreeSet::new();
    let mut assertions = Vec::with_capacity(source.assertions.len());
    for original in &source.assertions {
        if original.meta.record_id.is_empty()
            || original.meta.corpus_id != source.meta.corpus_id
            || original.meta.schema_version != 1
            || original.predicate_id.is_empty()
            || original.ontology_version != source.ontology_version
            || !mention_ids.contains(&original.subject_id)
            || !mention_ids.contains(&original.object_id)
            || !record_ids.insert(original.meta.record_id.clone())
            || !assertion_ids.insert(original.meta.record_id.clone())
        {
            return Err(AssemblyError::DuplicateID);
        }
        let mut assertion = original.clone();
        assertion.subject_id = resolve_endpoint(&original.subject_id)?;
        assertion.object_id = resolve_endpoint(&original.object_id)?;
        if !ontology.permits_canonical_endpoints(
            &assertion.predicate_id,
            &assertion.subject_id,
            &assertion.object_id,
        ) {
            return Err(AssemblyError::InvalidInput);
        }
        for qualifier in &mut assertion.qualifiers {
            match &qualifier.value {
                Some(graph::qualifier::Value::MentionId(id)) => {
                    let canonical = assigned
                        .get(id.as_str())
                        .ok_or(AssemblyError::UnresolvedMention)?;
                    qualifier.value = Some(graph::qualifier::Value::CanonicalId(
                        (*canonical).to_owned(),
                    ));
                }
                Some(graph::qualifier::Value::CanonicalId(_)) => {
                    return Err(AssemblyError::InvalidInput)
                }
                _ => {}
            }
        }
        assertions.push(assertion);
    }
    for original in &source.assertions {
        if original
            .exception_refs
            .iter()
            .any(|id| id == &original.meta.record_id || !assertion_ids.contains(id))
        {
            return Err(AssemblyError::InvalidInput);
        }
    }
    let mut support_ids = BTreeSet::new();
    let mut supported_assertions = BTreeSet::new();
    for support in &source.supports {
        if support.meta.record_id.is_empty()
            || support.meta.corpus_id != source.meta.corpus_id
            || support.meta.schema_version != 1
            || !record_ids.insert(support.meta.record_id.clone())
            || !support_ids.insert(support.meta.record_id.as_str())
        {
            return Err(AssemblyError::DuplicateID);
        }
        if !assertion_ids.contains(&support.assertion_id) {
            return Err(AssemblyError::MissingSupport);
        }
        if support.evidence_spans.is_empty()
            || support.evidence_spans.iter().any(|span| !valid_span(span))
            || support.source_refs.is_empty()
            || support
                .source_refs
                .iter()
                .any(|reference| !valid_source_ref(reference))
        {
            return Err(AssemblyError::MissingSupport);
        }
        supported_assertions.insert(support.assertion_id.as_str());
    }
    if supported_assertions.len() != assertions.len() {
        return Err(AssemblyError::MissingSupport);
    }
    Ok(ResolvedRelations {
        assertions,
        supports: source.supports.clone(),
        mentions: source.mentions.clone(),
    })
}

fn valid_span(span: &common::TextSpan) -> bool {
    !span.text_artifact_id.is_empty() && span.start_byte < span.end_byte
}

fn valid_source_ref(reference: &common::SourceVersionRef) -> bool {
    !reference.source_blob_id.is_empty()
        && !reference.regulation_id.is_empty()
        && !reference.provision_version_id.is_empty()
}

#[cfg(test)]
mod tests {
    use super::*;
    use protobuf::well_known_types::timestamp::Timestamp;
    use protobuf::{EnumOrUnknown, MessageField};

    fn hash() -> common::ContentHash {
        common::ContentHash {
            sha256: "a".repeat(64),
            ..Default::default()
        }
    }

    fn producer() -> common::ProducerManifest {
        common::ProducerManifest {
            software: "regulagraph-test".into(),
            build: "fixture".into(),
            schema_version: 1,
            config_hash: MessageField::some(hash()),
            ..Default::default()
        }
    }

    fn model(task: common::ModelTask) -> common::ModelManifest {
        common::ModelManifest {
            model_id: "model:fixture".into(),
            version: "1".into(),
            weights_hash: MessageField::some(hash()),
            tokenizer_hash: MessageField::some(hash()),
            task: EnumOrUnknown::new(task),
            max_tokens: 4096,
            precision: "fixture".into(),
            backend: "fixture".into(),
            ..Default::default()
        }
    }

    fn dependencies(id: &str) -> common::DependencyManifest {
        common::DependencyManifest {
            artifact_id: id.into(),
            producer_manifest: MessageField::some(producer()),
            ..Default::default()
        }
    }

    fn meta(id: &str) -> common::RecordMeta {
        common::RecordMeta {
            record_id: id.to_owned(),
            corpus_id: "corpus:1".into(),
            schema_version: 1,
            ..Default::default()
        }
    }

    fn materialize_resolved_relations(
        source: &graph::ExtractionBatch,
        resolution: &graph::ResolutionBatch,
        source_ref: &common::ArtifactRef,
        canonical: &BTreeSet<String>,
        max: usize,
    ) -> Result<ResolvedRelations, AssemblyError> {
        let ontology = Ontology::parse_jsonc(include_bytes!(concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/../../configs/ontology-v1.jsonc"
        )))
        .unwrap();
        super::materialize_resolved_relations(
            source, resolution, source_ref, &ontology, canonical, max,
        )
    }

    fn fixture() -> (
        graph::ExtractionBatch,
        graph::ResolutionBatch,
        common::ArtifactRef,
        BTreeSet<String>,
    ) {
        let context = common::RequestContext {
            schema_version: 1,
            request_id: "request:assembly".into(),
            trace_id: "trace:assembly".into(),
            corpus_id: "corpus:1".into(),
            snapshot_ref: MessageField::some(common::SnapshotRef {
                corpus_id: "corpus:1".into(),
                snapshot_id: "snapshot:1".into(),
                sequence: 1,
                manifest_hash: MessageField::some(hash()),
                representation_generation: "generation:1".into(),
                ..Default::default()
            }),
            deadline: MessageField::some(Timestamp {
                seconds: 1_900_000_000,
                ..Default::default()
            }),
            config_fingerprint: MessageField::some(hash()),
            auth_scope_ref: "scope:ingestion".into(),
            ..Default::default()
        };
        let source_ref = common::ArtifactRef {
            artifact_id: "extract:1".into(),
            content_hash: MessageField::some(common::ContentHash {
                sha256: "a".repeat(64),
                ..Default::default()
            }),
            storage_key: "sha256/extract".into(),
            media_type: "application/x-protobuf".into(),
            schema_version: 1,
            ..Default::default()
        };
        let make_mention = |id: &str| graph::Mention {
            meta: MessageField::some(meta(id)),
            text_span: MessageField::some(common::TextSpan {
                text_artifact_id: "text:1".into(),
                start_byte: 1,
                end_byte: 4,
                ..Default::default()
            }),
            source_refs: vec![common::SourceVersionRef {
                source_blob_id: "blob:1".into(),
                regulation_id: "reg:1".into(),
                provision_version_id: "version:1".into(),
                ..Default::default()
            }],
            candidate_type: "organization".into(),
            surface_form: "Badan A".into(),
            extraction_manifest: MessageField::some(producer()),
            ..Default::default()
        };
        let assertion = graph::RelationAssertion {
            meta: MessageField::some(meta("assertion:1")),
            subject_id: "mention:1".into(),
            object_id: "mention:2".into(),
            predicate_id: "references".into(),
            ontology_version: "id-regulation-ontology-v1".into(),
            origin: EnumOrUnknown::new(graph::AssertionOrigin::ASSERTION_ORIGIN_EXPLICIT),
            temporal_scope: MessageField::some(common::TemporalScope {
                mode: EnumOrUnknown::new(common::TemporalMode::TEMPORAL_MODE_CURRENT),
                unresolved_policy: EnumOrUnknown::new(
                    common::UnresolvedPolicy::UNRESOLVED_POLICY_REPORT,
                ),
                ..Default::default()
            }),
            qualifiers: vec![graph::Qualifier {
                predicate_id: "scope".into(),
                value: Some(graph::qualifier::Value::MentionId("mention:1".into())),
                ..Default::default()
            }],
            ..Default::default()
        };
        let supports = (1..=2)
            .map(|i| graph::SupportRecord {
                meta: MessageField::some(meta(&format!("support:{i}"))),
                assertion_id: "assertion:1".into(),
                evidence_spans: vec![common::TextSpan {
                    text_artifact_id: "text:1".into(),
                    start_byte: 1,
                    end_byte: 4,
                    ..Default::default()
                }],
                source_refs: vec![common::SourceVersionRef {
                    source_blob_id: "blob:1".into(),
                    regulation_id: "reg:1".into(),
                    provision_version_id: "version:1".into(),
                    ..Default::default()
                }],
                extraction_manifest: MessageField::some(producer()),
                independent_source_group: format!("source-group:{i}"),
                review_state: EnumOrUnknown::new(common::ReviewState::REVIEW_STATE_UNREVIEWED),
                ..Default::default()
            })
            .collect();
        let source = graph::ExtractionBatch {
            meta: MessageField::some(meta("extraction:1")),
            context: MessageField::some(context.clone()),
            source_document_batch: MessageField::some(source_ref.clone()),
            mentions: vec![make_mention("mention:1"), make_mention("mention:2")],
            assertions: vec![assertion],
            supports,
            ontology_version: "id-regulation-ontology-v1".into(),
            completeness: EnumOrUnknown::new(common::Completeness::COMPLETENESS_COMPLETE),
            dependencies: MessageField::some(dependencies("dependencies:extract")),
            model_manifest: MessageField::some(model(common::ModelTask::MODEL_TASK_EXTRACT)),
            prompt_hash: MessageField::some(hash()),
            item_counts: MessageField::some(common::Counts::new()),
            token_usage: MessageField::some(common::TokenUsage {
                tokenizer_id: "tokenizer:fixture".into(),
                ..Default::default()
            }),
            ..Default::default()
        };
        let proposal = graph::ResolutionProposal {
            meta: MessageField::some(meta("proposal:1")),
            mention_ids: vec!["mention:1".into()],
            candidate_ids: vec!["canonical:1".into()],
            action: EnumOrUnknown::new(graph::ResolutionAction::RESOLUTION_ACTION_LINK),
            expected_registry_revision: 7,
            evidence: MessageField::some(common::Provenance::new()),
            method: "fixture".into(),
            local_correlation_id: "correlation:1".into(),
            ..Default::default()
        };
        let decision = graph::ResolutionDecision {
            meta: MessageField::some(meta("decision:1")),
            proposal_id: "proposal:1".into(),
            assigned_canonical_ids: vec!["canonical:1".into()],
            action: EnumOrUnknown::new(graph::ResolutionAction::RESOLUTION_ACTION_LINK),
            registry_revision: 7,
            reason: "fixture".into(),
            actor: "actor:fixture".into(),
            ..Default::default()
        };
        let proposal_two = graph::ResolutionProposal {
            meta: MessageField::some(meta("proposal:2")),
            mention_ids: vec!["mention:2".into()],
            candidate_ids: vec!["canonical:2".into()],
            action: EnumOrUnknown::new(graph::ResolutionAction::RESOLUTION_ACTION_LINK),
            expected_registry_revision: 7,
            evidence: MessageField::some(common::Provenance::new()),
            method: "fixture".into(),
            local_correlation_id: "correlation:2".into(),
            ..Default::default()
        };
        let decision_two = graph::ResolutionDecision {
            meta: MessageField::some(meta("decision:2")),
            proposal_id: "proposal:2".into(),
            assigned_canonical_ids: vec!["canonical:2".into()],
            action: EnumOrUnknown::new(graph::ResolutionAction::RESOLUTION_ACTION_LINK),
            registry_revision: 7,
            reason: "fixture".into(),
            actor: "actor:fixture".into(),
            ..Default::default()
        };
        let resolution = graph::ResolutionBatch {
            meta: MessageField::some(meta("resolution:1")),
            context: MessageField::some(context),
            source_extraction_batch: MessageField::some(source_ref.clone()),
            proposals: vec![proposal, proposal_two],
            decisions: vec![decision, decision_two],
            ontology_version: "id-regulation-ontology-v1".into(),
            registry_revision: 7,
            completeness: EnumOrUnknown::new(common::Completeness::COMPLETENESS_COMPLETE),
            dependencies: MessageField::some(dependencies("dependencies:resolve")),
            model_manifest: MessageField::some(model(common::ModelTask::MODEL_TASK_RESOLVE)),
            item_counts: MessageField::some(common::Counts::new()),
            token_usage: MessageField::some(common::TokenUsage {
                tokenizer_id: "tokenizer:fixture".into(),
                ..Default::default()
            }),
            ..Default::default()
        };
        let canonical = ["canonical:1".into(), "canonical:2".into()]
            .into_iter()
            .collect();
        (source, resolution, source_ref, canonical)
    }

    #[test]
    fn resolves_both_endpoints_and_keeps_all_independent_supports() {
        let (source, resolution, source_ref, canonical) = fixture();
        validate(&source, Limits::default()).unwrap();
        validate(&resolution, Limits::default()).unwrap();
        let result =
            materialize_resolved_relations(&source, &resolution, &source_ref, &canonical, 10)
                .unwrap();
        assert_eq!(result.assertions.len(), 1);
        assert_eq!(result.assertions[0].subject_id, "canonical:1");
        assert_eq!(result.assertions[0].object_id, "canonical:2");
        assert_eq!(
            result.assertions[0].qualifiers[0].canonical_id(),
            "canonical:1"
        );
        assert_eq!(result.supports.len(), 2);
        assert_eq!(result.supports, source.supports);
    }

    #[test]
    fn rejects_unresolved_or_unsupported_graph_inputs() {
        let (source, mut resolution, source_ref, canonical) = fixture();
        resolution.decisions.pop();
        resolution.proposals.pop();
        assert!(matches!(
            materialize_resolved_relations(&source, &resolution, &source_ref, &canonical, 10),
            Err(AssemblyError::UnresolvedMention)
        ));
        resolution = fixture().1;
        resolution.decisions[0].action =
            EnumOrUnknown::new(graph::ResolutionAction::RESOLUTION_ACTION_MERGE);
        assert!(matches!(
            materialize_resolved_relations(&source, &resolution, &source_ref, &canonical, 10),
            Err(AssemblyError::UnsupportedAction)
        ));
        resolution.decisions = fixture().1.decisions;
        let mut without_support = source.clone();
        without_support.supports.clear();
        assert!(matches!(
            materialize_resolved_relations(
                &without_support,
                &resolution,
                &source_ref,
                &canonical,
                10
            ),
            Err(AssemblyError::MissingSupport)
        ));
        let mut wrong_ref = source_ref.clone();
        wrong_ref.artifact_id = "extract:other".into();
        assert!(matches!(
            materialize_resolved_relations(&source, &resolution, &wrong_ref, &canonical, 10),
            Err(AssemblyError::InvalidInput)
        ));
        resolution.decisions[0].assigned_canonical_ids[0] = "canonical:2".into();
        assert!(matches!(
            materialize_resolved_relations(&source, &resolution, &source_ref, &canonical, 10),
            Err(AssemblyError::InvalidInput)
        ));
        resolution = fixture().1;
        resolution.decisions[0].registry_revision = 6;
        assert!(matches!(
            materialize_resolved_relations(&source, &resolution, &source_ref, &canonical, 10),
            Err(AssemblyError::InvalidInput)
        ));
        resolution = fixture().1;
        resolution.decisions[1].meta = resolution.decisions[0].meta.clone();
        assert!(matches!(
            materialize_resolved_relations(&source, &resolution, &source_ref, &canonical, 10),
            Err(AssemblyError::DuplicateID)
        ));
        let mut bad_source = source.clone();
        bad_source.supports[0].evidence_spans[0] = common::TextSpan::new();
        assert!(matches!(
            materialize_resolved_relations(&bad_source, &fixture().1, &source_ref, &canonical, 10),
            Err(AssemblyError::InvalidInput)
        ));
        bad_source = source.clone();
        bad_source.assertions[0].exception_refs = vec!["assertion:1".into()];
        assert!(matches!(
            materialize_resolved_relations(&bad_source, &fixture().1, &source_ref, &canonical, 10),
            Err(AssemblyError::InvalidInput)
        ));
        bad_source = source.clone();
        bad_source.assertions[0].object_id = "canonical:2".into();
        assert!(matches!(
            materialize_resolved_relations(&bad_source, &fixture().1, &source_ref, &canonical, 10),
            Err(AssemblyError::InvalidInput)
        ));
        bad_source = source.clone();
        bad_source.supports[0].meta = bad_source.mentions[0].meta.clone();
        assert!(matches!(
            materialize_resolved_relations(&bad_source, &fixture().1, &source_ref, &canonical, 10),
            Err(AssemblyError::DuplicateID)
        ));
        resolution = fixture().1;
        resolution.proposals[1].candidate_ids[0] = "canonical:1".into();
        resolution.decisions[1].assigned_canonical_ids[0] = "canonical:1".into();
        assert!(matches!(
            materialize_resolved_relations(&source, &resolution, &source_ref, &canonical, 10),
            Err(AssemblyError::InvalidInput)
        ));
        let mut invalid_context = source.clone();
        invalid_context.context.as_mut().unwrap().request_id.clear();
        assert!(matches!(
            materialize_resolved_relations(
                &invalid_context,
                &fixture().1,
                &source_ref,
                &canonical,
                10
            ),
            Err(AssemblyError::InvalidInput)
        ));
    }
}
