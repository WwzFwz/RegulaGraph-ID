//! Menentukan mention yang menunjuk entitas sama dan keputusan canonical yang terlacak.
//!
//! Peran dalam komponen:
//! Menghubungkan fakta lintas dokumen tanpa mencampur entitas berbeda.
//!
//! Kontrak integrasi dan perhatian implementasi:
//! Gunakan tipe, konteks, dan identitas peraturan; mention yang belum terselesaikan tetap disimpan. Merge dapat dikoreksi dengan riwayat dependensi.
//!
//! Benchmark dan gate penerimaan:
//! [RESOLUTION] Ukur pairwise precision/recall/F1, false merge, false split, mention yang hilang, waktu per batch, dan biaya. Gate: semua mention tetap terlacak; pasal dari peraturan berbeda tidak digabung hanya karena nama sama. Blocking harus diukur juga terhadap pasangan benar yang terlewat.
//! Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
//! Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//!
//! Status: perakitan proposal LINK/DEFER dari pilihan eksplisit dan kandidat registry terpin aktif
//! sebagai library. Pemilih semantik, keputusan registry, serta stage RESOLVE belum aktif.
//! Bukti verifikasi: Test kandidat asing, pilihan ganda, homonym, revisi stale dan provenance.
//! Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

use crate::domain::wire::{validate, Limits};
use crate::wire::{common, graph};
use protobuf::{EnumOrUnknown, Message, MessageField};
use sha2::{Digest, Sha256};
use std::collections::{BTreeSet, HashMap};

