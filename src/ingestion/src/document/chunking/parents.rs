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
        Ok(index)
    }

    /// Mengembalikan ancestor semantik dari yang paling umum ke parent langsung.
    pub fn ancestors(&self, node_id: &str) -> Result<Vec<String>, ParentError> {
        if !self.parents.contains_key(node_id) {
            return Err(ParentError::UnknownNode(node_id.to_owned()));
        }
        let mut reversed = Vec::new();
        let mut seen = HashSet::new();
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
            if reversed.len() >= self.maximum_depth {
                return Err(ParentError::DepthLimit {
                    node_id: node_id.to_owned(),
                    maximum: self.maximum_depth,
                });
            }
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
