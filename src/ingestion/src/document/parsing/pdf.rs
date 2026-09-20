//! Mengekstrak teks dan locator halaman PDF melalui PDFium dengan ownership native yang dibatasi Rust.
//!
//! Peran dalam komponen:
//! Menjadi boundary parser teks I01: memverifikasi hash blob, membuka PDFium sekali per parser, menghasilkan
//! teks UTF-8 dokumen, block span, bounding box ternormalisasi, status halaman, dan kebutuhan OCR eksplisit.
//!
//! Kontrak integrasi dan perhatian implementasi:
//! Offset adalah byte UTF-8 start-inclusive/end-exclusive pada `raw_text`. Antarhalaman dipisahkan form-feed;
//! bounding box memakai koordinat top-left 0..1. Source/blob/hash dan manifest parser selalu diteruskan.
//! Parser tidak melakukan OCR, normalisasi, struktur pasal, chunking, atau publication. PDFium dianggap tidak
//! thread-safe; paralelisme dokumen dilakukan lintas proses worker dan file tak tepercaya dibatasi di sana.
//!
//! Benchmark dan gate penerimaan:
//! Ukur reading order/struktur terhadap gold, page p50/p95/p99, throughput agregat, timeout/error, dan peak RSS
//! pada workload configs/benchmark-targets.yaml. Hasil profiler Python memilih kandidat, tetapi gate produksi
//! tetap REQUIRED_UNMEASURED sampai normalisasi, chunking, source mapping, OCR, dan workload referensi aktif.
//!
//! Status: parser PDFium I01 aktif pada executable worker PARSE; process-level crash isolation multi-worker,
//! OCR, gold quality, dan publication belum aktif.

use pdfium_render::prelude::{
    PdfPage, PdfPageObject, PdfPageObjectsCommon, PdfPageRenderRotation, PdfRect, Pdfium,
};
use sha2::{Digest, Sha256};
use std::error::Error;
use std::fmt::{Display, Formatter};
use std::fs::File;
use std::io::{BufReader, Read};
use std::path::{Path, PathBuf};
use std::sync::Mutex;
use std::time::Instant;

const SCHEMA_VERSION: u32 = 1;
const ENGINE: &str = "pdfium";
const BINDING: &str = "pdfium-render";
const BINDING_VERSION: &str = "0.9.4";
const API_FEATURE: &str = "pdfium_6406";
const PAGE_SEPARATOR: char = '\u{000c}';

// pdfium-render menyimpan binding native dalam OnceCell global. Guard ini membuat ownership tersebut
// eksplisit dan mencegah parser kedua mengiklankan identitas library yang tidak sedang dieksekusi.
static PDFIUM_BIND_IDENTITY: Mutex<Option<String>> = Mutex::new(None);

/// Batas parser yang menjadi bagian dari fingerprint producer, bukan parameter tersembunyi.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct PdfParserConfig {
    pub minimum_text_characters: usize,
    pub maximum_pages: usize,
    pub maximum_page_text_bytes: usize,
    pub maximum_document_text_bytes: usize,
    pub maximum_xobject_depth: usize,
}

impl Default for PdfParserConfig {
    fn default() -> Self {
        Self {
            minimum_text_characters: 80,
            maximum_pages: 50_000,
            maximum_page_text_bytes: 16 * 1024 * 1024,
            maximum_document_text_bytes: 512 * 1024 * 1024,
            maximum_xobject_depth: 16,
        }
    }
}

/// Identitas input yang wajib cocok dengan byte file sebelum PDFium dipanggil.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct PdfParseRequest {
    pub source_id: String,
    pub source_blob_id: String,
    pub expected_sha256: String,
    pub path: PathBuf,
}

/// Manifest lokal yang nantinya dipetakan ke ProducerManifest C01 oleh boundary batch.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct PdfParserManifest {
    pub schema_version: u32,
    pub engine: &'static str,
    pub binding: &'static str,
    pub binding_version: &'static str,
    pub api_feature: &'static str,
    pub declared_core_version: String,
    pub library_sha256: String,
    pub config_sha256: String,
    pub input_sha256: String,
}

