//! Memvalidasi timeline ketentuan dan memilih versi hukum secara konservatif untuk tanggal tertentu.
//!
//! Peran dalam komponen:
//! Modul ini menjaga pemisahan `Provision` sebagai identitas logis dari `ProvisionVersion` sebagai edisi
//! teks. Ia mengikat versi ke change-event pendukung, mendeteksi overlap yang tidak dideklarasikan, dan
//! menghasilkan keputusan temporal yang tidak menyamakan tanggal observasi dengan tanggal berlaku.
//!
//! Kontrak integrasi dan perhatian implementasi:
//! Input memakai record C01 yang telah membawa corpus/provision/version/source identity. Interval
//! berlaku bersifat half-open `[start,end)`. Batas UNKNOWN/CONFLICT tidak ditebak; caller memilih policy
//! EXCLUDE, REPORT, atau REQUIRE_REVIEW. Record REJECTED/QUARANTINED tidak pernah dipilih sebagai versi
//! berlaku. Event amendment/insert wajib memiliki replacement evidence dan repeal tidak boleh membawa
//! replacement text. Publication dan keputusan hukum final tetap dimiliki coordinator/reviewer.
//!
//! Benchmark dan gate penerimaan:
//! Ukur version-selection accuracy, wrong-version leakage, unresolved handling, p95/p99, throughput, dan
//! peak RSS pada workload VERSIONING di `configs/benchmark-targets.yaml`. Target tetap
//! REQUIRED_UNMEASURED; unit test tidak membuktikan kebenaran hukum corpus produksi.
//!
//! Status: validator timeline dan selector as-of C01 aktif sebagai library. Rekonstruksi event dari teks,
//! registry canonical, persistence, gold temporal corpus, dan publication worker belum aktif.

use crate::domain::wire::{self, Limits};
use crate::wire::{common, documents};
use std::collections::{HashMap, HashSet};
use std::error::Error;
use std::fmt::{Display, Formatter};

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct TimelineConfig {
    pub maximum_versions: usize,
    pub maximum_events: usize,
    pub maximum_support_edges: usize,
}

