//! Memvalidasi parent graph dan memasang referensi konteks induk pada chunk.
//!
//! Peran dalam komponen:
//! Modul ini membangun indeks parent sekali per artefak, menolak node hilang atau siklik, lalu
//! menghasilkan urutan ancestor root-ke-parent langsung. Chunk menyimpan ID konteks saja; teks
//! ancestor diambil saat query oleh context builder agar ingestion tidak menggandakan isi dokumen.
//!
//! Kontrak integrasi dan perhatian implementasi:
//! Input berasal dari `StructureTree`; parent `Document` tidak dimasukkan ke `parent_refs` karena
//! tidak membawa konteks hukum khusus. Semua reference dipertahankan unik dan deterministik.
//! Batas kedalaman mencegah input rusak menghabiskan CPU atau memori.
//!
//! Benchmark dan gate penerimaan:
//! Ukur biaya pembangunan indeks, hidrasi parent, rasio duplikasi konteks, dan
//! `CONTEXT.BUILD_P95`; invariant node hilang/siklus wajib nol. Target configs/benchmark-targets.yaml
//! tetap REQUIRED_UNMEASURED sampai workload resmi dijalankan.
//!
//! Status: validasi parent graph dan pemasangan parent refs aktif; fetch teks di query belum aktif.

use crate::document::chunking::structural::{StructureKind, StructureNode};
use crate::domain::chunks::ChunkView;
use std::collections::{HashMap, HashSet};
use std::error::Error;
use std::fmt::{Display, Formatter};

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ParentIndexConfig {
    pub maximum_depth: usize,
}

impl Default for ParentIndexConfig {
    fn default() -> Self {
        Self { maximum_depth: 64 }
    }
}

#[derive(Clone, Debug)]
pub struct ParentIndex {
    parents: HashMap<String, Option<String>>,
    kinds: HashMap<String, StructureKind>,
    maximum_depth: usize,
}

impl ParentIndex {
    pub fn build(nodes: &[StructureNode], config: &ParentIndexConfig) -> Result<Self, ParentError> {
        if config.maximum_depth == 0 {
            return Err(ParentError::InvalidConfig("maximum_depth"));
        }
        let mut parents = HashMap::with_capacity(nodes.len());
        let mut kinds = HashMap::with_capacity(nodes.len());
        for node in nodes {
            if parents
                .insert(node.id.clone(), node.parent_id.clone())
                .is_some()
            {
                return Err(ParentError::DuplicateNode(node.id.clone()));
            }
            kinds.insert(node.id.clone(), node.kind);
        }
        for (node_id, parent_id) in &parents {
            if let Some(parent_id) = parent_id {
                if parent_id == node_id {
                    return Err(ParentError::Cycle(node_id.clone()));
                }
                if !parents.contains_key(parent_id) {
                    return Err(ParentError::MissingParent {
                        node_id: node_id.clone(),
                        parent_id: parent_id.clone(),
                    });
                }
            }
        }
        let index = Self {
            parents,
            kinds,
            maximum_depth: config.maximum_depth,
        };
        for node_id in index.parents.keys() {
            index.ancestors(node_id)?;
        }
        let mut document_roots = 0usize;
        for (node_id, parent_id) in &index.parents {
            let kind = index
                .kinds
                .get(node_id)
                .expect("kind is inserted with every parent record");
            match (kind, parent_id) {
                (StructureKind::Document, None) => document_roots += 1,
                (StructureKind::Document, Some(_)) | (_, None) => {
                    return Err(ParentError::InvalidRoot(node_id.clone()));
                }
                _ => {}
            }
        }
        if document_roots != 1 {
            return Err(ParentError::InvalidRoot(
                "expected exactly one document root".to_owned(),
            ));
        }
        Ok(index)
    }

