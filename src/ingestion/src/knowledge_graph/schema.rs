//! Mendefinisikan jenis node, predicate, atribut wajib, dan aturan kompatibilitas endpoint graph.
//!
//! Peran dalam komponen:
//! Menyelaraskan extraction, assembly, validation, dan pemetaan ke Neo4j.
//!
//! Kontrak integrasi dan perhatian implementasi:
//! Schema graph mengikuti domain, bukan objek SDK vendor; versioning schema dan migrasi perlu dinyatakan saat berubah.
//!
//! Benchmark dan gate penerimaan:
//! [DOMAIN] Gate deterministik: ID dan referensi sumber/versi tidak ambigu; invalid record ditolak atau dikarantina secara eksplisit. Kompatibilitas schema diperiksa dengan fixtures; belum ada hasil pengukuran.
//!
//! [GRAPH] Ukur validitas endpoint dan provenance, ketepatan predicate/arah, kelengkapan jalur bukti, waktu assembly/traversal, dan penggunaan memori. Gate: graph yang dipublikasikan tidak memiliki endpoint/bukti wajib yang hilang. Connectivity adalah diagnosis, bukan target memaksa satu komponen.
//! Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
//! Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//!
//! Status: compiler vocabulary versi bersama dan gate typed assertion aktif pada EXTRACT worker.
//! Source JSONC dimuat sekali, hash bytes dipin, dan hasil divalidasi sebelum artefak persisten.
//! Bukti: uji parser, endpoint, predicate, version, dan manifest; gold extraction serta target
//! numerik tetap REQUIRED_UNMEASURED menurut configs/benchmark-targets.yaml.

use crate::wire::{common, graph};
use serde::de::{MapAccess, SeqAccess, Visitor};
use serde::{Deserialize, Deserializer};
use serde_json::{Map, Value};
use sha2::{Digest, Sha256};
use std::collections::{HashMap, HashSet};
use std::fmt;

struct StrictValue(Value);

impl<'de> Deserialize<'de> for StrictValue {
    fn deserialize<D: Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {
        struct StrictVisitor;
        impl<'de> Visitor<'de> for StrictVisitor {
            type Value = StrictValue;
            fn expecting(&self, formatter: &mut fmt::Formatter) -> fmt::Result {
                formatter.write_str("JSON value without duplicate object keys")
            }
            fn visit_bool<E: serde::de::Error>(self, value: bool) -> Result<Self::Value, E> {
                Ok(StrictValue(Value::Bool(value)))
            }
            fn visit_i64<E: serde::de::Error>(self, value: i64) -> Result<Self::Value, E> {
                Ok(StrictValue(Value::Number(value.into())))
            }
            fn visit_u64<E: serde::de::Error>(self, value: u64) -> Result<Self::Value, E> {
                Ok(StrictValue(Value::Number(value.into())))
            }
            fn visit_f64<E: serde::de::Error>(self, value: f64) -> Result<Self::Value, E> {
                serde_json::Number::from_f64(value)
                    .map(Value::Number)
                    .map(StrictValue)
                    .ok_or_else(|| E::custom("nonfinite number"))
            }
            fn visit_str<E: serde::de::Error>(self, value: &str) -> Result<Self::Value, E> {
                Ok(StrictValue(Value::String(value.to_owned())))
            }
            fn visit_string<E: serde::de::Error>(self, value: String) -> Result<Self::Value, E> {
                Ok(StrictValue(Value::String(value)))
            }
            fn visit_none<E: serde::de::Error>(self) -> Result<Self::Value, E> {
                Ok(StrictValue(Value::Null))
            }
            fn visit_unit<E: serde::de::Error>(self) -> Result<Self::Value, E> {
                Ok(StrictValue(Value::Null))
            }
            fn visit_seq<A: SeqAccess<'de>>(
                self,
                mut sequence: A,
            ) -> Result<Self::Value, A::Error> {
                let mut values = Vec::new();
                while let Some(value) = sequence.next_element::<StrictValue>()? {
                    values.push(value.0);
                }
                Ok(StrictValue(Value::Array(values)))
            }
            fn visit_map<A: MapAccess<'de>>(self, mut access: A) -> Result<Self::Value, A::Error> {
                let mut values = Map::new();
                while let Some((key, value)) = access.next_entry::<String, StrictValue>()? {
                    if values.insert(key.clone(), value.0).is_some() {
                        return Err(serde::de::Error::custom(format!(
                            "duplicate ontology key {key}"
                        )));
                    }
                }
                Ok(StrictValue(Value::Object(values)))
            }
        }
        deserializer.deserialize_any(StrictVisitor)
    }
}

