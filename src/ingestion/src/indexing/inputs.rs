//! Renders a verified chunk's primary text with bounded parent labels for X01.
//!
//! The caller must load and authenticate the normalized TextArtifact and prove
//! that the Chunk/StructureNode records belong to the same complete
//! DocumentBatch. This local renderer checks UTF-8 byte boundaries, exact
//! artifact identity, ancestor references, and output bounds before native
//! inference. The rendering policy version and output hash must be pinned in
//! the IndexGeneration/build plan; this helper does not publish an index.
//! Measure added tokens, build throughput/RSS, and Recall@k by policy against
//! configs/benchmark-targets.yaml (REQUIRED_UNMEASURED).

use crate::wire::documents;
use sha2::{Digest, Sha256};
use std::collections::{HashMap, HashSet};

pub const RENDER_POLICY_VERSION: &str = "parent-labels-v1";
const MAX_PARENT_DEPTH: usize = 64;
const MAX_PARENT_LABEL_BYTES: usize = 1024;
const MAX_RENDER_BYTES: usize = 1024 * 1024;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum RenderError {
    InvalidLimit,
    MissingIdentity,
    InvalidSpan,
    MissingParent,
    DuplicateParent,
    InvalidParentLabel,
    InvalidParentChain,
    ForeignStructure,
    TooManyParents,
    TooLarge,
    EmptyText,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct RenderedEmbeddingInput {
    pub chunk_id: String,
    pub text_artifact_id: String,
    pub policy_version: &'static str,
    pub text: String,
    pub sha256: String,
}

/// Builds the structure lookup once per verified DocumentBatch so rendering
/// many chunks is O(structures + total parent references), not a full scan per
/// chunk. This lookup does not itself authenticate the surrounding batch.
pub struct EmbeddingInputRenderer<'a> {
    index: HashMap<&'a str, &'a documents::StructureNode>,
}

impl<'a> EmbeddingInputRenderer<'a> {
    pub fn new(structures: &'a [documents::StructureNode]) -> Result<Self, RenderError> {
        let index: HashMap<_, _> = structures
            .iter()
            .map(|node| (node.meta.record_id.as_str(), node))
            .collect();
        if index.len() != structures.len() {
            return Err(RenderError::DuplicateParent);
        }
        Ok(Self { index })
    }

