//! Merencanakan pemrosesan incremental dari perubahan content, producer, dependency, dan lookup scope.
//!
//! Peran dalam komponen:
//! Planner ini membandingkan state immutable antar-snapshot, lalu menghitung reverse-dependency closure
//! yang harus dihitung ulang. Hasilnya diproyeksikan ke `UpdatePlan` C01; Go tetap memiliki job,
//! checkpoint, publication, serta keputusan apakah item review boleh diterbitkan.
//!
//! Kontrak integrasi dan perhatian implementasi:
//! Fingerprint dibandingkan sebagai SHA-256 canonical yang sudah dibentuk pemilik artefak. Dependency
//! positif dan revision lookup negatif sama-sama menjadi input. Item hilang tidak dianggap pencabutan
//! hukum: ia masuk review dan hanya consumer yang masih ada yang direcompute. Traversal closure memakai
//! hash index dan queue sehingga linear terhadap item/edge; sorting hanya dilakukan pada output wire.
//!
//! Benchmark dan gate penerimaan:
//! Ukur reuse ratio, invalidation precision/recall terhadap full rebuild, planning throughput, p95/p99,
//! serta peak RSS pada workload UPDATE di `configs/benchmark-targets.yaml`. Target tetap
//! REQUIRED_UNMEASURED; unit test tidak membuktikan equivalence corpus atau freshness produksi.
//!
//! Status: planner incremental dan proyeksi `UpdatePlan` C01 aktif sebagai library. Integrasi worker,
//! persistence checkpoint, full-rebuild comparator, serta publication recovery belum aktif.

use crate::domain::wire::{self, Limits};
use crate::wire::{common, jobs};
use protobuf::MessageField;
use std::collections::{HashMap, HashSet, VecDeque};
use std::error::Error;
use std::fmt::{Display, Formatter};