#[derive(Clone, Debug)]
struct Predicate {
    subjects: HashSet<String>,
    objects: HashSet<String>,
    qualifiers: HashSet<String>,
    origins: HashSet<String>,
    allow_self: bool,
}

/// Immutable, exact-byte-pinned ontology compiled once at worker startup.
#[derive(Clone, Debug)]
pub struct Ontology {
    version: String,
    hash: String,
    entities: HashSet<String>,
    qualifiers: HashMap<String, HashSet<String>>,
    predicates: HashMap<String, Predicate>,
}

impl Ontology {
    pub fn parse_jsonc(raw: &[u8]) -> Result<Self, String> {
        if raw.is_empty() || raw.len() > 1 << 20 || raw.starts_with(&[0xef, 0xbb, 0xbf]) {
            return Err("ontology must be bounded UTF-8 without BOM".into());
        }
        let input = std::str::from_utf8(raw).map_err(|e| e.to_string())?;
        let mut started = false;
        let body = input
            .lines()
            .filter(|line| {
                if !started && (line.trim().is_empty() || line.trim().starts_with("//")) {
                    false
                } else {
                    started = true;
                    true
                }
            })
            .collect::<Vec<_>>()
            .join("\n");
        let mut decoder = serde_json::Deserializer::from_str(&body);
        let value = StrictValue::deserialize(&mut decoder)
            .map_err(|e| e.to_string())?
            .0;
        decoder.end().map_err(|e| e.to_string())?;
        let root = exact_object(
            &value,
            &[
                "schema_version",
                "ontology_version",
                "entity_types",
                "qualifiers",
                "predicates",
            ],
        )?;
        if root["schema_version"].as_u64() != Some(1) {
            return Err("ontology schema version must be 1".into());
        }
        let version = required_string(root, "ontology_version")?.to_owned();
        if version.is_empty()
            || version.len() > 128
            || !version
                .bytes()
                .all(|b| b.is_ascii_alphanumeric() || b"._:-".contains(&b))
        {
            return Err("ontology version is invalid".into());
        }
        let entities = unique_strings(&root["entity_types"])?;
        let mut qualifiers = HashMap::new();
        for entry in required_array(root, "qualifiers")? {
            let item = exact_object(entry, &["id", "value_kinds"])?;
            let id = required_string(item, "id")?.to_owned();
            let kinds = unique_strings(&item["value_kinds"])?;
            if !valid_id(&id)
                || kinds.is_empty()
                || kinds
                    .iter()
                    .any(|kind| !matches!(kind.as_str(), "entity" | "literal" | "date" | "number"))
                || qualifiers.insert(id.clone(), kinds).is_some()
            {
                return Err(format!("invalid qualifier {id}"));
            }
        }
        let mut predicates = HashMap::new();
        for entry in required_array(root, "predicates")? {
            let item = exact_object(
                entry,
                &[
                    "id",
                    "subject_types",
                    "object_types",
                    "qualifier_ids",
                    "origins",
                    "allow_self",
                ],
            )?;
            let id = required_string(item, "id")?.to_owned();
            if !valid_id(&id) {
                return Err(format!("invalid predicate {id}"));
            }
            let subjects = unique_strings(&item["subject_types"])?;
            let objects = unique_strings(&item["object_types"])?;
            let allowed = unique_strings(&item["qualifier_ids"])?;
            let origins = unique_strings(&item["origins"])?;
            if subjects.is_empty()
                || objects.is_empty()
                || origins.is_empty()
                || !subjects.is_subset(&entities)
                || !objects.is_subset(&entities)
                || allowed.iter().any(|q| !qualifiers.contains_key(q))
                || origins
                    .iter()
                    .any(|origin| !matches!(origin.as_str(), "explicit" | "inferred"))
            {
                return Err(format!("predicate {id} has dangling constraints"));
            }
            let predicate = Predicate {
                subjects,
                objects,
                qualifiers: allowed,
                origins,
                allow_self: item["allow_self"]
                    .as_bool()
                    .ok_or("allow_self must be bool")?,
            };
            if predicates.insert(id.clone(), predicate).is_some() {
                return Err(format!("duplicate predicate {id}"));
            }
        }
        if entities.is_empty() || qualifiers.is_empty() || predicates.is_empty() {
            return Err("ontology vocabulary is empty".into());
        }
        Ok(Self {
            version,
            hash: format!("{:x}", Sha256::digest(raw)),
            entities,
            qualifiers,
            predicates,
        })
    }

