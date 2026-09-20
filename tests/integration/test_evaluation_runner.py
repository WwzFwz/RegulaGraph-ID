"""Exercise the E01 runner across JSON, C01 protobuf, filesystem artifacts, and gate output.

Peran pengujian:
Membuktikan bundle frozen tervalidasi lintas boundary file/ProtoJSON dan menghasilkan artefak audit yang
dapat dibaca ulang. Fixture sintetis sengaja hanya mengukur satu gate; status keseluruhan harus tetap
NOT_MEASURED dan tidak merupakan klaim benchmark produksi.

Kontrak dan perhatian performa:
Semua file pendukung memiliki size/hash nyata. Test tidak memanggil runtime produksi dan tidak mengukur
latency. Target angka tetap berasal dari configs/benchmark-targets.yaml dan tidak diubah oleh fixture.
"""
from __future__ import annotations

import hashlib
import json
from pathlib import Path
import shutil
import unittest
import uuid

from google.protobuf import json_format
import evaluation_pb2 as pb
from regulagraph.v1 import common_pb2 as common

from evaluation.config import load_evaluation_config, load_target_suite
from evaluation.runner import RunnerInputError, run_bundle


ROOT = Path(__file__).resolve().parents[2]


def _hash(raw: bytes) -> str:
    return hashlib.sha256(raw).hexdigest()


