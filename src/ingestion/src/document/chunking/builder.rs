//! Membentuk chunk retrieval dari pohon struktur dengan source mapping dan token count eksplisit.
//!
//! Peran dalam komponen:
//! Builder membagi rentang teks yang dimiliki setiap node struktur, menjaga bagian preamble yang
//! tidak dimiliki anak, memecah unit panjang pada batas UTF-8/kata/kalimat, dan menghasilkan
//! `ChunkView` siap dikonversi ke kontrak wire. Parent context dipasang sebagai referensi ID.
//!
//! Kontrak integrasi dan perhatian implementasi:
//! Tokenizer disuntikkan dan harus sudah dimuat; builder tidak memuat model atau membuka koneksi.
//! Ukuran chunk dinyatakan dalam byte karena source mapping memakai byte UTF-8, sementara jumlah
//! token dicatat dari tokenizer produksi. Overlap tidak boleh menghilangkan progres atau melebihi
//! ukuran chunk. Seluruh output wajib lolos validator domain sebelum dikembalikan.
//!
//! Benchmark dan gate penerimaan:
//! Ukur `CHUNKING.THROUGHPUT`, `RUST.RSS_PEAK`, source/parent coverage, distribusi token, retention
//! pengecualian, dan dampak Recall@20 pada configs/benchmark-targets.yaml. Default config adalah
//! baseline implementasi, bukan hasil tuning. Semua target tetap REQUIRED_UNMEASURED.
//!
//! Status: structural chunk builder aktif; tokenizer model, wire batch, dan acceptance corpus belum aktif.

use crate::document::chunking::parents::{ParentError, ParentIndex, ParentIndexConfig};
use crate::document::chunking::structural::{StructureError, StructureNode, StructureTree};
use crate::document::normalization::text::{NormalizeError, NormalizedText};
use crate::domain::chunks::{
    text_sha256, ChunkValidationError, ChunkView, SourceMappedSpan, TokenCount,
    CHUNK_SCHEMA_VERSION,
};
use sha2::{Digest, Sha256};
use std::collections::HashSet;
use std::error::Error;
use std::fmt::{Display, Formatter};
use std::ops::Range;

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ChunkerConfig {
    pub maximum_chunk_bytes: usize,
    pub minimum_split_bytes: usize,
    pub overlap_bytes: usize,
    pub maximum_chunks: usize,
    pub maximum_parent_depth: usize,
}

impl Default for ChunkerConfig {
    fn default() -> Self {
        Self {
            maximum_chunk_bytes: 4_096,
            minimum_split_bytes: 1_024,
            overlap_bytes: 256,
            maximum_chunks: 2_000_000,
            maximum_parent_depth: 64,
        }
    }
}

pub trait TokenCounter {
    fn tokenizer_id(&self) -> &str;
    fn count_tokens(&self, text: &str) -> Result<u32, String>;
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ChunkBatch {
    pub schema_version: u32,
    pub chunker_config_sha256: String,
    pub normalized_sha256: String,
    pub chunks: Vec<ChunkView>,
}

#[derive(Debug, PartialEq, Eq)]
pub enum ChunkBuildError {
    InvalidConfig(&'static str),
    InvalidTokenizerId,
    Structure(StructureError),
    Parent(ParentError),
    SourceMapping(NormalizeError),
    Validation(ChunkValidationError),
    Tokenization(String),
    EmptyTokenCount,
    ChunkLimit { maximum: usize },
    InvalidStructureSpan,
}

impl Display for ChunkBuildError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::InvalidConfig(field) => write!(formatter, "invalid chunker config: {field}"),
            Self::InvalidTokenizerId => write!(formatter, "invalid tokenizer ID"),
            Self::Structure(error) => write!(formatter, "structure validation failed: {error}"),
            Self::Parent(error) => write!(formatter, "parent indexing failed: {error}"),
            Self::SourceMapping(error) => write!(formatter, "chunk source mapping failed: {error}"),
            Self::Validation(error) => write!(formatter, "chunk validation failed: {error}"),
            Self::Tokenization(error) => write!(formatter, "tokenization failed: {error}"),
            Self::EmptyTokenCount => write!(formatter, "tokenizer returned zero tokens"),
            Self::ChunkLimit { maximum } => write!(formatter, "chunk count exceeds {maximum}"),
            Self::InvalidStructureSpan => {
                write!(formatter, "invalid or overlapping structure span")
            }
        }
    }
}