    pub fn version(&self) -> &str {
        &self.version
    }
    pub fn sha256(&self) -> &str {
        &self.hash
    }
    pub fn content_hash(&self) -> common::ContentHash {
        common::ContentHash {
            sha256: self.hash.clone(),
            ..Default::default()
        }
    }

    /// Recheck self-edge policy after mention IDs become canonical IDs.
    /// Extraction validation alone cannot catch two mentions linked to one entity.
    pub fn permits_canonical_endpoints(
        &self,
        predicate_id: &str,
        subject_id: &str,
        object_id: &str,
    ) -> bool {
        self.predicates
            .get(predicate_id)
            .is_some_and(|rule| rule.allow_self || subject_id != object_id)
    }

    pub fn validate_extraction_batch(&self, batch: &graph::ExtractionBatch) -> Result<(), String> {
        if batch.ontology_version != self.version {
            return Err("extraction ontology version differs".into());
        }
        let mut mentions = HashMap::with_capacity(batch.mentions.len());
        for mention in &batch.mentions {
            let id = mention
                .meta
                .as_ref()
                .map(|m| m.record_id.as_str())
                .unwrap_or("");
            if id.is_empty()
                || !self.entities.contains(&mention.candidate_type)
                || mentions
                    .insert(id, mention.candidate_type.as_str())
                    .is_some()
            {
                return Err(format!("invalid ontology mention {id}"));
            }
        }
        for assertion in &batch.assertions {
            let id = assertion
                .meta
                .as_ref()
                .map(|m| m.record_id.as_str())
                .unwrap_or("");
            let rule = self
                .predicates
                .get(&assertion.predicate_id)
                .ok_or_else(|| format!("unknown predicate {id}"))?;
            let subject = mentions
                .get(assertion.subject_id.as_str())
                .ok_or("unknown subject")?;
            let object = mentions
                .get(assertion.object_id.as_str())
                .ok_or("unknown object")?;
            let origin = match assertion.origin.enum_value() {
                Ok(graph::AssertionOrigin::ASSERTION_ORIGIN_EXPLICIT) => "explicit",
                Ok(graph::AssertionOrigin::ASSERTION_ORIGIN_INFERRED) => "inferred",
                _ => "unknown",
            };
            if id.is_empty()
                || assertion.ontology_version != self.version
                || !rule.subjects.contains(*subject)
                || !rule.objects.contains(*object)
                || (!rule.allow_self && assertion.subject_id == assertion.object_id)
                || !rule.origins.contains(origin)
            {
                return Err(format!("typed predicate rejected assertion {id}"));
            }
            for qualifier in &assertion.qualifiers {
                let kind = match qualifier.value.as_ref() {
                    Some(graph::qualifier::Value::MentionId(_))
                    | Some(graph::qualifier::Value::CanonicalId(_)) => "entity",
                    Some(graph::qualifier::Value::Literal(_)) => "literal",
                    Some(graph::qualifier::Value::Date(_)) => "date",
                    Some(graph::qualifier::Value::Number(_)) => "number",
                    None => "unknown",
                };
                if !rule.qualifiers.contains(&qualifier.predicate_id)
                    || !self
                        .qualifiers
                        .get(&qualifier.predicate_id)
                        .is_some_and(|kinds| kinds.contains(kind))
                {
                    return Err(format!("invalid qualifier on assertion {id}"));
                }
            }
        }
        Ok(())
    }
}

fn exact_object<'a>(value: &'a Value, fields: &[&str]) -> Result<&'a Map<String, Value>, String> {
    let object = value.as_object().ok_or("ontology entry must be object")?;
    if object.len() != fields.len() || fields.iter().any(|field| !object.contains_key(*field)) {
        return Err("ontology entry has unknown or missing fields".into());
    }
    Ok(object)
}

