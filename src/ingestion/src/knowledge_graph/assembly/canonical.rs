//! Assigns deterministic assertion/support identities after verified endpoint resolution.
//!
//! Assertion identity includes corpus, directed canonical endpoints, predicate, qualifiers,
//! exception closure, origin, ontology and the full temporal scope. Source IDs/model output
//! IDs are excluded from assertion keys; supports retain source spans, versions, producer,
//! review state and independent-source group. Full temporal scope conservatively separates
//! different knowledge snapshots; this is not a global legal equivalence inference.
//!
//! Input is one bounded, prepublication ResolvedRelations batch. Output retains every old
//! assertion/support -> new ID mapping and all distinct support records. No storage mutation,
//! visibility closure, registry allocation or GraphDelta publication occurs here. Unsupported
//! cyclic exception references fail explicitly rather than dropping constraints. Hash domain
//! v1 and normalized protobuf payload are part of identity; change requires migration review.
//!
//! O((records + references) log records) apart from protobuf bytes and sorting local sets;
//! enforce item/byte budgets before cloning. Measure assembly p95/p99, RSS, duplicate ratio
//! and preservation of independent evidence under configs/benchmark-targets.yaml. Required
//! targets remain REQUIRED_UNMEASURED; deterministic dedup does not prove semantic truth.

use std::collections::{BTreeMap, BTreeSet};

use protobuf::{reflect::ReflectValueRef, Message, MessageDyn};
use sha2::{Digest, Sha256};

use super::builder::{materialize_resolved_relations, AssemblyError, ResolvedRelations};
use crate::domain::wire::{validate, Limits};
use crate::knowledge_graph::schema::Ontology;
use crate::wire::{common, graph};

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum CanonicalizationError {
    Resolution(AssemblyError),
    InvalidInput,
    BudgetExceeded,
    DuplicateId,
    MissingException,
    CyclicExceptions,
    MissingSupport,
    IdentityCollision,
    UnknownFields,
}

#[derive(Debug, Clone)]
pub struct CanonicalRelations {
    pub relations: ResolvedRelations,
    pub assertion_ids: BTreeMap<String, String>,
    pub support_ids: BTreeMap<String, String>,
}

/// Checked composition of endpoint materialization and canonical dedup. Go must still
/// authenticate artifact bytes and the committed registry view before dispatching input.
/// Input size is checked before endpoint materialization allocates its output records.
pub fn assemble_canonical_relations(
    source: &graph::ExtractionBatch,
    resolution: &graph::ResolutionBatch,
    source_ref: &common::ArtifactRef,
    ontology: &Ontology,
    canonical_ids: &BTreeSet<String>,
    maximum_items: usize,
    maximum_bytes: u64,
) -> Result<CanonicalRelations, CanonicalizationError> {
    if source
        .compute_size()
        .checked_add(resolution.compute_size())
        .is_none_or(|size| size > maximum_bytes)
    {
        return Err(CanonicalizationError::BudgetExceeded);
    }
    reject_unknown_fields(source, 0)?;
    reject_unknown_fields(resolution, 0)?;
    let resolved = materialize_resolved_relations(
        source,
        resolution,
        source_ref,
        ontology,
        canonical_ids,
        maximum_items,
    )
    .map_err(CanonicalizationError::Resolution)?;
    let output = canonicalize_relations(&resolved, maximum_items, maximum_bytes)?;
    if output
        .assertion_ids
        .values()
        .chain(output.support_ids.values())
        .any(|id| canonical_ids.contains(id))
    {
        return Err(CanonicalizationError::IdentityCollision);
    }
    Ok(output)
}