impl Error for ChunkBuildError {}

pub fn build_chunks<T: TokenCounter>(
    normalized: &NormalizedText,
    tree: &StructureTree,
    config: &ChunkerConfig,
    tokenizer: &T,
) -> Result<ChunkBatch, ChunkBuildError> {
    validate_config(config)?;
    if !valid_ascii_id(tokenizer.tokenizer_id()) {
        return Err(ChunkBuildError::InvalidTokenizerId);
    }
    tree.validate(normalized)
        .map_err(ChunkBuildError::Structure)?;
    let config_sha256 = config_fingerprint(config, tokenizer.tokenizer_id());
    let parent_index = ParentIndex::build(
        &tree.nodes,
        &ParentIndexConfig {
            maximum_depth: config.maximum_parent_depth,
        },
    )
    .map_err(ChunkBuildError::Parent)?;
    let known_structure_ids: HashSet<&str> =
        tree.nodes.iter().map(|node| node.id.as_str()).collect();
    let mut chunks = Vec::new();

    for node in &tree.nodes {
        for owned_span in owned_text_spans(node, tree, &normalized.text)? {
            for split in split_span(&normalized.text, owned_span, config) {
                if chunks.len() >= config.maximum_chunks {
                    return Err(ChunkBuildError::ChunkLimit {
                        maximum: config.maximum_chunks,
                    });
                }
                let text = &normalized.text[split.clone()];
                let tokens = tokenizer
                    .count_tokens(text)
                    .map_err(ChunkBuildError::Tokenization)?;
                if tokens == 0 {
                    return Err(ChunkBuildError::EmptyTokenCount);
                }
                let raw = normalized
                    .raw_cover(split.clone())
                    .map_err(ChunkBuildError::SourceMapping)?;
                let id = stable_chunk_id(
                    &tree.text_artifact_id,
                    &tree.provision_version_id,
                    &node.id,
                    &config_sha256,
                    &split,
                );
                let mut chunk = ChunkView {
                    schema_version: CHUNK_SCHEMA_VERSION,
                    id,
                    source_blob_id: tree.source_blob_id.clone(),
                    text_artifact_id: tree.text_artifact_id.clone(),
                    provision_version_refs: vec![tree.provision_version_id.clone()],
                    structure_node_refs: vec![node.id.clone()],
                    parent_refs: Vec::new(),
                    exception_refs: Vec::new(),
                    text_span: SourceMappedSpan {
                        normalized: split,
                        raw,
                    },
                    text_sha256: text_sha256(text),
                    chunker_config_sha256: config_sha256.clone(),
                    token_counts: vec![TokenCount {
                        tokenizer_id: tokenizer.tokenizer_id().to_owned(),
                        tokens,
                    }],
                };
                parent_index
                    .attach_to_chunk(&mut chunk)
                    .map_err(ChunkBuildError::Parent)?;
                chunk
                    .validate(normalized, &known_structure_ids)
                    .map_err(ChunkBuildError::Validation)?;
                chunks.push(chunk);
            }
        }
    }

    Ok(ChunkBatch {
        schema_version: CHUNK_SCHEMA_VERSION,
        chunker_config_sha256: config_sha256,
        normalized_sha256: normalized.normalized_sha256.clone(),
        chunks,
    })
}

fn owned_text_spans(
    node: &StructureNode,
    tree: &StructureTree,
    text: &str,
) -> Result<Vec<Range<usize>>, ChunkBuildError> {
    let mut children: Vec<&StructureNode> = node
        .ordered_children
        .iter()
        .map(|child_id| {
            tree.node(child_id)
                .ok_or(ChunkBuildError::InvalidStructureSpan)
        })
        .collect::<Result<_, _>>()?;
    children.sort_by_key(|child| child.normalized_span.start);
    let mut spans = Vec::with_capacity(children.len() + 1);
    let mut cursor = node.normalized_span.start;
    for child in children {
        if child.normalized_span.start < cursor
            || child.normalized_span.end > node.normalized_span.end
        {
            return Err(ChunkBuildError::InvalidStructureSpan);
        }
        push_trimmed_span(text, cursor..child.normalized_span.start, &mut spans);
        cursor = child.normalized_span.end;
    }
    push_trimmed_span(text, cursor..node.normalized_span.end, &mut spans);
    Ok(spans)
}

