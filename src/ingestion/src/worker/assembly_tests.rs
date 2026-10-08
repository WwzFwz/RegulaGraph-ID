//! Executes ASSEMBLE through the real worker processor and immutable file store.
//! Synthetic source/registry decisions are reused from builder fixtures; these
//! tests verify artifact/hash/context/checkpoint/cancellation behavior, not legal
//! model quality, durable Go receipt authority or backend graph publication.

use super::{assembly, BatchProcessor, ParseBatchProcessor, ParseBatchProcessorConfig};
use crate::{
    adapters::storage::{ArtifactStore, ArtifactStoreConfig},
    document::chunking::builder::TokenCounter,
    knowledge_graph::assembly::{
        delta_tests::{fixture, ontology},
        inputs,
    },
    wire::{common, graph, jobs},
};
use protobuf::{EnumOrUnknown, Message, MessageField};
use std::{
    path::PathBuf,
    sync::{
        atomic::{AtomicBool, AtomicU64, Ordering},
        Arc,
    },
};
use tonic::Code;

struct TestDir(PathBuf);
impl TestDir {
    fn new() -> Self {
        static SEQUENCE: AtomicU64 = AtomicU64::new(1);
        let root = std::env::temp_dir().join(format!(
            "regulagraph-assembly-{}-{}",
            std::process::id(),
            SEQUENCE.fetch_add(1, Ordering::Relaxed)
        ));
        std::fs::create_dir_all(&root).unwrap();
        Self(root)
    }
}
impl Drop for TestDir {
    fn drop(&mut self) {
        let _ = std::fs::remove_dir_all(&self.0);
    }
}
struct UnusedTokenizer;
impl TokenCounter for UnusedTokenizer {
    fn tokenizer_id(&self) -> &str {
        "fixture:unused"
    }
    fn count_tokens(&self, _: &str) -> Result<u32, String> {
        panic!("ASSEMBLE must not tokenize")
    }
}
fn persist<M: Message>(
    store: &ArtifactStore,
    message: &M,
    id: &str,
    media: &str,
) -> common::ArtifactRef {
    let mut reference = store
        .put_bytes(
            "assembly-test",
            media,
            1,
            &message.write_to_bytes().unwrap(),
        )
        .unwrap()
        .to_wire_ref()
        .unwrap();
    reference.artifact_id = id.into();
    reference
}
fn fixture_request(processor: &ParseBatchProcessor) -> jobs::ProcessBatchRequest {
    let mut f = fixture();
    let (mut plan, view) = f.plan_and_view();
    let reference = processor
        .store
        .put_bytes(
            "normalized-text",
            crate::domain::text_artifact_wire::TEXT_MEDIA_TYPE,
            1,
            f.texts.values().next().unwrap(),
        )
        .unwrap()
        .to_wire_ref()
        .unwrap();
    f.docs.text_artifacts[0].normalized_text_ref = MessageField::some(reference.clone());
    for version in &mut f.docs.versions {
        version.text_ref = MessageField::some(reference.clone());
    }
    plan.document_batch = MessageField::some(persist(
        &processor.store,
        &f.docs,
        "artifact:document",
        "application/vnd.regulagraph.document-batch+protobuf",
    ));
    for dependency in &mut f.extraction.dependencies.as_mut().unwrap().dependencies {
        if dependency.dependency_id == f.extraction.source_document_batch.artifact_id {
            dependency.dependency_id = plan.document_batch.artifact_id.clone();
            dependency.fingerprint = plan.document_batch.content_hash.clone();
        }
    }
    f.extraction.source_document_batch = plan.document_batch.clone();
    plan.extraction_batch = MessageField::some(persist(
        &processor.store,
        &f.extraction,
        "artifact:extract",
        "application/vnd.regulagraph.extraction-batch+protobuf",
    ));
    f.resolution.source_extraction_batch = plan.extraction_batch.clone();
    plan.resolution_batch = MessageField::some(persist(
        &processor.store,
        &f.resolution,
        "artifact:resolution",
        "application/x-protobuf",
    ));
    plan.registry_view = MessageField::some(persist(
        &processor.store,
        &view,
        &view.meta.record_id,
        inputs::REGISTRY_ENTITY_VIEW_MEDIA_TYPE,
    ));
    let reference = persist(
        &processor.store,
        &plan,
        &plan.meta.record_id,
        inputs::GRAPH_ASSEMBLY_PLAN_MEDIA_TYPE,
    );
    jobs::ProcessBatchRequest {
        context: plan.context.clone(),
        job_id: "job:assembly".into(),
        attempt: 1,
        lease: MessageField::some(jobs::Lease {
            owner_id: "worker:fixture".into(),
            fence: 1,
            expires_at: plan.context.deadline.clone(),
            ..Default::default()
        }),
        sources: vec![
            (*plan.document_batch).clone(),
            (*plan.extraction_batch).clone(),
            (*plan.resolution_batch).clone(),
            (*plan.registry_view).clone(),
        ],
        manifest: plan.producer_manifest,
        stages: vec![EnumOrUnknown::new(jobs::JobStage::JOB_STAGE_ASSEMBLE)],
        graph_assembly_plan: MessageField::some(reference),
        ..Default::default()
    }
}
fn processor(root: &TestDir, configured: bool) -> ParseBatchProcessor {
    let store = ArtifactStore::open(
        &root.0,
        ArtifactStoreConfig {
            maximum_artifact_bytes: 16 << 20,
            sync_data: false,
        },
    )
    .unwrap();
    let p = ParseBatchProcessor::new(
        store,
        None,
        Arc::new(UnusedTokenizer),
        ParseBatchProcessorConfig::default(),
    )
    .unwrap();
    if configured {
        p.with_assembly(Arc::new(ontology()))
    } else {
        p
    }
}