/// Bounding box top-left yang dinormalisasi terhadap lebar dan tinggi halaman.
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct NormalizedBoundingBox {
    pub x0: f32,
    pub y0: f32,
    pub x1: f32,
    pub y1: f32,
}

/// Segment teks dengan locator dan span byte ke raw_text dokumen.
#[derive(Clone, Debug, PartialEq)]
pub struct PdfTextBlock {
    pub page_number: u32,
    pub start_byte: u64,
    pub end_byte: u64,
    pub bounding_box: NormalizedBoundingBox,
}

/// Status tidak menyamarkan halaman scan/sparse sebagai ekstraksi teks lengkap.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum PdfPageStatus {
    Text,
    NeedsOcr,
    Sparse,
    Failed { code: &'static str, detail: String },
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum PdfDocumentStatus {
    Complete,
    Partial,
    Failed,
}

/// Hasil per halaman mempertahankan span, indikator image, latency, dan status eksplisit.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct PdfPageResult {
    pub page_number: u32,
    pub start_byte: u64,
    pub end_byte: u64,
    pub image_objects: u32,
    pub elapsed_microseconds: u64,
    pub status: PdfPageStatus,
}

/// Output parser sebelum normalisasi dan struktur hukum.
#[derive(Clone, Debug, PartialEq)]
pub struct PdfDocumentText {
    pub source_id: String,
    pub source_blob_id: String,
    pub raw_text: String,
    pub blocks: Vec<PdfTextBlock>,
    pub pages: Vec<PdfPageResult>,
    pub status: PdfDocumentStatus,
    pub manifest: PdfParserManifest,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum PdfParseError {
    InvalidConfig(&'static str),
    InvalidIdentity(&'static str),
    FileRead { path: PathBuf, detail: String },
    HashMismatch { expected: String, actual: String },
    LibraryHashMismatch { expected: String, actual: String },
    LibraryChanged { before: String, after: String },
    AlreadyBound { library_sha256: String },
    InputChanged { before: String, after: String },
    Bind(String),
    Open(String),
    PageLimit { actual: usize, maximum: usize },
    DocumentTextLimit { actual: usize, maximum: usize },
}

impl Display for PdfParseError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::InvalidConfig(field) => write!(formatter, "invalid parser config: {field}"),
            Self::InvalidIdentity(field) => write!(formatter, "invalid parse identity: {field}"),
            Self::FileRead { path, detail } => {
                write!(formatter, "cannot read {}: {detail}", path.display())
            }
            Self::HashMismatch { expected, actual } => write!(
                formatter,
                "PDF hash mismatch: expected {expected}, got {actual}"
            ),
            Self::LibraryHashMismatch { expected, actual } => {
                write!(
                    formatter,
                    "PDFium library hash mismatch: expected {expected}, got {actual}"
                )
            }
            Self::LibraryChanged { before, after } => write!(
                formatter,
                "PDFium library changed while binding: before {before}, after {after}"
            ),
            Self::AlreadyBound { library_sha256 } => write!(
                formatter,
                "PDFium process is already bound to library {library_sha256}"
            ),
            Self::InputChanged { before, after } => write!(
                formatter,
                "PDF changed while parsing: before {before}, after {after}"
            ),
            Self::Bind(detail) => write!(formatter, "cannot bind PDFium: {detail}"),
            Self::Open(detail) => write!(formatter, "cannot open PDF: {detail}"),
            Self::PageLimit { actual, maximum } => {
                write!(formatter, "PDF page count {actual} exceeds limit {maximum}")
            }
            Self::DocumentTextLimit { actual, maximum } => {
                write!(formatter, "PDF text bytes {actual} exceed limit {maximum}")
            }
        }
    }
}

impl Error for PdfParseError {}