/// Canonicalizes a single resolved, unpublished corpus view. Source bytes, ontology and
/// committed resolution receipts must already have been verified by the calling assembly.
pub fn canonicalize_relations(
    input: &ResolvedRelations,
    maximum_items: usize,
    maximum_bytes: u64,
) -> Result<CanonicalRelations, CanonicalizationError> {
    use CanonicalizationError as E;
    let mut remaining = maximum_bytes;
    let mut work = maximum_items;
    let mut corpus: Option<&str> = None;
    let mut all_ids = BTreeSet::new();
    let mut charge = |message: &dyn protobuf::MessageDyn,
                      meta: &crate::wire::common::RecordMeta,
                      references: usize|
     -> Result<(), E> {
        validate(message, Limits::default()).map_err(|_| E::InvalidInput)?;
        reject_unknown_fields(message, 0)?;
        work = work.checked_sub(1 + references).ok_or(E::BudgetExceeded)?;
        remaining = remaining
            .checked_sub(message.compute_size_dyn())
            .ok_or(E::BudgetExceeded)?;
        if meta.schema_version != 1
            || meta.visibility.is_some()
            || !all_ids.insert(meta.record_id.clone())
        {
            return Err(E::DuplicateId);
        }
        Ok(())
    };
    if maximum_items == 0 || maximum_bytes == 0 {
        return Err(E::BudgetExceeded);
    }
    // Validate and charge all input records before cloning normalized payloads.
    for assertion in &input.assertions {
        charge(
            assertion,
            &assertion.meta,
            assertion.qualifiers.len() + assertion.exception_refs.len(),
        )?;
    }
    for support in &input.supports {
        charge(
            support,
            &support.meta,
            support.source_refs.len() + support.evidence_spans.len(),
        )?;
    }
    for mention in &input.mentions {
        charge(mention, &mention.meta, mention.source_refs.len())?;
    }
    for meta in input
        .assertions
        .iter()
        .map(|v| &*v.meta)
        .chain(input.supports.iter().map(|v| &*v.meta))
        .chain(input.mentions.iter().map(|v| &*v.meta))
    {
        if corpus.is_some_and(|old| old != meta.corpus_id) {
            return Err(E::InvalidInput);
        }
        corpus = Some(&meta.corpus_id);
    }
    let by_id: BTreeMap<_, _> = input
        .assertions
        .iter()
        .map(|a| (a.meta.record_id.as_str(), a))
        .collect();
    let mention_ids: BTreeSet<_> = input
        .mentions
        .iter()
        .map(|m| m.meta.record_id.as_str())
        .collect();
    let mut pending = BTreeMap::new();
    let mut parents: BTreeMap<&str, Vec<&str>> = BTreeMap::new();
    let mut ready = BTreeSet::new();
    for assertion in &input.assertions {
        if mention_ids.contains(assertion.subject_id.as_str())
            || mention_ids.contains(assertion.object_id.as_str())
            || assertion
                .qualifiers
                .iter()
                .any(|q| matches!(q.value, Some(graph::qualifier::Value::MentionId(_))))
        {
            return Err(E::InvalidInput);
        }
        let exceptions: BTreeSet<_> = assertion
            .exception_refs
            .iter()
            .map(String::as_str)
            .collect();
        for exception in &exceptions {
            if !by_id.contains_key(exception) {
                return Err(E::MissingException);
            }
            parents
                .entry(exception)
                .or_default()
                .push(&assertion.meta.record_id);
        }
        pending.insert(assertion.meta.record_id.as_str(), exceptions.len());
        if exceptions.is_empty() {
            ready.insert(assertion.meta.record_id.as_str());
        }
    }
    let mut assertion_ids: BTreeMap<String, String> = BTreeMap::new();
    let mut assertions: BTreeMap<String, graph::RelationAssertion> = BTreeMap::new();
    let mut output_remaining = maximum_bytes;
    for mention in &input.mentions {
        output_remaining = output_remaining
            .checked_sub(mention.compute_size())
            .ok_or(E::BudgetExceeded)?;
    }
    while let Some(id) = ready.pop_first() {
        let mut value = by_id[id].clone();
        value
            .meta
            .as_mut()
            .ok_or(E::InvalidInput)?
            .record_id
            .clear();
        value.exception_refs = value
            .exception_refs
            .iter()
            .map(|id| assertion_ids[id].clone())
            .collect();
        value.exception_refs.sort();
        value.exception_refs.dedup();
        for qualifier in &mut value.qualifiers {
            if let Some(graph::qualifier::Value::Number(number)) = &mut qualifier.value {
                if *number == 0.0 {
                    *number = 0.0;
                } // canonical positive zero
            }
        }
        sort_unique_messages(&mut value.qualifiers)?;
        sort_unique_messages(
            &mut value
                .temporal_scope
                .as_mut()
                .ok_or(E::InvalidInput)?
                .compare_dates,
        )?;
        let key = identity("assertion:canonical:v1:", &value)?;
        value.meta.as_mut().ok_or(E::InvalidInput)?.record_id = key.clone();
        validate(&value, Limits::default()).map_err(|_| E::InvalidInput)?;
        if mention_ids.contains(key.as_str()) {
            return Err(E::IdentityCollision);
        }
        if !assertions.contains_key(&key) {
            output_remaining = output_remaining
                .checked_sub(value.compute_size())
                .ok_or(E::BudgetExceeded)?;
        }
        if let Some(old) = assertions.insert(key.clone(), value.clone()) {
            if old != value {
                return Err(E::IdentityCollision);
            }
        }
        assertion_ids.insert(id.to_owned(), key);
        for parent in parents.get(id).into_iter().flatten() {
            let count = pending.get_mut(parent).ok_or(E::InvalidInput)?;
            *count -= 1;
            if *count == 0 {
                ready.insert(*parent);
            }
        }
    }
    if assertion_ids.len() != input.assertions.len() {
        return Err(E::CyclicExceptions);
    }
    let mut support_ids = BTreeMap::new();
    let mut supports: BTreeMap<String, graph::SupportRecord> = BTreeMap::new();
    let mut supported = BTreeSet::new();
    for original in &input.supports {
        let mut value = original.clone();
        value.assertion_id = assertion_ids
            .get(&original.assertion_id)
            .ok_or(E::MissingSupport)?
            .clone();
        supported.insert(value.assertion_id.clone());
        value
            .meta
            .as_mut()
            .ok_or(E::InvalidInput)?
            .record_id
            .clear();
        sort_unique_messages(&mut value.source_refs)?;
        sort_unique_messages(&mut value.evidence_spans)?;
        let key = identity("support:canonical:v1:", &value)?;
        value.meta.as_mut().ok_or(E::InvalidInput)?.record_id = key.clone();
        validate(&value, Limits::default()).map_err(|_| E::InvalidInput)?;
        if mention_ids.contains(key.as_str()) || assertions.contains_key(&key) {
            return Err(E::IdentityCollision);
        }
        if !supports.contains_key(&key) {
            output_remaining = output_remaining
                .checked_sub(value.compute_size())
                .ok_or(E::BudgetExceeded)?;
        }
        if let Some(old) = supports.insert(key.clone(), value.clone()) {
            if old != value {
                return Err(E::IdentityCollision);
            }
        }
        support_ids.insert(original.meta.record_id.clone(), key);
    }
    if supported.len() != assertions.len() {
        return Err(E::MissingSupport);
    }
    let mut mentions = input.mentions.clone();
    mentions.sort_by(|a, b| a.meta.record_id.cmp(&b.meta.record_id));
    Ok(CanonicalRelations {
        relations: ResolvedRelations {
            assertions: assertions.into_values().collect(),
            supports: supports.into_values().collect(),
            mentions,
        },
        assertion_ids,
        support_ids,
    })
}