const WIRE_SCHEMA_VERSION: u32 = 1;

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct DependencyFingerprint {
    pub dependency_id: String,
    pub sha256: String,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct LookupRevision {
    pub scope_id: String,
    pub revision: u64,
    pub empty_result: bool,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct WorkItemState {
    pub item_id: String,
    pub kind: WorkItemKind,
    pub content_sha256: String,
    pub producer_sha256: String,
    pub dependencies: Vec<DependencyFingerprint>,
    pub lookup_revisions: Vec<LookupRevision>,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum WorkItemKind {
    Source,
    Derived,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct IncrementalPlanConfig {
    pub maximum_items: usize,
    pub maximum_dependency_edges: usize,
}

impl Default for IncrementalPlanConfig {
    fn default() -> Self {
        Self {
            maximum_items: 2_000_000,
            maximum_dependency_edges: 20_000_000,
        }
    }
}

#[derive(Clone, Debug, PartialEq)]
pub struct IncrementalPlanContext {
    pub corpus_id: String,
    pub plan_id: String,
    pub base_snapshot: common::SnapshotRef,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord)]
enum ChangeReason {
    Added,
    Content,
    Producer,
    Dependency,
    Lookup,
    Removed,
}

impl ChangeReason {
    fn as_str(self) -> &'static str {
        match self {
            Self::Added => "added",
            Self::Content => "content_changed",
            Self::Producer => "producer_changed",
            Self::Dependency => "dependency_changed",
            Self::Lookup => "lookup_revision_changed",
            Self::Removed => "removed_requires_review",
        }
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum IncrementalPlanError {
    InvalidConfig(&'static str),
    InvalidIdentity(&'static str),
    InvalidHash {
        field: &'static str,
        item_id: String,
    },
    DuplicateItem(String),
    DuplicateDependency {
        item_id: String,
        dependency_id: String,
    },
    DuplicateLookup {
        item_id: String,
        scope_id: String,
    },
    DependencyFingerprintConflict(String),
    LocalDependencyMismatch {
        item_id: String,
        dependency_id: String,
    },
    LookupRevisionConflict(String),
    ItemLimit {
        actual: usize,
        maximum: usize,
    },
    EdgeLimit {
        actual: usize,
        maximum: usize,
    },
    WireValidation(String),
}

impl Display for IncrementalPlanError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::InvalidConfig(field) => write!(formatter, "invalid incremental config: {field}"),
            Self::InvalidIdentity(field) => {
                write!(formatter, "invalid incremental identity: {field}")
            }
            Self::InvalidHash { field, item_id } => {
                write!(formatter, "invalid {field} SHA-256 for {item_id}")
            }
            Self::DuplicateItem(id) => write!(formatter, "duplicate work item: {id}"),
            Self::DuplicateDependency {
                item_id,
                dependency_id,
            } => write!(
                formatter,
                "duplicate dependency {dependency_id} for work item {item_id}"
            ),
            Self::DuplicateLookup { item_id, scope_id } => {
                write!(
                    formatter,
                    "duplicate lookup scope {scope_id} for work item {item_id}"
                )
            }
            Self::DependencyFingerprintConflict(dependency) => {
                write!(
                    formatter,
                    "conflicting dependency fingerprint for {dependency}"
                )
            }
            Self::LocalDependencyMismatch {
                item_id,
                dependency_id,
            } => write!(
                formatter,
                "work item {item_id} does not bind local dependency {dependency_id} content hash"
            ),
            Self::LookupRevisionConflict(scope) => {
                write!(formatter, "conflicting current lookup revision for {scope}")
            }
            Self::ItemLimit { actual, maximum } => {
                write!(
                    formatter,
                    "incremental items {actual} exceed limit {maximum}"
                )
            }
            Self::EdgeLimit { actual, maximum } => {
                write!(
                    formatter,
                    "dependency edges {actual} exceed limit {maximum}"
                )
            }
            Self::WireValidation(detail) => write!(formatter, "wire validation failed: {detail}"),
        }
    }
}

impl Error for IncrementalPlanError {}

pub fn plan_incremental_update(
    previous: &[WorkItemState],
    current: &[WorkItemState],
    context: &IncrementalPlanContext,
    config: &IncrementalPlanConfig,
) -> Result<jobs::UpdatePlan, IncrementalPlanError> {
    validate_config(config)?;
    validate_context(context)?;
    let total_items =
        previous
            .len()
            .checked_add(current.len())
            .ok_or(IncrementalPlanError::ItemLimit {
                actual: usize::MAX,
                maximum: config.maximum_items,
            })?;
    if total_items > config.maximum_items {
        return Err(IncrementalPlanError::ItemLimit {
            actual: total_items,
            maximum: config.maximum_items,
        });
    }

    let previous = index_states(previous, config.maximum_dependency_edges)?;
    let current = index_states(current, config.maximum_dependency_edges)?;
    let edge_count = previous.edge_count.checked_add(current.edge_count).ok_or(
        IncrementalPlanError::EdgeLimit {
            actual: usize::MAX,
            maximum: config.maximum_dependency_edges,
        },
    )?;
    if edge_count > config.maximum_dependency_edges {
        return Err(IncrementalPlanError::EdgeLimit {
            actual: edge_count,
            maximum: config.maximum_dependency_edges,
        });
    }

    let mut direct = HashMap::new();
    let all_ids: HashSet<&str> = previous
        .items
        .keys()
        .chain(current.items.keys())
        .copied()
        .collect();
    for id in all_ids {
        match (previous.items.get(id), current.items.get(id)) {
            (None, Some(_)) => {
                direct.insert(id, ChangeReason::Added);
            }
            (Some(_), None) => {
                direct.insert(id, ChangeReason::Removed);
            }
            (Some(before), Some(after)) => {
                if before.kind != after.kind {
                    return Err(IncrementalPlanError::InvalidIdentity(
                        "work item kind changed",
                    ));
                }
                if let Some(reason) = changed_reason(before, after) {
                    direct.insert(id, reason);
                }
            }
            (None, None) => unreachable!("union contains at least one state"),
        }
    }

    let reverse = reverse_dependencies(&previous, &current, config.maximum_dependency_edges)?;
    let mut affected: HashSet<&str> = direct.keys().copied().collect();
    let mut queue: VecDeque<&str> = affected.iter().copied().collect();
    while let Some(changed) = queue.pop_front() {
        if let Some(consumers) = reverse.get(changed) {
            for consumer in consumers {
                if affected.insert(consumer) {
                    queue.push_back(consumer);
                }
            }
        }
    }

    let removed: HashSet<&str> = previous
        .items
        .keys()
        .filter(|id| !current.items.contains_key(**id))
        .copied()
        .collect();
    let recompute: HashSet<&str> = affected
        .iter()
        .filter(|id| current.items.contains_key(**id))
        .copied()
        .collect();
    let reuse: HashSet<&str> = current
        .items
        .keys()
        .filter(|id| !recompute.contains(**id))
        .copied()
        .collect();
    let mut review: HashSet<&str> = removed.iter().copied().collect();
    for id in &recompute {
        if current.items[*id]
            .dependencies
            .iter()
            .any(|dependency| removed.contains(dependency.dependency_id.as_str()))
        {
            review.insert(id);
        }
    }

    let mut source_changes: Vec<_> = direct
        .iter()
        .filter(|(id, _)| {
            previous
                .items
                .get(**id)
                .or_else(|| current.items.get(**id))
                .is_some_and(|state| state.kind == WorkItemKind::Source)
        })
        .map(|(id, reason)| {
            source_change(id, previous.items.get(id), current.items.get(id), *reason)
        })
        .collect();
    source_changes.sort_by(|left, right| left.source_id.cmp(&right.source_id));
    let mut affected_closure: Vec<_> = affected.into_iter().map(str::to_owned).collect();
    let mut reuse_set: Vec<_> = reuse.into_iter().map(str::to_owned).collect();
    let mut recompute_set: Vec<_> = recompute.into_iter().map(str::to_owned).collect();
    let mut review_set: Vec<_> = review.into_iter().map(str::to_owned).collect();
    affected_closure.sort_unstable();
    reuse_set.sort_unstable();
    recompute_set.sort_unstable();
    review_set.sort_unstable();
    let lookup_revisions = current_lookup_revisions(&current);
    let plan = jobs::UpdatePlan {
        meta: MessageField::some(common::RecordMeta {
            schema_version: WIRE_SCHEMA_VERSION,
            corpus_id: context.corpus_id.clone(),
            record_id: context.plan_id.clone(),
            ..Default::default()
        }),
        source_changes,
        affected_closure,
        reuse_set,
        recompute_set,
        review_set,
        base_snapshot: MessageField::some(context.base_snapshot.clone()),
        lookup_revisions,
        ..Default::default()
    };
    wire::validate(&plan, Limits::default()).map_err(IncrementalPlanError::WireValidation)?;
    Ok(plan)
}

struct StateIndex<'a> {
    items: HashMap<&'a str, &'a WorkItemState>,
    lookup_revisions: HashMap<&'a str, (u64, bool)>,
    edge_count: usize,
}

fn index_states<'a>(
    states: &'a [WorkItemState],
    maximum_edges: usize,
) -> Result<StateIndex<'a>, IncrementalPlanError> {
    let mut items = HashMap::new();
    let mut dependency_fingerprints = HashMap::new();
    let mut lookup_revisions = HashMap::new();
    let mut edge_count = 0usize;
    for state in states {
        edge_count = edge_count
            .checked_add(state.dependencies.len())
            .and_then(|count| count.checked_add(state.lookup_revisions.len()))
            .ok_or(IncrementalPlanError::EdgeLimit {
                actual: usize::MAX,
                maximum: maximum_edges,
            })?;
        if edge_count > maximum_edges {
            return Err(IncrementalPlanError::EdgeLimit {
                actual: edge_count,
                maximum: maximum_edges,
            });
        }
        validate_state(state)?;
        if items.insert(state.item_id.as_str(), state).is_some() {
            return Err(IncrementalPlanError::DuplicateItem(state.item_id.clone()));
        }
        for dependency in &state.dependencies {
            if dependency_fingerprints
                .insert(
                    dependency.dependency_id.as_str(),
                    dependency.sha256.as_str(),
                )
                .is_some_and(|existing| existing != dependency.sha256)
            {
                return Err(IncrementalPlanError::DependencyFingerprintConflict(
                    dependency.dependency_id.clone(),
                ));
            }
        }
        for lookup in &state.lookup_revisions {
            let value = (lookup.revision, lookup.empty_result);
            if lookup_revisions
                .insert(lookup.scope_id.as_str(), value)
                .is_some_and(|existing| existing != value)
            {
                return Err(IncrementalPlanError::LookupRevisionConflict(
                    lookup.scope_id.clone(),
                ));
            }
        }
    }
    for state in states {
        for dependency in &state.dependencies {
            if items
                .get(dependency.dependency_id.as_str())
                .is_some_and(|target| target.content_sha256 != dependency.sha256)
            {
                return Err(IncrementalPlanError::LocalDependencyMismatch {
                    item_id: state.item_id.clone(),
                    dependency_id: dependency.dependency_id.clone(),
                });
            }
        }
    }
    Ok(StateIndex {
        items,
        lookup_revisions,
        edge_count,
    })
}

