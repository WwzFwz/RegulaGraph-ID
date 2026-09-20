//! Executable loopback gRPC worker for verified PDF parse batches.
//!
//! Configuration is explicit through REGULAGRAPH_WORKER_* environment variables. The process binds
//! PDFium and one pinned Hugging Face tokenizer once, serves bounded C01 messages, and writes immutable outputs under the shared artifact root.
//! Production remote transport requires a TLS front end; this binary refuses non-loopback listeners.

use protobuf::{EnumOrUnknown, MessageField};
use regulagraph_ingestion::adapters::inference::{SemanticClientConfig, TonicSemanticClient};
use regulagraph_ingestion::adapters::storage::{ArtifactStore, ArtifactStoreConfig};
use regulagraph_ingestion::document::chunking::tokenizer::HuggingFaceTokenizer;
use regulagraph_ingestion::document::parsing::pdf::{PdfParser, PdfParserConfig};
use regulagraph_ingestion::knowledge_graph::extraction::extractor::ExtractionBatchConfig;
use regulagraph_ingestion::wire::common;
use regulagraph_ingestion::worker::transport::worker_server::WorkerServer;
use regulagraph_ingestion::worker::{
    ExtractionRuntimeConfig, ParseBatchProcessor, ParseBatchProcessorConfig, WorkerService,
    WorkerServiceConfig,
};
use std::env;
use std::error::Error;
use std::fs;
use std::net::SocketAddr;
use std::path::PathBuf;
use std::sync::Arc;
use tonic::transport::Server;

const DEFAULT_MAX_MESSAGE_BYTES: usize = 16 << 20;