#[test]
fn assembly_worker_persists_deterministic_delta_and_bound_checkpoint() {
    let root = TestDir::new();
    let p = processor(&root, true);
    let request = fixture_request(&p);
    let cancelled = AtomicBool::new(false);
    let output = p
        .process(request.clone(), &cancelled, &|_, _, _| {})
        .unwrap();
    let replay = p.process(request, &cancelled, &|_, _, _| {}).unwrap();
    assert_eq!(output, replay);
    assert_eq!(
        output.status.enum_value(),
        Ok(common::CompletionStatus::COMPLETION_STATUS_SUCCEEDED)
    );
    assert_eq!(
        output.checkpoint.stage.enum_value(),
        Ok(jobs::JobStage::JOB_STAGE_ASSEMBLE)
    );
    assert_eq!(
        output.checkpoint.completed_batch_keys,
        vec![output.graph_delta.artifact_id.clone()]
    );
    assert_eq!(
        output.checkpoint.artifact_hashes,
        vec![(*output.graph_delta.content_hash).clone()]
    );
    assert_eq!(
        output.graph_delta.media_type,
        assembly::GRAPH_DELTA_MEDIA_TYPE
    );
    let descriptor =
        crate::adapters::storage::ArtifactDescriptor::from_wire_ref(&output.graph_delta).unwrap();
    let delta =
        graph::GraphDelta::parse_from_bytes(&p.store.read_verified(&descriptor).unwrap()).unwrap();
    assert_eq!(delta.meta.record_id, "delta:fixture");
    assert_eq!(delta.registry_revision, 7);
    assert_eq!(delta.assertions.len(), 1);
    assert_eq!(delta.entities.len(), 2);
    assert!(delta.validation_report.valid);
    assert_eq!(delta.entities[0].meta.visibility.from_seq, 2);
}

#[test]
fn assembly_worker_rejects_role_hash_scope_drift_and_cancellation() {
    let root = TestDir::new();
    let p = processor(&root, true);
    let request = fixture_request(&p);
    for mutate in [
        (|r: &mut jobs::ProcessBatchRequest| r.sources.swap(0, 1))
            as fn(&mut jobs::ProcessBatchRequest),
        |r| r.context.as_mut().unwrap().auth_scope_ref = "scope:other".into(),
        |r| r.context.as_mut().unwrap().corpus_id = "foreign".into(),
        |r| r.manifest.as_mut().unwrap().build = "different".into(),
        |r| {
            r.graph_assembly_plan
                .as_mut()
                .unwrap()
                .content_hash
                .as_mut()
                .unwrap()
                .sha256 = "0".repeat(64)
        },
        |r| r.graph_assembly_plan.as_mut().unwrap().artifact_id = "plan:other".into(),
        |r| r.graph_assembly_plan.as_mut().unwrap().byte_size = 17 << 20,
        |r| r.graph_assembly_plan = MessageField::none(),
        |r| r.index_build_plan = r.graph_assembly_plan.clone(),
    ] {
        let mut bad = request.clone();
        mutate(&mut bad);
        assert!(p
            .process(bad, &AtomicBool::new(false), &|_, _, _| {})
            .is_err());
    }
    assert_eq!(
        p.process(request.clone(), &AtomicBool::new(true), &|_, _, _| {})
            .unwrap_err()
            .code(),
        Code::Cancelled
    );
    let cancelled = AtomicBool::new(false);
    assert_eq!(
        p.process(request.clone(), &cancelled, &|_, done, _| {
            if done == 1 {
                cancelled.store(true, Ordering::Release)
            }
        })
        .unwrap_err()
        .code(),
        Code::Cancelled
    );
    let unconfigured = processor(&root, false);
    assert_eq!(
        unconfigured
            .process(request, &AtomicBool::new(false), &|_, _, _| {})
            .unwrap_err()
            .code(),
        Code::FailedPrecondition
    );
}

