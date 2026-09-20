"""Verify E01 gate status semantics across valid, failed, missing, and blocked evidence.

The fixtures satisfy the declared reference manifest but contain synthetic measurements. PASS here means
the evaluator classified a boundary correctly; it is not a RegulaGraph release benchmark result.
"""
from pathlib import Path
import unittest

from google.protobuf import json_format
import evaluation_pb2 as pb
from regulagraph.v1 import common_pb2 as common

from evaluation.config import load_evaluation_config, load_target_suite
from evaluation.gates import evaluate_gates


ROOT = Path(__file__).resolve().parents[2]
HASH = {"sha256": "a" * 64}


def _mixed_evidence(mixed, baseline, mixed_succeeded=None, baseline_succeeded=None):
    interval = 100_000_000  # 10 RPS from the frozen retrieval workload.
    population = len(mixed)
    return {
        "with_ingestion_samples_ms": mixed,
        "baseline_samples_ms": baseline,
        "with_ingestion_succeeded": (sum(value is not None and value <= 2000 for value in mixed)
                                      if mixed_succeeded is None else mixed_succeeded),
        "with_ingestion_scheduled": population,
        "baseline_succeeded": (sum(value is not None and value <= 2000 for value in baseline)
                               if baseline_succeeded is None else baseline_succeeded),
        "baseline_scheduled": len(baseline),
        "with_ingestion_scheduled_offsets_ns": [index * interval for index in range(population)],
        "baseline_scheduled_offsets_ns": [index * interval for index in range(len(baseline))],
    }


class EvaluationGatesTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.config = load_evaluation_config(ROOT / "configs/evaluation.yaml", ROOT)
        cls.suite = load_target_suite(cls.config.target_file)
        snapshot = {"corpus_id": "corpus", "snapshot_id": "snapshot", "sequence": "1", "manifest_hash": HASH, "representation_generation": "generation"}
        cls.dataset = json_format.ParseDict({
            "meta": {"schema_version": 1, "corpus_id": "corpus", "record_id": "dataset"}, "version": "v1",
            "dataset_hash": HASH, "corpus_snapshot": snapshot,
            "groups": [{"base_question_group": "group", "split": "DATASET_SPLIT_TEST"}], "label_schema": "v1",
            "reviews": [{"reviewer": "human", "reviewed_at": "2026-01-01T00:00:00Z", "method": "synthetic-test"}],
        }, pb.DatasetManifest())
        cls.manifest = json_format.ParseDict({
            "meta": {"schema_version": 1, "corpus_id": "corpus", "record_id": "run"},
            "software_hash": HASH, "config_hash": {"sha256": cls.config.sha256},
            "models": [
                {"model_id": "embed", "version": "v1", "weights_hash": HASH, "tokenizer_hash": HASH,
                 "task": "MODEL_TASK_EMBED", "dimensions": 16, "max_tokens": 128, "precision": "fp32", "backend": "fixture"},
                {"model_id": "rerank", "version": "v1", "weights_hash": HASH, "tokenizer_hash": HASH,
                 "task": "MODEL_TASK_RERANK", "max_tokens": 512, "precision": "fp32", "backend": "fixture"},
                {"model_id": "generate", "version": "v1", "weights_hash": HASH, "tokenizer_hash": HASH,
                 "prompt_hash": HASH, "task": "MODEL_TASK_GENERATE", "max_tokens": 256,
                 "precision": "fp32", "backend": "fixture"},
            ],
            "dataset_hash": HASH, "corpus_snapshot": snapshot, "target_suite_hash": {"sha256": cls.suite.sha256},
            "hardware": {"cpu": "fixture", "ram_bytes": str(64 << 30), "gpus": ["fixture"], "vram_bytes": [str(24 << 30)],
                         "cpu_limit": 16, "memory_limit_bytes": str(64 << 30), "operating_system": "linux_x86_64"},
            "cache_policy": "acceptance caches disabled",
            "workload": {"artifact_id": "workload", "content_hash": HASH, "storage_key": "fixtures/workload.json",
                         "media_type": "application/json", "byte_size": "1", "schema_version": 1},
        }, pb.RunManifest())
        cls.artifact = common.ArtifactRef(artifact_id="input", content_hash=common.ContentHash(sha256="b" * 64),
                                          storage_key="evaluation/run/input.json", media_type="application/json", byte_size=1, schema_version=1)
        cls.protocol = {key: value for key, value in cls.suite.protocol.items()
                        if key not in {"pass_rule", "eligibility", "quality_floor", "confidence_reporting"}}
        cls.workloads = {name: {"status": "measured", "reason": "", "facts": {k: v for k, v in target.items() if k != "note"}}
                         for name, target in cls.suite.workloads.items()}

    def evaluate(self, measurements, dataset=True):
        return evaluate_gates(self.suite, self.manifest, "hybrid_graphrag",
                              self.dataset if dataset else None, self.protocol, self.workloads, measurements,
                              self.artifact, [], {})

    def test_all_59_gates_report_and_missing_is_not_measured(self):
        summary = self.evaluate({})
        self.assertEqual(len(summary.results), 59)
        self.assertEqual(summary.counts["NOT_MEASURED"], 59)
        self.assertEqual(summary.release_status, "NOT_MEASURED")

    def test_each_run_must_pass_boundary(self):
        measurement = {"QUERY.EVIDENCE_P95": {"workload": "retrieval", "statistic": "p95", "unit": "ms", "runs": [
            {"run_id": "a", "sample_count": 10000, "evidence": {"samples": [500] * 10000}},
            {"run_id": "b", "sample_count": 10000, "evidence": {"samples": [499] * 10000}},
            {"run_id": "c", "sample_count": 10000, "evidence": {"samples": [500] * 10000}},
        ]}}
        result = self.evaluate(measurement).results[0]
        self.assertEqual(result.status, pb.GATE_STATUS_PASS)
        self.assertEqual(result.estimate, 500)
        measurement["QUERY.EVIDENCE_P95"]["runs"][1]["evidence"]["samples"][-501:] = [501] * 501
        result = self.evaluate(measurement).results[0]
        self.assertEqual(result.status, pb.GATE_STATUS_FAIL)
        self.assertEqual(result.estimate, 501)

    def test_invalid_or_incomplete_evidence_is_blocked(self):
        measurement = {"QUERY.SUCCESS": {"workload": "retrieval", "statistic": "ratio", "unit": "ratio", "runs": [
            {"run_id": "only-one", "sample_count": 10000, "evidence": {"numerator": 100, "denominator": 100}}
        ]}}
        summary = self.evaluate(measurement)
        self.assertEqual(next(r for r in summary.results if r.gate_id == "QUERY.SUCCESS").status, pb.GATE_STATUS_BLOCKED)
        blocked = self.evaluate(measurement, dataset=False)
        self.assertEqual(blocked.counts["BLOCKED"], 59)

    def test_unfinished_latency_fails_without_nonfinite_proto_value(self):
        measurement = {"QUERY.EVIDENCE_P95": {"workload": "retrieval", "statistic": "p95", "unit": "ms", "runs": [
            {"run_id": value, "sample_count": 10000,
             "evidence": {"samples": [1] * 9400 + [float("inf")] * 600}} for value in ("a", "b", "c")
        ]}}
        result = self.evaluate(measurement).results[0]
        self.assertEqual(result.status, pb.GATE_STATUS_FAIL)
        self.assertFalse(result.HasField("estimate"))

    def test_declared_workload_cannot_hide_too_few_run_samples(self):
        measurement = {"QUERY.SUCCESS": {"workload": "retrieval", "statistic": "ratio", "unit": "ratio", "runs": [
            {"run_id": value, "sample_count": 9999,
             "evidence": {"numerator": 9999, "denominator": 9999}} for value in ("a", "b", "c")
        ]}}
        result = next(r for r in self.evaluate(measurement).results if r.gate_id == "QUERY.SUCCESS")
        self.assertEqual(result.status, pb.GATE_STATUS_BLOCKED)
        self.assertIn("below workload minimum", result.reason)

    def test_quality_cannot_pass_without_uncertainty(self):
        measurement = {"QUALITY.ANSWER_CORRECT": {
            "workload": "quality", "statistic": "ratio", "unit": "ratio",
            "runs": [{"run_id": value, "sample_count": 1000,
                      "population_ids": [f"question-{index}" for index in range(1000)],
                      "evidence": {"numerator": 950, "denominator": 1000}}
                     for value in ("a", "b", "c")],
        }}
        result = next(r for r in self.evaluate(measurement).results if r.gate_id == "QUALITY.ANSWER_CORRECT")
        self.assertEqual(result.status, pb.GATE_STATUS_BLOCKED)
        self.assertIn("uncertainty", result.reason)

    def test_quality_scores_require_frozen_population_ids(self):
        scores = {f"nonexistent-gold-{index}": 1.0 for index in range(900)}
        measurement = {"QUALITY.RECALL_AT_20": {
            "workload": "quality", "statistic": "macro_mean", "unit": "ratio",
            "runs": [{"run_id": value, "sample_count": 900, "uncertainty": 0.01,
                      "evidence": {"group_scores": scores}}
                     for value in ("a", "b", "c")],
        }}
        result = next(r for r in self.evaluate(measurement).results if r.gate_id == "QUALITY.RECALL_AT_20")
        self.assertEqual(result.status, pb.GATE_STATUS_BLOCKED)
        self.assertIn("population_ids", result.reason)

    def test_declared_sample_count_must_equal_real_evidence(self):
        measurement = {"QUERY.EVIDENCE_P95": {
            "workload": "retrieval", "statistic": "p95", "unit": "ms",
            "runs": [{"run_id": value, "sample_count": 10000, "evidence": {"samples": [1]}}
                     for value in ("a", "b", "c")],
        }}
        result = self.evaluate(measurement).results[0]
        self.assertEqual(result.status, pb.GATE_STATUS_BLOCKED)
        self.assertIn("does not match evidence denominator", result.reason)

    def test_negative_latency_and_count_are_blocked(self):
        latency = {"QUERY.EVIDENCE_P95": {
            "workload": "retrieval", "statistic": "p95", "unit": "ms",
            "runs": [{"run_id": value, "sample_count": 10000, "evidence": {"samples": [-1] * 10000}}
                     for value in ("a", "b", "c")],
        }}
        self.assertEqual(self.evaluate(latency).results[0].status, pb.GATE_STATUS_BLOCKED)
        count = {"INVARIANT.ORPHAN_EDGES": {
            "workload": "invariants", "statistic": "count", "unit": "count",
            "runs": [{"run_id": value, "sample_count": 1_000_000,
                      "evidence": {"value": -1, "denominator": 1_000_000}}
                     for value in ("a", "b", "c")],
        }}
        result = next(r for r in self.evaluate(count).results if r.gate_id == "INVARIANT.ORPHAN_EDGES")
        self.assertEqual(result.status, pb.GATE_STATUS_BLOCKED)

    def test_comparison_ratio_requires_full_raw_populations(self):
        measurement = {"ISOLATION.QUERY_P95_RATIO": {
            "workload": "mixed_load", "statistic": "ratio", "unit": "ratio",
            "runs": [{"run_id": value, "sample_count": 10000,
                      "evidence": _mixed_evidence([10], [10])}
                     for value in ("a", "b", "c")],
        }}
        result = next(r for r in self.evaluate(measurement).results if r.gate_id == "ISOLATION.QUERY_P95_RATIO")
        self.assertEqual(result.status, pb.GATE_STATUS_BLOCKED)
        self.assertIn("open-loop measurement window", result.reason)

    def test_verified_corpus_population_replaces_suite_minimum(self):
        measurement = {"INVARIANT.ORPHAN_EDGES": {
            "workload": "invariants", "statistic": "count", "unit": "count",
            "runs": [{"run_id": value, "sample_count": 1_000_000,
                      "evidence": {"value": 0, "denominator": 1_000_000}}
                     for value in ("a", "b", "c")],
        }}
        summary = evaluate_gates(
            self.suite, self.manifest, "hybrid_graphrag", self.dataset, self.protocol,
            self.workloads, measurement, self.artifact, [], {"INVARIANT.ORPHAN_EDGES": 10_000_000})
        result = next(r for r in summary.results if r.gate_id == "INVARIANT.ORPHAN_EDGES")
        self.assertEqual(result.status, pb.GATE_STATUS_BLOCKED)
        self.assertIn("verified population", result.reason)

    def test_mixed_ratio_cannot_hide_absolute_query_failure(self):
        slow = [100_000] * 12000
        measurement = {"ISOLATION.QUERY_P95_RATIO": {
            "workload": "mixed_load", "statistic": "ratio", "unit": "ratio",
            "runs": [{"run_id": value, "sample_count": 12000,
                      "evidence": _mixed_evidence(slow, slow)}
                     for value in ("a", "b", "c")],
        }}
        result = next(r for r in self.evaluate(measurement).results if r.gate_id == "ISOLATION.QUERY_P95_RATIO")
        self.assertEqual(result.status, pb.GATE_STATUS_FAIL)
        self.assertEqual(result.estimate, 1)
        self.assertIn("absolute QUERY.EVIDENCE_P95", result.reason)

    def test_mixed_ratio_reconciles_unfinished_samples_with_success(self):
        samples = [10] * 11881 + [None] * 119
        measurement = {"ISOLATION.QUERY_P95_RATIO": {
            "workload": "mixed_load", "statistic": "ratio", "unit": "ratio",
            "runs": [{"run_id": value, "sample_count": 12000,
                      "evidence": _mixed_evidence(samples, [10] * 12000, mixed_succeeded=12000)}
                     for value in ("a", "b", "c")],
        }}
        result = next(r for r in self.evaluate(measurement).results if r.gate_id == "ISOLATION.QUERY_P95_RATIO")
        self.assertEqual(result.status, pb.GATE_STATUS_BLOCKED)
        self.assertIn("on-time latency", result.reason)

    def test_mixed_ratio_rejects_unfinished_baseline(self):
        measurement = {"ISOLATION.QUERY_P95_RATIO": {
            "workload": "mixed_load", "statistic": "ratio", "unit": "ratio",
            "runs": [{"run_id": value, "sample_count": 12000,
                      "evidence": _mixed_evidence([10] * 12000, [None] * 12000)}
                     for value in ("a", "b", "c")],
        }}
        result = next(r for r in self.evaluate(measurement).results if r.gate_id == "ISOLATION.QUERY_P95_RATIO")
        self.assertEqual(result.status, pb.GATE_STATUS_BLOCKED)
        self.assertIn("finite baseline", result.reason)

    def test_mixed_ratio_reconciles_success_with_completion_deadline(self):
        late = [10] * 11940 + [2001] * 60
        measurement = {"ISOLATION.QUERY_P95_RATIO": {
            "workload": "mixed_load", "statistic": "ratio", "unit": "ratio",
            "runs": [{"run_id": value, "sample_count": 12000,
                      "evidence": _mixed_evidence(late, [10] * 12000, mixed_succeeded=12000)}
                     for value in ("a", "b", "c")],
        }}
        result = next(r for r in self.evaluate(measurement).results if r.gate_id == "ISOLATION.QUERY_P95_RATIO")
        self.assertEqual(result.status, pb.GATE_STATUS_BLOCKED)
        self.assertIn("on-time", result.reason)

    def test_parity_uses_answerable_population_minimum(self):
        ids = [f"question-{index}" for index in range(900)]
        measurement = {"PARITY.RECALL_DROP": {
            "workload": "model_parity", "statistic": "difference", "unit": "percentage_points",
            "runs": [{"run_id": value, "sample_count": 900, "population_ids": ids,
                      "evidence": {"value": 0.0, "denominator": 900}}
                     for value in ("a", "b", "c")],
        }}
        result = next(r for r in self.evaluate(measurement).results if r.gate_id == "PARITY.RECALL_DROP")
        self.assertEqual(result.status, pb.GATE_STATUS_PASS)

    def test_combined_denominator_overflow_is_blocked(self):
        huge = 1 << 63
        measurement = {"PARITY.RECALL_DROP": {
            "workload": "model_parity", "statistic": "difference", "unit": "percentage_points",
            "runs": [{"run_id": value, "sample_count": huge, "population_ids": ["question"],
                      "evidence": {"value": 0.0, "denominator": huge}}
                     for value in ("a", "b", "c")],
        }}
        result = next(r for r in self.evaluate(measurement).results if r.gate_id == "PARITY.RECALL_DROP")
        self.assertEqual(result.status, pb.GATE_STATUS_BLOCKED)
        self.assertIn("uint64", result.reason)

    def test_extreme_numeric_evidence_is_blocked_without_exception(self):
        update = {"UPDATE.REBUILD_RATIO": {
            "workload": "incremental", "statistic": "ratio", "unit": "ratio",
            "runs": [{"run_id": value, "sample_count": 100,
                      "evidence": {"changed_documents": 100, "update_duration_seconds": 10 ** 400,
                                   "full_rebuild_duration_seconds": 1, "logical_state_diff_count": 0}}
                     for value in ("a", "b", "c")],
        }}
        update_result = next(r for r in self.evaluate(update).results if r.gate_id == "UPDATE.REBUILD_RATIO")
        self.assertEqual(update_result.status, pb.GATE_STATUS_BLOCKED)

        ids = [f"question-{index}" for index in range(900)]
        quality = {"QUALITY.ANSWER_CORRECT": {
            "workload": "quality", "statistic": "ratio", "unit": "ratio",
            "runs": [{"run_id": value, "sample_count": 900, "population_ids": ids,
                      "uncertainty": 10 ** 400, "evidence": {"numerator": 900, "denominator": 900}}
                     for value in ("a", "b", "c")],
        }}
        quality_result = next(r for r in self.evaluate(quality).results if r.gate_id == "QUALITY.ANSWER_CORRECT")
        self.assertEqual(quality_result.status, pb.GATE_STATUS_BLOCKED)

        ingestion = {"ISOLATION.INGESTION_RATIO": {
            "workload": "mixed_load", "statistic": "ratio", "unit": "ratio",
            "runs": [{"run_id": value, "sample_count": 10000,
                      "evidence": {"mixed_completed": 10000, "mixed_duration_seconds": 1e-308,
                                   "alone_completed": 10000, "alone_duration_seconds": 1e-308}}
                     for value in ("a", "b", "c")],
        }}
        ingestion_result = next(r for r in self.evaluate(ingestion).results
                                if r.gate_id == "ISOLATION.INGESTION_RATIO")
        self.assertEqual(ingestion_result.status, pb.GATE_STATUS_BLOCKED)

        ratio_overflow = {"ISOLATION.INGESTION_RATIO": {
            "workload": "mixed_load", "statistic": "ratio", "unit": "ratio",
            "runs": [{"run_id": value, "sample_count": 10000,
                      "evidence": {"mixed_completed": 10000, "mixed_duration_seconds": 1e-303,
                                   "alone_completed": 10000, "alone_duration_seconds": 1e308}}
                     for value in ("a", "b", "c")],
        }}
        overflow_result = next(r for r in self.evaluate(ratio_overflow).results
                               if r.gate_id == "ISOLATION.INGESTION_RATIO")
        self.assertEqual(overflow_result.status, pb.GATE_STATUS_BLOCKED)

    def test_parsing_f1_is_recomputed_from_each_page(self):
        pages = [f"page-{index}" for index in range(1000)]
        counts = {page: {"true_positive": 1, "false_positive": 0, "false_negative": 0} for page in pages}
        measurement = {"PARSING.STRUCTURE_F1": {
            "workload": "parsing_quality", "statistic": "micro_f1", "unit": "ratio",
            "runs": [{"run_id": value, "sample_count": 1000, "population_ids": pages,
                      "evidence": {"page_counts": counts}} for value in ("a", "b", "c")],
        }}
        result = next(r for r in self.evaluate(measurement).results if r.gate_id == "PARSING.STRUCTURE_F1")
        self.assertEqual(result.status, pb.GATE_STATUS_PASS)
        self.assertEqual(result.estimate, 1)

    def test_wire_invariant_is_recomputed_from_fixture_ids(self):
        ids = ["go-rust", "go-cpp"]
        measurement = {"INVARIANT.WIRE_PARITY": {
            "workload": "invariants", "statistic": "ratio", "unit": "ratio",
            "runs": [{"run_id": value, "sample_count": 2, "population_ids": ids,
                      "evidence": {"matching_fixture_ids": ids, "mismatching_fixture_ids": []}}
                     for value in ("a", "b", "c")],
        }}
        result = next(r for r in self.evaluate(measurement).results if r.gate_id == "INVARIANT.WIRE_PARITY")
        self.assertEqual(result.status, pb.GATE_STATUS_PASS)


if __name__ == "__main__":
    unittest.main()