    /// Mengembalikan ancestor semantik dari yang paling umum ke parent langsung.
    pub fn ancestors(&self, node_id: &str) -> Result<Vec<String>, ParentError> {
        if !self.parents.contains_key(node_id) {
            return Err(ParentError::UnknownNode(node_id.to_owned()));
        }
        let mut reversed = Vec::new();
        let mut seen = HashSet::new();
        let mut traversed_edges = 0usize;
        let mut current = node_id;
        loop {
            if !seen.insert(current) {
                return Err(ParentError::Cycle(current.to_owned()));
            }
            let parent = self
                .parents
                .get(current)
                .ok_or_else(|| ParentError::UnknownNode(current.to_owned()))?;
            let Some(parent_id) = parent.as_deref() else {
                break;
            };
            if traversed_edges >= self.maximum_depth {
                return Err(ParentError::DepthLimit {
                    node_id: node_id.to_owned(),
                    maximum: self.maximum_depth,
                });
            }
            traversed_edges += 1;
            if self.kinds.get(parent_id) != Some(&StructureKind::Document) {
                reversed.push(parent_id.to_owned());
            }
            current = parent_id;
        }
        reversed.reverse();
        Ok(reversed)
    }

    pub fn attach_to_chunk(&self, chunk: &mut ChunkView) -> Result<(), ParentError> {
        let mut ordered = Vec::new();
        let mut seen = HashSet::new();
        for structure_id in &chunk.structure_node_refs {
            for parent_id in self.ancestors(structure_id)? {
                if seen.insert(parent_id.clone()) {
                    ordered.push(parent_id);
                }
            }
        }
        chunk.parent_refs = ordered;
        Ok(())
    }

    pub fn attach_to_chunks(&self, chunks: &mut [ChunkView]) -> Result<(), ParentError> {
        for chunk in chunks {
            self.attach_to_chunk(chunk)?;
        }
        Ok(())
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum ParentError {
    InvalidConfig(&'static str),
    InvalidRoot(String),
    DuplicateNode(String),
    MissingParent { node_id: String, parent_id: String },
    UnknownNode(String),
    Cycle(String),
    DepthLimit { node_id: String, maximum: usize },
}

impl Display for ParentError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::InvalidConfig(field) => write!(formatter, "invalid parent config: {field}"),
            Self::InvalidRoot(node_id) => write!(formatter, "invalid document root: {node_id}"),
            Self::DuplicateNode(node_id) => {
                write!(formatter, "duplicate structure node: {node_id}")
            }
            Self::MissingParent { node_id, parent_id } => {
                write!(formatter, "node {node_id} has missing parent {parent_id}")
            }
            Self::UnknownNode(node_id) => write!(formatter, "unknown structure node: {node_id}"),
            Self::Cycle(node_id) => write!(formatter, "parent cycle at structure node: {node_id}"),
            Self::DepthLimit { node_id, maximum } => {
                write!(formatter, "parent depth for {node_id} exceeds {maximum}")
            }
        }
    }
}