#[test]
fn assembly_worker_rejects_corrupt_source_bytes_and_aggregate_read_overflow() {
    let root = TestDir::new();
    let p = processor(&root, true);
    let request = fixture_request(&p);
    let plan_bytes = std::fs::read(root.0.join(&request.graph_assembly_plan.storage_key)).unwrap();
    let mut plan = graph::GraphAssemblyPlan::parse_from_bytes(&plan_bytes).unwrap();
    plan.document_batch.as_mut().unwrap().byte_size = 16 << 20;
    let mut too_large = request.clone();
    too_large.sources[0] = (*plan.document_batch).clone();
    too_large.graph_assembly_plan = MessageField::some(persist(
        &p.store,
        &plan,
        &plan.meta.record_id,
        inputs::GRAPH_ASSEMBLY_PLAN_MEDIA_TYPE,
    ));
    assert_eq!(
        p.process(too_large, &AtomicBool::new(false), &|_, _, _| {})
            .unwrap_err()
            .code(),
        Code::InvalidArgument
    );

    // Tamper bytes at the exact content-addressed key, leaving request refs unchanged.
    let source_path = root.0.join(&request.sources[0].storage_key);
    let mut bytes = std::fs::read(&source_path).unwrap();
    bytes[0] ^= 1;
    std::fs::write(source_path, bytes).unwrap();
    assert_eq!(
        p.process(request, &AtomicBool::new(false), &|_, _, _| {})
            .unwrap_err()
            .code(),
        Code::FailedPrecondition
    );
}

#[tokio::test]
async fn assembly_worker_service_bridges_plan_and_replays_exact_request() {
    use super::transport::worker_server::Worker;
    let root = TestDir::new();
    let p = processor(&root, true);
    let mut request = fixture_request(&p);
    let deadline = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap()
        .as_secs() as i64
        + 60;
    request
        .context
        .as_mut()
        .unwrap()
        .deadline
        .as_mut()
        .unwrap()
        .seconds = deadline;
    request
        .lease
        .as_mut()
        .unwrap()
        .expires_at
        .as_mut()
        .unwrap()
        .seconds = deadline;
    let transport: super::transport::ProcessBatchRequest =
        prost::Message::decode(request.write_to_bytes().unwrap().as_slice()).unwrap();
    let service = super::WorkerService::new(p, super::WorkerServiceConfig::default()).unwrap();
    let first = service
        .process_batch(tonic::Request::new(transport.clone()))
        .await
        .unwrap()
        .into_inner();
    let replay = service
        .process_batch(tonic::Request::new(transport.clone()))
        .await
        .unwrap()
        .into_inner();
    assert_eq!(first, replay);
    let result =
        jobs::ProcessBatchResponse::parse_from_bytes(&prost::Message::encode_to_vec(&first))
            .unwrap();
    assert_eq!(
        result.checkpoint.stage.enum_value(),
        Ok(jobs::JobStage::JOB_STAGE_ASSEMBLE)
    );
    assert_eq!(
        result.graph_delta.content_hash,
        MessageField::some(result.checkpoint.artifact_hashes[0].clone())
    );
    let mut altered = transport;
    altered.graph_assembly_plan.as_mut().unwrap().artifact_id = "plan:other".into();
    assert_eq!(
        service
            .process_batch(tonic::Request::new(altered))
            .await
            .unwrap_err()
            .code(),
        Code::FailedPrecondition
    );
}
