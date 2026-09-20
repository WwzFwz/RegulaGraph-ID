//! Mendeteksi hierarki hukum Indonesia dari teks ternormalisasi secara deterministik.
//!
//! Peran dalam komponen:
//! Tahap ini mengubah baris heading yang ketat menjadi pohon dokumen, bab, bagian, paragraf,
//! pasal, ayat, butir, lampiran, dan penjelasan. Pohon menjadi batas semantik bagi chunker dan
//! mempertahankan mapping byte normalized-ke-raw untuk citation serta audit.
//!
//! Kontrak integrasi dan perhatian implementasi:
//! Offset adalah byte UTF-8 start-inclusive/end-exclusive. Identitas input wajib berupa ASCII ID
//! stabil milik source blob, text artifact, dan provision version. Pencocokan selalu berjangkar di
//! awal baris dan sengaja konservatif agar frasa seperti "sebagaimana dimaksud dalam Pasal 5"
//! tidak menjadi heading palsu. Tabel dan rekonstruksi urutan baca tetap tanggung jawab parser.
//!
//! Benchmark dan gate penerimaan:
//! Ukur `PARSING.STRUCTURE_F1`, `PARSING.CRITICAL_TOKENS`, `CHUNKING.THROUGHPUT`, dan
//! `INVARIANT.SOURCE_MAPPING` menurut configs/benchmark-targets.yaml. Status target tetap
//! REQUIRED_UNMEASURED sampai gold set dan workload resmi dijalankan.
//!
//! Status: parser hierarki deterministik dan proyeksi wire aktif; executable worker serta acceptance
//! corpus belum aktif.

use crate::document::normalization::text::{NormalizeError, NormalizedText};
use sha2::{Digest, Sha256};
use std::collections::HashMap;
use std::error::Error;
use std::fmt::{Display, Formatter};
use std::ops::Range;

const STRUCTURE_SCHEMA_VERSION: u32 = 1;

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum StructureKind {
    Document,
    Chapter,
    Part,
    Article,
    Paragraph,
    Item,
    Annex,
    Explanation,
}

