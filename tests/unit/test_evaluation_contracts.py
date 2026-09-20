"""Test evaluation input integrity independently of model or benchmark execution.

Synthetic manifests exercise split leakage, missing evidence for PASS and timing inconsistency.
Run with generated Python bindings on PYTHONPATH; no network or model initialization is required.
"""
import unittest

from google.protobuf import json_format
from evaluation.datasets import schema
import evaluation_pb2 as pb


class EvaluationContractsTest(unittest.TestCase):
    def setUp(self):
        self.hash = {"sha256": "a" * 64}
        self.meta = {"schema_version": 1, "corpus_id": "c", "record_id": "d"}
        self.review = {"reviewer": "fixture-reviewer", "reviewed_at": "2026-01-01T00:00:00Z", "method": "synthetic"}
        self.manifest = json_format.ParseDict({"meta": self.meta, "version": "v1", "dataset_hash": self.hash,
            "corpus_snapshot": {"corpus_id": "c", "snapshot_id": "s", "sequence": "1", "manifest_hash": self.hash, "representation_generation": "g"},
            "groups": [{"base_question_group": "group", "split": "DATASET_SPLIT_TEST"}],
            "label_schema": "v1", "reviews": [self.review]}, pb.DatasetManifest())
        self.question = json_format.ParseDict({"meta": dict(self.meta, record_id="q"), "question": "Synthetic question",
            "base_question_group": "group", "split": "DATASET_SPLIT_TEST", "slice_labels": ["synthetic"],
            "temporal_scope": {"mode": "TEMPORAL_MODE_AS_OF", "unresolved_policy": "UNRESOLVED_POLICY_REPORT", "effective_at": {"year": 2026, "month": 1, "day": 1}},
            "answerability": "ANSWERABILITY_UNANSWERABLE", "reviews": [self.review]}, pb.GoldQuestion())

    def test_group_split_and_duplicate_guard(self):
        schema.validate_dataset(self.manifest, [self.question])
        with self.assertRaises(ValueError):
            schema.validate_dataset(self.manifest, [self.question, self.question])
        self.question.split = pb.DATASET_SPLIT_DEVELOPMENT
        with self.assertRaises(ValueError):
            schema.validate_dataset(self.manifest, [self.question])

    def test_pass_requires_measurement_and_artifact(self):
        gate = json_format.ParseDict({"meta": self.meta, "gate_id": "fixture-gate", "applicable_workload": "synthetic",
            "threshold_reference": "fixture-only", "status": "GATE_STATUS_NOT_MEASURED", "reason": "No measurement"}, pb.GateResult())
        schema.validate(gate)
        gate.status = pb.GATE_STATUS_PASS
        with self.assertRaises(ValueError):
            schema.validate(gate)
        gate.denominator = 1
        gate.estimate = 0.5
        with self.assertRaises(ValueError):
            schema.validate(gate)

    def test_observation_inconsistent_timing(self):
        obs = json_format.ParseDict({"meta": self.meta, "run_id": "run", "request_id": "request",
            "scheduled_arrival": "2026-01-01T00:00:00Z", "outcome": "COMPLETION_STATUS_SUCCEEDED",
            "first_substantive_token_ns": "2", "completion_ns": "3"}, pb.Observation())
        schema.validate(obs)
        obs.first_substantive_token_ns = 4
        with self.assertRaises(ValueError):
            schema.validate(obs)
        obs.first_substantive_token_ns = 2
        obs.rejected_arrival = True
        with self.assertRaises(ValueError):
            schema.validate(obs)


if __name__ == "__main__":
    unittest.main()