fn push_trimmed_span(text: &str, span: Range<usize>, output: &mut Vec<Range<usize>>) {
    if span.is_empty() {
        return;
    }
    let value = &text[span.clone()];
    let start_trim = value.len() - value.trim_start().len();
    let end_trim = value.trim_end().len();
    let trimmed = (span.start + start_trim)..(span.start + end_trim);
    if !trimmed.is_empty() {
        output.push(trimmed);
    }
}

fn split_span(text: &str, span: Range<usize>, config: &ChunkerConfig) -> Vec<Range<usize>> {
    let mut output = Vec::new();
    let mut start = span.start;
    while span.end - start > config.maximum_chunk_bytes {
        let hard_end = previous_char_boundary(text, start + config.maximum_chunk_bytes);
        let minimum_end = next_char_boundary(text, start + config.minimum_split_bytes);
        let end = preferred_break(text, minimum_end, hard_end).unwrap_or(hard_end);
        output.push(start..end);
        let overlap_target = end.saturating_sub(config.overlap_bytes).max(start + 1);
        let next = next_word_boundary(text, overlap_target, end);
        start = if next >= end { end } else { next };
    }
    if start < span.end {
        output.push(start..span.end);
    }
    output
}

fn preferred_break(text: &str, minimum: usize, maximum: usize) -> Option<usize> {
    let slice = &text[minimum..maximum];
    for (offset, character) in slice.char_indices().rev() {
        if matches!(character, '\n' | '.' | ';' | '?' | '!') {
            return Some(minimum + offset + character.len_utf8());
        }
    }
    for (offset, character) in slice.char_indices().rev() {
        if character.is_whitespace() {
            return Some(minimum + offset);
        }
    }
    None
}

fn next_word_boundary(text: &str, target: usize, maximum: usize) -> usize {
    let target = next_char_boundary(text, target);
    if target >= maximum {
        return maximum;
    }
    for (offset, character) in text[target..maximum].char_indices() {
        if character.is_whitespace() {
            let after = target + offset + character.len_utf8();
            return next_non_whitespace(text, after, maximum);
        }
    }
    maximum
}

fn next_non_whitespace(text: &str, start: usize, maximum: usize) -> usize {
    for (offset, character) in text[start..maximum].char_indices() {
        if !character.is_whitespace() {
            return start + offset;
        }
    }
    maximum
}

fn previous_char_boundary(text: &str, mut index: usize) -> usize {
    index = index.min(text.len());
    while !text.is_char_boundary(index) {
        index -= 1;
    }
    index
}

fn next_char_boundary(text: &str, mut index: usize) -> usize {
    index = index.min(text.len());
    while !text.is_char_boundary(index) {
        index += 1;
    }
    index
}

fn validate_config(config: &ChunkerConfig) -> Result<(), ChunkBuildError> {
    if config.maximum_chunk_bytes < 64 {
        return Err(ChunkBuildError::InvalidConfig("maximum_chunk_bytes"));
    }
    if config.minimum_split_bytes == 0 || config.minimum_split_bytes > config.maximum_chunk_bytes {
        return Err(ChunkBuildError::InvalidConfig("minimum_split_bytes"));
    }
    if config.overlap_bytes >= config.maximum_chunk_bytes {
        return Err(ChunkBuildError::InvalidConfig("overlap_bytes"));
    }
    if config.maximum_chunks == 0 {
        return Err(ChunkBuildError::InvalidConfig("maximum_chunks"));
    }
    if config.maximum_parent_depth == 0 {
        return Err(ChunkBuildError::InvalidConfig("maximum_parent_depth"));
    }
    Ok(())
}

fn config_fingerprint(config: &ChunkerConfig, tokenizer_id: &str) -> String {
    let canonical = format!(
        "chunker-v1\nmaximum_chunk_bytes={}\nminimum_split_bytes={}\noverlap_bytes={}\nmaximum_chunks={}\nmaximum_parent_depth={}\ntokenizer_id={tokenizer_id}\n",
        config.maximum_chunk_bytes,
        config.minimum_split_bytes,
        config.overlap_bytes,
        config.maximum_chunks,
        config.maximum_parent_depth,
    );
    format!("{:x}", Sha256::digest(canonical.as_bytes()))
}

