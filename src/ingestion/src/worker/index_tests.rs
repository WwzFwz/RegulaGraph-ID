//! CHUNK-to-INDEX integration with real immutable artifacts and synthetic dense
//! inference. Exercises all-or-error output, artifact bindings and cancellation;
//! optional exported bytes are consumed by the Go admission test. These fixtures
//! prove transport/data integration, not embedding quality or benchmark targets.

use super::service::ProcessError;
use super::{BatchProcessor, IndexEmbedding, ParseBatchProcessor};
use crate::{
    adapters::document_batches::persist_document_batch,
    domain::wire::Limits,
    indexing::{
        analyzer::{analyze_document, ANALYZER_VERSION},
        build,
        dictionary::LexicalDictionary,
        dictionary_artifact::CheckedDictionary,
        inputs::RENDER_POLICY_VERSION,
        lexical::Bm25Statistics,
        loading::VerifiedIndexInputs,
        statistics_artifact::build_analyzer,
    },
    wire::{common, documents, evidence, inference, jobs},
};
use protobuf::{EnumOrUnknown, Message, MessageField, MessageFull};
use std::{
    collections::BTreeSet,
    sync::{
        atomic::{AtomicBool, AtomicUsize, Ordering},
        Arc,
    },
};

struct FakeEmbedding {
    model: common::ModelManifest,
    calls: Arc<AtomicUsize>,
    partial: AtomicBool,
}
impl IndexEmbedding for FakeEmbedding {
    fn model(&self) -> &common::ModelManifest {
        &self.model
    }
    fn embed(
        &self,
        _: common::RequestContext,
        items: Vec<inference::TextItem>,
        _: String,
        _: &AtomicBool,
    ) -> Result<Vec<(String, evidence::DenseVector)>, ProcessError> {
        self.calls.fetch_add(1, Ordering::Relaxed);
        if self.partial.load(Ordering::Acquire) {
            return Ok(vec![]);
        }
        Ok(items
            .into_iter()
            .rev()
            .map(|item| {
                (
                    item.item_id,
                    evidence::DenseVector {
                        values: vec![0.6, 0.8],
                        dimensions: 2,
                        model_id: self.model.model_id.clone(),
                        ..Default::default()
                    },
                )
            })
            .collect())
    }
}
fn persist<M: MessageFull>(
    processor: &ParseBatchProcessor,
    value: &M,
    id: &str,
) -> common::ArtifactRef {
    let mut reference = processor
        .store
        .put_bytes(
            "index-fixture",
            &format!(
                "application/x-protobuf; message={}",
                M::descriptor().full_name()
            ),
            1,
            &value.write_to_bytes().unwrap(),
        )
        .unwrap()
        .to_wire_ref()
        .unwrap();
    reference.artifact_id = id.into();
    reference
}

