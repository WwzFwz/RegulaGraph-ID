//! Verifies source-scoped GraphDelta assembly using actual DocumentBatch/EXTRACT
//! validators and synthetic committed decisions. Tests cover provenance, UTF-8,
//! canonical type/revision, global identity, dependency conflicts and budgets.
//! No fixture approval proves a legal fact or production model/performance quality.

use super::delta::{assemble_graph_delta, DeltaError as E, DeltaInput};
use crate::domain::wire::Limits;
use crate::knowledge_graph::extraction::extractor::{
    assemble_extraction_batch,
    tests::{fixture_parts, source_batch},
    ExtractionBatchConfig,
};
use crate::knowledge_graph::schema::Ontology;
use crate::wire::{common, documents, graph};
use protobuf::{EnumOrUnknown, Message, MessageField};
use sha2::{Digest, Sha256};
use std::collections::BTreeMap;

struct Fixture {
    docs: documents::DocumentBatch,
    extraction: graph::ExtractionBatch,
    resolution: graph::ResolutionBatch,
    extraction_ref: common::ArtifactRef,
    resolution_ref: common::ArtifactRef,
    registry_ref: common::ArtifactRef,
    entities: Vec<graph::CanonicalEntity>,
    texts: BTreeMap<String, Vec<u8>>,
    producer: common::ProducerManifest,
}
fn hash(c: char) -> common::ContentHash {
    common::ContentHash {
        sha256: c.to_string().repeat(64),
        ..Default::default()
    }
}
fn reference(id: &str, c: char) -> common::ArtifactRef {
    common::ArtifactRef {
        artifact_id: id.into(),
        content_hash: MessageField::some(hash(c)),
        schema_version: 1,
        storage_key: format!("sha256/{}", c.to_string().repeat(64)),
        media_type: "application/x-protobuf".into(),
        byte_size: 1,
        ..Default::default()
    }
}
fn meta(id: &str) -> MessageField<common::RecordMeta> {
    MessageField::some(common::RecordMeta {
        schema_version: 1,
        corpus_id: "regulagraph-id".into(),
        record_id: id.into(),
        ..Default::default()
    })
}
fn ontology() -> Ontology {
    Ontology::parse_jsonc(include_bytes!(concat!(
        env!("CARGO_MANIFEST_DIR"),
        "/../../configs/ontology-v1.jsonc"
    )))
    .unwrap()
}
fn fixture() -> Fixture {
    let mut docs = source_batch();
    let mut parts = fixture_parts();
    let base = common::SnapshotRef {
        corpus_id: "regulagraph-id".into(),
        snapshot_id: "snapshot:base".into(),
        sequence: 1,
        manifest_hash: MessageField::some(hash('a')),
        representation_generation: "generation:base".into(),
        ..Default::default()
    };
    docs.context.as_mut().unwrap().snapshot_ref = MessageField::some(base.clone());
    parts.context.snapshot_ref = MessageField::some(base);
    parts.ontology_version = ontology().version().into();
    parts.assertions[0].ontology_version = parts.ontology_version.clone();
    parts.assertions[0].predicate_id = "references".into();
    let raw = vec![b'a'; 31];
    docs.text_artifacts[0]
        .normalized_text_ref
        .as_mut()
        .unwrap()
        .content_hash = MessageField::some(common::ContentHash {
        sha256: format!("{:x}", Sha256::digest(&raw)),
        ..Default::default()
    });
    for version in &mut docs.versions {
        version.text_ref = docs.text_artifacts[0].normalized_text_ref.clone();
    }
    for m in &mut parts.mentions {
        m.surface_form = String::from_utf8(
            raw[m.text_span.start_byte as usize..m.text_span.end_byte as usize].to_vec(),
        )
        .unwrap();
    }
    let producer = parts
        .dependencies
        .producer_manifest
        .as_ref()
        .unwrap()
        .clone();
    let extraction =
        assemble_extraction_batch(parts, &docs, &ExtractionBatchConfig::default()).unwrap();
    let extraction_ref = reference("artifact:extract", 'd');
    let mut resolution = graph::ResolutionBatch {
        meta: meta("resolution:test"),
        context: extraction.context.clone(),
        source_extraction_batch: MessageField::some(extraction_ref.clone()),
        ontology_version: extraction.ontology_version.clone(),
        registry_revision: 7,
        completeness: EnumOrUnknown::new(common::Completeness::COMPLETENESS_COMPLETE),
        model_manifest: extraction.model_manifest.clone(),
        item_counts: MessageField::some(common::Counts {
            expected: 2,
            accepted: 2,
            ..Default::default()
        }),
        token_usage: extraction.token_usage.clone(),
        dependencies: MessageField::some(common::DependencyManifest {
            artifact_id: "dependencies:resolve".into(),
            producer_manifest: MessageField::some(producer.clone()),
            lookup_scope_revisions: vec![common::LookupScopeRevision {
                scope_id: "lookup:negative".into(),
                revision: 0,
                empty_result: true,
                ..Default::default()
            }],
            ..Default::default()
        }),
        ..Default::default()
    };
    resolution.model_manifest.as_mut().unwrap().task =
        EnumOrUnknown::new(common::ModelTask::MODEL_TASK_RESOLVE);
    let mut entities = vec![];
    for (i, mention) in extraction.mentions.iter().enumerate() {
        let canonical = format!("canonical:{i}");
        let proposal = format!("proposal:{i}");
        entities.push(graph::CanonicalEntity {
            meta: meta(&canonical),
            entity_type: mention.candidate_type.clone(),
            preferred_label: format!("concept {i}"),
            scope: "ID:national".into(),
            registry_revision: 6,
            review_state: EnumOrUnknown::new(common::ReviewState::REVIEW_STATE_UNREVIEWED),
            ..Default::default()
        });
        resolution.proposals.push(graph::ResolutionProposal {
            meta: meta(&proposal),
            mention_ids: vec![mention.meta.record_id.clone()],
            candidate_ids: vec![canonical.clone()],
            action: EnumOrUnknown::new(graph::ResolutionAction::RESOLUTION_ACTION_LINK),
            expected_registry_revision: 7,
            evidence: MessageField::some(common::Provenance::new()),
            method: "fixture".into(),
            local_correlation_id: format!("correlation:{i}"),
            ..Default::default()
        });
        resolution.decisions.push(graph::ResolutionDecision {
            meta: meta(&format!("decision:{i}")),
            proposal_id: proposal,
            assigned_canonical_ids: vec![canonical],
            action: EnumOrUnknown::new(graph::ResolutionAction::RESOLUTION_ACTION_LINK),
            registry_revision: 7,
            actor: "operator:fixture".into(),
            reason: "test".into(),
            ..Default::default()
        });
    }
    Fixture {
        docs,
        extraction,
        resolution,
        extraction_ref,
        resolution_ref: reference("artifact:resolution", 'e'),
        registry_ref: reference("artifact:registry", 'f'),
        entities,
        texts: BTreeMap::from([("text:fixture".into(), raw)]),
        producer,
    }
}
impl Fixture {
    fn input(&self) -> DeltaInput<'_> {
        DeltaInput {
            delta_id: "delta:fixture",
            target_sequence: 2,
            source_documents: &self.docs,
            normalized_texts: &self.texts,
            extraction: &self.extraction,
            extraction_ref: &self.extraction_ref,
            resolution: &self.resolution,
            resolution_ref: &self.resolution_ref,
            registry_ref: &self.registry_ref,
            registry_revision: 7,
            entities: &self.entities,
            producer: &self.producer,
        }
    }
    fn build(&self) -> Result<graph::GraphDelta, E> {
        assemble_graph_delta(self.input(), &ontology(), Limits::default())
    }
}

