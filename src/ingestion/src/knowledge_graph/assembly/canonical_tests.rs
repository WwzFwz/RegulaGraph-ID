//! Tests canonical graph identities with deterministic, synthetic resolved records.
//! Reordering/renaming extraction IDs must preserve results; qualifiers, direction,
//! temporal scope and independent support groups must survive. These are structural
//! correctness tests, not legal equivalence, native model quality or release benchmarks.

use protobuf::{EnumOrUnknown, Message, MessageField};

use super::builder::ResolvedRelations;
use super::canonical::{canonicalize_relations, CanonicalizationError as E};
use crate::wire::{common, graph};

fn meta(id: &str) -> MessageField<common::RecordMeta> {
    MessageField::some(common::RecordMeta {
        schema_version: 1,
        corpus_id: "corpus:test".into(),
        record_id: id.into(),
        ..Default::default()
    })
}

fn fixture() -> ResolvedRelations {
    let assertion = graph::RelationAssertion {
        meta: meta("assertion:a"),
        subject_id: "canonical:a".into(),
        object_id: "canonical:b".into(),
        predicate_id: "references".into(),
        ontology_version: "ontology:v1".into(),
        origin: EnumOrUnknown::new(graph::AssertionOrigin::ASSERTION_ORIGIN_EXPLICIT),
        temporal_scope: MessageField::some(common::TemporalScope {
            mode: EnumOrUnknown::new(common::TemporalMode::TEMPORAL_MODE_CURRENT),
            unresolved_policy: EnumOrUnknown::new(
                common::UnresolvedPolicy::UNRESOLVED_POLICY_REPORT,
            ),
            ..Default::default()
        }),
        ..Default::default()
    };
    let support = graph::SupportRecord {
        meta: meta("support:a"),
        assertion_id: "assertion:a".into(),
        evidence_spans: vec![common::TextSpan {
            text_artifact_id: "text:a".into(),
            start_byte: 0,
            end_byte: 4,
            ..Default::default()
        }],
        source_refs: vec![common::SourceVersionRef {
            source_blob_id: "blob:a".into(),
            regulation_id: "regulation:a".into(),
            provision_version_id: "version:a".into(),
            ..Default::default()
        }],
        extraction_manifest: MessageField::some(common::ProducerManifest {
            software: "fixture".into(),
            build: "test".into(),
            schema_version: 1,
            config_hash: MessageField::some(common::ContentHash {
                sha256: "a".repeat(64),
                ..Default::default()
            }),
            ..Default::default()
        }),
        independent_source_group: "mirror-group:a".into(),
        review_state: EnumOrUnknown::new(common::ReviewState::REVIEW_STATE_UNREVIEWED),
        ..Default::default()
    };
    ResolvedRelations {
        assertions: vec![assertion],
        supports: vec![support],
        mentions: vec![],
    }
}

fn duplicate(f: &mut ResolvedRelations) {
    let mut assertion = f.assertions[0].clone();
    assertion.meta = meta("assertion:copy");
    f.assertions.push(assertion);
    let mut support = f.supports[0].clone();
    support.meta = meta("support:copy");
    support.assertion_id = "assertion:copy".into();
    f.supports.push(support);
}

#[test]
fn canonical_dedup_preserves_mappings_and_independent_supports() {
    let mut f = fixture();
    duplicate(&mut f);
    let one = canonicalize_relations(&f, 100, 100000).unwrap();
    assert_eq!(one.relations.assertions.len(), 1);
    assert_eq!(one.relations.supports.len(), 1);
    assert_eq!(one.assertion_ids.len(), 2);
    assert_eq!(one.support_ids.len(), 2);
    f.supports[1].independent_source_group = "other-group".into();
    f.supports[1].source_refs[0].source_blob_id = "blob:other".into();
    let two = canonicalize_relations(&f, 100, 100000).unwrap();
    assert_eq!(two.relations.assertions.len(), 1);
    assert_eq!(two.relations.supports.len(), 2);
    // Withdrawing one source leaves the stable assertion and remaining support untouched.
    f.assertions.pop();
    f.supports.pop();
    let remaining = canonicalize_relations(&f, 100, 100000).unwrap();
    assert_eq!(remaining.relations.assertions, two.relations.assertions);
    assert!(two
        .relations
        .supports
        .contains(&remaining.relations.supports[0]));
}

#[test]
fn canonical_identity_ignores_extraction_ids_and_set_order() {
    let mut f = fixture();
    f.assertions[0].qualifiers = vec![
        graph::Qualifier {
            predicate_id: "condition:a".into(),
            value: Some(graph::qualifier::Value::Literal("A".into())),
            ..Default::default()
        },
        graph::Qualifier {
            predicate_id: "condition:b".into(),
            value: Some(graph::qualifier::Value::Number(-0.0)),
            ..Default::default()
        },
    ];
    let baseline = canonicalize_relations(&f, 100, 100000).unwrap();
    f.assertions[0].meta = meta("renamed:assertion");
    f.supports[0].assertion_id = "renamed:assertion".into();
    f.supports[0].meta = meta("renamed:support");
    f.assertions[0].qualifiers.reverse();
    f.assertions[0].qualifiers[0].value = Some(graph::qualifier::Value::Number(0.0));
    let repeated = f.assertions[0].qualifiers[1].clone();
    f.assertions[0].qualifiers.push(repeated);
    let reordered = canonicalize_relations(&f, 100, 100000).unwrap();
    assert_eq!(
        baseline.relations.assertions,
        reordered.relations.assertions
    );
    assert_eq!(baseline.relations.supports, reordered.relations.supports);
}