/// Handle PDFium yang diinisialisasi eksplisit; constructor memverifikasi binary sebelum dynamic loading.
pub struct PdfParser {
    pdfium: Pdfium,
    config: PdfParserConfig,
    library_sha256: String,
    declared_core_version: String,
    config_sha256: String,
}

impl PdfParser {
    pub fn bind(
        library_path: &Path,
        expected_library_sha256: &str,
        declared_core_version: impl Into<String>,
        config: PdfParserConfig,
    ) -> Result<Self, PdfParseError> {
        validate_config(&config)?;
        validate_sha256(expected_library_sha256, "library_sha256")?;
        let declared_core_version = declared_core_version.into();
        validate_ascii_id(&declared_core_version, "declared_core_version")?;
        let actual = sha256_file(library_path)?;
        if actual != expected_library_sha256 {
            return Err(PdfParseError::LibraryHashMismatch {
                expected: expected_library_sha256.to_owned(),
                actual,
            });
        }
        let mut bind_identity = PDFIUM_BIND_IDENTITY
            .lock()
            .map_err(|_| PdfParseError::Bind("PDFium bind identity lock is poisoned".to_owned()))?;
        if let Some(library_sha256) = bind_identity.as_ref() {
            return Err(PdfParseError::AlreadyBound {
                library_sha256: library_sha256.clone(),
            });
        }
        let bindings = Pdfium::bind_to_library(library_path)
            .map_err(|error| PdfParseError::Bind(error.to_string()))?;
        let final_library_hash = sha256_file(library_path)?;
        if final_library_hash != actual {
            return Err(PdfParseError::LibraryChanged {
                before: actual,
                after: final_library_hash,
            });
        }
        let parser = Self {
            pdfium: Pdfium::new(bindings),
            config_sha256: config_fingerprint(&config),
            config,
            library_sha256: final_library_hash,
            declared_core_version,
        };
        *bind_identity = Some(parser.library_sha256.clone());
        Ok(parser)
    }