#[test]
fn graph_delta_preserves_source_supports_decisions_and_dependencies() {
    let mut f = fixture();
    let output = f.build().unwrap();
    assert!(output.validation_report.valid);
    assert_eq!(output.registry_revision, 7);
    assert_eq!(output.assertions[0].subject_id, "canonical:0");
    assert_eq!(
        output.supports[0].assertion_id,
        output.assertions[0].meta.record_id
    );
    assert_eq!(
        output.supports[0].source_refs,
        f.extraction.supports[0].source_refs
    );
    assert_eq!(output.dependencies.dependencies.len(), 5);
    assert!(output.dependencies.lookup_scope_revisions[0].empty_result);
    assert_eq!(output.validation_report.checked_records, 9);
    assert_eq!(output.entities[0].meta.visibility.from_seq, 2);
    assert!(output.visibility_closures.is_empty());
    assert!(output.support_changes.is_empty());
    assert!(f.extraction.mentions[0].meta.visibility.is_none());
    f.entities.reverse();
    f.resolution.proposals.reverse();
    f.resolution.decisions.reverse();
    assert_eq!(
        output.write_to_bytes().unwrap(),
        f.build().unwrap().write_to_bytes().unwrap()
    );
}

#[test]
fn graph_delta_rejects_registry_identity_and_revision_drift() {
    for mutate in [
        (|f: &mut Fixture| f.entities[0].entity_type = "organization".into()) as fn(&mut Fixture),
        |f| f.entities[0].registry_revision = 8,
        |f| f.entities[0].meta.as_mut().unwrap().corpus_id = "foreign".into(),
        |f| f.entities.push(f.entities[0].clone()),
        |f| {
            f.entities.pop();
        },
        |f| f.entities[0].meta.as_mut().unwrap().record_id = "mention:subject".into(),
    ] {
        let mut f = fixture();
        mutate(&mut f);
        assert!(f.build().is_err());
    }
    let f = fixture();
    let mut input = f.input();
    input.registry_revision = 8;
    assert!(assemble_graph_delta(input, &ontology(), Limits::default()).is_err());
    let mut input = f.input();
    input.target_sequence = u64::MAX;
    assert!(assemble_graph_delta(input, &ontology(), Limits::default()).is_err());
    let mut input = f.input();
    input.target_sequence = 1;
    assert!(assemble_graph_delta(input, &ontology(), Limits::default()).is_err());
}

