//! Bounded gRPC transport and batch execution for the Rust ingestion worker.
//!
//! The module bridges Prost/Tonic transport messages to the existing descriptor-aware rust-protobuf
//! domain types through canonical Protobuf bytes. Go remains the durable job/publication owner.
//! Queue latency, RPC p95/p99, cancellation lag and peak RSS remain REQUIRED_UNMEASURED.

// Prost owns the generated oneof representation; boxing it locally would fork generated schema code.
#[allow(clippy::large_enum_variant)]
pub mod transport {
    tonic::include_proto!("regulagraph.v1");
}

mod processor;
mod service;

pub use processor::{ExtractionRuntimeConfig, ParseBatchProcessor, ParseBatchProcessorConfig};
pub use service::{BatchProcessor, ProcessError, WorkerService, WorkerServiceConfig};
