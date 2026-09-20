//! Menormalisasi artefak teks mekanis dengan mapping byte raw-ke-normalized yang lengkap.
//!
//! Peran dalam komponen:
//! Menjadi tahap I01 setelah parsing dan sebelum struktur/chunking. Transformasi dibatasi pada line ending,
//! whitespace horizontal, soft hyphen, dan ligature Unicode yang dapat dibalik melalui span mapping.
//!
//! Kontrak integrasi dan perhatian implementasi:
//! Seluruh offset adalah byte UTF-8 start-inclusive/end-exclusive. Mapping berurutan menutup semua byte raw
//! dan normalized, termasuk deletion span dengan rentang normalized kosong. Negasi, angka, tahun, unit,
//! punctuation, paragraph break, form-feed halaman, dan hyphen terlihat dipertahankan. Dehyphenation lintas
//! baris sengaja belum dilakukan sampai gold membuktikan aturan yang aman.
//!
//! Benchmark dan gate penerimaan:
//! Ukur throughput bersama structural chunking/source mapping pada workload `text_transform` dalam
//! configs/benchmark-targets.yaml, plus peak RSS dan fidelity critical token. Target tetap
//! REQUIRED_UNMEASURED sampai workload 100 MiB dan gold parsing dijalankan.
//!
//! Status: normalizer dan validator mapping aktif; proyeksi/persistence artefak wire tersedia pada
//! domain/adapter, sedangkan executable worker belum aktif.

use sha2::{Digest, Sha256};
use std::error::Error;
use std::fmt::{Display, Formatter};
use std::ops::Range;

const SCHEMA_VERSION: u32 = 1;

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct TextNormalizerConfig {
    pub maximum_input_bytes: usize,
    pub maximum_mapping_spans: usize,
    pub collapse_horizontal_whitespace: bool,
    pub expand_unicode_ligatures: bool,
    pub remove_soft_hyphen: bool,
}

