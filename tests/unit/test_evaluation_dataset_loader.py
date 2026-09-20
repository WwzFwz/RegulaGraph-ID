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


if __name__ == "__main__":
    unittest.main()