impl Error for ParentError {}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::domain::chunks::{ChunkView, SourceMappedSpan, CHUNK_SCHEMA_VERSION};

    fn node(id: &str, kind: StructureKind, parent: Option<&str>) -> StructureNode {
        StructureNode {
            schema_version: 1,
            id: id.to_owned(),
            kind,
            label: id.to_owned(),
            parent_id: parent.map(str::to_owned),
            ordered_children: Vec::new(),
            normalized_span: 0..1,
            raw_span: 0..1,
        }
    }

    fn hierarchy() -> Vec<StructureNode> {
        vec![
            node("root", StructureKind::Document, None),
            node("chapter", StructureKind::Chapter, Some("root")),
            node("article", StructureKind::Article, Some("chapter")),
            node("clause", StructureKind::Paragraph, Some("article")),
            node("item-a", StructureKind::Item, Some("clause")),
            node("item-b", StructureKind::Item, Some("clause")),
        ]
    }

    fn chunk(structure_refs: &[&str]) -> ChunkView {
        ChunkView {
            schema_version: CHUNK_SCHEMA_VERSION,
            id: "chunk:1".to_owned(),
            source_blob_id: "source:1".to_owned(),
            text_artifact_id: "text:1".to_owned(),
            provision_version_refs: vec!["version:1".to_owned()],
            structure_node_refs: structure_refs
                .iter()
                .map(|value| (*value).to_owned())
                .collect(),
            parent_refs: vec!["stale-parent".to_owned()],
            exception_refs: Vec::new(),
            text_span: SourceMappedSpan {
                normalized: 0..1,
                raw: 0..1,
            },
            text_sha256: "0".repeat(64),
            chunker_config_sha256: "1".repeat(64),
            token_counts: Vec::new(),
        }
    }

    #[test]
    fn returns_root_to_direct_parent_without_document_node() {
        let index = ParentIndex::build(&hierarchy(), &ParentIndexConfig::default())
            .expect("valid parent index");

        assert_eq!(
            index.ancestors("item-a").expect("known item"),
            ["chapter", "article", "clause"]
        );
        assert!(index
            .ancestors("chapter")
            .expect("known chapter")
            .is_empty());
    }

    #[test]
    fn attaches_deduplicated_parent_references_in_stable_order() {
        let index = ParentIndex::build(&hierarchy(), &ParentIndexConfig::default())
            .expect("valid parent index");
        let mut chunk = chunk(&["item-a", "item-b"]);

        index.attach_to_chunk(&mut chunk).expect("parents attach");

        assert_eq!(chunk.parent_refs, ["chapter", "article", "clause"]);
    }

    #[test]
    fn rejects_missing_duplicate_cyclic_and_too_deep_graphs() {
        let missing = vec![node("orphan", StructureKind::Article, Some("absent"))];
        assert_eq!(
            ParentIndex::build(&missing, &ParentIndexConfig::default()).unwrap_err(),
            ParentError::MissingParent {
                node_id: "orphan".to_owned(),
                parent_id: "absent".to_owned()
            }
        );

        let duplicate = vec![
            node("same", StructureKind::Document, None),
            node("same", StructureKind::Article, None),
        ];
        assert_eq!(
            ParentIndex::build(&duplicate, &ParentIndexConfig::default()).unwrap_err(),
            ParentError::DuplicateNode("same".to_owned())
        );

        let cycle = vec![
            node("a", StructureKind::Article, Some("b")),
            node("b", StructureKind::Paragraph, Some("a")),
        ];
        assert!(matches!(
            ParentIndex::build(&cycle, &ParentIndexConfig::default()),
            Err(ParentError::Cycle(_))
        ));

        let limited = ParentIndexConfig { maximum_depth: 2 };
        assert!(matches!(
            ParentIndex::build(&hierarchy(), &limited),
            Err(ParentError::DepthLimit { .. })
        ));

        ParentIndex::build(&hierarchy(), &ParentIndexConfig { maximum_depth: 4 })
            .expect("four parent edges including document root are allowed");

        let document_child = vec![
            node("root", StructureKind::Document, None),
            node("nested-root", StructureKind::Document, Some("root")),
        ];
        assert_eq!(
            ParentIndex::build(&document_child, &ParentIndexConfig::default()).unwrap_err(),
            ParentError::InvalidRoot("nested-root".to_owned())
        );

        let mut document_chain = vec![node("root", StructureKind::Document, None)];
        for index in 1..10 {
            document_chain.push(node(
                &format!("document-{index}"),
                StructureKind::Document,
                Some(if index == 1 {
                    "root".to_owned()
                } else {
                    format!("document-{}", index - 1)
                })
                .as_deref(),
            ));
        }
        assert!(matches!(
            ParentIndex::build(&document_chain, &ParentIndexConfig { maximum_depth: 2 }),
            Err(ParentError::DepthLimit { .. })
        ));
    }

    #[test]
    fn rejects_unknown_chunk_structure_reference() {
        let index = ParentIndex::build(&hierarchy(), &ParentIndexConfig::default())
            .expect("valid parent index");
        let mut chunk = chunk(&["missing"]);

        assert_eq!(
            index.attach_to_chunk(&mut chunk),
            Err(ParentError::UnknownNode("missing".to_owned()))
        );
    }
}