fn validate_state(state: &WorkItemState) -> Result<(), IncrementalPlanError> {
    if !valid_ascii_id(&state.item_id) {
        return Err(IncrementalPlanError::InvalidIdentity("item_id"));
    }
    if !valid_sha256(&state.content_sha256) {
        return Err(IncrementalPlanError::InvalidHash {
            field: "content",
            item_id: state.item_id.clone(),
        });
    }
    if !valid_sha256(&state.producer_sha256) {
        return Err(IncrementalPlanError::InvalidHash {
            field: "producer",
            item_id: state.item_id.clone(),
        });
    }
    let mut dependency_ids = HashSet::new();
    for dependency in &state.dependencies {
        if !valid_ascii_id(&dependency.dependency_id) {
            return Err(IncrementalPlanError::InvalidIdentity("dependency_id"));
        }
        if !valid_sha256(&dependency.sha256) {
            return Err(IncrementalPlanError::InvalidHash {
                field: "dependency",
                item_id: state.item_id.clone(),
            });
        }
        if !dependency_ids.insert(dependency.dependency_id.as_str()) {
            return Err(IncrementalPlanError::DuplicateDependency {
                item_id: state.item_id.clone(),
                dependency_id: dependency.dependency_id.clone(),
            });
        }
    }
    let mut lookup_ids = HashSet::new();
    for lookup in &state.lookup_revisions {
        if !valid_ascii_id(&lookup.scope_id) {
            return Err(IncrementalPlanError::InvalidIdentity("lookup.scope_id"));
        }
        if !lookup_ids.insert(lookup.scope_id.as_str()) {
            return Err(IncrementalPlanError::DuplicateLookup {
                item_id: state.item_id.clone(),
                scope_id: lookup.scope_id.clone(),
            });
        }
    }
    Ok(())
}