fn stable_chunk_id(
    text_artifact_id: &str,
    provision_version_id: &str,
    structure_node_id: &str,
    config_sha256: &str,
    span: &Range<usize>,
) -> String {
    let mut hasher = Sha256::new();
    for part in [
        "chunk-v1",
        text_artifact_id,
        provision_version_id,
        structure_node_id,
        config_sha256,
        &span.start.to_string(),
        &span.end.to_string(),
    ] {
        hasher.update((part.len() as u64).to_be_bytes());
        hasher.update(part.as_bytes());
    }
    format!("chunk:{:x}", hasher.finalize())
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
    use crate::document::chunking::structural::{
        parse_structure, StructureIdentity, StructureParserConfig,
    };
    use crate::document::normalization::text::{normalize_text, TextNormalizerConfig};

    struct Words;

    impl TokenCounter for Words {
        fn tokenizer_id(&self) -> &str {
            "test:words-v1"
        }

        fn count_tokens(&self, text: &str) -> Result<u32, String> {
            u32::try_from(text.split_whitespace().count()).map_err(|error| error.to_string())
        }
    }

    struct BrokenTokenizer {
        mode: &'static str,
    }

    impl TokenCounter for BrokenTokenizer {
        fn tokenizer_id(&self) -> &str {
            if self.mode == "id" {
                "invalid tokenizer"
            } else {
                "test:broken"
            }
        }

        fn count_tokens(&self, _text: &str) -> Result<u32, String> {
            match self.mode {
                "error" => Err("fixture failure".to_owned()),
                _ => Ok(0),
            }
        }
    }

    fn fixture(raw: &str) -> (NormalizedText, StructureTree) {
        let normalized =
            normalize_text(raw, &TextNormalizerConfig::default()).expect("fixture normalizes");
        let tree = parse_structure(
            &normalized,
            &StructureIdentity {
                source_blob_id: "source:fixture".to_owned(),
                text_artifact_id: "text:fixture".to_owned(),
                provision_version_id: "version:fixture".to_owned(),
            },
            &StructureParserConfig::default(),
        )
        .expect("fixture structure parses");
        (normalized, tree)
    }

    fn small_config() -> ChunkerConfig {
        ChunkerConfig {
            maximum_chunk_bytes: 96,
            minimum_split_bytes: 40,
            overlap_bytes: 16,
            maximum_chunks: 100,
            maximum_parent_depth: 16,
        }
    }

    #[test]
    fn chunks_owned_structure_text_with_parent_and_source_references() {
        let (normalized, tree) = fixture(
            "PEMBUKAAN\r\nBAB I\r\nKETENTUAN UMUM\r\nPasal 1\r\n\
             (1) Setiap warga memenuhi syarat utama.\r\na. membawa identitas sah;\r\n\
             (2) Kewajiban berlaku, kecuali keadaan darurat yang dapat dibuktikan.\r\n",
        );
        let batch = build_chunks(&normalized, &tree, &small_config(), &Words)
            .expect("chunks build successfully");

        assert!(!batch.chunks.is_empty());
        assert!(batch.chunks.iter().all(|chunk| {
            chunk.text_span.normalized.end - chunk.text_span.normalized.start <= 96
                && chunk.text_span.raw.end <= "PEMBUKAAN\r\nBAB I\r\nKETENTUAN UMUM\r\nPasal 1\r\n(1) Setiap warga memenuhi syarat utama.\r\na. membawa identitas sah;\r\n(2) Kewajiban berlaku, kecuali keadaan darurat yang dapat dibuktikan.\r\n".len()
                && chunk.provision_version_refs == ["version:fixture"]
                && chunk.token_counts[0].tokens > 0
        }));
        let rendered: Vec<_> = batch
            .chunks
            .iter()
            .map(|chunk| &normalized.text[chunk.text_span.normalized.clone()])
            .collect();
        assert!(rendered.iter().any(|text| text.contains("PEMBUKAAN")));
        assert!(rendered
            .iter()
            .any(|text| text.contains("kecuali keadaan darurat")));

        let item = batch
            .chunks
            .iter()
            .find(|chunk| {
                normalized.text[chunk.text_span.normalized.clone()].contains("identitas sah")
            })
            .expect("item chunk exists");
        let parent_labels: Vec<_> = item
            .parent_refs
            .iter()
            .map(|id| tree.node(id).expect("parent exists").label.as_str())
            .collect();
        assert_eq!(parent_labels, ["BAB I", "Pasal 1", "(1)"]);
    }

    #[test]
    fn splits_long_unicode_text_without_invalid_boundaries_and_is_deterministic() {
        let body = "akses ﬂeksibel untuk warga. ".repeat(20);
        let raw = format!("Pasal 1\n(1) {body}\n");
        let (normalized, tree) = fixture(&raw);
        let config = small_config();

        let first = build_chunks(&normalized, &tree, &config, &Words).expect("first build");
        let second = build_chunks(&normalized, &tree, &config, &Words).expect("second build");

        assert_eq!(first, second);
        assert!(first.chunks.len() > 2);
        for chunk in &first.chunks {
            assert!(normalized
                .text
                .is_char_boundary(chunk.text_span.normalized.start));
            assert!(normalized
                .text
                .is_char_boundary(chunk.text_span.normalized.end));
            assert!(chunk.text_span.normalized.end - chunk.text_span.normalized.start <= 96);
        }
        for pair in first.chunks.windows(2) {
            if pair[0].structure_node_refs == pair[1].structure_node_refs {
                assert!(pair[1].text_span.normalized.start <= pair[0].text_span.normalized.end);
                assert!(pair[1].text_span.normalized.start > pair[0].text_span.normalized.start);
            }
        }
    }

    #[test]
    fn chunks_unstructured_document_through_root_node() {
        let (normalized, tree) = fixture("Dokumen tanpa heading tetapi tetap dapat dicari.");
        let batch = build_chunks(&normalized, &tree, &small_config(), &Words)
            .expect("root fallback chunks");

        assert_eq!(batch.chunks.len(), 1);
        assert_eq!(
            batch.chunks[0].structure_node_refs,
            [tree.root().id.clone()]
        );
        assert!(batch.chunks[0].parent_refs.is_empty());
    }

    #[test]
    fn fingerprint_changes_with_config_and_tokenizer_identity() {
        struct OtherWords;
        impl TokenCounter for OtherWords {
            fn tokenizer_id(&self) -> &str {
                "test:words-v2"
            }
            fn count_tokens(&self, text: &str) -> Result<u32, String> {
                u32::try_from(text.split_whitespace().count()).map_err(|error| error.to_string())
            }
        }

        let (normalized, tree) = fixture("Pasal 1\nIsi ketentuan.\n");
        let base = build_chunks(&normalized, &tree, &small_config(), &Words).expect("base");
        let other_tokenizer = build_chunks(&normalized, &tree, &small_config(), &OtherWords)
            .expect("other tokenizer");
        let mut changed = small_config();
        changed.overlap_bytes += 1;
        let other_config =
            build_chunks(&normalized, &tree, &changed, &Words).expect("other config");

        assert_ne!(
            base.chunker_config_sha256,
            other_tokenizer.chunker_config_sha256
        );
        assert_ne!(
            base.chunker_config_sha256,
            other_config.chunker_config_sha256
        );
    }

    #[test]
    fn rejects_invalid_config_tokenizer_failures_and_chunk_limit() {
        let (normalized, tree) = fixture("Pasal 1\nIsi ketentuan.\n");
        let invalid = ChunkerConfig {
            maximum_chunk_bytes: 32,
            ..small_config()
        };
        assert_eq!(
            build_chunks(&normalized, &tree, &invalid, &Words),
            Err(ChunkBuildError::InvalidConfig("maximum_chunk_bytes"))
        );
        assert_eq!(
            build_chunks(
                &normalized,
                &tree,
                &small_config(),
                &BrokenTokenizer { mode: "id" }
            ),
            Err(ChunkBuildError::InvalidTokenizerId)
        );
        assert_eq!(
            build_chunks(
                &normalized,
                &tree,
                &small_config(),
                &BrokenTokenizer { mode: "error" }
            ),
            Err(ChunkBuildError::Tokenization("fixture failure".to_owned()))
        );
        assert_eq!(
            build_chunks(
                &normalized,
                &tree,
                &small_config(),
                &BrokenTokenizer { mode: "zero" }
            ),
            Err(ChunkBuildError::EmptyTokenCount)
        );

        let limited = ChunkerConfig {
            maximum_chunks: 1,
            ..small_config()
        };
        let (multi_normalized, multi_tree) = fixture("PEMBUKAAN\nPasal 1\nIsi ketentuan.\n");
        assert_eq!(
            build_chunks(&multi_normalized, &multi_tree, &limited, &Words),
            Err(ChunkBuildError::ChunkLimit { maximum: 1 })
        );
    }
}