    pub fn parse_file(&self, request: PdfParseRequest) -> Result<PdfDocumentText, PdfParseError> {
        validate_ascii_id(&request.source_id, "source_id")?;
        validate_ascii_id(&request.source_blob_id, "source_blob_id")?;
        validate_sha256(&request.expected_sha256, "expected_sha256")?;
        let actual_hash = sha256_file(&request.path)?;
        if actual_hash != request.expected_sha256 {
            return Err(PdfParseError::HashMismatch {
                expected: request.expected_sha256,
                actual: actual_hash,
            });
        }
        let document = self
            .pdfium
            .load_pdf_from_file(&request.path, None)
            .map_err(|error| PdfParseError::Open(error.to_string()))?;
        let page_count = usize::try_from(document.pages().len())
            .map_err(|_| PdfParseError::Open("PDFium returned a negative page count".to_owned()))?;
        if page_count > self.config.maximum_pages {
            return Err(PdfParseError::PageLimit {
                actual: page_count,
                maximum: self.config.maximum_pages,
            });
        }

        let mut raw_text = String::new();
        let mut blocks = Vec::new();
        let mut pages = Vec::with_capacity(page_count);
        for index in document.pages().as_range() {
            let index_usize = usize::try_from(index).map_err(|_| {
                PdfParseError::Open("PDFium returned an invalid page index".to_owned())
            })?;
            let page_number = u32::try_from(index_usize)
                .map_err(|_| PdfParseError::Open("PDF page index exceeds uint32".to_owned()))?
                + 1;
            let page_started = Instant::now();
            let parsed = match document.pages().get(index) {
                Ok(page) => parse_page(&page, page_number, &self.config),
                Err(error) => Err(("pdfium_page_open", error.to_string())),
            };
            let page_start = raw_text.len();
            match parsed {
                Ok(mut page) => {
                    if page_start.saturating_add(page.text.len())
                        > self.config.maximum_document_text_bytes
                    {
                        return Err(PdfParseError::DocumentTextLimit {
                            actual: page_start.saturating_add(page.text.len()),
                            maximum: self.config.maximum_document_text_bytes,
                        });
                    }
                    raw_text.push_str(&page.text);
                    let page_end = raw_text.len();
                    for block in &mut page.blocks {
                        block.start_byte += page_start as u64;
                        block.end_byte += page_start as u64;
                    }
                    blocks.extend(page.blocks);
                    pages.push(PdfPageResult {
                        page_number,
                        start_byte: page_start as u64,
                        end_byte: page_end as u64,
                        image_objects: page.image_objects,
                        elapsed_microseconds: micros(page_started.elapsed()),
                        status: page.status,
                    });
                }
                Err((code, detail)) => pages.push(PdfPageResult {
                    page_number,
                    start_byte: page_start as u64,
                    end_byte: page_start as u64,
                    image_objects: 0,
                    elapsed_microseconds: micros(page_started.elapsed()),
                    status: PdfPageStatus::Failed { code, detail },
                }),
            }
            if index_usize + 1 < page_count {
                if raw_text.len().saturating_add(PAGE_SEPARATOR.len_utf8())
                    > self.config.maximum_document_text_bytes
                {
                    return Err(PdfParseError::DocumentTextLimit {
                        actual: raw_text.len().saturating_add(PAGE_SEPARATOR.len_utf8()),
                        maximum: self.config.maximum_document_text_bytes,
                    });
                }
                raw_text.push(PAGE_SEPARATOR);
            }
        }

        let final_hash = sha256_file(&request.path)?;
        if final_hash != actual_hash {
            return Err(PdfParseError::InputChanged {
                before: actual_hash,
                after: final_hash,
            });
        }
        let status = document_status(&pages);
        Ok(PdfDocumentText {
            source_id: request.source_id,
            source_blob_id: request.source_blob_id,
            raw_text,
            blocks,
            pages,
            status,
            manifest: PdfParserManifest {
                schema_version: SCHEMA_VERSION,
                engine: ENGINE,
                binding: BINDING,
                binding_version: BINDING_VERSION,
                api_feature: API_FEATURE,
                declared_core_version: self.declared_core_version.clone(),
                library_sha256: self.library_sha256.clone(),
                config_sha256: self.config_sha256.clone(),
                input_sha256: final_hash,
            },
        })
    }
}

struct ParsedPage {
    text: String,
    blocks: Vec<PdfTextBlock>,
    image_objects: u32,
    status: PdfPageStatus,
}