fn changed_reason(before: &WorkItemState, after: &WorkItemState) -> Option<ChangeReason> {
    if before.content_sha256 != after.content_sha256 {
        Some(ChangeReason::Content)
    } else if before.producer_sha256 != after.producer_sha256 {
        Some(ChangeReason::Producer)
    } else if !same_dependencies(&before.dependencies, &after.dependencies) {
        Some(ChangeReason::Dependency)
    } else if !same_lookups(&before.lookup_revisions, &after.lookup_revisions) {
        Some(ChangeReason::Lookup)
    } else {
        None
    }
}

fn same_dependencies(before: &[DependencyFingerprint], after: &[DependencyFingerprint]) -> bool {
    if before.len() != after.len() {
        return false;
    }
    let indexed: HashMap<&str, &str> = before
        .iter()
        .map(|value| (value.dependency_id.as_str(), value.sha256.as_str()))
        .collect();
    after.iter().all(|value| {
        indexed.get(value.dependency_id.as_str()).copied() == Some(value.sha256.as_str())
    })
}

fn same_lookups(before: &[LookupRevision], after: &[LookupRevision]) -> bool {
    if before.len() != after.len() {
        return false;
    }
    let indexed: HashMap<&str, (u64, bool)> = before
        .iter()
        .map(|value| {
            (
                value.scope_id.as_str(),
                (value.revision, value.empty_result),
            )
        })
        .collect();
    after.iter().all(|value| {
        indexed.get(value.scope_id.as_str()).copied() == Some((value.revision, value.empty_result))
    })
}

