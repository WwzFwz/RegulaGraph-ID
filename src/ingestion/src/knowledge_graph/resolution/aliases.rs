//! Mengelola pemetaan alias dan mention ke canonical ID dengan konteksnya.
//!
//! Peran dalam komponen:
//! Mendukung assembly dan query linking memakai identitas bersama.
//!
//! Kontrak integrasi dan perhatian implementasi:
//! Jangan gunakan satu dictionary global nama-ke-ID tanpa ruang ambiguitas; label identik bisa merujuk beberapa entitas.
//!
//! Benchmark dan gate penerimaan:
//! [RESOLUTION] Ukur pairwise precision/recall/F1, false merge, false split, mention yang hilang, waktu per batch, dan biaya. Gate: semua mention tetap terlacak; pasal dari peraturan berbeda tidak digabung hanya karena nama sama. Blocking harus diukur juga terhadap pasangan benar yang terlewat.
//! Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
//! Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//!
//! Status: pembentukan alias dari keputusan LINK eksplisit aktif sebagai library; penerimaan
//! receipt registry, stage RESOLVE, dan pengukuran false merge/split belum diimplementasikan.
//! Caller wajib memverifikasi decision terhadap state/receipt registry Go sebelum memakai helper.
//! Bukti verifikasi: Test assignment, revisi, source evidence, scope, dan homonym lintas peraturan.
//! Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

use crate::domain::wire::{validate, Limits};
use crate::knowledge_graph::resolution::blocking::LookupKey;
use crate::wire::{common, graph};
use protobuf::MessageField;
use sha2::{Digest, Sha256};