#[test]
fn canonical_identity_keeps_semantic_distinctions() {
    let baseline = canonicalize_relations(&fixture(), 100, 100000).unwrap();
    for case in 0..6 {
        let mut f = fixture();
        let a = &mut f.assertions[0];
        match case {
            0 => {
                std::mem::swap(&mut a.subject_id, &mut a.object_id);
            }
            1 => a.predicate_id = "requires".into(),
            2 => a.origin = EnumOrUnknown::new(graph::AssertionOrigin::ASSERTION_ORIGIN_INFERRED),
            3 => a.ontology_version = "ontology:v2".into(),
            4 => a.qualifiers.push(graph::Qualifier {
                predicate_id: "condition".into(),
                value: Some(graph::qualifier::Value::Literal(
                    "only when licensed".into(),
                )),
                ..Default::default()
            }),
            _ => {
                a.temporal_scope.as_mut().unwrap().effective_at =
                    MessageField::some(common::CalendarDate {
                        year: 2026,
                        month: 10,
                        day: 9,
                        ..Default::default()
                    });
            }
        }
        let changed = canonicalize_relations(&f, 100, 100000).unwrap();
        assert_ne!(
            baseline.relations.assertions[0].meta.record_id,
            changed.relations.assertions[0].meta.record_id
        );
    }
}

#[test]
fn canonical_exceptions_are_remapped_before_parent_identity() {
    let mut f = fixture();
    duplicate(&mut f);
    f.assertions[1].predicate_id = "exception_to".into();
    f.assertions[0].exception_refs = vec!["assertion:copy".into()];
    let result = canonicalize_relations(&f, 100, 100000).unwrap();
    let parent = &result
        .relations
        .assertions
        .iter()
        .find(|a| a.predicate_id == "references")
        .unwrap();
    assert_eq!(
        parent.exception_refs,
        vec![result.assertion_ids["assertion:copy"].clone()]
    );
    f.assertions.reverse();
    f.supports.reverse();
    let reordered = canonicalize_relations(&f, 100, 100000).unwrap();
    assert_eq!(result.relations.assertions, reordered.relations.assertions);
    f.assertions[0].qualifiers.push(graph::Qualifier {
        predicate_id: "condition".into(),
        value: Some(graph::qualifier::Value::Literal("new condition".into())),
        ..Default::default()
    });
    let changed = canonicalize_relations(&f, 100, 100000).unwrap();
    assert_ne!(
        result.assertion_ids["assertion:a"],
        changed.assertion_ids["assertion:a"]
    );
}

#[test]
fn canonical_rejects_cycles_missing_support_and_budget_overflow() {
    let mut f = fixture();
    f.assertions[0].exception_refs = vec!["assertion:a".into()];
    assert_eq!(
        canonicalize_relations(&f, 100, 100000).unwrap_err(),
        E::CyclicExceptions
    );
    f.assertions[0].exception_refs = vec!["missing:a".into()];
    assert_eq!(
        canonicalize_relations(&f, 100, 100000).unwrap_err(),
        E::MissingException
    );
    f = fixture();
    f.supports.clear();
    assert_eq!(
        canonicalize_relations(&f, 100, 100000).unwrap_err(),
        E::MissingSupport
    );
    f = fixture();
    assert_eq!(
        canonicalize_relations(&f, 1, 100000).unwrap_err(),
        E::BudgetExceeded
    );
    assert_eq!(
        canonicalize_relations(&f, 100, 1).unwrap_err(),
        E::BudgetExceeded
    );
    let input_bytes = f.assertions[0].compute_size() + f.supports[0].compute_size();
    assert_eq!(
        canonicalize_relations(&f, 100, input_bytes).unwrap_err(),
        E::BudgetExceeded
    );
    f.supports[0].meta.as_mut().unwrap().corpus_id = "corpus:foreign".into();
    assert_eq!(
        canonicalize_relations(&f, 100, 100000).unwrap_err(),
        E::InvalidInput
    );
}

#[test]
fn canonical_rejects_unknown_fields_in_nested_and_top_level_records() {
    for nested in [false, true] {
        let mut f = fixture();
        if nested {
            f.supports[0].source_refs[0]
                .special_fields
                .mut_unknown_fields()
                .add_varint(100, 1);
        } else {
            f.assertions[0]
                .special_fields
                .mut_unknown_fields()
                .add_varint(100, 1);
        }
        assert_eq!(
            canonicalize_relations(&f, 100, 100000).unwrap_err(),
            E::UnknownFields
        );
    }
}

#[test]
fn canonical_does_not_emit_invalid_normalized_compare_scope() {
    let mut f = fixture();
    let scope = f.assertions[0].temporal_scope.as_mut().unwrap();
    scope.mode = EnumOrUnknown::new(common::TemporalMode::TEMPORAL_MODE_COMPARE);
    let date = common::CalendarDate {
        year: 2026,
        month: 10,
        day: 9,
        ..Default::default()
    };
    scope.compare_dates = vec![date.clone(), date];
    assert_eq!(
        canonicalize_relations(&f, 100, 100000).unwrap_err(),
        E::InvalidInput
    );
}

#[test]
fn canonical_rejects_collision_with_retained_mention_id() {
    let mut f = fixture();
    let baseline = canonicalize_relations(&f, 100, 100000).unwrap();
    f.mentions.push(graph::Mention {
        meta: meta(&baseline.relations.assertions[0].meta.record_id),
        surface_form: "izin".into(),
        candidate_type: "permit".into(),
        text_span: MessageField::some(f.supports[0].evidence_spans[0].clone()),
        source_refs: f.supports[0].source_refs.clone(),
        extraction_manifest: f.supports[0].extraction_manifest.clone(),
        ..Default::default()
    });
    assert_eq!(
        canonicalize_relations(&f, 100, 100000).unwrap_err(),
        E::IdentityCollision
    );
}
