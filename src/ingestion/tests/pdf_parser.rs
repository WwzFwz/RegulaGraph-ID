//! Integration test boundary PDFium Rust terhadap binary native dan PDF yang dibentuk deterministik.
//!
//! Peran: membuktikan dynamic binding, hash binding, extraction, locator, manifest, dan error input nyata.
//! Input/output: environment menunjuk binary PDFium; fixture ditulis ke target/test-fixtures dan dihapus.
//! Performa: correctness fixture bukan benchmark; corpus throughput/RSS tetap REQUIRED_UNMEASURED.

use protobuf::well_known_types::timestamp::Timestamp;
use protobuf::{EnumOrUnknown, MessageField};
use regulagraph_ingestion::adapters::document_batches::load_document_batch;
use regulagraph_ingestion::adapters::storage::{
    ArtifactDescriptor, ArtifactStore, ArtifactStoreConfig,
};
use regulagraph_ingestion::document::chunking::builder::TokenCounter;
use regulagraph_ingestion::document::normalization::text::TextNormalizerConfig;
use regulagraph_ingestion::document::parsing::pdf::{
    PdfDocumentStatus, PdfParseError, PdfParseRequest, PdfParser, PdfParserConfig,
};
use regulagraph_ingestion::domain::document_batch::DocumentBatchConfig;
use regulagraph_ingestion::wire::{common, documents, jobs};
use regulagraph_ingestion::worker::{
    BatchProcessor, ParseBatchProcessor, ParseBatchProcessorConfig,
};
use sha2::{Digest, Sha256};
use std::fs;
use std::path::{Path, PathBuf};
use std::sync::atomic::AtomicBool;
use std::sync::Arc;

struct FixtureTokenizer;

impl TokenCounter for FixtureTokenizer {
    fn tokenizer_id(&self) -> &str {
        "test:fixture-words-v1"
    }

    fn count_tokens(&self, text: &str) -> Result<u32, String> {
        u32::try_from(text.split_whitespace().count()).map_err(|error| error.to_string())
    }
}

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

    let artifact_root = fixture_dir.join(format!("worker-artifacts-{}", std::process::id()));
    let store = ArtifactStore::open(
        &artifact_root,
        ArtifactStoreConfig {
            maximum_artifact_bytes: 16 * 1024 * 1024,
            sync_data: false,
        },
    )
    .expect("worker artifact store opens");
    let input = store
        .put_bytes(
            "source-pdf",
            "application/pdf",
            1,
            &fs::read(&fixture).unwrap(),
        )
        .expect("source PDF enters shared artifact store")
        .to_wire_ref()
        .unwrap();
    let processor = ParseBatchProcessor::new(
        store,
        Some(parser),
        Arc::new(FixtureTokenizer),
        ParseBatchProcessorConfig {
            normalizer: TextNormalizerConfig::default(),
            document_batch: DocumentBatchConfig::default(),
            maximum_pages_per_document: 100,
            ..ParseBatchProcessorConfig::default()
        },
    )
    .unwrap();
    let response = processor
        .process(
            worker_request(input.clone()),
            &AtomicBool::new(false),
            &|_, _, _| {},
        )
        .expect("parse batch produces an immutable document batch");
    assert_eq!(
        response.status.enum_value(),
        Ok(common::CompletionStatus::COMPLETION_STATUS_SUCCEEDED)
    );
    let output = response.document_batch.as_ref().unwrap();
    let checkpoint = response.checkpoint.as_ref().unwrap();
    assert_eq!(checkpoint.job_id, "job:pdf-worker-test");
    assert_eq!(
        checkpoint.completed_batch_keys,
        [output.artifact_id.clone()]
    );
    assert_eq!(
        checkpoint.artifact_hashes,
        [output.content_hash.as_ref().unwrap().clone()]
    );
    assert_eq!(checkpoint.manifest.software, "regulagraph-ingestion");
    assert_ne!(checkpoint.manifest.build, "pdf-worker-test");
    let output_descriptor = ArtifactDescriptor {
        artifact_id: output.artifact_id.clone(),
        sha256: output.content_hash.sha256.clone(),
        storage_key: output.storage_key.clone(),
        media_type: output.media_type.clone(),
        byte_size: output.byte_size,
        schema_version: output.schema_version,
    };
    let reopened = ArtifactStore::open(
        &artifact_root,
        ArtifactStoreConfig {
            maximum_artifact_bytes: 16 * 1024 * 1024,
            sync_data: false,
        },
    )
    .unwrap();
    let batch = load_document_batch(&reopened, &output_descriptor).unwrap();
    assert_eq!(batch.sources.len(), 1);
    assert_eq!(batch.text_artifacts.len(), 1);
    assert!(batch.structures.is_empty());
    assert!(batch.provisions.is_empty());
    assert!(batch.versions.is_empty());
    assert!(batch.chunks.is_empty());
    assert_eq!(batch.observations.len(), 1);
    assert_eq!(
        batch.observations[0].source_blob_id.as_deref(),
        Some(batch.sources[0].meta.record_id.as_str())
    );

    let mut forged = worker_request(input);
    forged.observations[0].source_blob_id = Some(format!("source-blob:{}", "0".repeat(64)));
    let forged_error = processor
        .process(forged, &AtomicBool::new(false), &|_, _, _| {})
        .expect_err("observation bound to unrelated bytes must be rejected");
    assert_eq!(forged_error.code(), tonic::Code::InvalidArgument);

    fs::remove_file(fixture).expect("fixture PDF must be removable");
    fs::remove_file(malformed).expect("malformed fixture must be removable");
    fs::remove_dir_all(artifact_root).expect("worker artifact fixture must be removable");
}