/// A decision supplied by a semantic model or reviewer; candidate count alone is never a LINK.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ResolutionChoice {
    pub mention_id: String,
    pub candidate_id: Option<String>,
    pub method: String,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub enum ProposalError {
    InvalidInput(&'static str),
    InvalidChoice(&'static str),
    InvalidOutput(String),
}

/// Bind one proposal per mention to source evidence and a single registry revision.
///
/// Missing choices become DEFER; an explicit LINK must name a candidate returned for that
/// mention. The result is advisory only. Go still verifies the immutable source, candidate
/// artifact, registry receipt, and authoritative decision before publication. Work is bounded
/// by 16 MiB wire inputs and O(n log n) set operations; no registry RPC or model load occurs here.
pub fn propose_from_candidate_batch(
    source: &graph::ExtractionBatch,
    source_ref: &common::ArtifactRef,
    candidates: &graph::RegistryCandidateBatch,
    choices: &[ResolutionChoice],
    maximum_items: usize,
) -> Result<Vec<graph::ResolutionProposal>, ProposalError> {
    if maximum_items == 0
        || source.mentions.len() > maximum_items
        || candidates.lookups.len() != source.mentions.len()
        || choices.len() > maximum_items
        || source.completeness.enum_value() != Ok(common::Completeness::COMPLETENESS_COMPLETE)
        || candidates.completeness.enum_value() != Ok(common::Completeness::COMPLETENESS_COMPLETE)
        || candidates.registry_revision == 0
        || candidates.source_extraction_batch.as_ref() != Some(source_ref)
        || source.meta.schema_version != 1
        || candidates.meta.schema_version != source.meta.schema_version
        || source.context.schema_version != 1
        || source.context.schema_version != candidates.context.schema_version
        || source.meta.corpus_id.is_empty()
        || source.context.corpus_id != source.meta.corpus_id
        || source.meta.corpus_id != candidates.meta.corpus_id
        || source.context.corpus_id != candidates.context.corpus_id
        || source.context.config_fingerprint != candidates.context.config_fingerprint
        || source.context.auth_scope_ref != candidates.context.auth_scope_ref
        || source.context.snapshot_ref != candidates.context.snapshot_ref
        || candidates.dependencies.is_none()
        || source.compute_size() > Limits::default().max_bytes as u64
        || candidates.compute_size() > Limits::default().max_bytes as u64
    {
        return Err(ProposalError::InvalidInput(
            "source and candidate batch identity",
        ));
    }
    validate(source_ref, Limits::default())
        .map_err(|_| ProposalError::InvalidInput("invalid source artifact ref"))?;
    let candidate_bytes = candidates
        .write_to_bytes()
        .map_err(|_| ProposalError::InvalidInput("encode candidate batch"))?;
    let candidate_fingerprint = Sha256::digest(&candidate_bytes);
    if candidates.aliases.len() > maximum_items
        || candidates.dependencies.dependencies.len() > maximum_items
        || candidates.dependencies.lookup_scope_revisions.len() > maximum_items
        || candidates
            .candidates
            .iter()
            .map(|entity| entity.identity_keys.len())
            .sum::<usize>()
            > maximum_items
        || candidates.dependencies.producer_manifest.is_none()
        || !candidates
            .dependencies
            .dependencies
            .iter()
            .any(|dependency| {
                dependency.dependency_id == source_ref.artifact_id
                    && dependency.fingerprint == source_ref.content_hash
            })
    {
        return Err(ProposalError::InvalidInput(
            "candidate dependency or record limit",
        ));
    }
    let mut choice_by_mention = HashMap::with_capacity(choices.len());
    for choice in choices {
        if choice.mention_id.is_empty()
            || choice.method.trim().is_empty()
            || choice.method.len() > 128
            || choice.method.contains('\0')
            || choice_by_mention
                .insert(choice.mention_id.as_str(), choice)
                .is_some()
        {
            return Err(ProposalError::InvalidChoice("duplicate or invalid choice"));
        }
    }
    let mut lookup_by_mention = HashMap::with_capacity(candidates.lookups.len());
    let mut scope_count = 0usize;
    for lookup in &candidates.lookups {
        if lookup.mention_id.is_empty()
            || lookup.scopes.is_empty()
            || lookup_by_mention
                .insert(lookup.mention_id.as_str(), lookup)
                .is_some()
        {
            return Err(ProposalError::InvalidInput(
                "duplicate or empty candidate lookup",
            ));
        }
        scope_count = scope_count
            .checked_add(lookup.scopes.len())
            .ok_or(ProposalError::InvalidInput("lookup scope limit"))?;
        if scope_count > maximum_items {
            return Err(ProposalError::InvalidInput("lookup scope limit"));
        }
    }
    let mut entity_by_id = HashMap::with_capacity(candidates.candidates.len());
    if candidates.candidates.len() > maximum_items {
        return Err(ProposalError::InvalidInput("candidate limit"));
    }
    for entity in &candidates.candidates {
        let id = entity.meta.record_id.as_str();
        if id.is_empty()
            || entity.meta.corpus_id != source.meta.corpus_id
            || entity.registry_revision == 0
            || entity.registry_revision > candidates.registry_revision
            || !matches!(
                entity.review_state.enum_value(),
                Ok(common::ReviewState::REVIEW_STATE_APPROVED
                    | common::ReviewState::REVIEW_STATE_UNREVIEWED)
            )
            || entity_by_id.insert(id, entity).is_some()
        {
            return Err(ProposalError::InvalidInput("invalid candidate entity"));
        }
    }
    let mut alias_ids = BTreeSet::new();
    let mut alias_keys = BTreeSet::new();
    for alias in &candidates.aliases {
        let owner = entity_by_id
            .get(alias.canonical_id.as_str())
            .ok_or(ProposalError::InvalidInput("alias has unknown owner"))?;
        if alias.meta.record_id.is_empty()
            || !alias_ids.insert(alias.meta.record_id.as_str())
            || entity_by_id.contains_key(alias.meta.record_id.as_str())
            || alias.meta.corpus_id != source.meta.corpus_id
            || alias.scope != owner.scope
            || alias.normalized_lookup.is_empty()
            || alias.support_refs.is_empty()
        {
            return Err(ProposalError::InvalidInput("invalid candidate alias"));
        }
        alias_keys.insert((
            alias.canonical_id.as_str(),
            alias.scope.as_str(),
            alias.normalized_lookup.as_str(),
        ));
    }
    let mut seen_mentions = BTreeSet::new();
    let mut observed_candidates = BTreeSet::new();
    let mut observed_alias_keys = BTreeSet::new();
    let mut observed_scopes = HashMap::<&str, (&str, &str, &str, u64, bool, BTreeSet<&str>)>::new();
    let mut candidate_work = 0usize;
    let mut proposals = Vec::with_capacity(source.mentions.len());
    for mention in &source.mentions {
        let id = mention.meta.record_id.as_str();
        if id.is_empty()
            || mention.meta.corpus_id != source.meta.corpus_id
            || !seen_mentions.insert(id)
            || mention.text_span.is_none()
            || mention.source_refs.is_empty()
        {
            return Err(ProposalError::InvalidInput("invalid extraction mention"));
        }
        let lookup = lookup_by_mention
            .get(id)
            .ok_or(ProposalError::InvalidInput("missing mention lookup"))?;
        let mut candidate_ids = BTreeSet::new();
        let mut seen_mention_scopes = BTreeSet::new();
        for scope in &lookup.scopes {
            if scope.entity_type != mention.candidate_type
                || scope.revision.is_none()
                || scope.canonical_scope.is_empty()
                || scope.normalized_lookup.is_empty()
                || scope.revision.scope_id
                    != registry_scope_id(
                        &scope.entity_type,
                        &scope.canonical_scope,
                        &scope.normalized_lookup,
                    )
                || scope.revision.revision > candidates.registry_revision
                || scope.revision.empty_result != scope.candidate_ids.is_empty()
                || !scope.candidate_ids.is_empty() && scope.revision.revision == 0
                || !seen_mention_scopes.insert(scope.revision.scope_id.as_str())
            {
                return Err(ProposalError::InvalidInput("invalid lookup scope"));
            }
            let current_ids: BTreeSet<&str> =
                scope.candidate_ids.iter().map(String::as_str).collect();
            if current_ids.len() != scope.candidate_ids.len() {
                return Err(ProposalError::InvalidInput("repeated scope candidate"));
            }
            let scope_id = scope.revision.scope_id.as_str();
            let observation = (
                scope.entity_type.as_str(),
                scope.canonical_scope.as_str(),
                scope.normalized_lookup.as_str(),
                scope.revision.revision,
                scope.revision.empty_result,
                current_ids,
            );
            if let Some(prior) = observed_scopes.get(scope_id) {
                if prior != &observation {
                    return Err(ProposalError::InvalidInput("inconsistent lookup scope"));
                }
            } else {
                observed_scopes.insert(scope_id, observation);
            }
            for candidate in &scope.candidate_ids {
                let entity =
                    entity_by_id
                        .get(candidate.as_str())
                        .ok_or(ProposalError::InvalidInput(
                            "lookup names unknown candidate",
                        ))?;
                if entity.entity_type != scope.entity_type
                    || entity.scope != scope.canonical_scope
                    || !alias_keys.contains(&(
                        candidate.as_str(),
                        scope.canonical_scope.as_str(),
                        scope.normalized_lookup.as_str(),
                    ))
                    || !candidate_ids.insert(candidate.as_str())
                {
                    return Err(ProposalError::InvalidInput(
                        "candidate type, scope, or uniqueness",
                    ));
                }
                observed_candidates.insert(candidate.as_str());
                observed_alias_keys.insert((
                    candidate.as_str(),
                    scope.canonical_scope.as_str(),
                    scope.normalized_lookup.as_str(),
                ));
            }
        }
        candidate_work = candidate_work
            .checked_add(candidate_ids.len())
            .ok_or(ProposalError::InvalidInput("candidate limit"))?;
        if candidate_work > maximum_items {
            return Err(ProposalError::InvalidInput("candidate limit"));
        }
        let choice = choice_by_mention.get(id).copied();
        let (action, targets, method) = match choice {
            Some(ResolutionChoice {
                candidate_id: Some(target),
                method,
                ..
            }) => {
                if !candidate_ids.contains(target.as_str()) {
                    return Err(ProposalError::InvalidChoice(
                        "LINK target absent from pinned lookup",
                    ));
                }
                (
                    graph::ResolutionAction::RESOLUTION_ACTION_LINK,
                    vec![target.clone()],
                    method.clone(),
                )
            }
            Some(ResolutionChoice {
                candidate_id: None,
                method,
                ..
            }) => (
                graph::ResolutionAction::RESOLUTION_ACTION_DEFER,
                candidate_ids.iter().map(|id| (*id).to_owned()).collect(),
                method.clone(),
            ),
            None => (
                graph::ResolutionAction::RESOLUTION_ACTION_DEFER,
                candidate_ids.iter().map(|id| (*id).to_owned()).collect(),
                "unselected_registry_candidates".into(),
            ),
        };
        let mut digest = Sha256::new();
        for part in [
            &source.meta.corpus_id,
            &source_ref.artifact_id,
            &source_ref.content_hash.sha256,
            &candidates.meta.record_id,
            &mention.meta.record_id,
        ] {
            digest.update((part.len() as u64).to_be_bytes());
            digest.update(part.as_bytes());
        }
        digest.update(candidates.registry_revision.to_be_bytes());
        digest.update(candidate_fingerprint.as_slice());
        let action_name = if action == graph::ResolutionAction::RESOLUTION_ACTION_LINK {
            "LINK"
        } else {
            "DEFER"
        };
        for part in std::iter::once(action_name)
            .chain(std::iter::once(method.as_str()))
            .chain(targets.iter().map(String::as_str))
        {
            digest.update((part.len() as u64).to_be_bytes());
            digest.update(part.as_bytes());
        }
        let proposal_id = format!("proposal:{:x}", digest.finalize());
        let proposal = graph::ResolutionProposal {
            meta: MessageField::some(common::RecordMeta {
                schema_version: 1,
                corpus_id: source.meta.corpus_id.clone(),
                record_id: proposal_id.clone(),
                ..Default::default()
            }),
            mention_ids: vec![id.to_owned()],
            candidate_ids: targets,
            action: EnumOrUnknown::new(action),
            evidence: MessageField::some(common::Provenance {
                sources: mention.source_refs.clone(),
                spans: vec![mention.text_span.as_ref().unwrap().clone()],
                ..Default::default()
            }),
            method,
            expected_registry_revision: candidates.registry_revision,
            local_correlation_id: proposal_id,
            ..Default::default()
        };
        validate(&proposal, Limits::default()).map_err(ProposalError::InvalidOutput)?;
        proposals.push(proposal);
    }
    if choice_by_mention
        .keys()
        .any(|id| !seen_mentions.contains(id))
    {
        return Err(ProposalError::InvalidChoice("choice names unknown mention"));
    }
    if observed_candidates.len() != entity_by_id.len() {
        return Err(ProposalError::InvalidInput("unreferenced candidate entity"));
    }
    if alias_keys
        .iter()
        .any(|key| !observed_alias_keys.contains(key))
    {
        return Err(ProposalError::InvalidInput("unreferenced candidate alias"));
    }
    if observed_scopes.len() != candidates.dependencies.lookup_scope_revisions.len() {
        return Err(ProposalError::InvalidInput("lookup dependency coverage"));
    }
    let mut seen_revisions = BTreeSet::new();
    for revision in &candidates.dependencies.lookup_scope_revisions {
        let Some(observed) = observed_scopes.get(revision.scope_id.as_str()) else {
            return Err(ProposalError::InvalidInput("lookup dependency coverage"));
        };
        if !seen_revisions.insert(revision.scope_id.as_str())
            || observed.3 != revision.revision
            || observed.4 != revision.empty_result
        {
            return Err(ProposalError::InvalidInput("lookup dependency coverage"));
        }
    }
    proposals.sort_by(|left, right| left.mention_ids[0].cmp(&right.mention_ids[0]));
    Ok(proposals)
}

fn registry_scope_id(entity_type: &str, scope: &str, normalized: &str) -> String {
    let mut digest = Sha256::new();
    for part in ["registry-alias-lookup:v1", entity_type, scope, normalized] {
        digest.update((part.len() as u64).to_be_bytes());
        digest.update(part.as_bytes());
    }
    format!("lookup:{:x}", digest.finalize())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn meta(id: &str) -> MessageField<common::RecordMeta> {
        MessageField::some(common::RecordMeta {
            schema_version: 1,
            corpus_id: "corpus:test".into(),
            record_id: id.into(),
            ..Default::default()
        })
    }

    fn fixture(
        ids: &[&str],
    ) -> (
        graph::ExtractionBatch,
        common::ArtifactRef,
        graph::RegistryCandidateBatch,
    ) {
        let context = common::RequestContext {
            schema_version: 1,
            corpus_id: "corpus:test".into(),
            auth_scope_ref: "scope:test".into(),
            config_fingerprint: MessageField::some(common::ContentHash {
                sha256: "a".repeat(64),
                ..Default::default()
            }),
            ..Default::default()
        };
        let mention = graph::Mention {
            meta: meta("mention:one"),
            text_span: MessageField::some(common::TextSpan {
                text_artifact_id: "text:one".into(),
                start_byte: 4,
                end_byte: 10,
                ..Default::default()
            }),
            source_refs: vec![common::SourceVersionRef {
                source_blob_id: "blob:one".into(),
                regulation_id: "reg:one".into(),
                provision_version_id: "version:one".into(),
                ..Default::default()
            }],
            surface_form: "Badan A".into(),
            candidate_type: "organization".into(),
            ..Default::default()
        };
        let source = graph::ExtractionBatch {
            meta: meta("extract:one"),
            context: MessageField::some(context.clone()),
            mentions: vec![mention],
            completeness: EnumOrUnknown::new(common::Completeness::COMPLETENESS_COMPLETE),
            ..Default::default()
        };
        let source_ref = common::ArtifactRef {
            artifact_id: "artifact:extract".into(),
            content_hash: MessageField::some(common::ContentHash {
                sha256: "b".repeat(64),
                ..Default::default()
            }),
            storage_key: "sha256/extract".into(),
            media_type: "application/x-protobuf".into(),
            schema_version: 1,
            ..Default::default()
        };
        let candidates = graph::RegistryCandidateBatch {
            meta: meta("candidates:one"),
            context: MessageField::some(context),
            source_extraction_batch: MessageField::some(source_ref.clone()),
            registry_revision: 7,
            lookups: vec![graph::CandidateLookup {
                mention_id: "mention:one".into(),
                scopes: vec![graph::CandidateLookupScope {
                    revision: MessageField::some(common::LookupScopeRevision {
                        scope_id: registry_scope_id("organization", "national", "badan a"),
                        revision: 4,
                        empty_result: ids.is_empty(),
                        ..Default::default()
                    }),
                    entity_type: "organization".into(),
                    canonical_scope: "national".into(),
                    normalized_lookup: "badan a".into(),
                    candidate_ids: ids.iter().map(|id| (*id).to_owned()).collect(),
                    ..Default::default()
                }],
                ..Default::default()
            }],
            candidates: ids
                .iter()
                .map(|id| graph::CanonicalEntity {
                    meta: meta(id),
                    entity_type: "organization".into(),
                    scope: "national".into(),
                    preferred_label: "Badan A".into(),
                    registry_revision: 4,
                    review_state: EnumOrUnknown::new(common::ReviewState::REVIEW_STATE_UNREVIEWED),
                    ..Default::default()
                })
                .collect(),
            aliases: ids
                .iter()
                .map(|id| graph::Alias {
                    meta: meta(&format!("alias:{id}")),
                    canonical_id: (*id).into(),
                    surface: "Badan A".into(),
                    normalized_lookup: "badan a".into(),
                    language: "id".into(),
                    scope: "national".into(),
                    support_refs: vec!["mention:source".into()],
                    ..Default::default()
                })
                .collect(),
            dependencies: MessageField::some(common::DependencyManifest {
                artifact_id: "artifact:candidates".into(),
                dependencies: vec![common::Dependency {
                    dependency_id: source_ref.artifact_id.clone(),
                    fingerprint: source_ref.content_hash.clone(),
                    ..Default::default()
                }],
                lookup_scope_revisions: vec![common::LookupScopeRevision {
                    scope_id: registry_scope_id("organization", "national", "badan a"),
                    revision: 4,
                    empty_result: ids.is_empty(),
                    ..Default::default()
                }],
                producer_manifest: MessageField::some(common::ProducerManifest {
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
            }),
            completeness: EnumOrUnknown::new(common::Completeness::COMPLETENESS_COMPLETE),
            ..Default::default()
        };
        (source, source_ref, candidates)
    }

    #[test]
    fn absent_choice_defers_even_when_one_exact_candidate_exists() {
        let (source, source_ref, candidates) = fixture(&["canonical:a"]);
        let proposals =
            propose_from_candidate_batch(&source, &source_ref, &candidates, &[], 10).unwrap();
        assert_eq!(proposals.len(), 1);
        assert_eq!(
            proposals[0].action.enum_value(),
            Ok(graph::ResolutionAction::RESOLUTION_ACTION_DEFER)
        );
        assert_eq!(proposals[0].candidate_ids, ["canonical:a"]);
        assert_eq!(proposals[0].expected_registry_revision, 7);
        assert_eq!(
            proposals[0].evidence.sources,
            source.mentions[0].source_refs
        );
        assert_eq!(
            proposals[0].evidence.spans[0],
            *source.mentions[0].text_span
        );
    }

    #[test]
    fn explicit_link_is_bound_to_pinned_candidate_and_stable_id() {
        let (source, source_ref, candidates) = fixture(&["canonical:a", "canonical:b"]);
        let choice = ResolutionChoice {
            mention_id: "mention:one".into(),
            candidate_id: Some("canonical:b".into()),
            method: "reviewed_exact_key".into(),
        };
        let first =
            propose_from_candidate_batch(&source, &source_ref, &candidates, &[choice.clone()], 10)
                .unwrap();
        let second =
            propose_from_candidate_batch(&source, &source_ref, &candidates, &[choice], 10).unwrap();
        assert_eq!(first, second);
        assert_eq!(
            first[0].action.enum_value(),
            Ok(graph::ResolutionAction::RESOLUTION_ACTION_LINK)
        );
        assert_eq!(first[0].candidate_ids, ["canonical:b"]);
        assert!(first[0].confidence.is_none());
        let deferred =
            propose_from_candidate_batch(&source, &source_ref, &candidates, &[], 10).unwrap();
        assert_ne!(first[0].meta.record_id, deferred[0].meta.record_id);
        let mut newer = candidates.clone();
        newer.registry_revision = 8;
        let revised = propose_from_candidate_batch(&source, &source_ref, &newer, &[], 10).unwrap();
        assert_ne!(deferred[0].meta.record_id, revised[0].meta.record_id);
        let mut rehashed = source_ref.clone();
        rehashed.content_hash.as_mut().unwrap().sha256 = "c".repeat(64);
        newer.source_extraction_batch = MessageField::some(rehashed.clone());
        newer.dependencies.as_mut().unwrap().dependencies[0].fingerprint =
            rehashed.content_hash.clone();
        let changed_source =
            propose_from_candidate_batch(&source, &rehashed, &newer, &[], 10).unwrap();
        assert_ne!(revised[0].meta.record_id, changed_source[0].meta.record_id);
    }

    #[test]
    fn forged_choices_and_stale_batches_fail_closed() {
        let (source, source_ref, mut candidates) = fixture(&["canonical:a", "canonical:b"]);
        let foreign = ResolutionChoice {
            mention_id: "mention:one".into(),
            candidate_id: Some("canonical:foreign".into()),
            method: "test".into(),
        };
        assert_eq!(
            propose_from_candidate_batch(&source, &source_ref, &candidates, &[foreign], 10),
            Err(ProposalError::InvalidChoice(
                "LINK target absent from pinned lookup"
            ))
        );
        let repeated = ResolutionChoice {
            mention_id: "mention:one".into(),
            candidate_id: None,
            method: "test".into(),
        };
        assert_eq!(
            propose_from_candidate_batch(
                &source,
                &source_ref,
                &candidates,
                &[repeated.clone(), repeated],
                10
            ),
            Err(ProposalError::InvalidChoice("duplicate or invalid choice"))
        );
        candidates.lookups[0].scopes[0]
            .revision
            .as_mut()
            .unwrap()
            .revision = 8;
        assert_eq!(
            propose_from_candidate_batch(&source, &source_ref, &candidates, &[], 10),
            Err(ProposalError::InvalidInput("invalid lookup scope"))
        );
    }

    #[test]
    fn rejects_cross_snapshot_rejected_entity_and_scope_flood() {
        let (source, source_ref, mut candidates) = fixture(&["canonical:a"]);
        candidates.context.as_mut().unwrap().snapshot_ref =
            MessageField::some(common::SnapshotRef {
                corpus_id: "corpus:test".into(),
                snapshot_id: "snapshot:other".into(),
                sequence: 1,
                manifest_hash: MessageField::some(common::ContentHash {
                    sha256: "d".repeat(64),
                    ..Default::default()
                }),
                representation_generation: "gen:one".into(),
                ..Default::default()
            });
        assert_eq!(
            propose_from_candidate_batch(&source, &source_ref, &candidates, &[], 10),
            Err(ProposalError::InvalidInput(
                "source and candidate batch identity"
            ))
        );
        candidates.context.as_mut().unwrap().snapshot_ref = MessageField::none();
        candidates.candidates[0].review_state =
            EnumOrUnknown::new(common::ReviewState::REVIEW_STATE_REJECTED);
        assert_eq!(
            propose_from_candidate_batch(&source, &source_ref, &candidates, &[], 10),
            Err(ProposalError::InvalidInput("invalid candidate entity"))
        );
        candidates.candidates[0].review_state =
            EnumOrUnknown::new(common::ReviewState::REVIEW_STATE_UNREVIEWED);
        let empty = graph::CandidateLookupScope {
            revision: MessageField::some(common::LookupScopeRevision {
                scope_id: registry_scope_id("organization", "local", "badan a"),
                revision: 0,
                empty_result: true,
                ..Default::default()
            }),
            entity_type: "organization".into(),
            canonical_scope: "local".into(),
            normalized_lookup: "badan a".into(),
            ..Default::default()
        };
        candidates.lookups[0]
            .scopes
            .extend(std::iter::repeat_n(empty, 10));
        assert_eq!(
            propose_from_candidate_batch(&source, &source_ref, &candidates, &[], 10),
            Err(ProposalError::InvalidInput("lookup scope limit"))
        );
    }

    #[test]
    fn rejects_candidate_without_sourced_alias() {
        let (source, source_ref, mut candidates) = fixture(&["canonical:a"]);
        candidates.aliases.clear();
        assert_eq!(
            propose_from_candidate_batch(&source, &source_ref, &candidates, &[], 10),
            Err(ProposalError::InvalidInput(
                "candidate type, scope, or uniqueness"
            ))
        );
    }

    #[test]
    fn rejects_inconsistent_source_context_and_invalid_artifact_hash() {
        let (mut source, source_ref, mut candidates) = fixture(&["canonical:a"]);
        source.context.as_mut().unwrap().corpus_id = "corpus:foreign".into();
        candidates.context.as_mut().unwrap().corpus_id = "corpus:foreign".into();
        assert_eq!(
            propose_from_candidate_batch(&source, &source_ref, &candidates, &[], 10),
            Err(ProposalError::InvalidInput(
                "source and candidate batch identity"
            ))
        );
        source.context.as_mut().unwrap().corpus_id = "corpus:test".into();
        candidates.context.as_mut().unwrap().corpus_id = "corpus:test".into();
        let mut invalid_ref = source_ref.clone();
        invalid_ref.content_hash.as_mut().unwrap().sha256 = "bad".into();
        candidates.source_extraction_batch = MessageField::some(invalid_ref.clone());
        assert_eq!(
            propose_from_candidate_batch(&source, &invalid_ref, &candidates, &[], 10),
            Err(ProposalError::InvalidInput("invalid source artifact ref"))
        );
    }
}