#[derive(Clone, Debug, Eq, PartialEq)]
pub enum AliasError {
    InvalidInput(&'static str),
    UnsupportedDecision,
    MismatchedIdentity(&'static str),
    MissingEvidence,
    InvalidOutput(String),
}

/// Materialize one sourced alias only after the Go registry has authenticated this LINK decision.
///
/// The caller supplies the exact mention, proposal, decision, and registry entity from one
/// revision-pinned batch. The local evidence check scans spans and compares source-ref lists; it does
/// not perform fuzzy matching, registry access, or a merge. The mention ID remains support.
pub fn alias_from_registry_link(
    mention: &graph::Mention,
    proposal: &graph::ResolutionProposal,
    decision: &graph::ResolutionDecision,
    entity: &graph::CanonicalEntity,
    language: &str,
) -> Result<graph::Alias, AliasError> {
    let mention_meta = mention
        .meta
        .as_ref()
        .ok_or(AliasError::InvalidInput("mention.meta"))?;
    let proposal_meta = proposal
        .meta
        .as_ref()
        .ok_or(AliasError::InvalidInput("proposal.meta"))?;
    let decision_meta = decision
        .meta
        .as_ref()
        .ok_or(AliasError::InvalidInput("decision.meta"))?;
    let entity_meta = entity
        .meta
        .as_ref()
        .ok_or(AliasError::InvalidInput("entity.meta"))?;
    if [mention_meta, proposal_meta, decision_meta, entity_meta]
        .iter()
        .any(|meta| {
            meta.schema_version != 1
                || meta.record_id.is_empty()
                || meta.corpus_id != mention_meta.corpus_id
        })
    {
        return Err(AliasError::MismatchedIdentity("record meta"));
    }
    if proposal.action.enum_value() != Ok(graph::ResolutionAction::RESOLUTION_ACTION_LINK)
        || decision.action.enum_value() != Ok(graph::ResolutionAction::RESOLUTION_ACTION_LINK)
    {
        return Err(AliasError::UnsupportedDecision);
    }
    if decision.proposal_id != proposal_meta.record_id
        || !proposal
            .mention_ids
            .iter()
            .any(|id| id == &mention_meta.record_id)
        || proposal.candidate_ids.len() != 1
        || proposal.candidate_ids[0] != entity_meta.record_id
        || decision.assigned_canonical_ids.len() != 1
        || decision.assigned_canonical_ids[0] != entity_meta.record_id
    {
        return Err(AliasError::MismatchedIdentity("assignment"));
    }
    if proposal.expected_registry_revision == 0
        || entity.registry_revision > proposal.expected_registry_revision
        || decision.registry_revision < proposal.expected_registry_revision
    {
        return Err(AliasError::MismatchedIdentity("registry revision"));
    }
    if mention.candidate_type != entity.entity_type
        || !matches!(
            entity.entity_type.as_str(),
            "regulation"
                | "provision"
                | "organization"
                | "role"
                | "person"
                | "activity"
                | "obligation"
                | "requirement"
                | "exception"
                | "defined_term"
                | "permit"
                | "prohibition"
                | "procedure"
                | "document"
                | "sanction"
                | "date"
                | "place"
                | "legal_concept"
        )
        || entity.scope.trim().is_empty()
        || entity.scope.len() > 512
        || entity.scope.contains('\0')
        || entity.review_state.enum_value() != Ok(common::ReviewState::REVIEW_STATE_UNREVIEWED)
        || language.is_empty()
        || language.len() > 35
        || !language
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || byte == b'-')
    {
        return Err(AliasError::MismatchedIdentity("type, scope or language"));
    }
    let evidence = proposal
        .evidence
        .as_ref()
        .ok_or(AliasError::MissingEvidence)?;
    let span = mention
        .text_span
        .as_ref()
        .ok_or(AliasError::MissingEvidence)?;
    if span.start_byte >= span.end_byte
        || !evidence.spans.iter().any(|candidate| {
            candidate.text_artifact_id == span.text_artifact_id
                && candidate.start_byte <= span.start_byte
                && candidate.end_byte >= span.end_byte
        })
        || !mention.source_refs.iter().any(|source| {
            evidence.sources.iter().any(|candidate| {
                candidate.source_blob_id == source.source_blob_id
                    && candidate.provision_version_id == source.provision_version_id
                    && candidate.regulation_id == source.regulation_id
            })
        })
    {
        return Err(AliasError::MissingEvidence);
    }
    let lookup = LookupKey::new(&entity.entity_type, &entity.scope, &mention.surface_form)
        .map_err(|_| AliasError::InvalidInput("mention surface"))?;
    if lookup.normalized_surface.len() > 512 || lookup.normalized_surface.contains('\0') {
        return Err(AliasError::InvalidInput("normalized lookup"));
    }
    let mut hasher = Sha256::new();
    for part in [
        mention_meta.corpus_id.as_str(),
        entity_meta.record_id.as_str(),
        mention_meta.record_id.as_str(),
        decision_meta.record_id.as_str(),
        language,
    ] {
        hasher.update((part.len() as u64).to_be_bytes());
        hasher.update(part.as_bytes());
    }
    let alias = graph::Alias {
        meta: MessageField::some(common::RecordMeta {
            schema_version: 1,
            corpus_id: mention_meta.corpus_id.clone(),
            record_id: format!("alias:{:x}", hasher.finalize()),
            ..Default::default()
        }),
        canonical_id: entity_meta.record_id.clone(),
        surface: mention.surface_form.clone(),
        normalized_lookup: lookup.normalized_surface,
        language: language.to_owned(),
        scope: entity.scope.clone(),
        support_refs: vec![mention_meta.record_id.clone()],
        ..Default::default()
    };
    validate(&alias, Limits::default()).map_err(AliasError::InvalidOutput)?;
    Ok(alias)
}

#[cfg(test)]
mod tests {
    use super::*;
    use protobuf::EnumOrUnknown;

    fn meta(id: &str) -> MessageField<common::RecordMeta> {
        MessageField::some(common::RecordMeta {
            schema_version: 1,
            corpus_id: "corpus:1".into(),
            record_id: id.into(),
            ..Default::default()
        })
    }