fn reverse_dependencies<'a>(
    previous: &StateIndex<'a>,
    current: &StateIndex<'a>,
    maximum_edges: usize,
) -> Result<HashMap<&'a str, Vec<&'a str>>, IncrementalPlanError> {
    let known: HashSet<&str> = previous
        .items
        .keys()
        .chain(current.items.keys())
        .copied()
        .collect();
    let mut reverse: HashMap<&str, Vec<&str>> = HashMap::new();
    let mut edges = 0usize;
    for state in previous.items.values().chain(current.items.values()) {
        for dependency in &state.dependencies {
            if known.contains(dependency.dependency_id.as_str()) {
                reverse
                    .entry(dependency.dependency_id.as_str())
                    .or_default()
                    .push(state.item_id.as_str());
                edges = edges
                    .checked_add(1)
                    .ok_or(IncrementalPlanError::EdgeLimit {
                        actual: usize::MAX,
                        maximum: maximum_edges,
                    })?;
                if edges > maximum_edges {
                    return Err(IncrementalPlanError::EdgeLimit {
                        actual: edges,
                        maximum: maximum_edges,
                    });
                }
            }
        }
    }
    Ok(reverse)
}

fn source_change(
    id: &str,
    before: Option<&&WorkItemState>,
    after: Option<&&WorkItemState>,
    reason: ChangeReason,
) -> jobs::SourceChange {
    jobs::SourceChange {
        source_id: id.to_owned(),
        previous_hash: before
            .map(|state| content_hash(&state.content_sha256))
            .into(),
        new_hash: after
            .map(|state| content_hash(&state.content_sha256))
            .into(),
        reason: reason.as_str().to_owned(),
        ..Default::default()
    }
}

fn current_lookup_revisions(current: &StateIndex<'_>) -> Vec<common::LookupScopeRevision> {
    let mut revisions: Vec<_> = current
        .lookup_revisions
        .iter()
        .map(
            |(scope_id, (revision, empty_result))| common::LookupScopeRevision {
                scope_id: (*scope_id).to_owned(),
                revision: *revision,
                empty_result: *empty_result,
                ..Default::default()
            },
        )
        .collect();
    revisions.sort_by(|left, right| left.scope_id.cmp(&right.scope_id));
    revisions
}

fn validate_context(context: &IncrementalPlanContext) -> Result<(), IncrementalPlanError> {
    if !valid_ascii_id(&context.corpus_id) {
        return Err(IncrementalPlanError::InvalidIdentity("corpus_id"));
    }
    if !valid_ascii_id(&context.plan_id) {
        return Err(IncrementalPlanError::InvalidIdentity("plan_id"));
    }
    wire::validate(&context.base_snapshot, Limits::default())
        .map_err(IncrementalPlanError::WireValidation)?;
    if context.base_snapshot.corpus_id != context.corpus_id {
        return Err(IncrementalPlanError::InvalidIdentity(
            "base_snapshot.corpus_id",
        ));
    }
    Ok(())
}