impl StructureKind {
    fn wire_name(self) -> &'static str {
        match self {
            Self::Document => "DOCUMENT",
            Self::Chapter => "CHAPTER",
            Self::Part => "PART",
            Self::Article => "ARTICLE",
            Self::Paragraph => "PARAGRAPH",
            Self::Item => "ITEM",
            Self::Annex => "ANNEX",
            Self::Explanation => "EXPLANATION",
        }
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct StructureIdentity {
    pub source_blob_id: String,
    pub text_artifact_id: String,
    pub provision_version_id: String,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct StructureParserConfig {
    pub maximum_nodes: usize,
    pub maximum_heading_line_bytes: usize,
    pub recognize_list_items: bool,
}

impl Default for StructureParserConfig {
    fn default() -> Self {
        Self {
            maximum_nodes: 1_000_000,
            maximum_heading_line_bytes: 512,
            recognize_list_items: true,
        }
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct StructureNode {
    pub schema_version: u32,
    pub id: String,
    pub kind: StructureKind,
    pub label: String,
    pub parent_id: Option<String>,
    pub ordered_children: Vec<String>,
    pub normalized_span: Range<usize>,
    pub raw_span: Range<usize>,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct StructureTree {
    pub schema_version: u32,
    pub source_blob_id: String,
    pub text_artifact_id: String,
    pub provision_version_id: String,
    pub normalized_sha256: String,
    pub nodes: Vec<StructureNode>,
}

impl StructureTree {
    pub fn root(&self) -> &StructureNode {
        &self.nodes[0]
    }

    pub fn node(&self, id: &str) -> Option<&StructureNode> {
        self.nodes.iter().find(|node| node.id == id)
    }

    pub fn validate(&self, normalized: &NormalizedText) -> Result<(), StructureError> {
        normalized
            .validate_integrity()
            .map_err(StructureError::SourceMapping)?;
        if self.schema_version != STRUCTURE_SCHEMA_VERSION
            || self.source_blob_id.is_empty()
            || self.text_artifact_id.is_empty()
            || self.provision_version_id.is_empty()
            || self.normalized_sha256 != normalized.normalized_sha256
            || self.nodes.is_empty()
        {
            return Err(StructureError::InvalidTree("tree metadata"));
        }
        let root = &self.nodes[0];
        if root.kind != StructureKind::Document
            || root.parent_id.is_some()
            || root.normalized_span != (0..normalized.text.len())
        {
            return Err(StructureError::InvalidTree("root"));
        }
        let mut node_indices = HashMap::with_capacity(self.nodes.len());
        for (index, node) in self.nodes.iter().enumerate() {
            if node_indices.insert(node.id.as_str(), index).is_some() {
                return Err(StructureError::InvalidTree("duplicate node ID"));
            }
        }
        let mut child_owners = HashMap::with_capacity(self.nodes.len().saturating_sub(1));
        for node in &self.nodes {
            let mut previous_end = None;
            for child_id in &node.ordered_children {
                let child_index = node_indices
                    .get(child_id.as_str())
                    .copied()
                    .ok_or(StructureError::InvalidTree("unknown child"))?;
                let child = &self.nodes[child_index];
                if child.parent_id.as_deref() != Some(node.id.as_str())
                    || previous_end.is_some_and(|end| end > child.normalized_span.start)
                    || child_owners
                        .insert(child.id.as_str(), node.id.as_str())
                        .is_some()
                {
                    return Err(StructureError::InvalidTree("child order"));
                }
                previous_end = Some(child.normalized_span.end);
            }
        }
        for (index, node) in self.nodes.iter().enumerate() {
            if node.schema_version != STRUCTURE_SCHEMA_VERSION
                || !valid_ascii_id(&node.id)
                || node.normalized_span.is_empty()
                || node.normalized_span.end > normalized.text.len()
                || !normalized.text.is_char_boundary(node.normalized_span.start)
                || !normalized.text.is_char_boundary(node.normalized_span.end)
                || node.raw_span.is_empty()
            {
                return Err(StructureError::InvalidTree("node"));
            }
            if index > 0 {
                let parent_id = node
                    .parent_id
                    .as_deref()
                    .ok_or(StructureError::InvalidTree("missing parent"))?;
                let parent_index = node_indices
                    .get(parent_id)
                    .copied()
                    .ok_or(StructureError::InvalidTree("unknown parent"))?;
                if parent_index >= index {
                    return Err(StructureError::InvalidTree("parent order"));
                }
                let parent = &self.nodes[parent_index];
                if node.normalized_span.start < parent.normalized_span.start
                    || node.normalized_span.end > parent.normalized_span.end
                    || child_owners.get(node.id.as_str()).copied() != Some(parent_id)
                {
                    return Err(StructureError::InvalidTree("parent containment"));
                }
            }
            let expected_raw = normalized
                .raw_cover(node.normalized_span.clone())
                .map_err(StructureError::SourceMapping)?;
            if node.raw_span != expected_raw {
                return Err(StructureError::InvalidTree("raw span"));
            }
        }
        Ok(())
    }
}

#[derive(Debug, PartialEq, Eq)]
pub enum StructureError {
    InvalidConfig(&'static str),
    InvalidIdentity(&'static str),
    EmptyText,
    NodeLimit { maximum: usize },
    SourceMapping(NormalizeError),
    InvalidTree(&'static str),
}

impl Display for StructureError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::InvalidConfig(field) => write!(formatter, "invalid structure config: {field}"),
            Self::InvalidIdentity(field) => {
                write!(formatter, "invalid structure identity: {field}")
            }
            Self::EmptyText => write!(formatter, "normalized text must not be empty"),
            Self::NodeLimit { maximum } => write!(formatter, "structure exceeds {maximum} nodes"),
            Self::SourceMapping(error) => write!(formatter, "source mapping failed: {error}"),
            Self::InvalidTree(field) => write!(formatter, "invalid structure tree: {field}"),
        }
    }
}

impl Error for StructureError {}

#[derive(Clone, Debug)]
struct PendingNode {
    id: String,
    kind: StructureKind,
    label: String,
    parent_index: Option<usize>,
    children: Vec<usize>,
    start: usize,
    end: usize,
    level: u8,
}

#[derive(Clone, Debug)]
struct Heading {
    kind: StructureKind,
    label: String,
    level: u8,
}

pub fn parse_structure(
    normalized: &NormalizedText,
    identity: &StructureIdentity,
    config: &StructureParserConfig,
) -> Result<StructureTree, StructureError> {
    validate_inputs(normalized, identity, config)?;
    let root_id = stable_id(&[
        "structure-v1",
        &identity.text_artifact_id,
        StructureKind::Document.wire_name(),
        "root",
    ]);
    let mut pending = vec![PendingNode {
        id: root_id,
        kind: StructureKind::Document,
        label: "document".to_owned(),
        parent_index: None,
        children: Vec::new(),
        start: 0,
        end: normalized.text.len(),
        level: 0,
    }];
    let mut stack = vec![0usize];
    let mut duplicate_ordinals: HashMap<(usize, StructureKind, String), usize> = HashMap::new();
    let mut line_start = 0usize;

    for segment in normalized.text.split_inclusive(['\n', '\u{000C}']) {
        let line_without_separator = segment
            .strip_suffix('\n')
            .or_else(|| segment.strip_suffix('\u{000C}'))
            .unwrap_or(segment);
        let trimmed = line_without_separator.trim();
        let heading = if !trimmed.is_empty() {
            detect_heading(
                trimmed,
                config.recognize_list_items,
                config.maximum_heading_line_bytes,
            )
            .filter(|candidate| marker_parent_is_valid(candidate, &pending, &stack))
        } else {
            None
        };
        if let Some(heading) = heading {
            while stack.len() > 1
                && pending[*stack.last().expect("stack contains root")].level >= heading.level
            {
                let closed = stack.pop().expect("non-root stack entry");
                pending[closed].end = line_start;
            }
            let parent_index = *stack.last().expect("stack contains root");
            if pending.len() >= config.maximum_nodes {
                return Err(StructureError::NodeLimit {
                    maximum: config.maximum_nodes,
                });
            }
            let ordinal_key = (parent_index, heading.kind, heading.label.clone());
            let duplicate_ordinal = *duplicate_ordinals.get(&ordinal_key).unwrap_or(&0);
            duplicate_ordinals.insert(ordinal_key, duplicate_ordinal + 1);
            let node_id = stable_id(&[
                "structure-v1",
                &identity.text_artifact_id,
                &pending[parent_index].id,
                heading.kind.wire_name(),
                &heading.label,
                &duplicate_ordinal.to_string(),
            ]);
            let node_index = pending.len();
            pending.push(PendingNode {
                id: node_id,
                kind: heading.kind,
                label: heading.label,
                parent_index: Some(parent_index),
                children: Vec::new(),
                start: line_start,
                end: normalized.text.len(),
                level: heading.level,
            });
            pending[parent_index].children.push(node_index);
            stack.push(node_index);
        }
        line_start = line_start
            .checked_add(segment.len())
            .ok_or(StructureError::InvalidTree("line offset overflow"))?;
    }
    while stack.len() > 1 {
        let closed = stack.pop().expect("non-root stack entry");
        pending[closed].end = normalized.text.len();
    }

    let ids: Vec<String> = pending.iter().map(|node| node.id.clone()).collect();
    let mut nodes = Vec::with_capacity(pending.len());
    for node in pending {
        let normalized_span = node.start..node.end;
        let raw_span = normalized
            .raw_cover(normalized_span.clone())
            .map_err(StructureError::SourceMapping)?;
        nodes.push(StructureNode {
            schema_version: STRUCTURE_SCHEMA_VERSION,
            id: node.id,
            kind: node.kind,
            label: node.label,
            parent_id: node.parent_index.map(|index| ids[index].clone()),
            ordered_children: node
                .children
                .into_iter()
                .map(|index| ids[index].clone())
                .collect(),
            normalized_span,
            raw_span,
        });
    }
    let tree = StructureTree {
        schema_version: STRUCTURE_SCHEMA_VERSION,
        source_blob_id: identity.source_blob_id.clone(),
        text_artifact_id: identity.text_artifact_id.clone(),
        provision_version_id: identity.provision_version_id.clone(),
        normalized_sha256: normalized.normalized_sha256.clone(),
        nodes,
    };
    tree.validate(normalized)?;
    Ok(tree)
}

fn validate_inputs(
    normalized: &NormalizedText,
    identity: &StructureIdentity,
    config: &StructureParserConfig,
) -> Result<(), StructureError> {
    if normalized.text.is_empty() {
        return Err(StructureError::EmptyText);
    }
    for (name, value) in [
        ("source_blob_id", identity.source_blob_id.as_str()),
        ("text_artifact_id", identity.text_artifact_id.as_str()),
        (
            "provision_version_id",
            identity.provision_version_id.as_str(),
        ),
    ] {
        if !valid_ascii_id(value) {
            return Err(StructureError::InvalidIdentity(name));
        }
    }
    if config.maximum_nodes == 0 {
        return Err(StructureError::InvalidConfig("maximum_nodes"));
    }
    if config.maximum_heading_line_bytes < 8 {
        return Err(StructureError::InvalidConfig("maximum_heading_line_bytes"));
    }
    Ok(())
}

fn detect_heading(
    line: &str,
    recognize_list_items: bool,
    maximum_heading_line_bytes: usize,
) -> Option<Heading> {
    if let Some(marker) = clause_marker(line) {
        return Some(Heading {
            kind: StructureKind::Paragraph,
            label: marker.to_owned(),
            level: 50,
        });
    }
    if recognize_list_items {
        if let Some((marker, level)) = list_marker(line) {
            return Some(Heading {
                kind: StructureKind::Item,
                label: marker.to_owned(),
                level,
            });
        }
    }
    if line.len() > maximum_heading_line_bytes {
        return None;
    }
    let uppercase = line.to_uppercase();
    if uppercase == "PENJELASAN" || uppercase == "PENJELASAN ATAS" {
        return Some(Heading {
            kind: StructureKind::Explanation,
            label: line.to_owned(),
            level: 5,
        });
    }
    if uppercase == "LAMPIRAN"
        || strict_prefixed_identifier(&uppercase, "LAMPIRAN", annex_identifier)
    {
        return Some(Heading {
            kind: StructureKind::Annex,
            label: line.to_owned(),
            level: 5,
        });
    }
    if strict_prefixed_identifier(&uppercase, "BAB", roman_or_decimal_identifier) {
        return Some(Heading {
            kind: StructureKind::Chapter,
            label: line.to_owned(),
            level: 10,
        });
    }
    if strict_prefixed_identifier(&uppercase, "BAGIAN", word_identifier) {
        return Some(Heading {
            kind: StructureKind::Part,
            label: line.to_owned(),
            level: 20,
        });
    }
    if strict_prefixed_identifier(&uppercase, "PARAGRAF", roman_or_decimal_identifier) {
        return Some(Heading {
            kind: StructureKind::Paragraph,
            label: line.to_owned(),
            level: 30,
        });
    }
    if strict_prefixed_identifier(&uppercase, "PASAL", article_identifier) {
        return Some(Heading {
            kind: StructureKind::Article,
            label: line.to_owned(),
            level: 40,
        });
    }
    None
}

fn strict_prefixed_identifier(uppercase: &str, prefix: &str, validator: fn(&str) -> bool) -> bool {
    uppercase
        .strip_prefix(prefix)
        .and_then(|rest| rest.strip_prefix(' '))
        .is_some_and(|identifier| {
            !identifier.contains(char::is_whitespace) && validator(identifier)
        })
}

fn annex_identifier(value: &str) -> bool {
    value.is_empty() || roman_or_decimal_identifier(value) || article_identifier(value)
}

fn word_identifier(value: &str) -> bool {
    !value.is_empty() && value.chars().all(|character| character.is_alphabetic())
}

fn roman_or_decimal_identifier(value: &str) -> bool {
    !value.is_empty()
        && (value.chars().all(|character| character.is_ascii_digit())
            || value
                .chars()
                .all(|character| matches!(character, 'I' | 'V' | 'X' | 'L' | 'C' | 'D' | 'M')))
}

fn article_identifier(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 12
        && value
            .chars()
            .all(|character| character.is_ascii_alphanumeric())
        && value.chars().any(|character| character.is_ascii_digit())
}

fn clause_marker(line: &str) -> Option<&str> {
    let closing = line.find(')')?;
    let marker = line.get(..=closing)?;
    let number = marker.strip_prefix('(')?.strip_suffix(')')?;
    let rest = line.get(closing + 1..)?;
    if !number.is_empty()
        && number.len() <= 4
        && number.chars().all(|character| character.is_ascii_digit())
        && rest.starts_with(char::is_whitespace)
        && !rest.trim().is_empty()
    {
        Some(marker)
    } else {
        None
    }
}

fn list_marker(line: &str) -> Option<(&str, u8)> {
    let dot = line.find('.')?;
    let marker = line.get(..=dot)?;
    let identifier = marker.strip_suffix('.')?;
    let rest = line.get(dot + 1..)?;
    if !rest.starts_with(char::is_whitespace) || rest.trim().is_empty() {
        return None;
    }
    if identifier.len() == 1
        && identifier
            .chars()
            .all(|character| character.is_ascii_alphabetic())
    {
        return Some((marker, 60));
    }
    if !identifier.is_empty()
        && identifier.len() <= 4
        && identifier
            .chars()
            .all(|character| character.is_ascii_digit())
    {
        return Some((marker, 70));
    }
    None
}

fn marker_parent_is_valid(heading: &Heading, nodes: &[PendingNode], stack: &[usize]) -> bool {
    if heading.level < 50 {
        return true;
    }
    let Some(parent) = stack
        .iter()
        .rev()
        .map(|index| &nodes[*index])
        .find(|candidate| candidate.level < heading.level)
    else {
        return false;
    };
    match heading.level {
        50 => matches!(
            parent.kind,
            StructureKind::Article | StructureKind::Paragraph
        ),
        _ => matches!(
            parent.kind,
            StructureKind::Article | StructureKind::Paragraph | StructureKind::Item
        ),
    }
}

fn stable_id(parts: &[&str]) -> String {
    let mut hasher = Sha256::new();
    for part in parts {
        hasher.update((part.len() as u64).to_be_bytes());
        hasher.update(part.as_bytes());
    }
    format!("structure:{:x}", hasher.finalize())
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
    use crate::document::normalization::text::{normalize_text, TextNormalizerConfig};

    fn identity() -> StructureIdentity {
        StructureIdentity {
            source_blob_id: "source:fixture".to_owned(),
            text_artifact_id: "text:fixture".to_owned(),
            provision_version_id: "provision-version:fixture".to_owned(),
        }
    }

    fn normalized(raw: &str) -> NormalizedText {
        normalize_text(raw, &TextNormalizerConfig::default()).expect("fixture normalizes")
    }

    #[test]
    fn builds_ordered_nested_hierarchy_and_closes_sibling_spans() {
        let text = normalized(
            "PEMBUKAAN\nBAB I\nKETENTUAN UMUM\nBagian Kesatu\nPasal 1\n\
             (1) Setiap orang wajib patuh.\na. memenuhi syarat;\n1. menunjukkan bukti;\n\
             (2) Kewajiban dikecualikan.\nPasal 2\nKetentuan berikutnya.\n",
        );

        let tree = parse_structure(&text, &identity(), &StructureParserConfig::default())
            .expect("hierarchy parses");
        let labels: Vec<_> = tree.nodes.iter().map(|node| node.label.as_str()).collect();
        assert_eq!(
            labels,
            [
                "document",
                "BAB I",
                "Bagian Kesatu",
                "Pasal 1",
                "(1)",
                "a.",
                "1.",
                "(2)",
                "Pasal 2"
            ]
        );
        let article_one = &tree.nodes[3];
        let clause_one = &tree.nodes[4];
        let letter_item = &tree.nodes[5];
        let numeric_item = &tree.nodes[6];
        let clause_two = &tree.nodes[7];
        let article_two = &tree.nodes[8];
        assert_eq!(article_one.parent_id, Some(tree.nodes[2].id.clone()));
        assert_eq!(clause_one.parent_id, Some(article_one.id.clone()));
        assert_eq!(letter_item.parent_id, Some(clause_one.id.clone()));
        assert_eq!(numeric_item.parent_id, Some(letter_item.id.clone()));
        assert_eq!(clause_two.parent_id, Some(article_one.id.clone()));
        assert_eq!(article_two.parent_id, Some(tree.nodes[2].id.clone()));
        assert_eq!(
            article_one.normalized_span.end,
            article_two.normalized_span.start
        );
        tree.validate(&text).expect("generated tree remains valid");
    }

    #[test]
    fn ignores_inline_references_and_orphan_list_markers() {
        let text = normalized(
            "1. metadata halaman\nKetentuan sebagaimana dimaksud dalam Pasal 5 tetap berlaku.\n\
             Pasal 6 ayat (1) bukan heading.\nPasal 7\nIsi sah.\n",
        );
        let tree = parse_structure(&text, &identity(), &StructureParserConfig::default())
            .expect("conservative structure parses");

        assert_eq!(tree.nodes.len(), 2);
        assert_eq!(tree.nodes[1].label, "Pasal 7");
    }

    #[test]
    fn maps_unicode_nodes_back_to_crlf_raw_offsets() {
        let raw = "BAB I\r\nPasal 1\r\n(1) Warga wajib menjaga aksesibilitas ﬂeksibel.\r\n";
        let text = normalized(raw);
        let tree = parse_structure(&text, &identity(), &StructureParserConfig::default())
            .expect("mapped structure parses");

        for node in &tree.nodes {
            assert_eq!(
                node.raw_span,
                text.raw_cover(node.normalized_span.clone())
                    .expect("node is source mapped")
            );
            assert!(raw.is_char_boundary(node.raw_span.start));
            assert!(raw.is_char_boundary(node.raw_span.end));
        }
        assert!(tree.nodes[3].normalized_span.start < tree.nodes[3].normalized_span.end);
    }

    #[test]
    fn stable_ids_depend_on_artifact_and_hierarchy_not_raw_line_endings() {
        let unix = normalized("BAB I\nPasal 1\nIsi.\n");
        let windows = normalized("BAB I\r\nPasal 1\r\nIsi.\r\n");
        let first = parse_structure(&unix, &identity(), &StructureParserConfig::default())
            .expect("first tree");
        let second = parse_structure(&windows, &identity(), &StructureParserConfig::default())
            .expect("second tree");
        assert_eq!(
            first.nodes.iter().map(|node| &node.id).collect::<Vec<_>>(),
            second.nodes.iter().map(|node| &node.id).collect::<Vec<_>>()
        );

        let mut other_identity = identity();
        other_identity.text_artifact_id = "text:other".to_owned();
        let other = parse_structure(&unix, &other_identity, &StructureParserConfig::default())
            .expect("other artifact tree");
        assert_ne!(first.root().id, other.root().id);
    }

    #[test]
    fn recognizes_annex_without_identifier_and_respects_item_switch() {
        let text = normalized("LAMPIRAN\nPasal 1\n(1) Isi.\na. butir.\n");
        let config = StructureParserConfig {
            recognize_list_items: false,
            ..StructureParserConfig::default()
        };
        let tree = parse_structure(&text, &identity(), &config).expect("annex parses");

        assert_eq!(tree.nodes[1].kind, StructureKind::Annex);
        assert!(tree
            .nodes
            .iter()
            .all(|node| node.kind != StructureKind::Item));
    }

    #[test]
    fn rejects_invalid_identity_limits_and_tampered_links() {
        let text = normalized("BAB I\nPasal 1\nIsi.\n");
        let mut invalid_identity = identity();
        invalid_identity.source_blob_id = "contains whitespace".to_owned();
        assert_eq!(
            parse_structure(&text, &invalid_identity, &StructureParserConfig::default()),
            Err(StructureError::InvalidIdentity("source_blob_id"))
        );

        let limited = StructureParserConfig {
            maximum_nodes: 2,
            ..StructureParserConfig::default()
        };
        assert_eq!(
            parse_structure(&text, &identity(), &limited),
            Err(StructureError::NodeLimit { maximum: 2 })
        );

        let mut tree = parse_structure(&text, &identity(), &StructureParserConfig::default())
            .expect("valid tree");
        tree.nodes[0].ordered_children.clear();
        assert_eq!(
            tree.validate(&text),
            Err(StructureError::InvalidTree("parent containment"))
        );
    }

    #[test]
    fn recognizes_page_boundary_headings_and_long_clause_bodies() {
        let raw = format!(
            "Pasal 1\nIsi pertama.\u{000C}Pasal 2\n(1) {}\n(2) pendek.\n",
            "a".repeat(700)
        );
        let text = normalized(&raw);
        let tree = parse_structure(&text, &identity(), &StructureParserConfig::default())
            .expect("page-separated structure parses");
        let labels: Vec<_> = tree.nodes.iter().map(|node| node.label.as_str()).collect();

        assert!(labels.contains(&"Pasal 1"));
        assert!(labels.contains(&"Pasal 2"));
        assert!(labels.contains(&"(1)"));
        assert!(labels.contains(&"(2)"));
    }

    #[test]
    fn keeps_chapters_and_articles_under_annex() {
        let text = normalized("LAMPIRAN\nBAB I\nPasal 1\nIsi lampiran.\n");
        let tree = parse_structure(&text, &identity(), &StructureParserConfig::default())
            .expect("annex hierarchy parses");
        let annex = tree
            .nodes
            .iter()
            .find(|node| node.label == "LAMPIRAN")
            .unwrap();
        let chapter = tree
            .nodes
            .iter()
            .find(|node| node.label == "BAB I")
            .unwrap();
        let article = tree
            .nodes
            .iter()
            .find(|node| node.label == "Pasal 1")
            .unwrap();

        assert_eq!(chapter.parent_id.as_deref(), Some(annex.id.as_str()));
        assert_eq!(article.parent_id.as_deref(), Some(chapter.id.as_str()));
    }

    #[test]
    fn rejects_overlapping_siblings_and_untrusted_normalized_mapping() {
        let text = normalized("Pasal 1\nIsi pertama.\nPasal 2\nIsi kedua.\n");
        let mut tree = parse_structure(&text, &identity(), &StructureParserConfig::default())
            .expect("baseline tree parses");
        let first_end = tree.nodes[1].normalized_span.end;
        tree.nodes[2].normalized_span.start = first_end - 1;
        assert_eq!(
            tree.validate(&text),
            Err(StructureError::InvalidTree("child order"))
        );

        let mut shifted = text.clone();
        for span in &mut shifted.mapping {
            span.raw_start_byte += 100_000;
            span.raw_end_byte += 100_000;
        }
        assert!(matches!(
            parse_structure(&shifted, &identity(), &StructureParserConfig::default()),
            Err(StructureError::SourceMapping(_))
        ));
    }
}
