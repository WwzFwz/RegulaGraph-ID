//! C01 Rust boundary validation uses generated descriptors and common field-rule extensions.
//! Bounded decoding preserves unknown fields; validation never opens models/storage or resolves remote IDs.
//! Shared scalar/temporal/vector checks protect worker inputs. Production graph/storage invariants belong
//! to their domain owners; performance gates require real workloads, not these compatibility fixtures.
use crate::wire::{common, documents, evidence, graph, inference};
use protobuf::reflect::ReflectValueRef;
use protobuf::{CodedInputStream, MessageDyn};
use std::collections::HashSet;

#[derive(Clone, Copy)]
pub struct Limits {
    pub max_bytes: usize,
    pub max_depth: u32,
    pub max_items: usize,
}
impl Default for Limits {
    fn default() -> Self {
        Self {
            max_bytes: 16 << 20,
            max_depth: 64,
            max_items: 100000,
        }
    }
}
fn require(ok: bool, field: &str) -> Result<(), String> {
    if ok {
        Ok(())
    } else {
        Err(field.to_string())
    }
}

pub fn validate(message: &dyn MessageDyn, limits: Limits) -> Result<(), String> {
    require(
        limits.max_bytes > 0 && limits.max_depth > 0 && limits.max_items > 0,
        "invalid limits",
    )?;
    require(
        message.compute_size_dyn() <= limits.max_bytes as u64,
        "byte limit",
    )?;
    walk(message, 0, limits, &mut limits.max_items.clone(), "")
}
fn walk(
    m: &dyn MessageDyn,
    depth: u32,
    limits: Limits,
    remaining: &mut usize,
    inherited: &str,
) -> Result<(), String> {
    let own = corpus(m);
    require(
        own.is_empty() || inherited.is_empty() || own == inherited,
        "nested corpus mismatch",
    )?;
    let active = if own.is_empty() { inherited } else { &own };
    require(depth <= limits.max_depth, "depth limit")?;
    let descriptor = m.descriptor_dyn();
    for f in descriptor.fields() {
        let rule = f
            .proto()
            .options
            .as_ref()
            .and_then(|o| common::exts::rules.get(o))
            .unwrap_or_default();
        let where_ = f.full_name();
        let repeated = f.is_repeated();
        require(
            !rule.required
                || if repeated {
                    !f.get_repeated(m).is_empty()
                } else {
                    f.has_field(m)
                },
            &where_,
        )?;
        let values: Vec<ReflectValueRef> = if repeated {
            let list = f.get_repeated(m);
            require(list.len() >= rule.min_items as usize, &where_)?;
            require(list.len() <= *remaining, "item limit")?;
            *remaining -= list.len();
            (0..list.len()).map(|i| list.get(i)).collect()
        } else if !f.has_field(m)
            && (f.proto().proto3_optional()
                || f.containing_oneof_including_synthetic().is_some()
                || matches!(
                    f.singular_runtime_type(),
                    protobuf::reflect::RuntimeType::Message(_)
                ))
        {
            Vec::new()
        } else {
            vec![f.get_singular_field_or_default(m)]
        };
        let mut seen = HashSet::new();
        for v in values {
            if rule.unique {
                require(seen.insert(format!("{v:?}")), &where_)?;
            }
            if let ReflectValueRef::Message(ref child) = v {
                require(*remaining > 0, "item limit")?;
                *remaining -= 1;
                walk(&**child, depth + 1, limits, remaining, active)?;
                continue;
            }
            if let ReflectValueRef::String(s) = v {
                if rule.ascii_id {
                    require(
                        !s.is_empty()
                            && s.len() <= 256
                            && s.bytes().all(|c| (33..=126).contains(&c)),
                        &where_,
                    )?;
                }
                if rule.sha256 {
                    require(
                        s.len() == 64
                            && s.bytes()
                                .all(|c| c.is_ascii_digit() || (b'a'..=b'f').contains(&c)),
                        &where_,
                    )?;
                }
            }
            if let ReflectValueRef::Enum(ref en, n) = v {
                if rule.required {
                    require(n != 0 && en.value_by_number(n).is_some(), &where_)?;
                }
            }
            let num = match v {
                ReflectValueRef::I32(n) => Some(n as f64),
                ReflectValueRef::I64(n) => Some(n as f64),
                ReflectValueRef::U32(n) => Some(n as f64),
                ReflectValueRef::U64(n) => Some(n as f64),
                ReflectValueRef::F32(n) => Some(n as f64),
                ReflectValueRef::F64(n) => Some(n),
                _ => None,
            };
            if let Some(n) = num {
                if rule.positive || rule.finite || rule.probability {
                    require(
                        n.is_finite()
                            && (!rule.positive || n > 0.)
                            && (!rule.probability || (0. ..=1.).contains(&n)),
                        &where_,
                    )?;
                }
                if f.name() == "schema_version" {
                    require(n == 1., "schema version")?;
                }
            }
        }
    }
    for oneof in descriptor.oneofs() {
        require(oneof.fields().any(|f| f.has_field(m)), &oneof.full_name())?;
    }
    semantic(m)
}
fn corpus(m: &dyn MessageDyn) -> String {
    let d = m.descriptor_dyn();
    if let Some(f) = d.field_by_name("corpus_id") {
        return f
            .get_singular_field_or_default(m)
            .to_str()
            .unwrap_or("")
            .to_string();
    }
    for name in ["meta", "context", "batch"] {
        if let Some(f) = d.field_by_name(name) {
            if f.has_field(m) {
                if let Some(child) = f.get_singular(m).and_then(|v| v.to_message()) {
                    let c = corpus(&*child);
                    if !c.is_empty() {
                        return c;
                    }
                }
            }
        }
    }
    String::new()
}
fn semantic(m: &dyn MessageDyn) -> Result<(), String> {
    if let Some(p) = m.downcast_ref::<common::ArtifactRef>() {
        let k = &p.storage_key;
        require(
            !k.contains(':')
                && !k.contains('\\')
                && k.split('/')
                    .all(|part| !part.is_empty() && part != "." && part != ".."),
            "ArtifactRef storage key",
        )?;
    }
    if let Some(p) = m.downcast_ref::<common::TemporalScope>() {
        require(
            p.mode.value() != common::TemporalMode::TEMPORAL_MODE_AS_OF as i32
                || p.effective_at.is_some(),
            "TemporalScope AS_OF",
        )?;
        require(
            if p.mode.value() == common::TemporalMode::TEMPORAL_MODE_COMPARE as i32 {
                p.compare_dates.len() >= 2 && p.effective_at.is_none()
            } else {
                p.compare_dates.is_empty()
            },
            "TemporalScope compare",
        )?;
    }
    if let Some(p) = m.downcast_ref::<graph::GraphPath>() {
        require(
            p.ordered_node_ids.len() == p.ordered_assertion_ids.len() + 1
                && p.selected_support_ids.len() == p.ordered_assertion_ids.len(),
            "GraphPath cardinality",
        )?;
    }
    if let Some(p) = m.downcast_ref::<documents::SourceBlob>() {
        require(
            p.byte_size == p.artifact_ref.byte_size
                && p.raw_sha256.sha256 == p.artifact_ref.content_hash.sha256,
            "SourceBlob artifact",
        )?;
    }
    if let Some(p) = m.downcast_ref::<documents::SourceObservation>() {
        require(
            p.status.value() != documents::ObservationStatus::OBSERVATION_STATUS_COMPLETE as i32
                || (p.source_blob_id.is_some() && p.error.is_none()),
            "SourceObservation completion",
        )?;
    }

    if let Some(p) = m.downcast_ref::<common::CalendarDate>() {
        let y = p.year;
        require(
            (1..=9999).contains(&y) && (1..=12).contains(&p.month),
            "CalendarDate",
        )?;
        let mut days = [31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
        if y % 4 == 0 && (y % 100 != 0 || y % 400 == 0) {
            days[1] = 29
        };
        require(
            p.day > 0 && p.day <= days[p.month as usize - 1],
            "CalendarDate",
        )?;
    }
    if let Some(p) = m.downcast_ref::<common::DateAssertion>() {
        require(
            (p.knowledge.value() == common::DateKnowledge::DATE_KNOWLEDGE_KNOWN as i32)
                == p.value.is_some(),
            "DateAssertion value",
        )?;
        require(
            if p.knowledge.value() == common::DateKnowledge::DATE_KNOWLEDGE_CONFLICT as i32 {
                p.alternatives.len() >= 2
            } else {
                p.alternatives.is_empty()
            },
            "DateAssertion alternatives",
        )?;
    }
    if let Some(p) = m.downcast_ref::<common::Visibility>() {
        require(p.to_seq.is_none_or(|v| v > p.from_seq), "Visibility")?;
    }
    if let Some(p) = m.downcast_ref::<common::TextSpan>() {
        require(p.end_byte >= p.start_byte, "TextSpan")?;
    }
    if let Some(p) = m.downcast_ref::<common::BoundingBox>() {
        require(p.x1 >= p.x0 && p.y1 >= p.y0, "BoundingBox")?;
    }
    if let Some(p) = m.downcast_ref::<common::LegalInterval>() {
        if let (Some(s), Some(e)) = (p.start.value.as_ref(), p.end.value.as_ref()) {
            let number = |d: &common::CalendarDate| {
                i64::from(d.year) * 10000 + i64::from(d.month) * 100 + i64::from(d.day)
            };
            require(number(s) < number(e), "LegalInterval")?;
        }
    }
    if let Some(p) = m.downcast_ref::<evidence::SparseVector>() {
        require(
            p.indices.len() == p.values.len() && p.indices.windows(2).all(|v| v[0] < v[1]),
            "SparseVector",
        )?;
    }
    if let Some(p) = m.downcast_ref::<evidence::DenseVector>() {
        require(p.dimensions as usize == p.values.len(), "DenseVector")?;
    }
    if let Some(p) = m.downcast_ref::<common::Counts>() {
        require(
            p.accepted <= p.expected && p.rejected == p.expected - p.accepted,
            "Counts",
        )?;
    }
    if let Some(p) = m.downcast_ref::<common::TruncationInfo>() {
        require(
            p.retained_tokens <= p.original_tokens
                && (p.truncated || p.retained_tokens == p.original_tokens),
            "TruncationInfo",
        )?;
    }
    if let Some(p) = m.downcast_ref::<common::RequestContext>() {
        if let Some(s) = p.snapshot_ref.as_ref() {
            require(p.corpus_id == s.corpus_id, "context corpus")?;
        }
    }
    if let Some(p) = m.downcast_ref::<protobuf::well_known_types::timestamp::Timestamp>() {
        require(
            (-62135596800..=253402300799).contains(&p.seconds)
                && (0..=999999999).contains(&p.nanos),
            "Timestamp",
        )?;
    }
    if let Some(p) = m.downcast_ref::<common::ModelManifest>() {
        require(
            p.task.value() != common::ModelTask::MODEL_TASK_EMBED as i32 || p.dimensions.is_some(),
            "model dimensions",
        )?;
    }
    if let Some(p) = m.downcast_ref::<inference::EmbedBatchRequest>() {
        let ids: HashSet<_> = p.items.iter().map(|v| &v.item_id).collect();
        require(
            ids.len() == p.items.len()
                && p.model.task.value() == common::ModelTask::MODEL_TASK_EMBED as i32,
            "EmbedBatch",
        )?;
    }
    if let Some(p) = m.downcast_ref::<inference::RerankBatchRequest>() {
        let ids: HashSet<_> = p.pairs.iter().map(|v| &v.pair_id).collect();
        require(
            ids.len() == p.pairs.len()
                && p.model.task.value() == common::ModelTask::MODEL_TASK_RERANK as i32,
            "RerankBatch",
        )?;
    }
    Ok(())
}
pub fn check_utf8_span(text: &[u8], start: u64, end: u64) -> Result<(), String> {
    let text = std::str::from_utf8(text).map_err(|_| "invalid UTF-8")?;
    require(start <= end && end <= text.len() as u64, "span bounds")?;
    require(
        text.is_char_boundary(start as usize) && text.is_char_boundary(end as usize),
        "UTF-8 split",
    )
}
pub fn decode(
    raw: &[u8],
    descriptor: &protobuf::reflect::MessageDescriptor,
    limits: Limits,
) -> Result<Box<dyn MessageDyn>, String> {
    require(raw.len() <= limits.max_bytes, "byte limit")?;
    let mut input = CodedInputStream::from_bytes(raw);
    input.set_recursion_limit(limits.max_depth);
    let m = descriptor
        .parse_from(&mut input)
        .map_err(|e| e.to_string())?;
    input.check_eof().map_err(|e| e.to_string())?;
    validate(&*m, limits)?;
    Ok(m)
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn unicode_boundaries() {
        let text = "Pasal \u{00e9} \u{2014}".as_bytes();
        assert!(check_utf8_span(text, 0, 7).is_err());
        assert!(check_utf8_span(text, 0, 8).is_ok());
    }
    #[test]
    #[ignore = "requires fixture generation via tests/integration/wire_roundtrip.py"]
    fn wire_fixture_interop() {
        let dir = std::env::var_os("REGULAGRAPH_WIRE_FIXTURES").expect("missing wire fixtures");
        let dir = std::path::PathBuf::from(dir);
        let files = [
            crate::wire::common::file_descriptor(),
            crate::wire::documents::file_descriptor(),
            crate::wire::graph::file_descriptor(),
            crate::wire::evidence::file_descriptor(),
            crate::wire::answers::file_descriptor(),
            crate::wire::jobs::file_descriptor(),
            crate::wire::inference::file_descriptor(),
            crate::wire::evaluation::file_descriptor(),
        ];
        let out = dir.join("rust");
        std::fs::create_dir_all(&out).unwrap();
        for line in std::fs::read_to_string(dir.join("cases.tsv"))
            .unwrap()
            .lines()
        {
            let f: Vec<_> = line.split('\t').collect();
            let desc = files
                .iter()
                .find_map(|d| d.message_by_full_name(&format!(".{}", f[1])))
                .unwrap();
            let raw = std::fs::read(dir.join(format!("{}.bin", f[0]))).unwrap();
            let limits = Limits {
                max_bytes: f[3].parse().unwrap(),
                ..Limits::default()
            };
            let result = decode(&raw, &desc, limits);
            assert_eq!(
                result.is_ok(),
                f[2] == "valid",
                "{}: {:?}",
                f[0],
                result.err()
            );
            let m = desc.parse_from_bytes(&raw).unwrap();
            std::fs::write(
                out.join(format!("{}.bin", f[0])),
                m.write_to_bytes_dyn().unwrap(),
            )
            .unwrap();
        }
    }
}