class EvaluationRunnerIntegrationTest(unittest.TestCase):
    def setUp(self):
        self.base = ROOT / ".cache" / f"evaluation-runner-{uuid.uuid4().hex}"
        self.base.mkdir(parents=True)
        self.config = load_evaluation_config(ROOT / "configs/evaluation.yaml", ROOT)
        self.suite = load_target_suite(self.config.target_file)

    def tearDown(self):
        if self.base.resolve() != ROOT and ROOT in self.base.resolve().parents:
            shutil.rmtree(self.base, ignore_errors=True)

    def _artifact(self, name: str, raw: bytes, media_type: str) -> common.ArtifactRef:
        path = self.base / name
        path.write_bytes(raw)
        return common.ArtifactRef(
            artifact_id=name.replace(".", "-"), content_hash=common.ContentHash(sha256=_hash(raw)),
            storage_key=path.relative_to(ROOT).as_posix(), media_type=media_type,
            byte_size=len(raw), schema_version=1,
        )

    def _bundle(self) -> dict:
        question = {
            "meta": {"schema_version": 1, "corpus_id": "corpus", "record_id": "question"},
            "question": "Pertanyaan tanpa bukti?", "base_question_group": "group",
            "split": "DATASET_SPLIT_TEST", "slice_labels": ["unanswerable"],
            "temporal_scope": {"mode": "TEMPORAL_MODE_CURRENT", "unresolved_policy": "UNRESOLVED_POLICY_REPORT"},
            "answerability": "ANSWERABILITY_UNANSWERABLE",
            "reviews": [{"reviewer": "human", "reviewed_at": "2026-01-01T00:00:00Z", "method": "fixture"}],
        }
        dataset_artifact = self._artifact(
            "dataset.jsonl", (json.dumps(question) + "\n").encode(), "application/x-ndjson")
        runtime_artifact = self._artifact("runtime.yaml", b"profile: fixture\n", "application/yaml")
        corpus_document = {
            "schema_version": 1, "corpus_id": "corpus", "snapshot_id": "snapshot",
            "representation_generation": "generation", "documents": 1, "chunks": 1,
            "canonical_entities": 1, "graph_edges": 1,
        }
        corpus_artifact = self._artifact(
            "corpus.json", json.dumps(corpus_document, sort_keys=True).encode(), "application/json")
        snapshot = {
            "corpus_id": "corpus", "snapshot_id": "snapshot", "sequence": "1",
            "manifest_hash": {"sha256": corpus_artifact.content_hash.sha256},
            "representation_generation": "generation",
        }
        dataset = json_format.ParseDict({
            "meta": {"schema_version": 1, "corpus_id": "corpus", "record_id": "dataset"},
            "version": "v1", "dataset_hash": {"sha256": dataset_artifact.content_hash.sha256},
            "corpus_snapshot": snapshot,
            "groups": [{"base_question_group": "group", "split": "DATASET_SPLIT_TEST"}],
            "label_schema": "v1",
            "reviews": [{"reviewer": "human", "reviewed_at": "2026-01-01T00:00:00Z", "method": "fixture"}],
        }, pb.DatasetManifest())
        protocol = {key: value for key, value in self.suite.protocol.items()
                    if key not in {"pass_rule", "eligibility", "quality_floor", "confidence_reporting"}}
        workloads = {
            name: {"status": "measured", "reason": "", "facts": {k: v for k, v in target.items() if k != "note"}}
            for name, target in self.suite.workloads.items()
        }
        for name in ("parsing_quality", "graph_quality"):
            workloads[name]["status"] = "blocked"
            workloads[name]["reason"] = "synthetic fixture has no semantic gold inventory"
        workload_document = {
            "schema_version": 1, "suite_id": self.suite.suite_id, "profile_id": "hybrid_graphrag",
            "protocol": protocol, "workloads": workloads,
            "eligibility_artifacts": {},
            "environment": {
                "storage": "local_nvme", "database_placement": "same_node",
                "generator_placement": "dedicated_separate_endpoint", "generator_network_rtt_p95_ms": 1,
                "generator_model_identity": "generate@v1", "generator_capacity_sustained": True,
                "generation_timing_includes_provider_and_network": True,
                "runtime_versions": {"go": "fixture", "rust": "fixture", "cpp": "fixture",
                                     "postgresql": "fixture", "neo4j": "fixture", "qdrant": "fixture"},
            },
            "runs": [{"run_id": run_id, "workload": "invariants", "warmup_seconds": 0,
                      "measured_seconds": 0, "sample_counts": {"INVARIANT.WIRE_PARITY": 1}}
                     for run_id in ("one", "two", "three")],
        }
        workload_artifact = self._artifact(
            "workload.json", json.dumps(workload_document, sort_keys=True).encode(), "application/json")
        manifest = json_format.ParseDict({
            "meta": {"schema_version": 1, "corpus_id": "corpus", "record_id": "e01-fixture"},
            "software_hash": {"sha256": "b" * 64},
            "config_hash": {"sha256": runtime_artifact.content_hash.sha256},
            "models": [
                {"model_id": "embed", "version": "v1", "weights_hash": {"sha256": "c" * 64},
                 "tokenizer_hash": {"sha256": "d" * 64}, "task": "MODEL_TASK_EMBED", "dimensions": 16,
                 "max_tokens": 128, "precision": "fp32", "backend": "fixture"},
                {"model_id": "rerank", "version": "v1", "weights_hash": {"sha256": "c" * 64},
                 "tokenizer_hash": {"sha256": "d" * 64}, "task": "MODEL_TASK_RERANK",
                 "max_tokens": 512, "precision": "fp32", "backend": "fixture"},
                {"model_id": "generate", "version": "v1", "weights_hash": {"sha256": "c" * 64},
                 "tokenizer_hash": {"sha256": "d" * 64}, "prompt_hash": {"sha256": "e" * 64},
                 "task": "MODEL_TASK_GENERATE", "max_tokens": 256, "precision": "fp32", "backend": "fixture"},
            ],
            "dataset_hash": {"sha256": dataset_artifact.content_hash.sha256},
            "corpus_snapshot": snapshot, "target_suite_hash": {"sha256": self.suite.sha256},
            "hardware": {
                "cpu": "fixture", "ram_bytes": str(64 << 30), "gpus": ["fixture"],
                "vram_bytes": [str(24 << 30)], "cpu_limit": 16,
                "memory_limit_bytes": str(64 << 30), "operating_system": "linux_x86_64",
            },
            "cache_policy": "acceptance caches disabled",
            "workload": json_format.MessageToDict(workload_artifact, preserving_proto_field_name=True),
        }, pb.RunManifest())
        return {
            "schema_version": 1,
            "profile_id": "hybrid_graphrag",
            "evaluation_config_sha256": self.config.sha256,
            "target_suite_sha256": self.suite.sha256,
            "dataset_manifest": json_format.MessageToDict(dataset, preserving_proto_field_name=True),
            "run_manifest": json_format.MessageToDict(manifest, preserving_proto_field_name=True),
            "protocol": protocol,
            "workloads": workloads,
            "measurements": {
                "INVARIANT.WIRE_PARITY": {
                    "workload": "invariants", "statistic": "ratio", "unit": "ratio",
                    "runs": [
                        {"run_id": run_id, "sample_count": 1,
                         "evidence": {"numerator": 1, "denominator": 1}}
                        for run_id in ("one", "two", "three")
                    ],
                },
            },
            "observation_runs": [],
            "supporting_artifacts": [
                json_format.MessageToDict(value, preserving_proto_field_name=True)
                for value in (dataset_artifact, corpus_artifact, runtime_artifact, workload_artifact)
            ],
        }

    def test_runner_writes_auditable_nonpassing_release_result(self):
        source = self.base / "bundle.json"
        source.write_text(json.dumps(self._bundle()), encoding="utf-8")
        output = self.base / "result"
        report = run_bundle(ROOT / "configs/evaluation.yaml", source, output, ROOT)
        self.assertEqual(report["acceptance_status"], "BLOCKED")
        self.assertEqual(report["gate_total"], 59)
        self.assertEqual(report["gate_counts"]["PASS"], 0)
        self.assertEqual(report["gate_counts"]["BLOCKED"], 59)
        for name in ("input.json", "observations.pb", "gate-results.pb", "gate-results.jsonl", "report.json"):
            self.assertTrue((output / name).is_file())
        wire = next(json.loads(line) for line in (output / "gate-results.jsonl").read_text(encoding="utf-8").splitlines()
                    if json.loads(line)["gate_id"] == "INVARIANT.WIRE_PARITY")
        self.assertEqual(wire["status"], "GATE_STATUS_BLOCKED")
        self.assertIn("published-record invariant inventory missing", wire["reason"])
        with self.assertRaises(RunnerInputError):
            run_bundle(ROOT / "configs/evaluation.yaml", source, output, ROOT)

    def test_runner_rejects_tampered_supporting_artifact(self):
        bundle = self._bundle()
        source = self.base / "bundle.json"
        source.write_text(json.dumps(bundle), encoding="utf-8")
        (self.base / "runtime.yaml").write_bytes(b"tampered\n")
        with self.assertRaisesRegex(RunnerInputError, "size/hash mismatch"):
            run_bundle(ROOT / "configs/evaluation.yaml", source, self.base / "bad-result", ROOT)

    def test_runner_requires_observations_for_telemetry_owned_gate(self):
        bundle = self._bundle()
        bundle["measurements"] = {"QUERY.SUCCESS": {
            "workload": "retrieval", "statistic": "ratio", "unit": "ratio",
            "runs": [{"run_id": run_id, "sample_count": 10000,
                      "evidence": {"numerator": 10000, "denominator": 10000}}
                     for run_id in ("one", "two", "three")],
        }}
        source = self.base / "manual-telemetry.json"
        source.write_text(json.dumps(bundle), encoding="utf-8")
        with self.assertRaisesRegex(RunnerInputError, "telemetry-owned"):
            run_bundle(ROOT / "configs/evaluation.yaml", source, self.base / "manual-result", ROOT)


if __name__ == "__main__":
    unittest.main()