// C01 transport preserves unknown fields for compatibility. Hash-v1 cannot safely
// interpret them or serialize their HashMap order canonically, so it rejects them
// recursively instead of dropping possibly meaningful future qualifiers/constraints.
fn reject_unknown_fields(
    message: &dyn MessageDyn,
    depth: u32,
) -> Result<(), CanonicalizationError> {
    if depth > Limits::default().max_depth {
        return Err(CanonicalizationError::BudgetExceeded);
    }
    if message.unknown_fields_dyn().iter().next().is_some() {
        return Err(CanonicalizationError::UnknownFields);
    }
    for field in message.descriptor_dyn().fields() {
        if field.is_map() {
            return Err(CanonicalizationError::UnknownFields);
        }
        if field.is_repeated() {
            let values = field.get_repeated(message);
            for i in 0..values.len() {
                if let ReflectValueRef::Message(child) = values.get(i) {
                    reject_unknown_fields(&*child, depth + 1)?;
                }
            }
        } else if let Some(ReflectValueRef::Message(child)) = field.get_singular(message) {
            reject_unknown_fields(&*child, depth + 1)?;
        }
    }
    Ok(())
}

fn identity<M: Message>(domain: &str, message: &M) -> Result<String, CanonicalizationError> {
    let bytes = message
        .write_to_bytes()
        .map_err(|_| CanonicalizationError::InvalidInput)?;
    let mut digest = Sha256::new();
    digest.update(domain.as_bytes());
    digest.update([0]);
    digest.update(bytes);
    Ok(format!("{domain}{:x}", digest.finalize()))
}

fn sort_unique_messages<M: Message + Clone>(
    items: &mut Vec<M>,
) -> Result<(), CanonicalizationError> {
    let mut encoded = items
        .iter()
        .map(|item| item.write_to_bytes().map(|bytes| (bytes, item.clone())))
        .collect::<Result<Vec<_>, _>>()
        .map_err(|_| CanonicalizationError::InvalidInput)?;
    encoded.sort_by(|a, b| a.0.cmp(&b.0));
    encoded.dedup_by(|a, b| a.0 == b.0);
    *items = encoded.into_iter().map(|(_, item)| item).collect();
    Ok(())
}