fn validate_config(config: &IncrementalPlanConfig) -> Result<(), IncrementalPlanError> {
    if config.maximum_items == 0 {
        return Err(IncrementalPlanError::InvalidConfig("maximum_items"));
    }
    if config.maximum_dependency_edges == 0 {
        return Err(IncrementalPlanError::InvalidConfig(
            "maximum_dependency_edges",
        ));
    }
    Ok(())
}

fn content_hash(sha256: &str) -> common::ContentHash {
    common::ContentHash {
        sha256: sha256.to_owned(),
        ..Default::default()
    }
}

fn valid_ascii_id(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 256
        && value.bytes().all(|byte| {
            byte.is_ascii_alphanumeric() || matches!(byte, b':' | b'_' | b'-' | b'.' | b'/')
        })
}

fn valid_sha256(value: &str) -> bool {
    value.len() == 64
        && value
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
}

#[cfg(test)]
mod tests {
    use super::*;

    const CORPUS: &str = "regulagraph-id";

    fn hash(character: char) -> String {
        character.to_string().repeat(64)
    }

    fn state(
        id: &str,
        content: char,
        producer: char,
        dependencies: &[(&str, char)],
    ) -> WorkItemState {
        WorkItemState {
            item_id: id.to_owned(),
            kind: if id.starts_with("source:") {
                WorkItemKind::Source
            } else {
                WorkItemKind::Derived
            },
            content_sha256: hash(content),
            producer_sha256: hash(producer),
            dependencies: dependencies
                .iter()
                .map(|(dependency_id, fingerprint)| DependencyFingerprint {
                    dependency_id: (*dependency_id).to_owned(),
                    sha256: hash(*fingerprint),
                })
                .collect(),
            lookup_revisions: Vec::new(),
        }
    }

    fn context() -> IncrementalPlanContext {
        IncrementalPlanContext {
            corpus_id: CORPUS.to_owned(),
            plan_id: "update-plan:fixture".to_owned(),
            base_snapshot: common::SnapshotRef {
                corpus_id: CORPUS.to_owned(),
                snapshot_id: "snapshot:base".to_owned(),
                sequence: 1,
                manifest_hash: MessageField::some(content_hash(&hash('f'))),
                representation_generation: "generation:1".to_owned(),
                ..Default::default()
            },
        }
    }

    #[test]
    fn reuses_identical_items_and_ignores_input_order() {
        let a = state("source:a", 'a', '1', &[]);
        let b = state("chunk:b", 'b', '2', &[("source:a", 'a')]);
        let plan = plan_incremental_update(
            &[a.clone(), b.clone()],
            &[b, a],
            &context(),
            &IncrementalPlanConfig::default(),
        )
        .unwrap();

        assert!(plan.source_changes.is_empty());
        assert!(plan.affected_closure.is_empty());
        assert!(plan.recompute_set.is_empty());
        assert_eq!(plan.reuse_set, ["chunk:b", "source:a"]);
        assert!(plan.review_set.is_empty());
    }

    #[test]
    fn propagates_content_change_through_reverse_dependency_closure() {
        let before = vec![
            state("source:a", 'a', '1', &[]),
            state("text:a", 'b', '2', &[("source:a", 'a')]),
            state("chunk:a", 'c', '3', &[("text:a", 'b')]),
            state("source:shared", 'd', '1', &[]),
        ];
        let mut after = before.clone();
        after[0].content_sha256 = hash('9');
        after[1].dependencies[0].sha256 = hash('9');

        let plan = plan_incremental_update(
            &before,
            &after,
            &context(),
            &IncrementalPlanConfig::default(),
        )
        .unwrap();

        assert_eq!(plan.affected_closure, ["chunk:a", "source:a", "text:a"]);
        assert_eq!(plan.recompute_set, plan.affected_closure);
        assert_eq!(plan.reuse_set, ["source:shared"]);
        assert_eq!(plan.source_changes.len(), 1);
        assert_eq!(plan.source_changes[0].source_id, "source:a");
        assert_eq!(plan.source_changes[0].reason, "content_changed");
    }