pub(super) fn exercise_index(
    processor: ParseBatchProcessor,
    source: &documents::DocumentBatch,
    mut generation: evidence::IndexGeneration,
) -> ParseBatchProcessor {
    let meta = |id: &str| common::RecordMeta {
        schema_version: 1,
        corpus_id: source.meta.corpus_id.clone(),
        record_id: id.into(),
        ..Default::default()
    };
    let snapshot = common::SnapshotRef {
        corpus_id: source.meta.corpus_id.clone(),
        snapshot_id: "snapshot:index".into(),
        sequence: 7,
        manifest_hash: MessageField::some(common::ContentHash {
            sha256: "b".repeat(64),
            ..Default::default()
        }),
        representation_generation: generation.meta.record_id.clone(),
        ..Default::default()
    };
    let mut source = source.clone();
    source.context.as_mut().unwrap().snapshot_ref = MessageField::some(snapshot.clone());
    let source_ref = persist_document_batch(&processor.store, &source)
        .unwrap()
        .reference;
    let selected: Vec<_> = source
        .chunks
        .iter()
        .take(2)
        .map(|c| c.meta.record_id.clone())
        .collect();
    let inputs = VerifiedIndexInputs::new(&source)
        .unwrap()
        .load_selected(
            &processor.store,
            &processor.config.normalizer,
            &selected,
            1024,
            &AtomicBool::new(false),
        )
        .unwrap();
    let terms: Vec<_> = inputs
        .iter()
        .map(|i| analyze_document(&i.text).unwrap())
        .collect();
    let vocabulary: BTreeSet<_> = terms.iter().flatten().cloned().collect();
    let entries: Vec<_> = vocabulary
        .into_iter()
        .enumerate()
        .map(|(i, term)| (term, (i + 1) as u32))
        .collect();
    let dictionary =
        LexicalDictionary::from_allocated_entries(ANALYZER_VERSION, "lexrev:1", entries.clone())
            .unwrap();
    let dictionary = evidence::LexicalDictionaryArtifact {
        meta: MessageField::some(meta("dictionary:index")),
        analyzer_id: ANALYZER_VERSION.into(),
        registry_revision: 1,
        mapping_fingerprint: MessageField::some(common::ContentHash {
            sha256: dictionary
                .fingerprint()
                .iter()
                .map(|b| format!("{b:02x}"))
                .collect(),
            ..Default::default()
        }),
        entries: entries
            .into_iter()
            .map(|(term, term_id)| evidence::LexicalDictionaryEntry {
                term,
                term_id,
                ..Default::default()
            })
            .collect(),
        ..Default::default()
    };
    let checked = CheckedDictionary::from_artifact(
        &dictionary,
        &source.meta.corpus_id,
        None,
        Limits::default(),
    )
    .unwrap();
    let mut stats = Bm25Statistics::new(ANALYZER_VERSION).unwrap();
    for (id, terms) in selected.iter().zip(&terms) {
        stats
            .upsert(ANALYZER_VERSION, id, terms.iter().map(String::as_str))
            .unwrap();
    }
    let mut statistics = stats
        .freeze_artifact(
            &meta("statistics:index"),
            &snapshot,
            &checked,
            1.2,
            0.75,
            Limits::default(),
        )
        .unwrap();
    statistics.input_policy = RENDER_POLICY_VERSION.into();
    let analyzer = build_analyzer(&meta("analyzer:index"), Limits::default()).unwrap();
    generation.lexical_analyzer =
        MessageField::some(persist(&processor, &analyzer, &analyzer.meta.record_id));
    generation.lexical_dictionary =
        MessageField::some(persist(&processor, &dictionary, &dictionary.meta.record_id));
    generation.lexical_statistics =
        MessageField::some(persist(&processor, &statistics, &statistics.meta.record_id));
    generation.ontology_version = "ontology:v1".into();
    let model = (*generation.dense_manifest).clone();
    let producer = (*source.dependency_manifest.producer_manifest).clone();
    let plan = evidence::IndexBuildPlan {
        meta: MessageField::some(meta("plan:index")),
        target_snapshot: MessageField::some(snapshot.clone()),
        document_batch: MessageField::some(source_ref.clone()),
        source_snapshot: MessageField::some(snapshot),
        dictionary_chain: vec![(*generation.lexical_dictionary).clone()],
        generation: MessageField::some(generation),
        items: selected
            .iter()
            .enumerate()
            .map(|(i, chunk_id)| evidence::IndexBuildItem {
                chunk_id: chunk_id.clone(),
                record_id: format!("index:{i}"),
                ..Default::default()
            })
            .collect(),
        output_batch_id: "index-batch:fixture".into(),
        producer: MessageField::some(producer),
        lexical_input_policy: RENDER_POLICY_VERSION.into(),
        ..Default::default()
    };
    let plan_ref = persist(&processor, &plan, "plan:index");
    let calls = Arc::new(AtomicUsize::new(0));
    let fake = Arc::new(FakeEmbedding {
        model,
        calls: calls.clone(),
        partial: AtomicBool::new(false),
    });
    let processor = processor.with_indexing(fake.clone()).unwrap();
    let request = jobs::ProcessBatchRequest {
        context: source.context.clone(),
        job_id: "job:index".into(),
        attempt: 1,
        lease: MessageField::some(jobs::Lease {
            owner_id: "worker:fixture".into(),
            fence: 7,
            expires_at: source.context.deadline.clone(),
            ..Default::default()
        }),
        sources: vec![source_ref],
        manifest: plan.producer.clone(),
        stages: vec![EnumOrUnknown::new(jobs::JobStage::JOB_STAGE_INDEX)],
        index_build_plan: MessageField::some(plan_ref.clone()),
        ..Default::default()
    };
    let output = processor
        .process(request.clone(), &AtomicBool::new(false), &|_, _, _| {})
        .unwrap();
    assert_eq!(calls.load(Ordering::Relaxed), 1);
    let batch: evidence::IndexBatch = build::load_typed(
        &processor.store,
        &output.index_batch,
        &AtomicBool::new(false),
    )
    .unwrap();
    assert_eq!(batch.records.len(), selected.len());
    assert_eq!(batch.records[0].chunk_id, selected[0]);
    assert_eq!(batch.counts.accepted, selected.len() as u64);
    assert_eq!(
        batch.operations_checksum.sha256,
        build::operations_checksum(&batch).unwrap()
    );
    assert_eq!(
        output.checkpoint.stage.enum_value(),
        Ok(jobs::JobStage::JOB_STAGE_INDEX)
    );
    // Retry identical input yields identical content-addressed output (no silent truncation).
    let again = processor
        .process(request.clone(), &AtomicBool::new(false), &|_, _, _| {})
        .unwrap();
    assert_eq!(output.index_batch, again.index_batch);
    let before = calls.load(Ordering::Relaxed);
    assert!(processor
        .process(request.clone(), &AtomicBool::new(true), &|_, _, _| {})
        .is_err());
    assert_eq!(calls.load(Ordering::Relaxed), before);
    fake.partial.store(true, Ordering::Release);
    assert!(processor
        .process(request.clone(), &AtomicBool::new(false), &|_, _, _| {})
        .is_err());
    fake.partial.store(false, Ordering::Release);
    let before = calls.load(Ordering::Relaxed);
    for which in 0..9 {
        let mut bad = plan.clone();
        match which {
            0 => bad.items.push(bad.items[0].clone()),
            1 => bad.items[0].chunk_id = "chunk:missing".into(),
            2 => bad.source_snapshot.as_mut().unwrap().snapshot_id = "snapshot:wrong".into(),
            3 => {
                bad.generation
                    .as_mut()
                    .unwrap()
                    .dense_manifest
                    .as_mut()
                    .unwrap()
                    .version = "drift".into()
            }
            4 => {
                bad.generation
                    .as_mut()
                    .unwrap()
                    .lexical_statistics
                    .as_mut()
                    .unwrap()
                    .content_hash
                    .as_mut()
                    .unwrap()
                    .sha256 = "c".repeat(64)
            }
            5 => bad.lexical_input_policy = "unsupported".into(),
            6 => bad.document_batch.as_mut().unwrap().byte_size = (16 << 20) + 1,
            7 => {
                bad.generation
                    .as_mut()
                    .unwrap()
                    .lexical_analyzer
                    .as_mut()
                    .unwrap()
                    .byte_size = (16 << 20) + 1
            }
            _ => {
                let mut reference = (*bad.generation.lexical_dictionary).clone();
                reference.byte_size = 16 << 20;
                bad.dictionary_chain = vec![reference.clone(); 5];
                bad.generation.as_mut().unwrap().lexical_dictionary = MessageField::some(reference);
            }
        }
        bad.meta.as_mut().unwrap().record_id = "plan:bad".into();
        let mut bad_request = request.clone();
        bad_request.sources = vec![(*bad.document_batch).clone()];
        bad_request.index_build_plan = MessageField::some(persist(&processor, &bad, "plan:bad"));
        assert!(
            processor
                .process(bad_request, &AtomicBool::new(false), &|_, _, _| {})
                .is_err(),
            "case {which}"
        );
    }
    assert_eq!(calls.load(Ordering::Relaxed), before);
    if let Ok(directory) = std::env::var("REGULAGRAPH_INDEX_FIXTURE_DIR") {
        let directory = std::path::PathBuf::from(directory);
        std::fs::create_dir_all(&directory).unwrap();
        for (name, bytes) in [
            ("source.pb", source.write_to_bytes().unwrap()),
            ("plan.pb", plan.write_to_bytes().unwrap()),
            ("plan-ref.pb", plan_ref.write_to_bytes().unwrap()),
            ("batch.pb", batch.write_to_bytes().unwrap()),
        ] {
            std::fs::write(directory.join(name), bytes).unwrap();
        }
    }
    processor
}