    fn fixture() -> (
        graph::Mention,
        graph::ResolutionProposal,
        graph::ResolutionDecision,
        graph::CanonicalEntity,
    ) {
        let source = common::SourceVersionRef {
            source_blob_id: "blob:a".into(),
            provision_version_id: "version:a".into(),
            regulation_id: "law:a".into(),
            ..Default::default()
        };
        let span = common::TextSpan {
            text_artifact_id: "text:a".into(),
            start_byte: 10,
            end_byte: 15,
            ..Default::default()
        };
        let mention = graph::Mention {
            meta: meta("mention:a"),
            text_span: MessageField::some(span.clone()),
            source_refs: vec![source.clone()],
            surface_form: " Badan   A ".into(),
            candidate_type: "organization".into(),
            extraction_manifest: MessageField::some(common::ProducerManifest {
                software: "fixture".into(),
                build: "fixture".into(),
                schema_version: 1,
                config_hash: MessageField::some(common::ContentHash {
                    sha256: "a".repeat(64),
                    ..Default::default()
                }),
                ..Default::default()
            }),
            ..Default::default()
        };
        let proposal = graph::ResolutionProposal {
            meta: meta("proposal:a"),
            mention_ids: vec!["mention:a".into()],
            candidate_ids: vec!["canonical:a".into()],
            action: EnumOrUnknown::new(graph::ResolutionAction::RESOLUTION_ACTION_LINK),
            evidence: MessageField::some(common::Provenance {
                sources: vec![source],
                spans: vec![span],
                ..Default::default()
            }),
            method: "registry".into(),
            expected_registry_revision: 2,
            local_correlation_id: "correlation:a".into(),
            ..Default::default()
        };
        let decision = graph::ResolutionDecision {
            meta: meta("decision:a"),
            proposal_id: "proposal:a".into(),
            assigned_canonical_ids: vec!["canonical:a".into()],
            action: EnumOrUnknown::new(graph::ResolutionAction::RESOLUTION_ACTION_LINK),
            reason: "explicit registry assignment".into(),
            actor: "registry".into(),
            registry_revision: 3,
            ..Default::default()
        };
        let entity = graph::CanonicalEntity {
            meta: meta("canonical:a"),
            entity_type: "organization".into(),
            preferred_label: "Organisasi A".into(),
            scope: "national".into(),
            registry_revision: 2,
            review_state: EnumOrUnknown::new(common::ReviewState::REVIEW_STATE_UNREVIEWED),
            ..Default::default()
        };
        (mention, proposal, decision, entity)
    }

    #[test]
    fn link_keeps_source_and_scope_without_fusing_homonyms() {
        let (mention, proposal, decision, entity) = fixture();
        for input in [
            &mention as &dyn protobuf::MessageDyn,
            &proposal,
            &decision,
            &entity,
        ] {
            validate(input, Limits::default()).unwrap();
        }
        let alias =
            alias_from_registry_link(&mention, &proposal, &decision, &entity, "id").unwrap();
        assert_eq!(alias.canonical_id, "canonical:a");
        assert_eq!(alias.normalized_lookup, "badan a");
        assert_eq!(alias.scope, "national");
        assert_eq!(alias.support_refs, ["mention:a"]);
        assert_eq!(
            alias.meta.record_id,
            alias_from_registry_link(&mention, &proposal, &decision, &entity, "id")
                .unwrap()
                .meta
                .record_id
        );
        let mut another_law = entity.clone();
        another_law.scope = "regional".into();
        another_law.meta = meta("canonical:b");
        assert!(matches!(
            alias_from_registry_link(&mention, &proposal, &decision, &another_law, "id"),
            Err(AliasError::MismatchedIdentity("assignment"))
        ));
    }