    #[test]
    fn invalidates_negative_lookup_and_producer_changes() {
        let mut before = state("extract:a", 'a', '1', &[]);
        before.lookup_revisions.push(LookupRevision {
            scope_id: "lookup:definitions".to_owned(),
            revision: 7,
            empty_result: true,
        });
        let mut lookup_changed = before.clone();
        lookup_changed.lookup_revisions[0].revision = 8;
        lookup_changed.lookup_revisions[0].empty_result = false;
        let plan = plan_incremental_update(
            &[before.clone()],
            &[lookup_changed],
            &context(),
            &IncrementalPlanConfig::default(),
        )
        .unwrap();
        assert!(plan.source_changes.is_empty());
        assert_eq!(plan.recompute_set, ["extract:a"]);
        assert_eq!(plan.lookup_revisions[0].revision, 8);
        assert!(!plan.lookup_revisions[0].empty_result);

        let before = state("source:config", 'a', '1', &[]);
        let mut producer_changed = before.clone();
        producer_changed.producer_sha256 = hash('2');
        let plan = plan_incremental_update(
            &[before],
            &[producer_changed],
            &context(),
            &IncrementalPlanConfig::default(),
        )
        .unwrap();
        assert_eq!(plan.source_changes[0].reason, "producer_changed");
    }

    #[test]
    fn removal_requires_review_without_deleting_shared_independent_work() {
        let previous = vec![
            state("source:removed", 'a', '1', &[]),
            state("source:kept", 'b', '1', &[]),
            state(
                "assertion:shared",
                'c',
                '2',
                &[("source:removed", 'a'), ("source:kept", 'b')],
            ),
            state("chunk:independent", 'd', '2', &[("source:kept", 'b')]),
        ];
        let current = vec![
            previous[1].clone(),
            previous[2].clone(),
            previous[3].clone(),
        ];
        let plan = plan_incremental_update(
            &previous,
            &current,
            &context(),
            &IncrementalPlanConfig::default(),
        )
        .unwrap();

        assert_eq!(
            plan.affected_closure,
            ["assertion:shared", "source:removed"]
        );
        assert_eq!(plan.recompute_set, ["assertion:shared"]);
        assert_eq!(plan.review_set, ["assertion:shared", "source:removed"]);
        assert_eq!(plan.reuse_set, ["chunk:independent", "source:kept"]);
        assert!(plan.source_changes[0].new_hash.is_none());
    }

    #[test]
    fn terminates_on_cycles_and_rejects_invalid_or_oversized_state() {
        let previous = vec![
            state("item:a", 'a', '1', &[("item:b", 'b')]),
            state("item:b", 'b', '1', &[("item:a", 'a')]),
        ];
        let mut current = previous.clone();
        current[0].content_sha256 = hash('9');
        current[1].dependencies[0].sha256 = hash('9');
        let plan = plan_incremental_update(
            &previous,
            &current,
            &context(),
            &IncrementalPlanConfig::default(),
        )
        .unwrap();
        assert_eq!(plan.recompute_set, ["item:a", "item:b"]);

        let duplicate = vec![previous[0].clone(), previous[0].clone()];
        assert!(matches!(
            plan_incremental_update(
                &duplicate,
                &[],
                &context(),
                &IncrementalPlanConfig::default()
            ),
            Err(IncrementalPlanError::DuplicateItem(_))
        ));

        assert_eq!(
            plan_incremental_update(
                &previous,
                &current,
                &context(),
                &IncrementalPlanConfig {
                    maximum_items: 10,
                    maximum_dependency_edges: 1,
                },
            )
            .unwrap_err(),
            IncrementalPlanError::EdgeLimit {
                actual: 2,
                maximum: 1,
            }
        );
    }

