//! Integration test boundary PDFium Rust terhadap binary native dan PDF yang dibentuk deterministik.
//!
//! Peran: membuktikan dynamic binding, hash binding, extraction, locator, manifest, dan error input nyata.
//! Input/output: environment menunjuk binary PDFium; fixture ditulis ke target/test-fixtures dan dihapus.
//! Performa: correctness fixture bukan benchmark; corpus throughput/RSS tetap REQUIRED_UNMEASURED.

use regulagraph_ingestion::document::parsing::pdf::{
    PdfDocumentStatus, PdfParseError, PdfParseRequest, PdfParser, PdfParserConfig,
};
use sha2::{Digest, Sha256};
use std::fs;
use std::path::{Path, PathBuf};

#[test]
fn pdfium_boundary_parses_and_binds_real_inputs() {
    let Some(library_path) = std::env::var_os("REGULAGRAPH_PDFIUM_LIBRARY").map(PathBuf::from)
    else {
        eprintln!("SKIP: REGULAGRAPH_PDFIUM_LIBRARY is not set");
        return;
    };
    let Some(core_version) = std::env::var_os("REGULAGRAPH_PDFIUM_CORE_VERSION")
        .map(|value| value.to_string_lossy().into_owned())
    else {
        eprintln!("SKIP: REGULAGRAPH_PDFIUM_CORE_VERSION is not set");
        return;
    };
    let library_sha256 = sha256_file(&library_path);
    let config = PdfParserConfig {
        minimum_text_characters: 5,
        ..PdfParserConfig::default()
    };
    let parser = PdfParser::bind(&library_path, &library_sha256, &core_version, config)
        .expect("pinned PDFium library must bind");
    let second_bind = PdfParser::bind(
        &library_path,
        &library_sha256,
        &core_version,
        PdfParserConfig::default(),
    );
    assert!(matches!(
        second_bind,
        Err(PdfParseError::AlreadyBound { .. })
    ));

    let fixture_dir = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../target/test-fixtures");
    fs::create_dir_all(&fixture_dir).expect("fixture directory must be writable");
    let fixture = fixture_dir.join(format!("pdfium-parser-{}.pdf", std::process::id()));
    let malformed = fixture_dir.join(format!("pdfium-malformed-{}.pdf", std::process::id()));
    fs::write(
        &fixture,
        minimal_text_pdf("Pasal 1 berlaku untuk semua pihak."),
    )
    .expect("fixture PDF must be writable");
    fs::write(&malformed, b"not a pdf").expect("malformed fixture must be writable");
    let input_sha256 = sha256_file(&fixture);

    let result = parser
        .parse_file(PdfParseRequest {
            source_id: "source:test-pdf".into(),
            source_blob_id: "blob:test-pdf".into(),
            expected_sha256: input_sha256.clone(),
            path: fixture.clone(),
        })
        .expect("valid fixture must parse");
    assert_eq!(result.status, PdfDocumentStatus::Complete);
    assert_eq!(result.pages.len(), 1);
    assert!(result.raw_text.contains("Pasal 1"));
    assert_eq!(result.pages[0].start_byte, 0);
    assert_eq!(result.pages[0].end_byte as usize, result.raw_text.len());
    assert!(!result.blocks.is_empty());
    for block in &result.blocks {
        let value = &result.raw_text[block.start_byte as usize..block.end_byte as usize];
        assert!(!value.is_empty());
        assert!(block.bounding_box.x0 <= block.bounding_box.x1);
        assert!(block.bounding_box.y0 <= block.bounding_box.y1);
        assert!((0.0..=1.0).contains(&block.bounding_box.x0));
        assert!((0.0..=1.0).contains(&block.bounding_box.y1));
    }
    assert_eq!(result.manifest.library_sha256, library_sha256);
    assert_eq!(result.manifest.input_sha256, input_sha256);
    assert_eq!(result.manifest.declared_core_version, core_version);
    assert_eq!(result.manifest.config_sha256.len(), 64);

    let mismatch = parser.parse_file(PdfParseRequest {
        source_id: "source:test-pdf".into(),
        source_blob_id: "blob:test-pdf".into(),
        expected_sha256: "0".repeat(64),
        path: fixture.clone(),
    });
    assert!(matches!(mismatch, Err(PdfParseError::HashMismatch { .. })));

    let malformed_result = parser.parse_file(PdfParseRequest {
        source_id: "source:malformed".into(),
        source_blob_id: "blob:malformed".into(),
        expected_sha256: sha256_file(&malformed),
        path: malformed.clone(),
    });
    assert!(matches!(malformed_result, Err(PdfParseError::Open(_))));

    fs::remove_file(fixture).expect("fixture PDF must be removable");
    fs::remove_file(malformed).expect("malformed fixture must be removable");
}

fn sha256_file(path: &Path) -> String {
    format!(
        "{:x}",
        Sha256::digest(fs::read(path).expect("test input must be readable"))
    )
}

fn minimal_text_pdf(text: &str) -> Vec<u8> {
    assert!(text.is_ascii() && !text.contains(['(', ')', '\\']));
    let content = format!("BT /F1 12 Tf 72 720 Td ({text}) Tj ET");
    let objects = [
        "<< /Type /Catalog /Pages 2 0 R >>".to_owned(),
        "<< /Type /Pages /Kids [3 0 R] /Count 1 >>".to_owned(),
        "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>".to_owned(),
        format!("<< /Length {} >>\nstream\n{content}\nendstream", content.len()),
        "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>".to_owned(),
    ];
    let mut result = b"%PDF-1.4\n".to_vec();
    let mut offsets = Vec::with_capacity(objects.len());
    for (index, object) in objects.iter().enumerate() {
        offsets.push(result.len());
        result.extend_from_slice(format!("{} 0 obj\n{}\nendobj\n", index + 1, object).as_bytes());
    }
    let xref = result.len();
    result.extend_from_slice(
        format!("xref\n0 {}\n0000000000 65535 f \n", objects.len() + 1).as_bytes(),
    );
    for offset in offsets {
        result.extend_from_slice(format!("{offset:010} 00000 n \n").as_bytes());
    }
    result.extend_from_slice(
        format!(
            "trailer\n<< /Size {} /Root 1 0 R >>\nstartxref\n{xref}\n%%EOF\n",
            objects.len() + 1
        )
        .as_bytes(),
    );
    result
}