    #[test]
    fn rejects_unapproved_or_unproven_aliases() {
        let (mention, proposal, decision, entity) = fixture();
        let mut wrong_decision = decision.clone();
        wrong_decision.action =
            EnumOrUnknown::new(graph::ResolutionAction::RESOLUTION_ACTION_DEFER);
        assert!(matches!(
            alias_from_registry_link(&mention, &proposal, &wrong_decision, &entity, "id"),
            Err(AliasError::UnsupportedDecision)
        ));
        let mut stale = decision.clone();
        stale.registry_revision = 1;
        assert!(matches!(
            alias_from_registry_link(&mention, &proposal, &stale, &entity, "id"),
            Err(AliasError::MismatchedIdentity("registry revision"))
        ));
        let mut unsupported = proposal.clone();
        unsupported.evidence = MessageField::some(common::Provenance {
            sources: proposal.evidence.sources.clone(),
            ..Default::default()
        });
        assert!(matches!(
            alias_from_registry_link(&mention, &unsupported, &decision, &entity, "id"),
            Err(AliasError::MissingEvidence)
        ));
        let mut unsupported_type = entity.clone();
        unsupported_type.entity_type = "unknown_entity_type".into();
        assert!(matches!(
            alias_from_registry_link(&mention, &proposal, &decision, &unsupported_type, "id"),
            Err(AliasError::MismatchedIdentity("type, scope or language"))
        ));
        let mut long_mention = mention.clone();
        long_mention.surface_form = "a".repeat(513);
        assert!(matches!(
            alias_from_registry_link(&long_mention, &proposal, &decision, &entity, "id"),
            Err(AliasError::InvalidInput("normalized lookup"))
        ));
        long_mention.surface_form = "a\0b".into();
        assert!(matches!(
            alias_from_registry_link(&long_mention, &proposal, &decision, &entity, "id"),
            Err(AliasError::InvalidInput("normalized lookup"))
        ));
    }

    #[test]
    fn sourced_alias_supports_provision_identity_without_merging_articles() {
        let (mut mention, proposal, decision, mut entity) = fixture();
        mention.candidate_type = "provision".into();
        entity.entity_type = "provision".into();
        entity.scope = "regulation:fixture".into();
        let alias = alias_from_registry_link(&mention, &proposal, &decision, &entity, "id")
            .expect("authenticated provision LINK can retain its parent regulation scope");
        assert_eq!(alias.scope, "regulation:fixture");
        assert_eq!(alias.support_refs, vec![mention.meta.record_id.clone()]);
    }

    #[test]
    fn accepts_evidence_enclosing_the_mention_but_not_a_different_text() {
        let (mention, mut proposal, decision, entity) = fixture();
        proposal.evidence = MessageField::some(common::Provenance {
            sources: mention.source_refs.clone(),
            spans: vec![common::TextSpan {
                text_artifact_id: "text:a".into(),
                start_byte: 5,
                end_byte: 20,
                ..Default::default()
            }],
            ..Default::default()
        });
        assert!(alias_from_registry_link(&mention, &proposal, &decision, &entity, "id").is_ok());
        let mut wrong_text = proposal.clone();
        wrong_text.evidence = MessageField::some(common::Provenance {
            sources: mention.source_refs.clone(),
            spans: vec![common::TextSpan {
                text_artifact_id: "text:b".into(),
                start_byte: 5,
                end_byte: 20,
                ..Default::default()
            }],
            ..Default::default()
        });
        assert!(matches!(
            alias_from_registry_link(&mention, &wrong_text, &decision, &entity, "id"),
            Err(AliasError::MissingEvidence)
        ));
        let mut wrong_source = proposal.clone();
        wrong_source.evidence = MessageField::some(common::Provenance {
            sources: vec![common::SourceVersionRef {
                source_blob_id: "blob:b".into(),
                provision_version_id: "version:a".into(),
                regulation_id: "law:a".into(),
                ..Default::default()
            }],
            spans: proposal.evidence.spans.clone(),
            ..Default::default()
        });
        assert!(matches!(
            alias_from_registry_link(&mention, &wrong_source, &decision, &entity, "id"),
            Err(AliasError::MissingEvidence)
        ));
    }
}