    #[test]
    fn rejects_conflicting_current_lookup_revisions() {
        let mut a = state("item:a", 'a', '1', &[]);
        let mut b = state("item:b", 'b', '1', &[]);
        a.lookup_revisions.push(LookupRevision {
            scope_id: "lookup:shared".to_owned(),
            revision: 1,
            empty_result: true,
        });
        b.lookup_revisions.push(LookupRevision {
            scope_id: "lookup:shared".to_owned(),
            revision: 2,
            empty_result: false,
        });
        assert_eq!(
            plan_incremental_update(&[], &[a, b], &context(), &IncrementalPlanConfig::default(),)
                .unwrap_err(),
            IncrementalPlanError::LookupRevisionConflict("lookup:shared".to_owned())
        );
    }

    #[test]
    fn dependency_order_is_canonical_but_external_fingerprint_changes_invalidate() {
        let before = state(
            "chunk:a",
            'a',
            '1',
            &[("external:first", '2'), ("external:second", '3')],
        );
        let reordered = state(
            "chunk:a",
            'a',
            '1',
            &[("external:second", '3'), ("external:first", '2')],
        );
        let unchanged = plan_incremental_update(
            std::slice::from_ref(&before),
            std::slice::from_ref(&reordered),
            &context(),
            &IncrementalPlanConfig::default(),
        )
        .unwrap();
        assert_eq!(unchanged.reuse_set, ["chunk:a"]);

        let mut changed = reordered;
        changed.dependencies[0].sha256 = hash('9');
        let invalidated = plan_incremental_update(
            &[before],
            &[changed],
            &context(),
            &IncrementalPlanConfig::default(),
        )
        .unwrap();
        assert_eq!(invalidated.recompute_set, ["chunk:a"]);
        assert!(invalidated.source_changes.is_empty());
    }

    #[test]
    fn rejects_conflicting_external_dependency_fingerprints() {
        let a = state("chunk:a", 'a', '1', &[("external:shared", '2')]);
        let b = state("chunk:b", 'b', '1', &[("external:shared", '3')]);

        assert_eq!(
            plan_incremental_update(&[], &[a, b], &context(), &IncrementalPlanConfig::default())
                .unwrap_err(),
            IncrementalPlanError::DependencyFingerprintConflict("external:shared".to_owned())
        );
    }

    #[test]
    fn rejects_local_dependency_fingerprint_that_does_not_bind_target_content() {
        let source = state("source:a", 'a', '1', &[]);
        let consumer = state("chunk:a", 'b', '2', &[("source:a", '9')]);

        assert_eq!(
            plan_incremental_update(
                &[],
                &[source, consumer],
                &context(),
                &IncrementalPlanConfig::default(),
            )
            .unwrap_err(),
            IncrementalPlanError::LocalDependencyMismatch {
                item_id: "chunk:a".to_owned(),
                dependency_id: "source:a".to_owned(),
            }
        );
    }

    #[test]
    fn rejects_item_kind_changes_and_malformed_hashes() {
        let before = state("source:a", 'a', '1', &[]);
        let mut changed_kind = before.clone();
        changed_kind.kind = WorkItemKind::Derived;
        assert_eq!(
            plan_incremental_update(
                std::slice::from_ref(&before),
                &[changed_kind],
                &context(),
                &IncrementalPlanConfig::default(),
            )
            .unwrap_err(),
            IncrementalPlanError::InvalidIdentity("work item kind changed")
        );

        let mut malformed = before;
        malformed.content_sha256 = "ABC".to_owned();
        assert!(matches!(
            plan_incremental_update(
                &[],
                &[malformed],
                &context(),
                &IncrementalPlanConfig::default(),
            ),
            Err(IncrementalPlanError::InvalidHash {
                field: "content",
                ..
            })
        ));
    }
}
