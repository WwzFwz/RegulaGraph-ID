"""Verify E01 eligibility counts come from typed dataset/corpus artifacts rather than declarations.

Peran pengujian:
Membuktikan loader membaca GoldQuestion C01, menjaga group/split, dan melaporkan dataset/corpus yang lebih
kecil dari target. Fixture satu pertanyaan sengaja tidak eligible dan tidak menyatakan quality PASS.

Kontrak:
JSONL dan corpus JSON memakai file nyata; target numerik dibaca dari suite resmi tanpa diubah.
"""
from __future__ import annotations

import json
from pathlib import Path
import shutil
import unittest
import uuid

from google.protobuf import json_format
import evaluation_pb2 as pb

from evaluation.config import load_evaluation_config, load_target_suite
from evaluation.datasets.loader import (DatasetLoadError, corpus_eligibility_errors, dataset_eligibility_errors,
                                        load_corpus_facts, load_gold_questions, load_id_inventory)
from evaluation.runner import _measurement_population_errors, _measurement_run_errors


ROOT = Path(__file__).resolve().parents[2]


class EvaluationDatasetLoaderTest(unittest.TestCase):
    def setUp(self):
        self.base = ROOT / ".cache" / f"dataset-loader-{uuid.uuid4().hex}"
        self.base.mkdir(parents=True)
        config = load_evaluation_config(ROOT / "configs/evaluation.yaml", ROOT)
        self.suite = load_target_suite(config.target_file)

    def tearDown(self):
        if ROOT in self.base.resolve().parents:
            shutil.rmtree(self.base, ignore_errors=True)

    def test_actual_question_and_corpus_counts_block_small_fixture(self):
        question = {
            "meta": {"schema_version": 1, "corpus_id": "corpus", "record_id": "question"},
            "question": "Tidak ada bukti?", "base_question_group": "group",
            "split": "DATASET_SPLIT_TEST", "slice_labels": ["unanswerable"],
            "temporal_scope": {"mode": "TEMPORAL_MODE_CURRENT", "unresolved_policy": "UNRESOLVED_POLICY_REPORT"},
            "answerability": "ANSWERABILITY_UNANSWERABLE",
            "reviews": [{"reviewer": "human", "reviewed_at": "2026-01-01T00:00:00Z", "method": "fixture"}],
        }
        dataset_path = self.base / "dataset.jsonl"
        dataset_path.write_text(json.dumps(question) + "\n", encoding="utf-8")
        snapshot = {
            "corpus_id": "corpus", "snapshot_id": "snapshot", "sequence": "1",
            "manifest_hash": {"sha256": "a" * 64}, "representation_generation": "generation",
        }
        manifest = json_format.ParseDict({
            "meta": {"schema_version": 1, "corpus_id": "corpus", "record_id": "dataset"},
            "version": "v1", "dataset_hash": {"sha256": "b" * 64}, "corpus_snapshot": snapshot,
            "groups": [{"base_question_group": "group", "split": "DATASET_SPLIT_TEST"}],
            "label_schema": "v1",
            "reviews": [{"reviewer": "human", "reviewed_at": "2026-01-01T00:00:00Z", "method": "fixture"}],
        }, pb.DatasetManifest())
        questions = load_gold_questions(dataset_path, manifest)
        dataset_errors = dataset_eligibility_errors(questions, self.suite.workloads["quality"])
        self.assertTrue(any("test questions 1 below" in error for error in dataset_errors))
        self.assertTrue(any("slice factual has 0" in error for error in dataset_errors))

        corpus_path = self.base / "corpus.json"
        corpus_path.write_text(json.dumps({
            "schema_version": 1, "corpus_id": "corpus", "snapshot_id": "snapshot",
            "representation_generation": "generation", "documents": 1, "chunks": 1,
            "canonical_entities": 1, "graph_edges": 1,
        }), encoding="utf-8")
        facts = load_corpus_facts(corpus_path)
        corpus_errors = corpus_eligibility_errors(facts, manifest.corpus_snapshot, self.suite.reference["corpus"])
        self.assertEqual(len(corpus_errors), 4)

        question["temporal_scope"]["knowledge_snapshot"] = {
            **snapshot, "snapshot_id": "different-snapshot",
        }
        dataset_path.write_text(json.dumps(question) + "\n", encoding="utf-8")
        with self.assertRaisesRegex(DatasetLoadError, "temporal snapshot differs"):
            load_gold_questions(dataset_path, manifest)

    def test_gold_inventory_requires_unique_real_ids(self):
        path = self.base / "inventory.json"
        path.write_text(json.dumps({"schema_version": 1, "page_ids": ["page-1", "page-2"]}), encoding="utf-8")
        self.assertEqual(load_id_inventory(path, ("page_ids",))["page_ids"], ("page-1", "page-2"))
        path.write_text(json.dumps({"schema_version": 1, "page_ids": ["page-1", "page-1"]}), encoding="utf-8")
        with self.assertRaisesRegex(DatasetLoadError, "unique"):
            load_id_inventory(path, ("page_ids",))

    def test_semantic_aggregate_cannot_cover_a_declared_inventory(self):
        populations = {
            "PARSING.STRUCTURE_F1": {"page-1", "page-2"},
            "PARSING.OCR_CER": {"scan-1", "scan-2"},
            "EXTRACTION.PRECISION": {"relation-1", "relation-2"},
            "EXTRACTION.RECALL": {"relation-1", "relation-2"},
        }
        measurements = {
            "PARSING.STRUCTURE_F1": {"runs": [{"population_ids": ["page-1", "page-2"],
                "evidence": {"page_counts": {"page-1": {"true_positive": 1,
                    "false_positive": 0, "false_negative": 0}}}}]},
            "PARSING.OCR_CER": {"runs": [{"population_ids": ["scan-1", "scan-2"],
                "evidence": {"page_counts": {"scan-1": {"edits": 0, "gold_units": 1}}}}]},
            "EXTRACTION.PRECISION": {"runs": [{"run_id": "run", "population_ids": ["relation-1", "relation-2"],
                "evidence": {"predicted_relation_ids": ["relation-1"],
                    "matched_gold_relation_ids": ["relation-1"], "missed_gold_relation_ids": []}}]},
            "EXTRACTION.RECALL": {"runs": [{"run_id": "run", "population_ids": ["relation-1", "relation-2"],
                "evidence": {"predicted_relation_ids": ["relation-1"],
                    "matched_gold_relation_ids": ["relation-1"], "missed_gold_relation_ids": []}}]},
        }
        errors = _measurement_population_errors(measurements, populations, ())
        self.assertTrue(any("annotated pages" in error for error in errors))
        self.assertTrue(any("standard scan pages" in error for error in errors))
        self.assertTrue(any("gold relations" in error for error in errors))

        malformed = {"PARSING.CRITICAL_TOKENS": {"runs": [{"population_ids": ["token-1"],
            "evidence": {"correct_token_ids": [{}], "incorrect_token_ids": []}}]}}
        malformed_errors = _measurement_population_errors(
            malformed, {"PARSING.CRITICAL_TOKENS": {"token-1"}}, ())
        self.assertTrue(any("critical tokens" in error for error in malformed_errors))

    def test_pair_false_positive_is_part_of_the_full_pair_universe(self):
        populations = {
            "RESOLUTION.PAIR_PRECISION": {"same-1", "different-1"},
            "RESOLUTION.PAIR_RECALL": {"same-1"},
        }
        evidence = {"predicted_same_pair_ids": ["same-1", "different-1"],
                    "matched_same_pair_ids": ["same-1"], "missed_same_pair_ids": []}
        measurements = {
            gate_id: {"runs": [{"run_id": "run", "population_ids": sorted(population), "evidence": evidence}]}
            for gate_id, population in populations.items()
        }
        self.assertEqual(_measurement_population_errors(measurements, populations, ()), [])

    def test_mixed_load_requires_warmup_and_measurement_window(self):
        gate_id = "ISOLATION.QUERY_P95_RATIO"
        measurements = {gate_id: {"workload": "mixed_load", "runs": [
            {"run_id": "run", "sample_count": 12000}
        ]}}
        declared = {("run", "mixed_load"): {
            "warmup_seconds": 0, "measured_seconds": 0, "sample_counts": {gate_id: 12000}
        }}
        errors = _measurement_run_errors(measurements, self.suite, declared)
        self.assertTrue(any("warmup" in error for error in errors))
        self.assertTrue(any("measurement window" in error for error in errors))

        throughput_gate = "PARSING.TEXT_THROUGHPUT"
        throughput = {throughput_gate: {"workload": "pdf_text", "runs": [
            {"run_id": "parse", "sample_count": 10000,
             "evidence": {"completed": 10000, "duration_seconds": 1}}
        ]}}
        declared_throughput = {("parse", "pdf_text"): {
            "warmup_seconds": 120, "measured_seconds": 1200,
            "sample_counts": {throughput_gate: 10000},
        }}
        throughput_errors = _measurement_run_errors(throughput, self.suite, declared_throughput)
        self.assertTrue(any("throughput duration" in error for error in throughput_errors))

        ingestion_gate = "ISOLATION.INGESTION_RATIO"
        inter_token_gate = "QUERY.INTER_TOKEN_P95"
        unit_specific = {
            ingestion_gate: {"workload": "mixed_load", "runs": [{"run_id": "ingest",
                "sample_count": 120000, "evidence": {"mixed_completed": 120000,
                    "mixed_duration_seconds": 1200, "alone_completed": 120000,
                    "alone_duration_seconds": 1200}}]},
            inter_token_gate: {"workload": "answer", "runs": [{"run_id": "tokens",
                "sample_count": 304800, "evidence": {"samples": [1]}}]},
        }
        unit_declared = {
            ("ingest", "mixed_load"): {"warmup_seconds": 120, "measured_seconds": 1200,
                "sample_counts": {ingestion_gate: 120000}},
            ("tokens", "answer"): {"warmup_seconds": 120, "measured_seconds": 1200,
                "sample_counts": {inter_token_gate: 304800}},
        }
        self.assertEqual(_measurement_run_errors(unit_specific, self.suite, unit_declared), [])

    def test_malformed_coherence_run_shape_does_not_escape(self):
        measurements = {
            "EXTRACTION.PRECISION": {"runs": None},
            "EXTRACTION.RECALL": {"runs": [{"run_id": [], "evidence": {}}]},
        }
        self.assertEqual(_measurement_population_errors(measurements, {}, ()), [])


if __name__ == "__main__":
    unittest.main()