fn worker_request(source: common::ArtifactRef) -> jobs::ProcessBatchRequest {
    let hash = |character: char| common::ContentHash {
        sha256: character.to_string().repeat(64),
        ..Default::default()
    };
    let source_blob_id = format!("source-blob:{}", source.content_hash.sha256);
    jobs::ProcessBatchRequest {
        context: MessageField::some(common::RequestContext {
            schema_version: 1,
            request_id: "request:pdf-worker-test".to_owned(),
            trace_id: "trace:pdf-worker-test".to_owned(),
            corpus_id: "regulagraph-id".to_owned(),
            deadline: MessageField::some(Timestamp {
                seconds: 1_900_000_000,
                ..Default::default()
            }),
            config_fingerprint: MessageField::some(hash('a')),
            auth_scope_ref: "scope:ingestion-test".to_owned(),
            ..Default::default()
        }),
        job_id: "job:pdf-worker-test".to_owned(),
        attempt: 1,
        lease: MessageField::some(jobs::Lease {
            owner_id: "worker:test".to_owned(),
            fence: 1,
            expires_at: MessageField::some(Timestamp {
                seconds: 1_900_000_060,
                ..Default::default()
            }),
            ..Default::default()
        }),
        sources: vec![source],
        observations: vec![documents::SourceObservation {
            meta: MessageField::some(common::RecordMeta {
                schema_version: 1,
                corpus_id: "regulagraph-id".to_owned(),
                record_id: "source-observation:pdf-worker-test".to_owned(),
                ..Default::default()
            }),
            portal_id: "bpk".to_owned(),
            detail_url: "https://peraturan.bpk.go.id/Details/1/test".to_owned(),
            fetched_at: MessageField::some(Timestamp {
                seconds: 1_800_000_000,
                ..Default::default()
            }),
            status: EnumOrUnknown::new(documents::ObservationStatus::OBSERVATION_STATUS_COMPLETE),
            source_blob_id: Some(source_blob_id),
            ..Default::default()
        }],
        manifest: MessageField::some(common::ProducerManifest {
            software: "regulagraph-ingestion".to_owned(),
            build: "pdf-worker-test".to_owned(),
            schema_version: 1,
            parser_version: Some("pdfium-test".to_owned()),
            config_hash: MessageField::some(hash('b')),
            ..Default::default()
        }),
        stages: vec![EnumOrUnknown::new(jobs::JobStage::JOB_STAGE_PARSE)],
        ..Default::default()
    }
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