fn parse_page(
    page: &PdfPage<'_>,
    page_number: u32,
    config: &PdfParserConfig,
) -> Result<ParsedPage, (&'static str, String)> {
    let page_text = page
        .text()
        .map_err(|error| ("pdfium_text_open", error.to_string()))?;
    let segments = page_text.segments();
    let media_bounds = page
        .boundaries()
        .media()
        .map_err(|error| ("pdfium_media_box", error.to_string()))?
        .bounds;
    let crop_bounds = page
        .boundaries()
        .crop()
        .map(|boundary| boundary.bounds)
        .unwrap_or(media_bounds);
    let page_bounds = intersect_bounds(media_bounds, crop_bounds).ok_or_else(|| {
        (
            "invalid_page_bounds",
            format!("page {page_number} has no visible MediaBox/CropBox intersection"),
        )
    })?;
    let page_rotation = page
        .rotation()
        .map_err(|error| ("pdfium_page_rotation", error.to_string()))?;
    let mut text = String::new();
    let mut blocks = Vec::with_capacity(segments.len());
    for segment_index in segments.as_range() {
        let segment = segments
            .get(segment_index)
            .map_err(|error| ("pdfium_text_segment", error.to_string()))?;
        let value = segment.text();
        if value.is_empty() {
            continue;
        }
        if !text.is_empty() {
            text.push('\n');
        }
        let start_byte = text.len() as u64;
        if text.len().saturating_add(value.len()) > config.maximum_page_text_bytes {
            return Err((
                "page_text_limit",
                format!(
                    "page {page_number} exceeds {} bytes",
                    config.maximum_page_text_bytes
                ),
            ));
        }
        text.push_str(&value);
        blocks.push(PdfTextBlock {
            page_number,
            start_byte,
            end_byte: text.len() as u64,
            bounding_box: normalize_bounds(segment.bounds(), page_bounds, page_rotation)
                .ok_or_else(|| {
                    (
                        "invalid_text_bounds",
                        format!("page {page_number} contains non-finite text bounds"),
                    )
                })?,
        });
    }
    let image_scan = count_images(page.objects().iter(), config.maximum_xobject_depth, 0);
    if image_scan.truncated {
        return Err((
            "xobject_depth_limit",
            format!(
                "page {page_number} exceeds XObject depth {}",
                config.maximum_xobject_depth
            ),
        ));
    }
    let image_objects = image_scan.count.min(u32::MAX as usize) as u32;
    let visible_characters = text
        .chars()
        .filter(|character| !character.is_whitespace())
        .count();
    let status = classify_page(
        visible_characters,
        image_objects,
        config.minimum_text_characters,
    );
    Ok(ParsedPage {
        text,
        blocks,
        image_objects,
        status,
    })
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
struct ImageScan {
    count: usize,
    truncated: bool,
}

fn count_images<'a>(
    objects: impl Iterator<Item = PdfPageObject<'a>>,
    maximum_depth: usize,
    depth: usize,
) -> ImageScan {
    let mut scan = ImageScan::default();
    for object in objects {
        match object {
            PdfPageObject::Image(_) => scan.count = scan.count.saturating_add(1),
            PdfPageObject::XObjectForm(form) if depth < maximum_depth => {
                let nested = count_images(form.iter(), maximum_depth, depth + 1);
                scan.count = scan.count.saturating_add(nested.count);
                scan.truncated |= nested.truncated;
            }
            PdfPageObject::XObjectForm(_) => scan.truncated = true,
            _ => {}
        }
    }
    scan
}

fn classify_page(
    visible_characters: usize,
    image_objects: u32,
    minimum_text_characters: usize,
) -> PdfPageStatus {
    if visible_characters >= minimum_text_characters {
        PdfPageStatus::Text
    } else if image_objects > 0 {
        PdfPageStatus::NeedsOcr
    } else {
        PdfPageStatus::Sparse
    }
}

fn document_status(pages: &[PdfPageResult]) -> PdfDocumentStatus {
    if pages.is_empty()
        || pages
            .iter()
            .all(|page| matches!(page.status, PdfPageStatus::Failed { .. }))
    {
        PdfDocumentStatus::Failed
    } else if pages
        .iter()
        .any(|page| !matches!(page.status, PdfPageStatus::Text))
    {
        PdfDocumentStatus::Partial
    } else {
        PdfDocumentStatus::Complete
    }
}

fn intersect_bounds(first: PdfRect, second: PdfRect) -> Option<PdfRect> {
    let left = first.left().value.max(second.left().value);
    let bottom = first.bottom().value.max(second.bottom().value);
    let right = first.right().value.min(second.right().value);
    let top = first.top().value.min(second.top().value);
    if ![left, bottom, right, top]
        .iter()
        .all(|value| value.is_finite())
        || left >= right
        || bottom >= top
    {
        return None;
    }
    Some(PdfRect::new_from_values(bottom, left, top, right))
}