#[test]
fn graph_delta_rejects_forged_or_missing_text_and_source_provenance() {
    for mutate in [
        (|f: &mut Fixture| {
            f.texts.clear();
        }) as fn(&mut Fixture),
        |f| f.texts.get_mut("text:fixture").unwrap()[0] = b'z',
        |f| f.extraction.mentions[0].surface_form = "invented".into(),
        |f| f.extraction.supports[0].source_refs[0].source_blob_id = "source:foreign".into(),
        |f| f.extraction.supports[0].evidence_spans[0].end_byte = 99,
        |f| f.docs.completeness = EnumOrUnknown::new(common::Completeness::COMPLETENESS_PARTIAL),
    ] {
        let mut f = fixture();
        mutate(&mut f);
        assert_eq!(f.build().unwrap_err(), E::InvalidSource);
    }
    let mut f = fixture();
    let raw = f.texts.get_mut("text:fixture").unwrap();
    raw[0] = 0xc3;
    raw[1] = 0xa9;
    f.docs.text_artifacts[0]
        .normalized_text_ref
        .as_mut()
        .unwrap()
        .content_hash
        .as_mut()
        .unwrap()
        .sha256 = format!("{:x}", Sha256::digest(&raw));
    for version in &mut f.docs.versions {
        version.text_ref = f.docs.text_artifacts[0].normalized_text_ref.clone();
    }
    f.extraction.mentions[0].surface_form = "éaaaa".into();
    assert!(f.build().is_ok(), "valid UTF-8 must reach assembly");
    f.extraction.mentions[0]
        .text_span
        .as_mut()
        .unwrap()
        .start_byte = 1;
    assert_eq!(f.build().unwrap_err(), E::InvalidSource);
}

#[test]
fn graph_delta_rejects_conflicting_dependencies_and_resource_overflow() {
    let mut f = fixture();
    f.resolution
        .dependencies
        .as_mut()
        .unwrap()
        .dependencies
        .push(common::Dependency {
            dependency_id: f.extraction_ref.artifact_id.clone(),
            fingerprint: MessageField::some(hash('0')),
            ..Default::default()
        });
    assert_eq!(f.build().unwrap_err(), E::DependencyConflict);
    let f = fixture();
    assert_eq!(
        assemble_graph_delta(
            f.input(),
            &ontology(),
            Limits {
                max_bytes: 1,
                ..Limits::default()
            }
        )
        .unwrap_err(),
        E::BudgetExceeded
    );
    assert!(assemble_graph_delta(
        f.input(),
        &ontology(),
        Limits {
            max_items: 1,
            ..Limits::default()
        }
    )
    .is_err());
    let mut input = f.input();
    input.delta_id = &f.extraction_ref.artifact_id;
    assert_eq!(
        assemble_graph_delta(input, &ontology(), Limits::default()).unwrap_err(),
        E::DependencyConflict
    );
}