    /// Renders labels in the chunk's root-to-parent order and the exact primary
    /// byte span. Ancestor full text and exceptions remain evidence for hydration,
    /// not silent additions to embedding input. The caller supplies a verified
    /// normalized string and pins this policy to the generation.
    pub fn render_embedding_input(
        &self,
        chunk: &documents::Chunk,
        text_artifact_id: &str,
        normalized_text: &str,
        maximum_bytes: usize,
    ) -> Result<RenderedEmbeddingInput, RenderError> {
        if maximum_bytes == 0 || maximum_bytes > MAX_RENDER_BYTES {
            return Err(RenderError::InvalidLimit);
        }
        let chunk_id = &chunk.meta.record_id;
        let span = chunk.text_span.as_ref().ok_or(RenderError::InvalidSpan)?;
        if chunk_id.is_empty()
            || text_artifact_id.is_empty()
            || span.text_artifact_id != text_artifact_id
        {
            return Err(RenderError::MissingIdentity);
        }
        let start = usize::try_from(span.start_byte).map_err(|_| RenderError::InvalidSpan)?;
        let end = usize::try_from(span.end_byte).map_err(|_| RenderError::InvalidSpan)?;
        if start >= end {
            return Err(RenderError::InvalidSpan);
        }
        let primary = normalized_text
            .get(start..end)
            .ok_or(RenderError::InvalidSpan)?;
        if primary.len() > maximum_bytes {
            return Err(RenderError::TooLarge);
        }
        if primary.trim().is_empty() {
            return Err(RenderError::EmptyText);
        }
        if chunk.parent_refs.len() > MAX_PARENT_DEPTH {
            return Err(RenderError::TooManyParents);
        }
        if chunk.structure_node_refs.len() != 1 {
            return Err(RenderError::InvalidParentChain);
        }
        let direct = self
            .index
            .get(chunk.structure_node_refs[0].as_str())
            .ok_or(RenderError::MissingParent)?;
        if direct.source_spans.is_empty()
            || direct
                .source_spans
                .iter()
                .any(|source| source.text_artifact_id != text_artifact_id)
            || !direct.source_spans.iter().any(|source| {
                source.start_byte <= span.start_byte && source.end_byte >= span.end_byte
            })
        {
            return Err(RenderError::ForeignStructure);
        }
        let mut expected_reverse = Vec::new();
        let mut current = direct;
        let mut traversed = HashSet::new();
        loop {
            if !traversed.insert(current.meta.record_id.as_str()) {
                return Err(RenderError::InvalidParentChain);
            }
            let Some(parent_id) = current.parent_id.as_deref() else {
                break;
            };
            if traversed.len() > MAX_PARENT_DEPTH + 1 {
                return Err(RenderError::TooManyParents);
            }
            let parent = self
                .index
                .get(parent_id)
                .ok_or(RenderError::MissingParent)?;
            if parent.source_spans.is_empty()
                || parent
                    .source_spans
                    .iter()
                    .any(|source| source.text_artifact_id != text_artifact_id)
            {
                return Err(RenderError::ForeignStructure);
            }
            if parent.kind.enum_value() != Ok(documents::StructureKind::STRUCTURE_KIND_DOCUMENT) {
                expected_reverse.push(parent_id);
            }
            current = parent;
        }
        expected_reverse.reverse();
        if chunk
            .parent_refs
            .iter()
            .map(String::as_str)
            .collect::<Vec<_>>()
            != expected_reverse
        {
            return Err(RenderError::InvalidParentChain);
        }
        let mut seen = HashSet::with_capacity(chunk.parent_refs.len());
        let mut labels = Vec::with_capacity(chunk.parent_refs.len());
        let mut label_bytes = 0usize;
        for id in &chunk.parent_refs {
            if !seen.insert(id.as_str()) {
                return Err(RenderError::DuplicateParent);
            }
            let node = self
                .index
                .get(id.as_str())
                .ok_or(RenderError::MissingParent)?;
            let label = node.label.trim();
            if label.is_empty() || label.chars().any(char::is_control) {
                return Err(RenderError::InvalidParentLabel);
            }
            label_bytes = label_bytes
                .checked_add(label.len())
                .ok_or(RenderError::TooLarge)?;
            if label_bytes > MAX_PARENT_LABEL_BYTES {
                return Err(RenderError::TooLarge);
            }
            labels.push(label);
        }
        let rendered_len = if labels.is_empty() {
            primary.len()
        } else {
            9usize // "Konteks: "
                .checked_add(label_bytes)
                .and_then(|len| len.checked_add(3 * (labels.len() - 1))) // " > "
                .and_then(|len| len.checked_add(7)) // "\nTeks: "
                .and_then(|len| len.checked_add(primary.len()))
                .ok_or(RenderError::TooLarge)?
        };
        if rendered_len > maximum_bytes {
            return Err(RenderError::TooLarge);
        }
        let text = if labels.is_empty() {
            primary.to_owned()
        } else {
            format!("Konteks: {}\nTeks: {}", labels.join(" > "), primary)
        };
        if text.len() > maximum_bytes {
            return Err(RenderError::TooLarge);
        }
        let sha256 = format!("{:x}", Sha256::digest(text.as_bytes()));
        Ok(RenderedEmbeddingInput {
            chunk_id: chunk_id.clone(),
            text_artifact_id: text_artifact_id.to_owned(),
            policy_version: RENDER_POLICY_VERSION,
            text,
            sha256,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::wire::common::{RecordMeta, TextSpan};
    use protobuf::{EnumOrUnknown, MessageField};

    fn fixture() -> (documents::Chunk, Vec<documents::StructureNode>, String) {
        let text = "BAB I\nPasal 1\n(1) Syarat berlaku, kecuali darurat.".to_owned();
        let start = text.find("(1)").unwrap();
        let chunk = documents::Chunk {
            meta: MessageField::some(RecordMeta {
                record_id: "chunk:1".into(),
                ..Default::default()
            }),
            text_span: MessageField::some(TextSpan {
                text_artifact_id: "text:1".into(),
                start_byte: start as u64,
                end_byte: text.len() as u64,
                ..Default::default()
            }),
            parent_refs: vec!["node:bab".into(), "node:pasal".into()],
            structure_node_refs: vec!["node:ayat".into()],
            ..Default::default()
        };
        let nodes = [
            (
                "node:bab",
                "BAB I",
                Some("node:root"),
                documents::StructureKind::STRUCTURE_KIND_CHAPTER,
            ),
            (
                "node:pasal",
                "Pasal 1",
                Some("node:bab"),
                documents::StructureKind::STRUCTURE_KIND_ARTICLE,
            ),
            (
                "node:ayat",
                "(1)",
                Some("node:pasal"),
                documents::StructureKind::STRUCTURE_KIND_PARAGRAPH,
            ),
            (
                "node:root",
                "Dokumen",
                None,
                documents::StructureKind::STRUCTURE_KIND_DOCUMENT,
            ),
        ]
        .into_iter()
        .map(|(id, label, parent, kind)| documents::StructureNode {
            meta: MessageField::some(RecordMeta {
                record_id: id.into(),
                ..Default::default()
            }),
            label: label.into(),
            parent_id: parent.map(str::to_owned),
            kind: EnumOrUnknown::new(kind),
            source_spans: vec![TextSpan {
                text_artifact_id: "text:1".into(),
                start_byte: 0,
                end_byte: text.len() as u64,
                ..Default::default()
            }],
            ..Default::default()
        })
        .collect();
        (chunk, nodes, text)
    }

    #[test]
    fn preserves_parent_order_and_exact_primary_evidence() {
        let (chunk, nodes, text) = fixture();
        let renderer = EmbeddingInputRenderer::new(&nodes).unwrap();
        let result = renderer
            .render_embedding_input(&chunk, "text:1", &text, 1024)
            .unwrap();
        assert_eq!(
            result.text,
            "Konteks: BAB I > Pasal 1\nTeks: (1) Syarat berlaku, kecuali darurat."
        );
        assert_eq!(result.sha256.len(), 64);
        assert_eq!(result.policy_version, RENDER_POLICY_VERSION);
        assert_eq!(result.chunk_id, "chunk:1");
    }

    #[test]
    fn refuses_utf8_mismatch_missing_parent_and_truncated_output() {
        let (mut chunk, mut nodes, text) = fixture();
        let renderer = EmbeddingInputRenderer::new(&nodes).unwrap();
        assert_eq!(
            renderer.render_embedding_input(&chunk, "wrong", &text, 1024),
            Err(RenderError::MissingIdentity)
        );
        chunk.parent_refs.push("node:missing".into());
        assert_eq!(
            renderer.render_embedding_input(&chunk, "text:1", &text, 1024),
            Err(RenderError::InvalidParentChain)
        );
        chunk.parent_refs.pop();
        assert_eq!(
            renderer.render_embedding_input(&chunk, "text:1", &text, 10),
            Err(RenderError::TooLarge)
        );
        nodes[0].label = "BAB\nI".into();
        let renderer = EmbeddingInputRenderer::new(&nodes).unwrap();
        assert_eq!(
            renderer.render_embedding_input(&chunk, "text:1", &text, 1024),
            Err(RenderError::InvalidParentLabel)
        );
        chunk.parent_refs.clear();
        let utf8 = "é(1)";
        chunk.text_span.as_mut().unwrap().start_byte = 1;
        chunk.text_span.as_mut().unwrap().end_byte = utf8.len() as u64;
        assert_eq!(
            renderer.render_embedding_input(&chunk, "text:1", utf8, 1024),
            Err(RenderError::InvalidSpan)
        );
    }

    #[test]
    fn rejects_cross_document_and_reordered_ancestor_context() {
        let (mut chunk, mut nodes, text) = fixture();
        chunk.parent_refs.swap(0, 1);
        let renderer = EmbeddingInputRenderer::new(&nodes).unwrap();
        assert_eq!(
            renderer.render_embedding_input(&chunk, "text:1", &text, 1024),
            Err(RenderError::InvalidParentChain)
        );
        chunk.parent_refs.swap(0, 1);
        nodes[2].source_spans.push(TextSpan {
            text_artifact_id: "text:foreign".into(),
            start_byte: 0,
            end_byte: text.len() as u64,
            ..Default::default()
        });
        let renderer = EmbeddingInputRenderer::new(&nodes).unwrap();
        assert_eq!(
            renderer.render_embedding_input(&chunk, "text:1", &text, 1024),
            Err(RenderError::ForeignStructure)
        );
        nodes[2].source_spans.pop();
        nodes.push(nodes[0].clone());
        assert!(matches!(
            EmbeddingInputRenderer::new(&nodes),
            Err(RenderError::DuplicateParent)
        ));
    }

    #[test]
    fn rejects_large_primary_span_before_rendering() {
        let (mut chunk, mut nodes, _) = fixture();
        let large = "x".repeat(2 * 1024 * 1024);
        chunk.text_span.as_mut().unwrap().start_byte = 0;
        chunk.text_span.as_mut().unwrap().end_byte = large.len() as u64;
        nodes[2].source_spans[0].end_byte = large.len() as u64;
        let renderer = EmbeddingInputRenderer::new(&nodes).unwrap();
        assert_eq!(
            renderer.render_embedding_input(&chunk, "text:1", &large, 1024),
            Err(RenderError::TooLarge)
        );
    }
}