fn normalize_bounds(
    bounds: PdfRect,
    page_bounds: PdfRect,
    rotation: PdfPageRenderRotation,
) -> Option<NormalizedBoundingBox> {
    let width = page_bounds.width().value;
    let height = page_bounds.height().value;
    let coordinates = [
        bounds.left().value,
        bounds.top().value,
        bounds.right().value,
        bounds.bottom().value,
        page_bounds.left().value,
        page_bounds.top().value,
        page_bounds.right().value,
        page_bounds.bottom().value,
        width,
        height,
    ];
    if !coordinates.iter().all(|value| value.is_finite()) || width <= 0.0 || height <= 0.0 {
        return None;
    }
    let normalize_point = |x: f32, y: f32| {
        let x = (x - page_bounds.left().value) / width;
        let y = (page_bounds.top().value - y) / height;
        match rotation {
            PdfPageRenderRotation::None => (x, y),
            PdfPageRenderRotation::Degrees90 => (1.0 - y, x),
            PdfPageRenderRotation::Degrees180 => (1.0 - x, 1.0 - y),
            PdfPageRenderRotation::Degrees270 => (y, 1.0 - x),
        }
    };
    let corners = [
        normalize_point(bounds.left().value, bounds.top().value),
        normalize_point(bounds.right().value, bounds.top().value),
        normalize_point(bounds.left().value, bounds.bottom().value),
        normalize_point(bounds.right().value, bounds.bottom().value),
    ];
    let (mut left, mut top, mut right, mut bottom) = (
        f32::INFINITY,
        f32::INFINITY,
        f32::NEG_INFINITY,
        f32::NEG_INFINITY,
    );
    for (x, y) in corners {
        left = left.min(x);
        top = top.min(y);
        right = right.max(x);
        bottom = bottom.max(y);
    }
    Some(NormalizedBoundingBox {
        x0: left.clamp(0.0, 1.0),
        y0: top.clamp(0.0, 1.0),
        x1: right.clamp(0.0, 1.0),
        y1: bottom.clamp(0.0, 1.0),
    })
}

fn validate_config(config: &PdfParserConfig) -> Result<(), PdfParseError> {
    if config.minimum_text_characters == 0 {
        return Err(PdfParseError::InvalidConfig("minimum_text_characters"));
    }
    if config.maximum_pages == 0 {
        return Err(PdfParseError::InvalidConfig("maximum_pages"));
    }
    if config.maximum_page_text_bytes == 0 {
        return Err(PdfParseError::InvalidConfig("maximum_page_text_bytes"));
    }
    if config.maximum_document_text_bytes < config.maximum_page_text_bytes {
        return Err(PdfParseError::InvalidConfig("maximum_document_text_bytes"));
    }
    if config.maximum_xobject_depth == 0 {
        return Err(PdfParseError::InvalidConfig("maximum_xobject_depth"));
    }
    Ok(())
}

fn validate_ascii_id(value: &str, field: &'static str) -> Result<(), PdfParseError> {
    if value.is_empty()
        || !value.is_ascii()
        || !value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'.' | b'_' | b':' | b'-'))
    {
        return Err(PdfParseError::InvalidIdentity(field));
    }
    Ok(())
}

fn validate_sha256(value: &str, field: &'static str) -> Result<(), PdfParseError> {
    if value.len() != 64
        || !value
            .bytes()
            .all(|byte| byte.is_ascii_hexdigit() && !byte.is_ascii_uppercase())
    {
        return Err(PdfParseError::InvalidIdentity(field));
    }
    Ok(())
}

fn sha256_file(path: &Path) -> Result<String, PdfParseError> {
    let file = File::open(path).map_err(|error| PdfParseError::FileRead {
        path: path.to_owned(),
        detail: error.to_string(),
    })?;
    let mut reader = BufReader::with_capacity(1024 * 1024, file);
    let mut digest = Sha256::new();
    let mut buffer = vec![0u8; 1024 * 1024];
    loop {
        let count = reader
            .read(&mut buffer)
            .map_err(|error| PdfParseError::FileRead {
                path: path.to_owned(),
                detail: error.to_string(),
            })?;
        if count == 0 {
            break;
        }
        digest.update(&buffer[..count]);
    }
    Ok(format!("{:x}", digest.finalize()))
}

fn config_fingerprint(config: &PdfParserConfig) -> String {
    let canonical = format!(
        "schema={SCHEMA_VERSION}\nminimum_text_characters={}\nmaximum_pages={}\nmaximum_page_text_bytes={}\nmaximum_document_text_bytes={}\nmaximum_xobject_depth={}\n",
        config.minimum_text_characters,
        config.maximum_pages,
        config.maximum_page_text_bytes,
        config.maximum_document_text_bytes,
        config.maximum_xobject_depth,
    );
    format!("{:x}", Sha256::digest(canonical.as_bytes()))
}