impl Default for TextNormalizerConfig {
    fn default() -> Self {
        Self {
            maximum_input_bytes: 512 * 1024 * 1024,
            maximum_mapping_spans: 2_000_000,
            collapse_horizontal_whitespace: true,
            expand_unicode_ligatures: true,
            remove_soft_hyphen: true,
        }
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum MappingKind {
    Identity,
    LineEnding,
    HorizontalWhitespace,
    LigatureExpansion,
    SoftHyphenRemoval,
}

/// Satu transformasi monoton. Rentang kosong hanya sah pada sisi normalized untuk deletion.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct TextMappingSpan {
    pub raw_start_byte: u64,
    pub raw_end_byte: u64,
    pub normalized_start_byte: u64,
    pub normalized_end_byte: u64,
    pub kind: MappingKind,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct NormalizedText {
    pub schema_version: u32,
    pub raw_sha256: String,
    pub raw_byte_length: usize,
    pub normalized_sha256: String,
    pub config_sha256: String,
    pub text: String,
    pub mapping: Vec<TextMappingSpan>,
}

impl NormalizedText {
    /// Memeriksa integritas mandiri sebelum artefak dipakai tanpa raw text di boundary berikutnya.
    pub fn validate_integrity(&self) -> Result<(), NormalizeError> {
        if self.schema_version != SCHEMA_VERSION {
            return Err(NormalizeError::SchemaMismatch {
                actual: self.schema_version,
            });
        }
        if !is_sha256(&self.raw_sha256)
            || !is_sha256(&self.normalized_sha256)
            || !is_sha256(&self.config_sha256)
            || sha256(self.text.as_bytes()) != self.normalized_sha256
        {
            return Err(NormalizeError::HashMismatch);
        }
        let mut raw_cursor = 0usize;
        let mut normalized_cursor = 0usize;
        for span in &self.mapping {
            let raw_start = usize::try_from(span.raw_start_byte)
                .map_err(|_| NormalizeError::InvalidMapping("raw_start_byte"))?;
            let raw_end = usize::try_from(span.raw_end_byte)
                .map_err(|_| NormalizeError::InvalidMapping("raw_end_byte"))?;
            let normalized_start = usize::try_from(span.normalized_start_byte)
                .map_err(|_| NormalizeError::InvalidMapping("normalized_start_byte"))?;
            let normalized_end = usize::try_from(span.normalized_end_byte)
                .map_err(|_| NormalizeError::InvalidMapping("normalized_end_byte"))?;
            if raw_start != raw_cursor
                || normalized_start != normalized_cursor
                || raw_start >= raw_end
                || raw_end > self.raw_byte_length
                || normalized_start > normalized_end
                || normalized_end > self.text.len()
                || !self.text.is_char_boundary(normalized_start)
                || !self.text.is_char_boundary(normalized_end)
                || (normalized_start == normalized_end
                    && span.kind != MappingKind::SoftHyphenRemoval)
            {
                return Err(NormalizeError::InvalidMapping("span integrity"));
            }
            validate_mapping_shape(
                span.kind,
                raw_end - raw_start,
                normalized_end - normalized_start,
            )?;
            raw_cursor = raw_end;
            normalized_cursor = normalized_end;
        }
        if raw_cursor != self.raw_byte_length || normalized_cursor != self.text.len() {
            return Err(NormalizeError::InvalidMapping("integrity coverage"));
        }
        if self.raw_byte_length == 0 && !self.mapping.is_empty() {
            return Err(NormalizeError::InvalidMapping("empty input"));
        }
        Ok(())
    }

    /// Mengembalikan raw cover minimal untuk span normalized non-kosong pada batas karakter UTF-8.
    pub fn raw_cover(&self, normalized: Range<usize>) -> Result<Range<usize>, NormalizeError> {
        validate_requested_span(&self.text, &normalized)?;
        if normalized.is_empty() {
            return Err(NormalizeError::EmptyRequestedSpan);
        }
        let first_index = self
            .mapping
            .partition_point(|span| span.normalized_end_byte <= normalized.start as u64);
        let first = self.mapping[first_index..]
            .iter()
            .find(|span| {
                span.normalized_end_byte > normalized.start as u64
                    && span.normalized_start_byte < normalized.end as u64
            })
            .ok_or(NormalizeError::UnmappedSpan)?;
        let after_last = self
            .mapping
            .partition_point(|span| span.normalized_start_byte < normalized.end as u64);
        let last = self.mapping[..after_last]
            .iter()
            .rev()
            .find(|span| {
                span.normalized_end_byte > normalized.start as u64
                    && span.normalized_start_byte < normalized.end as u64
            })
            .ok_or(NormalizeError::UnmappedSpan)?;
        let raw_start = project_normalized_boundary(first, normalized.start as u64, true)?;
        let raw_end = project_normalized_boundary(last, normalized.end as u64, false)?;
        let raw_start = usize::try_from(raw_start)
            .map_err(|_| NormalizeError::InvalidMapping("raw_cover_start"))?;
        let raw_end = usize::try_from(raw_end)
            .map_err(|_| NormalizeError::InvalidMapping("raw_cover_end"))?;
        let first_raw_start = usize::try_from(first.raw_start_byte)
            .map_err(|_| NormalizeError::InvalidMapping("raw_cover_first_start"))?;
        let last_raw_end = usize::try_from(last.raw_end_byte)
            .map_err(|_| NormalizeError::InvalidMapping("raw_cover_last_end"))?;
        if raw_start >= raw_end
            || raw_start < first_raw_start
            || raw_end > last_raw_end
            || raw_end > self.raw_byte_length
        {
            return Err(NormalizeError::InvalidMapping("raw_cover_order"));
        }
        Ok(raw_start..raw_end)
    }

    /// Mengembalikan cover normalized yang aman untuk rentang raw, termasuk rentang halaman kosong.
    /// Transform many-to-one/one-to-many diperlebar ke seluruh span transformasi agar tidak mengklaim
    /// pemetaan byte parsial yang tidak dapat dibalik secara tepat.
    pub fn normalized_cover(&self, raw: Range<usize>) -> Result<Range<usize>, NormalizeError> {
        self.validate_integrity()?;
        if raw.start > raw.end || raw.end > self.raw_byte_length {
            return Err(NormalizeError::InvalidRequestedSpan);
        }
        if raw.is_empty() {
            if raw.start == 0 {
                return Ok(0..0);
            }
            if raw.start == self.raw_byte_length {
                return Ok(self.text.len()..self.text.len());
            }
            let index = self
                .mapping
                .partition_point(|span| span.raw_end_byte <= raw.start as u64);
            let span = self
                .mapping
                .get(index)
                .ok_or(NormalizeError::UnmappedSpan)?;
            let boundary = project_raw_boundary(span, raw.start as u64, true)?;
            let boundary = usize::try_from(boundary)
                .map_err(|_| NormalizeError::InvalidMapping("normalized_cover_boundary"))?;
            return Ok(boundary..boundary);
        }
        let first_index = self
            .mapping
            .partition_point(|span| span.raw_end_byte <= raw.start as u64);
        let after_last = self
            .mapping
            .partition_point(|span| span.raw_start_byte < raw.end as u64);
        let overlaps = &self.mapping[first_index..after_last];
        let first = overlaps
            .iter()
            .find(|span| {
                span.raw_end_byte > raw.start as u64 && span.raw_start_byte < raw.end as u64
            })
            .ok_or(NormalizeError::UnmappedSpan)?;
        let last = overlaps
            .iter()
            .rev()
            .find(|span| {
                span.raw_end_byte > raw.start as u64 && span.raw_start_byte < raw.end as u64
            })
            .ok_or(NormalizeError::UnmappedSpan)?;
        let normalized_start =
            usize::try_from(project_raw_boundary(first, raw.start as u64, true)?)
                .map_err(|_| NormalizeError::InvalidMapping("normalized_cover_start"))?;
        let normalized_end = usize::try_from(project_raw_boundary(last, raw.end as u64, false)?)
            .map_err(|_| NormalizeError::InvalidMapping("normalized_cover_end"))?;
        if normalized_start > normalized_end || normalized_end > self.text.len() {
            return Err(NormalizeError::InvalidMapping("normalized_cover_order"));
        }
        Ok(normalized_start..normalized_end)
    }

    pub fn validate_mapping(
        &self,
        raw: &str,
        config: &TextNormalizerConfig,
    ) -> Result<(), NormalizeError> {
        validate_config(config)?;
        self.validate_integrity()?;
        if config_fingerprint(config) != self.config_sha256 {
            return Err(NormalizeError::ConfigMismatch);
        }
        if raw.len() > config.maximum_input_bytes {
            return Err(NormalizeError::InputTooLarge {
                actual: raw.len(),
                maximum: config.maximum_input_bytes,
            });
        }
        if raw.len() != self.raw_byte_length || sha256(raw.as_bytes()) != self.raw_sha256 {
            return Err(NormalizeError::HashMismatch);
        }
        let mut raw_cursor = 0usize;
        let mut normalized_cursor = 0usize;
        for span in &self.mapping {
            let raw_start = usize::try_from(span.raw_start_byte)
                .map_err(|_| NormalizeError::InvalidMapping("raw_start_byte"))?;
            let raw_end = usize::try_from(span.raw_end_byte)
                .map_err(|_| NormalizeError::InvalidMapping("raw_end_byte"))?;
            let normalized_start = usize::try_from(span.normalized_start_byte)
                .map_err(|_| NormalizeError::InvalidMapping("normalized_start_byte"))?;
            let normalized_end = usize::try_from(span.normalized_end_byte)
                .map_err(|_| NormalizeError::InvalidMapping("normalized_end_byte"))?;
            if raw_start != raw_cursor
                || normalized_start != normalized_cursor
                || raw_start >= raw_end
                || normalized_start > normalized_end
                || raw_end > raw.len()
                || normalized_end > self.text.len()
                || !raw.is_char_boundary(raw_start)
                || !raw.is_char_boundary(raw_end)
                || !self.text.is_char_boundary(normalized_start)
                || !self.text.is_char_boundary(normalized_end)
                || (normalized_start == normalized_end
                    && span.kind != MappingKind::SoftHyphenRemoval)
            {
                return Err(NormalizeError::InvalidMapping("span"));
            }
            validate_transform(
                &raw[raw_start..raw_end],
                &self.text[normalized_start..normalized_end],
                span.kind,
            )?;
            validate_transform_policy(&raw[raw_start..raw_end], span.kind, config)?;
            raw_cursor = raw_end;
            normalized_cursor = normalized_end;
        }
        if raw_cursor != raw.len() || normalized_cursor != self.text.len() {
            return Err(NormalizeError::InvalidMapping("coverage"));
        }
        if raw.is_empty() && !self.mapping.is_empty() {
            return Err(NormalizeError::InvalidMapping("empty_input"));
        }
        let expected = build_mapping(raw, config)?;
        if self.text != expected.text || self.mapping != expected.mapping {
            return Err(NormalizeError::InvalidMapping("canonical_transform"));
        }
        Ok(())
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum NormalizeError {
    InvalidConfig(&'static str),
    SchemaMismatch { actual: u32 },
    ConfigMismatch,
    InputTooLarge { actual: usize, maximum: usize },
    MappingLimit { maximum: usize },
    InvalidRequestedSpan,
    EmptyRequestedSpan,
    UnmappedSpan,
    InvalidMapping(&'static str),
    HashMismatch,
}

impl Display for NormalizeError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::InvalidConfig(field) => write!(formatter, "invalid normalizer config: {field}"),
            Self::SchemaMismatch { actual } => {
                write!(formatter, "normalizer schema {actual} is not supported")
            }
            Self::ConfigMismatch => write!(formatter, "normalizer config fingerprint mismatch"),
            Self::InputTooLarge { actual, maximum } => {
                write!(formatter, "input bytes {actual} exceed limit {maximum}")
            }
            Self::MappingLimit { maximum } => {
                write!(formatter, "text mapping exceeds {maximum} spans")
            }
            Self::InvalidRequestedSpan => write!(formatter, "invalid normalized UTF-8 span"),
            Self::EmptyRequestedSpan => write!(formatter, "normalized span must not be empty"),
            Self::UnmappedSpan => write!(formatter, "normalized span has no raw mapping"),
            Self::InvalidMapping(field) => write!(formatter, "invalid text mapping: {field}"),
            Self::HashMismatch => write!(formatter, "text mapping hash mismatch"),
        }
    }
}

impl Error for NormalizeError {}

pub fn normalize_text(
    raw: &str,
    config: &TextNormalizerConfig,
) -> Result<NormalizedText, NormalizeError> {
    validate_config(config)?;
    if raw.len() > config.maximum_input_bytes {
        return Err(NormalizeError::InputTooLarge {
            actual: raw.len(),
            maximum: config.maximum_input_bytes,
        });
    }

    let builder = build_mapping(raw, config)?;

    Ok(NormalizedText {
        schema_version: SCHEMA_VERSION,
        raw_sha256: sha256(raw.as_bytes()),
        raw_byte_length: raw.len(),
        normalized_sha256: sha256(builder.text.as_bytes()),
        config_sha256: config_fingerprint(config),
        text: builder.text,
        mapping: builder.mapping,
    })
}

fn build_mapping(
    raw: &str,
    config: &TextNormalizerConfig,
) -> Result<MappingBuilder, NormalizeError> {
    let mut builder = MappingBuilder::with_capacity(raw.len(), config.maximum_mapping_spans);
    let mut cursor = 0usize;
    while cursor < raw.len() {
        let character = raw[cursor..]
            .chars()
            .next()
            .expect("cursor remains on a character boundary");
        let end = cursor + character.len_utf8();

        if character == '\r' {
            let raw_end = if raw[end..].starts_with('\n') {
                end + '\n'.len_utf8()
            } else {
                end
            };
            builder.push(cursor, raw_end, "\n", MappingKind::LineEnding)?;
            cursor = raw_end;
            continue;
        }
        if config.collapse_horizontal_whitespace && is_horizontal_whitespace(character) {
            let mut raw_end = end;
            while raw_end < raw.len() {
                let next = raw[raw_end..]
                    .chars()
                    .next()
                    .expect("raw_end remains on a character boundary");
                if !is_horizontal_whitespace(next) {
                    break;
                }
                raw_end += next.len_utf8();
            }
            if &raw[cursor..raw_end] == " " {
                builder.push(cursor, raw_end, " ", MappingKind::Identity)?;
            } else {
                builder.push(cursor, raw_end, " ", MappingKind::HorizontalWhitespace)?;
            }
            cursor = raw_end;
            continue;
        }
        if config.remove_soft_hyphen && character == '\u{00ad}' {
            builder.push(cursor, end, "", MappingKind::SoftHyphenRemoval)?;
            cursor = end;
            continue;
        }
        if config.expand_unicode_ligatures {
            if let Some(replacement) = ligature_replacement(character) {
                builder.push(cursor, end, replacement, MappingKind::LigatureExpansion)?;
                cursor = end;
                continue;
            }
        }
        builder.push(cursor, end, &raw[cursor..end], MappingKind::Identity)?;
        cursor = end;
    }
    Ok(builder)
}

struct MappingBuilder {
    text: String,
    mapping: Vec<TextMappingSpan>,
    maximum_mapping_spans: usize,
}

impl MappingBuilder {
    fn with_capacity(capacity: usize, maximum_mapping_spans: usize) -> Self {
        Self {
            text: String::with_capacity(capacity),
            mapping: Vec::new(),
            maximum_mapping_spans,
        }
    }

    fn push(
        &mut self,
        raw_start: usize,
        raw_end: usize,
        replacement: &str,
        kind: MappingKind,
    ) -> Result<(), NormalizeError> {
        let normalized_start = self.text.len();
        let merge_identity = kind == MappingKind::Identity
            && self.mapping.last().is_some_and(|last| {
                last.kind == MappingKind::Identity
                    && last.raw_end_byte == raw_start as u64
                    && last.normalized_end_byte == normalized_start as u64
            });
        if !merge_identity && self.mapping.len() >= self.maximum_mapping_spans {
            return Err(NormalizeError::MappingLimit {
                maximum: self.maximum_mapping_spans,
            });
        }
        self.text.push_str(replacement);
        let normalized_end = self.text.len();
        if merge_identity {
            if let Some(last) = self.mapping.last_mut() {
                last.raw_end_byte = raw_end as u64;
                last.normalized_end_byte = normalized_end as u64;
                return Ok(());
            }
        }
        self.mapping.push(TextMappingSpan {
            raw_start_byte: raw_start as u64,
            raw_end_byte: raw_end as u64,
            normalized_start_byte: normalized_start as u64,
            normalized_end_byte: normalized_end as u64,
            kind,
        });
        Ok(())
    }
}

fn validate_config(config: &TextNormalizerConfig) -> Result<(), NormalizeError> {
    if config.maximum_input_bytes == 0 {
        return Err(NormalizeError::InvalidConfig("maximum_input_bytes"));
    }
    if config.maximum_mapping_spans == 0 {
        return Err(NormalizeError::InvalidConfig("maximum_mapping_spans"));
    }
    Ok(())
}

fn validate_requested_span(text: &str, span: &Range<usize>) -> Result<(), NormalizeError> {
    if span.start > span.end
        || span.end > text.len()
        || !text.is_char_boundary(span.start)
        || !text.is_char_boundary(span.end)
    {
        return Err(NormalizeError::InvalidRequestedSpan);
    }
    Ok(())
}

fn validate_mapping_shape(
    kind: MappingKind,
    raw_length: usize,
    normalized_length: usize,
) -> Result<(), NormalizeError> {
    let valid = match kind {
        MappingKind::Identity => raw_length == normalized_length,
        MappingKind::LineEnding => matches!(raw_length, 1 | 2) && normalized_length == 1,
        MappingKind::HorizontalWhitespace => raw_length >= 1 && normalized_length == 1,
        MappingKind::LigatureExpansion => raw_length == 3 && matches!(normalized_length, 2 | 3),
        MappingKind::SoftHyphenRemoval => raw_length == 2 && normalized_length == 0,
    };
    if valid {
        Ok(())
    } else {
        Err(NormalizeError::InvalidMapping("transform shape"))
    }
}

fn project_normalized_boundary(
    span: &TextMappingSpan,
    boundary: u64,
    is_start: bool,
) -> Result<u64, NormalizeError> {
    if boundary < span.normalized_start_byte || boundary > span.normalized_end_byte {
        return Err(NormalizeError::UnmappedSpan);
    }
    if span.kind == MappingKind::Identity {
        let offset = boundary
            .checked_sub(span.normalized_start_byte)
            .ok_or(NormalizeError::InvalidMapping("raw_cover_offset"))?;
        return span
            .raw_start_byte
            .checked_add(offset)
            .ok_or(NormalizeError::InvalidMapping("raw_cover_overflow"));
    }
    Ok(if is_start {
        span.raw_start_byte
    } else {
        span.raw_end_byte
    })
}

fn project_raw_boundary(
    span: &TextMappingSpan,
    boundary: u64,
    is_start: bool,
) -> Result<u64, NormalizeError> {
    if boundary < span.raw_start_byte || boundary > span.raw_end_byte {
        return Err(NormalizeError::UnmappedSpan);
    }
    if span.kind == MappingKind::Identity {
        let offset = boundary
            .checked_sub(span.raw_start_byte)
            .ok_or(NormalizeError::InvalidMapping("normalized_cover_offset"))?;
        return span
            .normalized_start_byte
            .checked_add(offset)
            .ok_or(NormalizeError::InvalidMapping("normalized_cover_overflow"));
    }
    Ok(if is_start {
        span.normalized_start_byte
    } else {
        span.normalized_end_byte
    })
}

fn validate_transform(
    raw: &str,
    normalized: &str,
    kind: MappingKind,
) -> Result<(), NormalizeError> {
    let valid = match kind {
        MappingKind::Identity => raw == normalized,
        MappingKind::LineEnding => matches!(raw, "\r" | "\r\n") && normalized == "\n",
        MappingKind::HorizontalWhitespace => {
            !raw.is_empty()
                && raw.chars().all(is_horizontal_whitespace)
                && raw != " "
                && normalized == " "
        }
        MappingKind::LigatureExpansion => {
            let mut characters = raw.chars();
            characters.next().and_then(ligature_replacement) == Some(normalized)
                && characters.next().is_none()
        }
        MappingKind::SoftHyphenRemoval => raw == "\u{00ad}" && normalized.is_empty(),
    };
    if valid {
        Ok(())
    } else {
        Err(NormalizeError::InvalidMapping("transform"))
    }
}

fn validate_transform_policy(
    raw: &str,
    kind: MappingKind,
    config: &TextNormalizerConfig,
) -> Result<(), NormalizeError> {
    let allowed = match kind {
        MappingKind::Identity => identity_is_canonical(raw, config),
        MappingKind::LineEnding => true,
        MappingKind::HorizontalWhitespace => config.collapse_horizontal_whitespace,
        MappingKind::LigatureExpansion => config.expand_unicode_ligatures,
        MappingKind::SoftHyphenRemoval => config.remove_soft_hyphen,
    };
    if allowed {
        Ok(())
    } else {
        Err(NormalizeError::InvalidMapping("config_policy"))
    }
}

fn identity_is_canonical(raw: &str, config: &TextNormalizerConfig) -> bool {
    let mut previous_horizontal = false;
    for character in raw.chars() {
        if character == '\r'
            || (config.remove_soft_hyphen && character == '\u{00ad}')
            || (config.expand_unicode_ligatures && ligature_replacement(character).is_some())
        {
            return false;
        }
        if config.collapse_horizontal_whitespace && is_horizontal_whitespace(character) {
            if character != ' ' || previous_horizontal {
                return false;
            }
            previous_horizontal = true;
        } else {
            previous_horizontal = false;
        }
    }
    true
}

fn is_horizontal_whitespace(character: char) -> bool {
    matches!(
        character,
        ' ' | '\t' | '\u{00a0}' | '\u{1680}' | '\u{2000}'
            ..='\u{200a}' | '\u{202f}' | '\u{205f}' | '\u{3000}'
    )
}

fn ligature_replacement(character: char) -> Option<&'static str> {
    match character {
        '\u{fb00}' => Some("ff"),
        '\u{fb01}' => Some("fi"),
        '\u{fb02}' => Some("fl"),
        '\u{fb03}' => Some("ffi"),
        '\u{fb04}' => Some("ffl"),
        '\u{fb05}' | '\u{fb06}' => Some("st"),
        _ => None,
    }
}

fn config_fingerprint(config: &TextNormalizerConfig) -> String {
    let canonical = format!(
        "schema={SCHEMA_VERSION}\nmaximum_input_bytes={}\nmaximum_mapping_spans={}\ncollapse_horizontal_whitespace={}\nexpand_unicode_ligatures={}\nremove_soft_hyphen={}\n",
        config.maximum_input_bytes,
        config.maximum_mapping_spans,
        config.collapse_horizontal_whitespace,
        config.expand_unicode_ligatures,
        config.remove_soft_hyphen,
    );
    sha256(canonical.as_bytes())
}

fn sha256(value: &[u8]) -> String {
    format!("{:x}", Sha256::digest(value))
}

fn is_sha256(value: &str) -> bool {
    value.len() == 64
        && value
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn normalizes_mechanical_artifacts_with_complete_mapping() {
        let raw = "Pasal\r\n\tﬁskal\u{00a0}\u{00a0}berla\u{00ad}ku\rakhir";
        let result = normalize_text(raw, &TextNormalizerConfig::default()).unwrap();
        assert_eq!(result.text, "Pasal\n fiskal berlaku\nakhir");
        result
            .validate_mapping(raw, &TextNormalizerConfig::default())
            .unwrap();
        assert!(result.mapping.iter().any(|span| {
            span.kind == MappingKind::LigatureExpansion
                && span.raw_end_byte - span.raw_start_byte == 3
                && span.normalized_end_byte - span.normalized_start_byte == 2
        }));
        assert!(result.mapping.iter().any(|span| {
            span.kind == MappingKind::SoftHyphenRemoval
                && span.normalized_start_byte == span.normalized_end_byte
        }));
    }

    #[test]
    fn preserves_critical_legal_tokens_and_visible_hyphens() {
        let raw = "tidak boleh 2024 10 kg Pasal 1 ayat (2), kecuali anak-anak\n";
        let result = normalize_text(raw, &TextNormalizerConfig::default()).unwrap();
        assert_eq!(result.text, raw);
        assert_eq!(result.mapping.len(), 1);
        assert_eq!(result.mapping[0].kind, MappingKind::Identity);
    }

    #[test]
    fn maps_normalized_utf8_span_back_to_raw_cover() {
        let raw = "A\u{fb01}\u{00ad}\u{00a0}\u{00a0}Bé";
        let result = normalize_text(raw, &TextNormalizerConfig::default()).unwrap();
        assert_eq!(result.text, "Afi Bé");
        let start = result.text.find("fi B").unwrap();
        let cover = result.raw_cover(start..result.text.len()).unwrap();
        assert_eq!(&raw[cover], "\u{fb01}\u{00ad}\u{00a0}\u{00a0}Bé");
        assert!(matches!(
            result.raw_cover(6..7),
            Err(NormalizeError::InvalidRequestedSpan)
        ));
    }

    #[test]
    fn maps_partial_identity_span_without_widening_to_document() {
        let raw = "tidak boleh dilakukan";
        let result = normalize_text(raw, &TextNormalizerConfig::default()).unwrap();
        let start = result.text.find("boleh").unwrap();
        let cover = result.raw_cover(start..start + "boleh".len()).unwrap();
        assert_eq!(&raw[cover], "boleh");

        let mut corrupted = result;
        corrupted.mapping[0].raw_start_byte = u64::MAX;
        corrupted.mapping[0].raw_end_byte = u64::MAX;
        assert!(matches!(
            corrupted.raw_cover(0..1),
            Err(NormalizeError::InvalidMapping(_))
        ));
    }

    #[test]
    fn honors_disabled_transforms_and_input_limit() {
        let config = TextNormalizerConfig {
            collapse_horizontal_whitespace: false,
            expand_unicode_ligatures: false,
            remove_soft_hyphen: false,
            ..TextNormalizerConfig::default()
        };
        let raw = "\t\u{fb01}\u{00ad}";
        assert_eq!(normalize_text(raw, &config).unwrap().text, raw);

        let limited = TextNormalizerConfig {
            maximum_input_bytes: 2,
            ..TextNormalizerConfig::default()
        };
        assert!(matches!(
            normalize_text("abc", &limited),
            Err(NormalizeError::InputTooLarge {
                actual: 3,
                maximum: 2
            })
        ));
    }

    #[test]
    fn rejects_tampered_mapping_and_hashes() {
        let raw = "abc";
        let mut result = normalize_text(raw, &TextNormalizerConfig::default()).unwrap();
        result.mapping[0].raw_end_byte = 2;
        assert!(matches!(
            result.validate_mapping(raw, &TextNormalizerConfig::default()),
            Err(NormalizeError::InvalidMapping(_))
        ));

        let mut result = normalize_text(raw, &TextNormalizerConfig::default()).unwrap();
        result.normalized_sha256 = "0".repeat(64);
        assert_eq!(
            result.validate_mapping(raw, &TextNormalizerConfig::default()),
            Err(NormalizeError::HashMismatch)
        );

        let mut result = normalize_text(raw, &TextNormalizerConfig::default()).unwrap();
        result.mapping[0].raw_start_byte += 100_000;
        result.mapping[0].raw_end_byte += 100_000;
        assert!(matches!(
            result.validate_integrity(),
            Err(NormalizeError::InvalidMapping(_))
        ));

        let mut result = normalize_text(raw, &TextNormalizerConfig::default()).unwrap();
        result.raw_byte_length += 1;
        assert!(matches!(
            result.validate_integrity(),
            Err(NormalizeError::InvalidMapping(_))
        ));

        let mut result = normalize_text("Pasal 1\nWajib patuh.", &TextNormalizerConfig::default())
            .expect("identity fixture normalizes");
        result.mapping[0].raw_end_byte = 1;
        result.raw_byte_length = 1;
        assert_eq!(
            result.validate_integrity(),
            Err(NormalizeError::InvalidMapping("transform shape"))
        );

        let result = normalize_text(raw, &TextNormalizerConfig::default()).unwrap();
        let changed_config = TextNormalizerConfig {
            remove_soft_hyphen: false,
            ..TextNormalizerConfig::default()
        };
        assert_eq!(
            result.validate_mapping(raw, &changed_config),
            Err(NormalizeError::ConfigMismatch)
        );
    }

    #[test]
    fn rejects_mapping_that_violates_bound_config_policy() {
        let raw = "\t";
        let mut result = normalize_text(raw, &TextNormalizerConfig::default()).unwrap();
        let disabled = TextNormalizerConfig {
            collapse_horizontal_whitespace: false,
            ..TextNormalizerConfig::default()
        };
        result.config_sha256 = config_fingerprint(&disabled);
        assert_eq!(
            result.validate_mapping(raw, &disabled),
            Err(NormalizeError::InvalidMapping("config_policy"))
        );

        let raw = "abc";
        let mut result = normalize_text(raw, &TextNormalizerConfig::default()).unwrap();
        let limited = TextNormalizerConfig {
            maximum_input_bytes: 2,
            ..TextNormalizerConfig::default()
        };
        result.config_sha256 = config_fingerprint(&limited);
        assert_eq!(
            result.validate_mapping(raw, &limited),
            Err(NormalizeError::InputTooLarge {
                actual: 3,
                maximum: 2
            })
        );

        let mapping_limited = TextNormalizerConfig {
            maximum_mapping_spans: 4,
            ..TextNormalizerConfig::default()
        };
        assert_eq!(
            normalize_text("a\tb\tc", &mapping_limited),
            Err(NormalizeError::MappingLimit { maximum: 4 })
        );
    }

    #[test]
    fn rejects_noncanonical_splits_across_transform_boundaries() {
        let config = TextNormalizerConfig::default();
        let mut crlf = normalize_text("\r\n", &config).unwrap();
        crlf.text = "\n\n".into();
        crlf.normalized_sha256 = sha256(crlf.text.as_bytes());
        crlf.mapping = vec![
            TextMappingSpan {
                raw_start_byte: 0,
                raw_end_byte: 1,
                normalized_start_byte: 0,
                normalized_end_byte: 1,
                kind: MappingKind::LineEnding,
            },
            TextMappingSpan {
                raw_start_byte: 1,
                raw_end_byte: 2,
                normalized_start_byte: 1,
                normalized_end_byte: 2,
                kind: MappingKind::Identity,
            },
        ];
        assert_eq!(
            crlf.validate_mapping("\r\n", &config),
            Err(NormalizeError::InvalidMapping("canonical_transform"))
        );

        let mut spaces = normalize_text("  ", &config).unwrap();
        spaces.text = "  ".into();
        spaces.normalized_sha256 = sha256(spaces.text.as_bytes());
        spaces.mapping = vec![
            TextMappingSpan {
                raw_start_byte: 0,
                raw_end_byte: 1,
                normalized_start_byte: 0,
                normalized_end_byte: 1,
                kind: MappingKind::Identity,
            },
            TextMappingSpan {
                raw_start_byte: 1,
                raw_end_byte: 2,
                normalized_start_byte: 1,
                normalized_end_byte: 2,
                kind: MappingKind::Identity,
            },
        ];
        assert_eq!(
            spaces.validate_mapping("  ", &config),
            Err(NormalizeError::InvalidMapping("canonical_transform"))
        );
    }

    #[test]
    fn empty_input_has_empty_valid_mapping() {
        let result = normalize_text("", &TextNormalizerConfig::default()).unwrap();
        assert!(result.text.is_empty());
        assert!(result.mapping.is_empty());
        result
            .validate_mapping("", &TextNormalizerConfig::default())
            .unwrap();
    }

    #[test]
    fn projects_raw_page_ranges_to_normalized_byte_covers() {
        let raw = "Pasal 1\r\nA\u{00ad}B\x0cPasal 2";
        let result = normalize_text(raw, &TextNormalizerConfig::default()).unwrap();
        let page_break = raw.find('\x0c').unwrap();

        let first = result.normalized_cover(0..page_break).unwrap();
        let second = result.normalized_cover(page_break + 1..raw.len()).unwrap();
        assert_eq!(&result.text[first], "Pasal 1\nAB");
        assert_eq!(&result.text[second], "Pasal 2");
        assert_eq!(result.normalized_cover(0..0).unwrap(), 0..0);
        assert_eq!(
            result.normalized_cover(raw.len()..raw.len()).unwrap(),
            result.text.len()..result.text.len()
        );
    }

    #[test]
    fn mixed_utf8_inputs_keep_valid_monotonic_mapping() {
        let atoms = [
            "a", "é", "中", " ", "\t", "\r", "\n", "\r\n", "\u{00a0}", "\u{00ad}", "\u{fb03}", "-",
            "\u{000c}", "tidak", "2024",
        ];
        let mut state = 0x9e37_79b9_u32;
        for case in 0..512 {
            let mut raw = String::new();
            for _ in 0..(case % 47) {
                state = state.wrapping_mul(1_664_525).wrapping_add(1_013_904_223);
                raw.push_str(atoms[state as usize % atoms.len()]);
            }
            let result = normalize_text(&raw, &TextNormalizerConfig::default()).unwrap();
            result
                .validate_mapping(&raw, &TextNormalizerConfig::default())
                .unwrap();
            let boundaries: Vec<_> = result
                .text
                .char_indices()
                .map(|(index, _)| index)
                .chain(std::iter::once(result.text.len()))
                .collect();
            for pair in boundaries.windows(2) {
                let cover = result.raw_cover(pair[0]..pair[1]).unwrap();
                assert!(raw.is_char_boundary(cover.start));
                assert!(raw.is_char_boundary(cover.end));
                assert!(cover.start < cover.end);
            }
        }
    }
}