fn required_string<'a>(object: &'a Map<String, Value>, field: &str) -> Result<&'a str, String> {
    object[field]
        .as_str()
        .ok_or_else(|| format!("{field} must be string"))
}

fn required_array<'a>(
    object: &'a Map<String, Value>,
    field: &str,
) -> Result<&'a Vec<Value>, String> {
    object[field]
        .as_array()
        .ok_or_else(|| format!("{field} must be array"))
}

fn unique_strings(value: &Value) -> Result<HashSet<String>, String> {
    let array = value.as_array().ok_or("ontology IDs must be array")?;
    let mut result = HashSet::with_capacity(array.len());
    for entry in array {
        let id = entry.as_str().ok_or("ontology ID must be string")?;
        if !valid_id(id) || !result.insert(id.to_owned()) {
            return Err(format!("invalid or duplicate ontology ID {id}"));
        }
    }
    Ok(result)
}

fn valid_id(value: &str) -> bool {
    let bytes = value.as_bytes();
    !bytes.is_empty()
        && bytes.len() <= 64
        && bytes[0].is_ascii_lowercase()
        && bytes[1..]
            .iter()
            .all(|b| b.is_ascii_lowercase() || b.is_ascii_digit() || *b == b'_')
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn shared_ontology_compiles_and_pins_bytes() {
        let raw = include_bytes!(concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/../../configs/ontology-v1.jsonc"
        ));
        let ontology = Ontology::parse_jsonc(raw).unwrap();
        assert_eq!(ontology.version(), "id-regulation-ontology-v1");
        assert_eq!(ontology.sha256(), format!("{:x}", Sha256::digest(raw)));
    }

    #[test]
    fn typed_edges_reject_unknown_predicates_and_endpoint_drift() {
        use protobuf::{EnumOrUnknown, MessageField};
        let ontology = Ontology::parse_jsonc(include_bytes!(concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/../../configs/ontology-v1.jsonc"
        )))
        .unwrap();
        let mention = |id: &str, kind: &str| graph::Mention {
            meta: MessageField::some(common::RecordMeta {
                record_id: id.to_owned(),
                ..Default::default()
            }),
            candidate_type: kind.to_owned(),
            ..Default::default()
        };
        let mut batch = graph::ExtractionBatch {
            ontology_version: ontology.version().to_owned(),
            mentions: vec![
                mention("organization:1", "organization"),
                mention("permit:1", "permit"),
            ],
            assertions: vec![graph::RelationAssertion {
                meta: MessageField::some(common::RecordMeta {
                    record_id: "assertion:1".to_owned(),
                    ..Default::default()
                }),
                subject_id: "organization:1".to_owned(),
                object_id: "permit:1".to_owned(),
                predicate_id: "permits".to_owned(),
                ontology_version: ontology.version().to_owned(),
                origin: EnumOrUnknown::new(graph::AssertionOrigin::ASSERTION_ORIGIN_EXPLICIT),
                ..Default::default()
            }],
            ..Default::default()
        };
        ontology.validate_extraction_batch(&batch).unwrap();
        batch.assertions[0].predicate_id = "permits_typo".to_owned();
        assert!(ontology.validate_extraction_batch(&batch).is_err());
        batch.assertions[0].predicate_id = "permits".to_owned();
        batch.mentions[1].candidate_type = "date".to_owned();
        assert!(ontology.validate_extraction_batch(&batch).is_err());
        batch.mentions[1].candidate_type = "permit".to_owned();
        batch.assertions[0].object_id = "organization:1".to_owned();
        assert!(ontology.validate_extraction_batch(&batch).is_err());
    }

    #[test]
    fn invalid_qualifier_id_is_rejected() {
        let source = include_str!(concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/../../configs/ontology-v1.jsonc"
        ));
        let changed = source.replacen("\"id\": \"scope\"", "\"id\": \"Scope\"", 1);
        assert_ne!(source, changed);
        assert!(Ontology::parse_jsonc(changed.as_bytes()).is_err());
    }

    #[test]
    fn duplicate_object_key_is_rejected() {
        let source = include_str!(concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/../../configs/ontology-v1.jsonc"
        ));
        let changed = source.replacen(
            "\"schema_version\": 1",
            "\"schema_version\": 1, \"schema_version\": 1",
            1,
        );
        assert_ne!(source, changed);
        assert!(Ontology::parse_jsonc(changed.as_bytes()).is_err());
    }
}