#[tokio::main]
async fn main() -> Result<(), Box<dyn Error>> {
    let listen: SocketAddr = optional("REGULAGRAPH_WORKER_LISTEN", "127.0.0.1:50051").parse()?;
    if !listen.ip().is_loopback() {
        return Err(
            "REGULAGRAPH_WORKER_LISTEN must be loopback until TLS termination is configured".into(),
        );
    }
    let artifact_root = PathBuf::from(required("REGULAGRAPH_WORKER_ARTIFACT_ROOT")?);
    let pdfium_library = PathBuf::from(required("REGULAGRAPH_WORKER_PDFIUM_LIBRARY")?);
    let pdfium_sha256 = required("REGULAGRAPH_WORKER_PDFIUM_SHA256")?;
    let pdfium_version = required("REGULAGRAPH_WORKER_PDFIUM_VERSION")?;
    let tokenizer_json = PathBuf::from(required("REGULAGRAPH_WORKER_TOKENIZER_JSON")?);
    let tokenizer_sha256 = required("REGULAGRAPH_WORKER_TOKENIZER_SHA256")?;
    let maximum_message_bytes = parse_positive(
        "REGULAGRAPH_WORKER_MAX_MESSAGE_BYTES",
        DEFAULT_MAX_MESSAGE_BYTES,
    )?;
    let maximum_artifact_bytes =
        parse_positive("REGULAGRAPH_WORKER_MAX_ARTIFACT_BYTES", 512 * 1024 * 1024)?;
    let maximum_concurrent_batches =
        parse_positive("REGULAGRAPH_WORKER_MAX_CONCURRENT_BATCHES", 2)?;
    let maximum_jobs = parse_positive("REGULAGRAPH_WORKER_MAX_JOBS", 10_000)?;

    let store = ArtifactStore::open(
        &artifact_root,
        ArtifactStoreConfig {
            maximum_artifact_bytes,
            sync_data: true,
        },
    )?;
    let extraction = if let Some(endpoint) = configured("REGULAGRAPH_WORKER_SEMANTIC_ENDPOINT") {
        let schema_path = PathBuf::from(required("REGULAGRAPH_WORKER_EXTRACTION_OUTPUT_SCHEMA")?);
        let schema_bytes = fs::read(&schema_path)?;
        let schema_ref = store
            .put_bytes(
                "extraction-output-schema",
                "application/schema+json",
                1,
                &schema_bytes,
            )?
            .to_wire_ref()?;
        let prompt_hash = content_hash(required("REGULAGRAPH_WORKER_EXTRACTION_PROMPT_SHA256")?);
        let model = common::ModelManifest {
            model_id: required("REGULAGRAPH_WORKER_EXTRACTION_MODEL_ID")?,
            version: required("REGULAGRAPH_WORKER_EXTRACTION_MODEL_VERSION")?,
            weights_hash: MessageField::some(content_hash(required(
                "REGULAGRAPH_WORKER_EXTRACTION_WEIGHTS_SHA256",
            )?)),
            tokenizer_hash: MessageField::some(content_hash(required(
                "REGULAGRAPH_WORKER_EXTRACTION_TOKENIZER_SHA256",
            )?)),
            task: EnumOrUnknown::new(common::ModelTask::MODEL_TASK_EXTRACT),
            max_tokens: u32::try_from(parse_positive(
                "REGULAGRAPH_WORKER_EXTRACTION_MAX_TOKENS",
                8192,
            )?)?,
            precision: required("REGULAGRAPH_WORKER_EXTRACTION_PRECISION")?,
            backend: required("REGULAGRAPH_WORKER_EXTRACTION_BACKEND")?,
            prompt_hash: MessageField::some(prompt_hash),
            ..Default::default()
        };
        let client = TonicSemanticClient::connect(
            &endpoint,
            SemanticClientConfig {
                maximum_message_bytes,
                ..Default::default()
            },
        )
        .await?;
        Some((
            Arc::new(client),
            ExtractionRuntimeConfig {
                model,
                ontology_version: required("REGULAGRAPH_WORKER_EXTRACTION_ONTOLOGY_VERSION")?,
                output_schema: schema_ref,
                batch: ExtractionBatchConfig::default(),
                maximum_items_per_rpc: parse_positive(
                    "REGULAGRAPH_WORKER_EXTRACTION_MAX_ITEMS_PER_RPC",
                    256,
                )?,
                maximum_input_bytes_per_rpc: parse_positive(
                    "REGULAGRAPH_WORKER_EXTRACTION_MAX_INPUT_BYTES_PER_RPC",
                    4 * 1024 * 1024,
                )?,
            },
        ))
    } else {
        None
    };
    let parser = PdfParser::bind(
        &pdfium_library,
        &pdfium_sha256,
        pdfium_version,
        PdfParserConfig::default(),
    )?;
    let tokenizer = Arc::new(HuggingFaceTokenizer::from_file(
        tokenizer_json,
        &tokenizer_sha256,
    )?);
    let processor = ParseBatchProcessor::new(
        store,
        Some(parser),
        tokenizer,
        ParseBatchProcessorConfig::default(),
    )?;
    let processor = if let Some((client, config)) = extraction {
        processor.with_extraction(client, config)?
    } else {
        processor
    };
    let service = WorkerService::new(
        processor,
        WorkerServiceConfig {
            maximum_jobs,
            maximum_concurrent_batches,
        },
    )?;
    let worker = WorkerServer::new(service)
        .max_decoding_message_size(maximum_message_bytes)
        .max_encoding_message_size(maximum_message_bytes);

    eprintln!("regulagraph worker listening on {listen}");
    Server::builder()
        .add_service(worker)
        .serve_with_shutdown(listen, shutdown_signal())
        .await?;
    Ok(())
}

async fn shutdown_signal() {
    if let Err(error) = tokio::signal::ctrl_c().await {
        eprintln!("worker shutdown signal failed: {error}");
    }
}

fn required(name: &str) -> Result<String, Box<dyn Error>> {
    match env::var(name) {
        Ok(value) if !value.trim().is_empty() => Ok(value),
        _ => Err(format!("{name} is required").into()),
    }
}

fn optional(name: &str, default: &str) -> String {
    env::var(name)
        .ok()
        .filter(|value| !value.trim().is_empty())
        .unwrap_or_else(|| default.to_owned())
}

fn configured(name: &str) -> Option<String> {
    env::var(name).ok().filter(|value| !value.trim().is_empty())
}

fn content_hash(sha256: String) -> common::ContentHash {
    common::ContentHash {
        sha256,
        ..Default::default()
    }
}

fn parse_positive(name: &str, default: usize) -> Result<usize, Box<dyn Error>> {
    let value = optional(name, &default.to_string()).parse::<usize>()?;
    if value == 0 {
        return Err(format!("{name} must be positive").into());
    }
    Ok(value)
}