impl Default for TimelineConfig {
    fn default() -> Self {
        Self {
            maximum_versions: 100_000,
            maximum_events: 100_000,
            maximum_support_edges: 1_000_000,
        }
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum TemporalDecision {
    Resolved,
    NotEffective,
    Conflict,
    Unresolved,
    RequiresReview,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct VersionSelection {
    pub decision: TemporalDecision,
    pub selected_version_ids: Vec<String>,
    pub unresolved_version_ids: Vec<String>,
    pub review_required_version_ids: Vec<String>,
    pub excluded_version_ids: Vec<String>,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum TimelineError {
    InvalidConfig(&'static str),
    InvalidIdentity(&'static str),
    WireValidation(String),
    VersionLimit {
        actual: usize,
        maximum: usize,
    },
    EventLimit {
        actual: usize,
        maximum: usize,
    },
    SupportLimit {
        actual: usize,
        maximum: usize,
    },
    DuplicateVersion(String),
    DuplicateEvent(String),
    DuplicateSupportingEvent {
        version_id: String,
        event_id: String,
    },
    WrongProvision {
        version_id: String,
    },
    MissingSupportingEvent {
        version_id: String,
        event_id: String,
    },
    UnrelatedSupportingEvent {
        version_id: String,
        event_id: String,
    },
    InvalidSupportingEventState {
        version_id: String,
        event_id: String,
    },
    MissingChangeEvidence(String),
    InvalidChangeEvidence(String),
    InvalidReplacementEvidence(String),
    InvalidEffectiveDate(String),
    SupportingEventDateMismatch {
        version_id: String,
        event_id: String,
    },
    UndeclaredOverlap {
        left: String,
        right: String,
    },
    InvalidUnresolvedPolicy,
}

impl Display for TimelineError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::InvalidConfig(field) => write!(formatter, "invalid timeline config: {field}"),
            Self::InvalidIdentity(field) => write!(formatter, "invalid timeline identity: {field}"),
            Self::WireValidation(detail) => write!(formatter, "wire validation failed: {detail}"),
            Self::VersionLimit { actual, maximum } => {
                write!(formatter, "versions {actual} exceed limit {maximum}")
            }
            Self::EventLimit { actual, maximum } => {
                write!(formatter, "events {actual} exceed limit {maximum}")
            }
            Self::SupportLimit { actual, maximum } => {
                write!(formatter, "support edges {actual} exceed limit {maximum}")
            }
            Self::DuplicateVersion(id) => write!(formatter, "duplicate provision version: {id}"),
            Self::DuplicateEvent(id) => write!(formatter, "duplicate legal change event: {id}"),
            Self::DuplicateSupportingEvent {
                version_id,
                event_id,
            } => write!(
                formatter,
                "version {version_id} references event {event_id} more than once"
            ),
            Self::WrongProvision { version_id } => {
                write!(
                    formatter,
                    "version {version_id} belongs to another provision"
                )
            }
            Self::MissingSupportingEvent {
                version_id,
                event_id,
            } => write!(
                formatter,
                "version {version_id} references missing event {event_id}"
            ),
            Self::UnrelatedSupportingEvent {
                version_id,
                event_id,
            } => write!(
                formatter,
                "event {event_id} does not affect the provision of version {version_id}"
            ),
            Self::InvalidSupportingEventState {
                version_id,
                event_id,
            } => write!(
                formatter,
                "version {version_id} relies on rejected or quarantined event {event_id}"
            ),
            Self::MissingChangeEvidence(id) => {
                write!(formatter, "change event {id} has no provenance evidence")
            }
            Self::InvalidChangeEvidence(id) => {
                write!(formatter, "change event {id} has an empty provenance span")
            }
            Self::InvalidReplacementEvidence(id) => {
                write!(
                    formatter,
                    "change event {id} has invalid replacement evidence"
                )
            }
            Self::InvalidEffectiveDate(id) => {
                write!(
                    formatter,
                    "change event {id} has an unbounded effective date"
                )
            }
            Self::SupportingEventDateMismatch {
                version_id,
                event_id,
            } => write!(
                formatter,
                "event {event_id} effective date does not match version {version_id} boundary"
            ),
            Self::UndeclaredOverlap { left, right } => write!(
                formatter,
                "approved versions {left} and {right} overlap without conflict status"
            ),
            Self::InvalidUnresolvedPolicy => {
                write!(formatter, "invalid unresolved temporal policy")
            }
        }
    }
}

impl Error for TimelineError {}

#[derive(Debug)]
pub struct ProvisionTimeline<'a> {
    provision: &'a documents::Provision,
    versions: Vec<&'a documents::ProvisionVersion>,
    relevant_events: Vec<&'a documents::LegalChangeEvent>,
    support_review_required: HashSet<&'a str>,
}

impl<'a> ProvisionTimeline<'a> {
    pub fn build(
        provision: &'a documents::Provision,
        versions: &'a [documents::ProvisionVersion],
        events: &'a [documents::LegalChangeEvent],
        config: &TimelineConfig,
    ) -> Result<Self, TimelineError> {
        validate_config(config)?;
        preflight_limits(versions, events, config)?;
        validate_wire(provision)?;
        let corpus_id = provision.meta.corpus_id.as_str();
        let provision_id = provision.meta.record_id.as_str();
        if !valid_ascii_id(corpus_id) || !valid_ascii_id(provision_id) {
            return Err(TimelineError::InvalidIdentity("provision.meta"));
        }
        if provision.parent_provision_id.as_deref() == Some(provision_id)
            || provision.lineage_refs.iter().any(|id| id == provision_id)
        {
            return Err(TimelineError::InvalidIdentity("provision self reference"));
        }
        let lineage_ids: HashSet<_> = provision.lineage_refs.iter().collect();
        if lineage_ids.len() != provision.lineage_refs.len() {
            return Err(TimelineError::InvalidIdentity("duplicate lineage_refs"));
        }

        let mut event_by_id = HashMap::with_capacity(events.len());
        let mut relevant_events = Vec::new();
        let mut relevant_event_ids = HashSet::new();
        for event in events {
            validate_wire(event)?;
            check_record_corpus(&event.meta, corpus_id, "event.meta.corpus_id")?;
            let event_id = event.meta.record_id.as_str();
            if event_by_id.insert(event_id, event).is_some() {
                return Err(TimelineError::DuplicateEvent(event_id.to_owned()));
            }
            validate_change_event(event)?;
            if event
                .affected_provisions
                .iter()
                .any(|id| id == provision_id)
            {
                relevant_events.push(event);
                relevant_event_ids.insert(event_id);
            }
        }

        let mut seen_versions = HashSet::with_capacity(versions.len());
        let mut timeline_versions = Vec::with_capacity(versions.len());
        let mut support_review_required = HashSet::new();
        for version in versions {
            validate_wire(version)?;
            check_record_corpus(&version.meta, corpus_id, "version.meta.corpus_id")?;
            let version_id = version.meta.record_id.as_str();
            if !seen_versions.insert(version_id) {
                return Err(TimelineError::DuplicateVersion(version_id.to_owned()));
            }
            if version.provision_id != provision_id {
                return Err(TimelineError::WrongProvision {
                    version_id: version_id.to_owned(),
                });
            }
            let mut supporting_ids = HashSet::with_capacity(version.supporting_events.len());
            for event_id in &version.supporting_events {
                if !supporting_ids.insert(event_id.as_str()) {
                    return Err(TimelineError::DuplicateSupportingEvent {
                        version_id: version_id.to_owned(),
                        event_id: event_id.clone(),
                    });
                }
                let event = event_by_id.get(event_id.as_str()).ok_or_else(|| {
                    TimelineError::MissingSupportingEvent {
                        version_id: version_id.to_owned(),
                        event_id: event_id.clone(),
                    }
                })?;
                if !relevant_event_ids.contains(event_id.as_str()) {
                    return Err(TimelineError::UnrelatedSupportingEvent {
                        version_id: version_id.to_owned(),
                        event_id: event_id.clone(),
                    });
                }
                match event.review_state.enum_value() {
                    Ok(
                        common::ReviewState::REVIEW_STATE_REJECTED
                        | common::ReviewState::REVIEW_STATE_QUARANTINED,
                    ) => {
                        return Err(TimelineError::InvalidSupportingEventState {
                            version_id: version_id.to_owned(),
                            event_id: event_id.clone(),
                        });
                    }
                    Ok(common::ReviewState::REVIEW_STATE_UNREVIEWED) => {
                        support_review_required.insert(version_id);
                    }
                    _ => {}
                }
                match supporting_event_date_relation(version, event) {
                    SupportDateRelation::Matches => {}
                    SupportDateRelation::Unresolved => {
                        support_review_required.insert(version_id);
                    }
                    SupportDateRelation::Mismatch => {
                        return Err(TimelineError::SupportingEventDateMismatch {
                            version_id: version_id.to_owned(),
                            event_id: event_id.clone(),
                        });
                    }
                }
            }
            timeline_versions.push(version);
        }

        timeline_versions.sort_by(|left, right| {
            interval_sort_key(&left.legal_interval)
                .cmp(&interval_sort_key(&right.legal_interval))
                .then_with(|| left.meta.record_id.cmp(&right.meta.record_id))
        });
        relevant_events.sort_by(|left, right| {
            assertion_sort_key(&left.effective_date)
                .cmp(&assertion_sort_key(&right.effective_date))
                .then_with(|| left.meta.record_id.cmp(&right.meta.record_id))
        });
        reject_undeclared_overlaps(&timeline_versions)?;

        Ok(Self {
            provision,
            versions: timeline_versions,
            relevant_events,
            support_review_required,
        })
    }

    pub fn provision(&self) -> &'a documents::Provision {
        self.provision
    }

    pub fn versions(&self) -> &[&'a documents::ProvisionVersion] {
        &self.versions
    }

    pub fn relevant_events(&self) -> &[&'a documents::LegalChangeEvent] {
        &self.relevant_events
    }

    pub fn select_as_of(
        &self,
        date: &common::CalendarDate,
        policy: common::UnresolvedPolicy,
    ) -> Result<VersionSelection, TimelineError> {
        if !matches!(
            policy,
            common::UnresolvedPolicy::UNRESOLVED_POLICY_EXCLUDE
                | common::UnresolvedPolicy::UNRESOLVED_POLICY_REPORT
                | common::UnresolvedPolicy::UNRESOLVED_POLICY_REQUIRE_REVIEW
        ) {
            return Err(TimelineError::InvalidUnresolvedPolicy);
        }
        validate_wire(date)?;
        let query = DateKey::from(date);
        let mut exact = Vec::new();
        let mut unresolved = Vec::new();
        let mut review_required = Vec::new();
        let mut excluded = Vec::new();
        let mut declared_conflict = false;

        for version in &self.versions {
            let id = version.meta.record_id.as_str();
            if matches!(
                version.review_state.enum_value(),
                Ok(common::ReviewState::REVIEW_STATE_REJECTED
                    | common::ReviewState::REVIEW_STATE_QUARANTINED)
            ) {
                excluded.push(id.to_owned());
                continue;
            }
            match interval_applicability(&version.legal_interval, query) {
                Applicability::Applicable => {
                    exact.push(id.to_owned());
                    declared_conflict |= version.legal_status.enum_value()
                        == Ok(documents::LegalStatus::LEGAL_STATUS_CONFLICT);
                    if version.review_state.enum_value()
                        == Ok(common::ReviewState::REVIEW_STATE_UNREVIEWED)
                        || version.legal_status.enum_value()
                            == Ok(documents::LegalStatus::LEGAL_STATUS_UNKNOWN)
                        || self.support_review_required.contains(id)
                    {
                        review_required.push(id.to_owned());
                    }
                }
                Applicability::Unresolved => unresolved.push(id.to_owned()),
                Applicability::Outside => {}
            }
        }

        exact.sort_unstable();
        unresolved.sort_unstable();
        review_required.sort_unstable();
        excluded.sort_unstable();
        let decision = if exact.len() > 1 || declared_conflict {
            TemporalDecision::Conflict
        } else if !review_required.is_empty() {
            TemporalDecision::RequiresReview
        } else {
            match policy {
                common::UnresolvedPolicy::UNRESOLVED_POLICY_EXCLUDE => resolved_decision(&exact),
                common::UnresolvedPolicy::UNRESOLVED_POLICY_REPORT => {
                    if unresolved.is_empty() {
                        resolved_decision(&exact)
                    } else {
                        TemporalDecision::Unresolved
                    }
                }
                common::UnresolvedPolicy::UNRESOLVED_POLICY_REQUIRE_REVIEW => {
                    if unresolved.is_empty() {
                        resolved_decision(&exact)
                    } else {
                        TemporalDecision::RequiresReview
                    }
                }
                _ => unreachable!("policy validated at selector entry"),
            }
        };

        Ok(VersionSelection {
            decision,
            selected_version_ids: exact,
            unresolved_version_ids: unresolved,
            review_required_version_ids: review_required,
            excluded_version_ids: excluded,
        })
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord)]
struct DateKey(i32, u32, u32);

impl From<&common::CalendarDate> for DateKey {
    fn from(value: &common::CalendarDate) -> Self {
        Self(value.year, value.month, value.day)
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum Bound {
    Known(DateKey),
    Unbounded,
    Unresolved,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum Applicability {
    Applicable,
    Outside,
    Unresolved,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum SupportDateRelation {
    Matches,
    Mismatch,
    Unresolved,
}

fn interval_applicability(interval: &common::LegalInterval, query: DateKey) -> Applicability {
    let start = assertion_bound(&interval.start);
    let end = assertion_bound(&interval.end);
    if matches!(start, Bound::Known(value) if query < value)
        || matches!(end, Bound::Known(value) if query >= value)
    {
        return Applicability::Outside;
    }
    if matches!(start, Bound::Unresolved) || matches!(end, Bound::Unresolved) {
        Applicability::Unresolved
    } else {
        Applicability::Applicable
    }
}

fn assertion_bound(assertion: &common::DateAssertion) -> Bound {
    match assertion.knowledge.enum_value() {
        Ok(common::DateKnowledge::DATE_KNOWLEDGE_KNOWN) => assertion
            .value
            .as_ref()
            .map(DateKey::from)
            .map(Bound::Known)
            .unwrap_or(Bound::Unresolved),
        Ok(common::DateKnowledge::DATE_KNOWLEDGE_UNBOUNDED) => Bound::Unbounded,
        _ => Bound::Unresolved,
    }
}

fn resolved_decision(exact: &[String]) -> TemporalDecision {
    match exact.len() {
        0 => TemporalDecision::NotEffective,
        1 => TemporalDecision::Resolved,
        _ => TemporalDecision::Conflict,
    }
}

fn supporting_event_date_relation(
    version: &documents::ProvisionVersion,
    event: &documents::LegalChangeEvent,
) -> SupportDateRelation {
    let effective = assertion_bound(&event.effective_date);
    let start = assertion_bound(&version.legal_interval.start);
    let end = assertion_bound(&version.legal_interval.end);
    match event.operation.enum_value() {
        Ok(documents::ChangeOperation::CHANGE_OPERATION_AMEND) => {
            compare_event_to_either_boundary(effective, start, end)
        }
        Ok(documents::ChangeOperation::CHANGE_OPERATION_INSERT) => {
            compare_event_boundary(effective, start)
        }
        Ok(documents::ChangeOperation::CHANGE_OPERATION_REPEAL) => {
            compare_event_boundary(effective, end)
        }
        Ok(documents::ChangeOperation::CHANGE_OPERATION_RENUMBER) => {
            compare_event_to_either_boundary(effective, start, end)
        }
        _ => SupportDateRelation::Mismatch,
    }
}

fn compare_event_boundary(event: Bound, boundary: Bound) -> SupportDateRelation {
    match (event, boundary) {
        (Bound::Known(event), Bound::Known(boundary)) if event == boundary => {
            SupportDateRelation::Matches
        }
        (Bound::Unresolved, _) | (_, Bound::Unresolved) => SupportDateRelation::Unresolved,
        _ => SupportDateRelation::Mismatch,
    }
}

fn compare_event_to_either_boundary(event: Bound, start: Bound, end: Bound) -> SupportDateRelation {
    if matches!(
        compare_event_boundary(event, start),
        SupportDateRelation::Matches
    ) || matches!(
        compare_event_boundary(event, end),
        SupportDateRelation::Matches
    ) {
        SupportDateRelation::Matches
    } else if matches!(event, Bound::Unresolved)
        || matches!(start, Bound::Unresolved)
        || matches!(end, Bound::Unresolved)
    {
        SupportDateRelation::Unresolved
    } else {
        SupportDateRelation::Mismatch
    }
}

fn reject_undeclared_overlaps(
    versions: &[&documents::ProvisionVersion],
) -> Result<(), TimelineError> {
    let mut active: Option<(&documents::ProvisionVersion, ExactRange)> = None;
    for version in versions {
        if !approved_without_conflict(version) {
            continue;
        }
        let Some(range) = exact_interval(&version.legal_interval) else {
            continue;
        };
        if let Some((left, active_range)) = active {
            if starts_before_end(range.start, active_range.end) {
                return Err(TimelineError::UndeclaredOverlap {
                    left: left.meta.record_id.clone(),
                    right: version.meta.record_id.clone(),
                });
            }
        }
        active = Some((version, range));
    }
    Ok(())
}

fn approved_without_conflict(version: &documents::ProvisionVersion) -> bool {
    version.review_state.enum_value() == Ok(common::ReviewState::REVIEW_STATE_APPROVED)
        && version.legal_status.enum_value() != Ok(documents::LegalStatus::LEGAL_STATUS_CONFLICT)
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
struct ExactRange {
    start: Option<DateKey>,
    end: Option<DateKey>,
}

fn exact_interval(interval: &common::LegalInterval) -> Option<ExactRange> {
    match (
        assertion_bound(&interval.start),
        assertion_bound(&interval.end),
    ) {
        (Bound::Known(start), Bound::Known(end)) => Some(ExactRange {
            start: Some(start),
            end: Some(end),
        }),
        (Bound::Unbounded, Bound::Known(end)) => Some(ExactRange {
            start: None,
            end: Some(end),
        }),
        (Bound::Known(start), Bound::Unbounded) => Some(ExactRange {
            start: Some(start),
            end: None,
        }),
        (Bound::Unbounded, Bound::Unbounded) => Some(ExactRange {
            start: None,
            end: None,
        }),
        _ => None,
    }
}

fn starts_before_end(start: Option<DateKey>, end: Option<DateKey>) -> bool {
    match (start, end) {
        (_, None) | (None, Some(_)) => true,
        (Some(start), Some(end)) => start < end,
    }
}

fn interval_sort_key(interval: &common::LegalInterval) -> (u8, DateKey) {
    assertion_sort_key(&interval.start)
}

fn assertion_sort_key(assertion: &common::DateAssertion) -> (u8, DateKey) {
    match assertion_bound(assertion) {
        Bound::Unbounded => (0, DateKey(0, 0, 0)),
        Bound::Known(value) => (1, value),
        Bound::Unresolved => (2, DateKey(0, 0, 0)),
    }
}

fn validate_change_event(event: &documents::LegalChangeEvent) -> Result<(), TimelineError> {
    let event_id = event.meta.record_id.as_str();
    if event
        .supports
        .spans
        .iter()
        .any(|span| span.start_byte >= span.end_byte)
    {
        return Err(TimelineError::InvalidChangeEvidence(event_id.to_owned()));
    }
    if event.supports.sources.is_empty()
        && !event
            .supports
            .spans
            .iter()
            .any(|span| span.start_byte < span.end_byte)
        && event.supports.locators.is_empty()
    {
        return Err(TimelineError::MissingChangeEvidence(event_id.to_owned()));
    }
    if matches!(assertion_bound(&event.effective_date), Bound::Unbounded) {
        return Err(TimelineError::InvalidEffectiveDate(event_id.to_owned()));
    }
    if event
        .replacement_spans
        .iter()
        .any(|span| span.start_byte >= span.end_byte)
    {
        return Err(TimelineError::InvalidReplacementEvidence(
            event_id.to_owned(),
        ));
    }
    match event.operation.enum_value() {
        Ok(
            documents::ChangeOperation::CHANGE_OPERATION_AMEND
            | documents::ChangeOperation::CHANGE_OPERATION_INSERT,
        ) if event.replacement_spans.is_empty() => Err(TimelineError::InvalidReplacementEvidence(
            event_id.to_owned(),
        )),
        Ok(documents::ChangeOperation::CHANGE_OPERATION_REPEAL)
            if !event.replacement_spans.is_empty() =>
        {
            Err(TimelineError::InvalidReplacementEvidence(
                event_id.to_owned(),
            ))
        }
        _ => Ok(()),
    }
}

fn preflight_limits(
    versions: &[documents::ProvisionVersion],
    events: &[documents::LegalChangeEvent],
    config: &TimelineConfig,
) -> Result<(), TimelineError> {
    if versions.len() > config.maximum_versions {
        return Err(TimelineError::VersionLimit {
            actual: versions.len(),
            maximum: config.maximum_versions,
        });
    }
    if events.len() > config.maximum_events {
        return Err(TimelineError::EventLimit {
            actual: events.len(),
            maximum: config.maximum_events,
        });
    }
    let mut edges = 0usize;
    for version in versions {
        edges = checked_add_edges(edges, version.supporting_events.len(), config)?;
    }
    for event in events {
        edges = checked_add_edges(edges, event.affected_provisions.len(), config)?;
        edges = checked_add_edges(edges, event.replacement_spans.len(), config)?;
        edges = checked_add_edges(edges, event.supports.sources.len(), config)?;
        edges = checked_add_edges(edges, event.supports.spans.len(), config)?;
        edges = checked_add_edges(edges, event.supports.locators.len(), config)?;
    }
    Ok(())
}

fn checked_add_edges(
    current: usize,
    additional: usize,
    config: &TimelineConfig,
) -> Result<usize, TimelineError> {
    let actual = current
        .checked_add(additional)
        .ok_or(TimelineError::SupportLimit {
            actual: usize::MAX,
            maximum: config.maximum_support_edges,
        })?;
    if actual > config.maximum_support_edges {
        return Err(TimelineError::SupportLimit {
            actual,
            maximum: config.maximum_support_edges,
        });
    }
    Ok(actual)
}

fn check_record_corpus(
    meta: &common::RecordMeta,
    corpus_id: &str,
    field: &'static str,
) -> Result<(), TimelineError> {
    if meta.corpus_id != corpus_id {
        return Err(TimelineError::InvalidIdentity(field));
    }
    Ok(())
}

fn validate_wire(message: &dyn protobuf::MessageDyn) -> Result<(), TimelineError> {
    wire::validate(message, Limits::default()).map_err(TimelineError::WireValidation)
}

fn validate_config(config: &TimelineConfig) -> Result<(), TimelineError> {
    if config.maximum_versions == 0 {
        return Err(TimelineError::InvalidConfig("maximum_versions"));
    }
    if config.maximum_events == 0 {
        return Err(TimelineError::InvalidConfig("maximum_events"));
    }
    if config.maximum_support_edges == 0 {
        return Err(TimelineError::InvalidConfig("maximum_support_edges"));
    }
    Ok(())
}

fn valid_ascii_id(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 256
        && value.bytes().all(|byte| {
            byte.is_ascii_alphanumeric() || matches!(byte, b':' | b'_' | b'-' | b'.' | b'/')
        })
}

#[cfg(test)]
mod tests {
    use super::*;
    use protobuf::{EnumOrUnknown, MessageField};

    const CORPUS: &str = "regulagraph-id";

    fn hash(character: char) -> common::ContentHash {
        common::ContentHash {
            sha256: character.to_string().repeat(64),
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

    fn date(year: i32, month: u32, day: u32) -> common::CalendarDate {
        common::CalendarDate {
            year,
            month,
            day,
            ..Default::default()
        }
    }

    fn known(value: common::CalendarDate) -> common::DateAssertion {
        common::DateAssertion {
            knowledge: EnumOrUnknown::new(common::DateKnowledge::DATE_KNOWLEDGE_KNOWN),
            value: MessageField::some(value),
            support_refs: vec!["source:evidence".to_owned()],
            ..Default::default()
        }
    }

    fn unknown() -> common::DateAssertion {
        common::DateAssertion {
            knowledge: EnumOrUnknown::new(common::DateKnowledge::DATE_KNOWLEDGE_UNKNOWN),
            support_refs: vec!["source:evidence".to_owned()],
            ..Default::default()
        }
    }

    fn unbounded() -> common::DateAssertion {
        common::DateAssertion {
            knowledge: EnumOrUnknown::new(common::DateKnowledge::DATE_KNOWLEDGE_UNBOUNDED),
            ..Default::default()
        }
    }

    fn provision() -> documents::Provision {
        documents::Provision {
            meta: MessageField::some(meta("provision:article-1")),
            regulation_id: "regulation:fixture".to_owned(),
            structural_path: vec!["Pasal 1".to_owned()],
            ..Default::default()
        }
    }

    fn artifact() -> common::ArtifactRef {
        common::ArtifactRef {
            artifact_id: "artifact:normalized".to_owned(),
            content_hash: MessageField::some(hash('a')),
            storage_key: "sha256/aa/value".to_owned(),
            media_type: "text/plain; charset=utf-8".to_owned(),
            byte_size: 100,
            schema_version: 1,
            ..Default::default()
        }
    }

    fn version(
        id: &str,
        start: common::DateAssertion,
        end: common::DateAssertion,
        supporting_events: &[&str],
    ) -> documents::ProvisionVersion {
        documents::ProvisionVersion {
            meta: MessageField::some(meta(id)),
            provision_id: "provision:article-1".to_owned(),
            text_ref: MessageField::some(artifact()),
            spans: vec![common::TextSpan {
                text_artifact_id: "artifact:normalized".to_owned(),
                start_byte: 0,
                end_byte: 50,
                ..Default::default()
            }],
            legal_interval: MessageField::some(common::LegalInterval {
                start: MessageField::some(start),
                end: MessageField::some(end),
                ..Default::default()
            }),
            legal_status: EnumOrUnknown::new(documents::LegalStatus::LEGAL_STATUS_ACTIVE),
            supporting_events: supporting_events
                .iter()
                .map(|value| (*value).to_owned())
                .collect(),
            review_state: EnumOrUnknown::new(common::ReviewState::REVIEW_STATE_APPROVED),
            ..Default::default()
        }
    }

    fn event(
        id: &str,
        operation: documents::ChangeOperation,
        effective: common::DateAssertion,
        replacement: bool,
    ) -> documents::LegalChangeEvent {
        documents::LegalChangeEvent {
            meta: MessageField::some(meta(id)),
            amending_source: MessageField::some(common::SourceVersionRef {
                source_blob_id: "source:amendment".to_owned(),
                provision_version_id: "version:amending".to_owned(),
                regulation_id: "regulation:amendment".to_owned(),
                ..Default::default()
            }),
            affected_provisions: vec!["provision:article-1".to_owned()],
            operation: EnumOrUnknown::new(operation),
            replacement_spans: if replacement {
                vec![common::TextSpan {
                    text_artifact_id: "artifact:amendment".to_owned(),
                    start_byte: 10,
                    end_byte: 20,
                    ..Default::default()
                }]
            } else {
                Vec::new()
            },
            effective_date: MessageField::some(effective),
            supports: MessageField::some(common::Provenance {
                sources: vec![common::SourceVersionRef {
                    source_blob_id: "source:amendment".to_owned(),
                    provision_version_id: "version:amending".to_owned(),
                    regulation_id: "regulation:amendment".to_owned(),
                    ..Default::default()
                }],
                ..Default::default()
            }),
            review_state: EnumOrUnknown::new(common::ReviewState::REVIEW_STATE_APPROVED),
            ..Default::default()
        }
    }

    #[test]
    fn selects_half_open_historical_versions_at_amendment_boundary() {
        let versions = vec![
            version(
                "version:original",
                known(date(2020, 1, 1)),
                known(date(2023, 7, 1)),
                &[],
            ),
            version(
                "version:amended",
                known(date(2023, 7, 1)),
                unbounded(),
                &["change:amend"],
            ),
        ];
        let events = vec![event(
            "change:amend",
            documents::ChangeOperation::CHANGE_OPERATION_AMEND,
            known(date(2023, 7, 1)),
            true,
        )];
        let provision = provision();
        let timeline =
            ProvisionTimeline::build(&provision, &versions, &events, &TimelineConfig::default())
                .unwrap();

        let before = timeline
            .select_as_of(
                &date(2023, 6, 30),
                common::UnresolvedPolicy::UNRESOLVED_POLICY_REPORT,
            )
            .unwrap();
        assert_eq!(before.decision, TemporalDecision::Resolved);
        assert_eq!(before.selected_version_ids, ["version:original"]);

        let boundary = timeline
            .select_as_of(
                &date(2023, 7, 1),
                common::UnresolvedPolicy::UNRESOLVED_POLICY_REPORT,
            )
            .unwrap();
        assert_eq!(boundary.decision, TemporalDecision::Resolved);
        assert_eq!(boundary.selected_version_ids, ["version:amended"]);
    }

    #[test]
    fn reports_unknown_dates_without_using_observation_time() {
        let versions = vec![version("version:uncertain", unknown(), unbounded(), &[])];
        let provision = provision();
        let timeline =
            ProvisionTimeline::build(&provision, &versions, &[], &TimelineConfig::default())
                .unwrap();

        let reported = timeline
            .select_as_of(
                &date(2024, 1, 1),
                common::UnresolvedPolicy::UNRESOLVED_POLICY_REPORT,
            )
            .unwrap();
        assert_eq!(reported.decision, TemporalDecision::Unresolved);
        assert_eq!(reported.unresolved_version_ids, ["version:uncertain"]);

        let review = timeline
            .select_as_of(
                &date(2024, 1, 1),
                common::UnresolvedPolicy::UNRESOLVED_POLICY_REQUIRE_REVIEW,
            )
            .unwrap();
        assert_eq!(review.decision, TemporalDecision::RequiresReview);

        let excluded = timeline
            .select_as_of(
                &date(2024, 1, 1),
                common::UnresolvedPolicy::UNRESOLVED_POLICY_EXCLUDE,
            )
            .unwrap();
        assert_eq!(excluded.decision, TemporalDecision::NotEffective);
        assert_eq!(excluded.unresolved_version_ids, ["version:uncertain"]);
    }

    #[test]
    fn known_boundary_can_exclude_even_when_other_boundary_is_unknown() {
        let versions = vec![version(
            "version:future",
            known(date(2030, 1, 1)),
            unknown(),
            &[],
        )];
        let provision = provision();
        let timeline =
            ProvisionTimeline::build(&provision, &versions, &[], &TimelineConfig::default())
                .unwrap();
        let selection = timeline
            .select_as_of(
                &date(2029, 12, 31),
                common::UnresolvedPolicy::UNRESOLVED_POLICY_REPORT,
            )
            .unwrap();
        assert_eq!(selection.decision, TemporalDecision::NotEffective);
        assert!(selection.unresolved_version_ids.is_empty());
    }

    #[test]
    fn rejects_undeclared_overlap_but_preserves_declared_conflict() {
        let versions = vec![
            version(
                "version:a",
                known(date(2020, 1, 1)),
                known(date(2025, 1, 1)),
                &[],
            ),
            version(
                "version:b",
                known(date(2024, 1, 1)),
                known(date(2026, 1, 1)),
                &[],
            ),
        ];
        let provision = provision();
        assert_eq!(
            ProvisionTimeline::build(&provision, &versions, &[], &TimelineConfig::default(),)
                .unwrap_err(),
            TimelineError::UndeclaredOverlap {
                left: "version:a".to_owned(),
                right: "version:b".to_owned(),
            }
        );

        let mut declared = versions;
        declared[1].legal_status =
            EnumOrUnknown::new(documents::LegalStatus::LEGAL_STATUS_CONFLICT);
        let timeline =
            ProvisionTimeline::build(&provision, &declared, &[], &TimelineConfig::default())
                .unwrap();
        let selection = timeline
            .select_as_of(
                &date(2024, 6, 1),
                common::UnresolvedPolicy::UNRESOLVED_POLICY_REPORT,
            )
            .unwrap();
        assert_eq!(selection.decision, TemporalDecision::Conflict);
        assert_eq!(selection.selected_version_ids, ["version:a", "version:b"]);
        assert_eq!(
            timeline
                .select_as_of(
                    &date(2024, 6, 1),
                    common::UnresolvedPolicy::UNRESOLVED_POLICY_UNSPECIFIED,
                )
                .unwrap_err(),
            TimelineError::InvalidUnresolvedPolicy
        );
    }

    #[test]
    fn detects_unbounded_overlap_and_requires_review_for_uncertain_status() {
        let versions = vec![
            version("version:open", known(date(2020, 1, 1)), unbounded(), &[]),
            version("version:later", known(date(2025, 1, 1)), unbounded(), &[]),
        ];
        let provision = provision();
        assert!(matches!(
            ProvisionTimeline::build(&provision, &versions, &[], &TimelineConfig::default()),
            Err(TimelineError::UndeclaredOverlap { .. })
        ));

        let mut uncertain = version(
            "version:unreviewed",
            known(date(2020, 1, 1)),
            unbounded(),
            &[],
        );
        uncertain.review_state = EnumOrUnknown::new(common::ReviewState::REVIEW_STATE_UNREVIEWED);
        uncertain.legal_status = EnumOrUnknown::new(documents::LegalStatus::LEGAL_STATUS_UNKNOWN);
        let uncertain_versions = vec![uncertain];
        let timeline = ProvisionTimeline::build(
            &provision,
            &uncertain_versions,
            &[],
            &TimelineConfig::default(),
        )
        .unwrap();
        let selection = timeline
            .select_as_of(
                &date(2024, 1, 1),
                common::UnresolvedPolicy::UNRESOLVED_POLICY_EXCLUDE,
            )
            .unwrap();
        assert_eq!(selection.decision, TemporalDecision::RequiresReview);
        assert_eq!(
            selection.review_required_version_ids,
            ["version:unreviewed"]
        );
    }

    #[test]
    fn treats_conflicting_date_assertion_as_unresolved_and_rejects_duplicate_support() {
        let conflict = common::DateAssertion {
            knowledge: EnumOrUnknown::new(common::DateKnowledge::DATE_KNOWLEDGE_CONFLICT),
            alternatives: vec![date(2022, 1, 1), date(2023, 1, 1)],
            support_refs: vec!["source:a".to_owned(), "source:b".to_owned()],
            ..Default::default()
        };
        let versions = vec![version(
            "version:conflicting-date",
            conflict,
            unbounded(),
            &[],
        )];
        let provision = provision();
        let timeline =
            ProvisionTimeline::build(&provision, &versions, &[], &TimelineConfig::default())
                .unwrap();
        let selection = timeline
            .select_as_of(
                &date(2024, 1, 1),
                common::UnresolvedPolicy::UNRESOLVED_POLICY_REPORT,
            )
            .unwrap();
        assert_eq!(selection.decision, TemporalDecision::Unresolved);

        let duplicated = vec![version(
            "version:duplicate-support",
            known(date(2024, 1, 1)),
            unbounded(),
            &["change:a", "change:a"],
        )];
        let events = vec![event(
            "change:a",
            documents::ChangeOperation::CHANGE_OPERATION_INSERT,
            known(date(2024, 1, 1)),
            true,
        )];
        assert!(matches!(
            ProvisionTimeline::build(&provision, &duplicated, &events, &TimelineConfig::default()),
            Err(TimelineError::DuplicateSupportingEvent { .. })
        ));

        let versions = vec![version(
            "version:unreviewed-support",
            known(date(2024, 1, 1)),
            unbounded(),
            &["change:unreviewed"],
        )];
        let mut unreviewed_event = event(
            "change:unreviewed",
            documents::ChangeOperation::CHANGE_OPERATION_AMEND,
            unknown(),
            true,
        );
        unreviewed_event.review_state =
            EnumOrUnknown::new(common::ReviewState::REVIEW_STATE_UNREVIEWED);
        let events = vec![unreviewed_event];
        let timeline =
            ProvisionTimeline::build(&provision, &versions, &events, &TimelineConfig::default())
                .unwrap();
        let selection = timeline
            .select_as_of(
                &date(2025, 1, 1),
                common::UnresolvedPolicy::UNRESOLVED_POLICY_EXCLUDE,
            )
            .unwrap();
        assert_eq!(selection.decision, TemporalDecision::RequiresReview);
        assert_eq!(
            selection.review_required_version_ids,
            ["version:unreviewed-support"]
        );
    }

    #[test]
    fn rejects_missing_unrelated_and_invalid_change_evidence() {
        let provision = provision();
        let missing = vec![version(
            "version:a",
            unbounded(),
            unbounded(),
            &["change:missing"],
        )];
        assert!(matches!(
            ProvisionTimeline::build(&provision, &missing, &[], &TimelineConfig::default()),
            Err(TimelineError::MissingSupportingEvent { .. })
        ));

        let mut unrelated = event(
            "change:other",
            documents::ChangeOperation::CHANGE_OPERATION_RENUMBER,
            known(date(2024, 1, 1)),
            false,
        );
        unrelated.affected_provisions = vec!["provision:other".to_owned()];
        let versions = vec![version(
            "version:a",
            unbounded(),
            unbounded(),
            &["change:other"],
        )];
        assert!(matches!(
            ProvisionTimeline::build(
                &provision,
                &versions,
                &[unrelated],
                &TimelineConfig::default()
            ),
            Err(TimelineError::UnrelatedSupportingEvent { .. })
        ));

        let invalid_repeal = event(
            "change:repeal",
            documents::ChangeOperation::CHANGE_OPERATION_REPEAL,
            known(date(2025, 1, 1)),
            true,
        );
        assert_eq!(
            ProvisionTimeline::build(
                &provision,
                &[],
                &[invalid_repeal],
                &TimelineConfig::default(),
            )
            .unwrap_err(),
            TimelineError::InvalidReplacementEvidence("change:repeal".to_owned())
        );

        let versions = vec![version(
            "version:rejected-support",
            known(date(2025, 1, 1)),
            unbounded(),
            &["change:rejected"],
        )];
        let mut rejected_event = event(
            "change:rejected",
            documents::ChangeOperation::CHANGE_OPERATION_INSERT,
            known(date(2025, 1, 1)),
            true,
        );
        rejected_event.review_state =
            EnumOrUnknown::new(common::ReviewState::REVIEW_STATE_REJECTED);
        assert!(matches!(
            ProvisionTimeline::build(
                &provision,
                &versions,
                &[rejected_event],
                &TimelineConfig::default()
            ),
            Err(TimelineError::InvalidSupportingEventState { .. })
        ));

        let versions = vec![version(
            "version:mismatched-date",
            known(date(2020, 1, 1)),
            known(date(2025, 1, 1)),
            &["change:late-amendment"],
        )];
        let events = vec![event(
            "change:late-amendment",
            documents::ChangeOperation::CHANGE_OPERATION_AMEND,
            known(date(2030, 1, 1)),
            true,
        )];
        assert!(matches!(
            ProvisionTimeline::build(&provision, &versions, &events, &TimelineConfig::default()),
            Err(TimelineError::SupportingEventDateMismatch { .. })
        ));

        let invalid_date = event(
            "change:unbounded",
            documents::ChangeOperation::CHANGE_OPERATION_RENUMBER,
            unbounded(),
            false,
        );
        assert_eq!(
            ProvisionTimeline::build(&provision, &[], &[invalid_date], &TimelineConfig::default(),)
                .unwrap_err(),
            TimelineError::InvalidEffectiveDate("change:unbounded".to_owned())
        );

        let mut empty_replacement = event(
            "change:empty-replacement",
            documents::ChangeOperation::CHANGE_OPERATION_AMEND,
            known(date(2020, 1, 1)),
            true,
        );
        empty_replacement.replacement_spans[0].end_byte =
            empty_replacement.replacement_spans[0].start_byte;
        assert_eq!(
            ProvisionTimeline::build(
                &provision,
                &[],
                &[empty_replacement],
                &TimelineConfig::default(),
            )
            .unwrap_err(),
            TimelineError::InvalidReplacementEvidence("change:empty-replacement".to_owned())
        );

        let mut empty_support = event(
            "change:empty-support",
            documents::ChangeOperation::CHANGE_OPERATION_RENUMBER,
            known(date(2020, 1, 1)),
            false,
        );
        let supports = empty_support.supports.as_mut().unwrap();
        supports.sources.clear();
        supports.spans.push(common::TextSpan {
            text_artifact_id: "artifact:amendment".to_owned(),
            start_byte: 10,
            end_byte: 10,
            ..Default::default()
        });
        assert_eq!(
            ProvisionTimeline::build(
                &provision,
                &[],
                &[empty_support],
                &TimelineConfig::default(),
            )
            .unwrap_err(),
            TimelineError::InvalidChangeEvidence("change:empty-support".to_owned())
        );
    }

    #[test]
    fn filters_rejected_versions_and_enforces_preflight_limits() {
        let mut rejected = version("version:rejected", unbounded(), unbounded(), &[]);
        rejected.review_state = EnumOrUnknown::new(common::ReviewState::REVIEW_STATE_REJECTED);
        let versions = vec![rejected];
        let provision = provision();
        let timeline =
            ProvisionTimeline::build(&provision, &versions, &[], &TimelineConfig::default())
                .unwrap();
        let selection = timeline
            .select_as_of(
                &date(2024, 1, 1),
                common::UnresolvedPolicy::UNRESOLVED_POLICY_REPORT,
            )
            .unwrap();
        assert_eq!(selection.decision, TemporalDecision::NotEffective);
        assert_eq!(selection.excluded_version_ids, ["version:rejected"]);

        assert_eq!(
            ProvisionTimeline::build(
                &provision,
                &versions,
                &[event(
                    "change:renumber",
                    documents::ChangeOperation::CHANGE_OPERATION_RENUMBER,
                    known(date(2024, 1, 1)),
                    false,
                )],
                &TimelineConfig {
                    maximum_versions: 1,
                    maximum_events: 1,
                    maximum_support_edges: 1,
                },
            )
            .unwrap_err(),
            TimelineError::SupportLimit {
                actual: 2,
                maximum: 1,
            }
        );

        assert_eq!(
            ProvisionTimeline::build(
                &provision,
                &[
                    documents::ProvisionVersion::default(),
                    documents::ProvisionVersion::default()
                ],
                &[],
                &TimelineConfig {
                    maximum_versions: 1,
                    maximum_events: 1,
                    maximum_support_edges: 1,
                },
            )
            .unwrap_err(),
            TimelineError::VersionLimit {
                actual: 2,
                maximum: 1,
            }
        );
    }

    #[test]
    fn canonicalizes_input_order_and_rejects_unspecified_policy() {
        let versions = vec![
            version(
                "version:second",
                known(date(2023, 1, 1)),
                unbounded(),
                &["change:second"],
            ),
            version(
                "version:first",
                known(date(2020, 1, 1)),
                known(date(2023, 1, 1)),
                &[],
            ),
        ];
        let events = vec![
            event(
                "change:later",
                documents::ChangeOperation::CHANGE_OPERATION_REPEAL,
                known(date(2030, 1, 1)),
                false,
            ),
            event(
                "change:second",
                documents::ChangeOperation::CHANGE_OPERATION_AMEND,
                known(date(2023, 1, 1)),
                true,
            ),
        ];
        let provision = provision();
        let timeline =
            ProvisionTimeline::build(&provision, &versions, &events, &TimelineConfig::default())
                .unwrap();
        let ordered_versions: Vec<_> = timeline
            .versions()
            .iter()
            .map(|version| version.meta.record_id.as_str())
            .collect();
        let ordered_events: Vec<_> = timeline
            .relevant_events()
            .iter()
            .map(|event| event.meta.record_id.as_str())
            .collect();
        assert_eq!(ordered_versions, ["version:first", "version:second"]);
        assert_eq!(ordered_events, ["change:second", "change:later"]);
        assert_eq!(timeline.provision().meta.record_id, "provision:article-1");
        assert_eq!(
            timeline
                .select_as_of(
                    &date(2024, 1, 1),
                    common::UnresolvedPolicy::UNRESOLVED_POLICY_UNSPECIFIED,
                )
                .unwrap_err(),
            TimelineError::InvalidUnresolvedPolicy
        );
    }
}