fn micros(duration: std::time::Duration) -> u64 {
    duration.as_micros().min(u128::from(u64::MAX)) as u64
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn classifies_text_scan_and_sparse_without_false_completion() {
        assert_eq!(classify_page(80, 0, 80), PdfPageStatus::Text);
        assert_eq!(classify_page(20, 1, 80), PdfPageStatus::NeedsOcr);
        assert_eq!(classify_page(20, 0, 80), PdfPageStatus::Sparse);
    }

    #[test]
    fn document_status_preserves_partial_and_failed_pages() {
        let page = |status| PdfPageResult {
            page_number: 1,
            start_byte: 0,
            end_byte: 0,
            image_objects: 0,
            elapsed_microseconds: 0,
            status,
        };
        assert_eq!(
            document_status(&[page(PdfPageStatus::Text)]),
            PdfDocumentStatus::Complete
        );
        assert_eq!(
            document_status(&[page(PdfPageStatus::NeedsOcr)]),
            PdfDocumentStatus::Partial
        );
        assert_eq!(
            document_status(&[page(PdfPageStatus::Failed {
                code: "x",
                detail: "y".into()
            })]),
            PdfDocumentStatus::Failed
        );
    }

    #[test]
    fn rejects_ambiguous_id_hash_and_config() {
        assert!(validate_ascii_id("source:abc-1", "source").is_ok());
        assert!(validate_ascii_id("../source", "source").is_err());
        assert!(validate_sha256(&"a".repeat(64), "hash").is_ok());
        assert!(validate_sha256(&"A".repeat(64), "hash").is_err());
        let mut config = PdfParserConfig::default();
        config.maximum_document_text_bytes = config.maximum_page_text_bytes - 1;
        assert!(validate_config(&config).is_err());
    }

    #[test]
    fn config_fingerprint_changes_with_parser_limits() {
        let first = PdfParserConfig::default();
        let mut second = first.clone();
        second.minimum_text_characters += 1;
        assert_ne!(config_fingerprint(&first), config_fingerprint(&second));
    }

    #[test]
    fn normalizes_translated_page_origin_and_intrinsic_rotation() {
        let page = PdfRect::new_from_values(200.0, 100.0, 400.0, 300.0);
        let text = PdfRect::new_from_values(340.0, 150.0, 360.0, 170.0);
        let unrotated = normalize_bounds(text, page, PdfPageRenderRotation::None).unwrap();
        assert_bbox(unrotated, [0.25, 0.20, 0.35, 0.30]);

        let clockwise = normalize_bounds(text, page, PdfPageRenderRotation::Degrees90).unwrap();
        assert_bbox(clockwise, [0.70, 0.25, 0.80, 0.35]);
    }

    #[test]
    fn clips_crop_box_to_media_box_before_normalizing() {
        let media = PdfRect::new_from_values(200.0, 100.0, 400.0, 300.0);
        let oversized_crop = PdfRect::new_from_values(0.0, 0.0, 500.0, 500.0);
        assert_eq!(intersect_bounds(media, oversized_crop), Some(media));

        let crop = PdfRect::new_from_values(220.0, 120.0, 380.0, 280.0);
        assert_eq!(intersect_bounds(media, crop), Some(crop));

        let outside = PdfRect::new_from_values(0.0, 0.0, 50.0, 50.0);
        assert!(intersect_bounds(media, outside).is_none());
    }

    fn assert_bbox(actual: NormalizedBoundingBox, expected: [f32; 4]) {
        for (actual, expected) in [actual.x0, actual.y0, actual.x1, actual.y1]
            .into_iter()
            .zip(expected)
        {
            assert!((actual - expected).abs() < 0.0001, "{actual} != {expected}");
        }
    }
}
