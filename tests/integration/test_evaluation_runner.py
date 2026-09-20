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

    def _artifact(self, name: str, raw: bytes) -> common.ArtifactRef:
        path = self.base / name
        path.write_bytes(raw)
        return common.ArtifactRef(
            artifact_id=name.replace(".", "-"), content_hash=common.ContentHash(sha256=_hash(raw)),
            storage_key=path.relative_to(ROOT).as_posix(), media_type="application/octet-stream",
            byte_size=len(raw), schema_version=1,
        )

    def _bundle(self) -> dict:
        dataset_artifact = self._artifact("dataset.jsonl", b'{"question":"fixture"}\n')
        runtime_artifact = self._artifact("runtime.yaml", b"profile: fixture\n")
        workload_artifact = self._artifact("workload.json", b'{"workload":"frozen"}\n')
        snapshot = {
            "corpus_id": "corpus", "snapshot_id": "snapshot", "sequence": "1",
            "manifest_hash": {"sha256": "a" * 64}, "representation_generation": "generation",
        }
        dataset = json_format.ParseDict({
            "meta": {"schema_version": 1, "corpus_id": "corpus", "record_id": "dataset"},
            "version": "v1", "dataset_hash": {"sha256": dataset_artifact.content_hash.sha256},
            "corpus_snapshot": snapshot,
            "groups": [{"base_question_group": "group", "split": "DATASET_SPLIT_TEST"}],
            "label_schema": "v1",
            "reviews": [{"reviewer": "human", "reviewed_at": "2026-01-01T00:00:00Z", "method": "fixture"}],
        }, pb.DatasetManifest())
        manifest = json_format.ParseDict({
            "meta": {"schema_version": 1, "corpus_id": "corpus", "record_id": "e01-fixture"},
            "software_hash": {"sha256": "b" * 64},
            "config_hash": {"sha256": runtime_artifact.content_hash.sha256},
            "models": [{
                "model_id": "fixture", "version": "v1", "weights_hash": {"sha256": "c" * 64},
                "tokenizer_hash": {"sha256": "d" * 64}, "task": "MODEL_TASK_GENERATE",
                "max_tokens": 256, "precision": "fp32", "backend": "fixture",
            }],
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
        protocol = {key: value for key, value in self.suite.protocol.items()
                    if key not in {"pass_rule", "eligibility", "quality_floor", "confidence_reporting"}}
        workloads = {
            name: {"status": "measured", "reason": "", "facts": {k: v for k, v in target.items() if k != "note"}}
            for name, target in self.suite.workloads.items()
        }
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
                "QUERY.SUCCESS": {
                    "workload": "retrieval", "statistic": "ratio", "unit": "ratio",
                    "runs": [
                        {"run_id": run_id, "sample_count": 10000,
                         "evidence": {"numerator": 10000, "denominator": 10000}}
                        for run_id in ("one", "two", "three")
                    ],
                },
            },
            "observation_runs": [],
            "supporting_artifacts": [
                json_format.MessageToDict(value, preserving_proto_field_name=True)
                for value in (dataset_artifact, runtime_artifact, workload_artifact)
            ],
        }

    def test_runner_writes_auditable_nonpassing_release_result(self):
        source = self.base / "bundle.json"
        source.write_text(json.dumps(self._bundle()), encoding="utf-8")
        output = self.base / "result"
        report = run_bundle(ROOT / "configs/evaluation.yaml", source, output, ROOT)
        self.assertEqual(report["acceptance_status"], "NOT_MEASURED")
        self.assertEqual(report["gate_total"], 59)
        self.assertEqual(report["gate_counts"]["PASS"], 1)
        for name in ("input.json", "observations.pb", "gate-results.pb", "gate-results.jsonl", "report.json"):
            self.assertTrue((output / name).is_file())
        query = next(json.loads(line) for line in (output / "gate-results.jsonl").read_text(encoding="utf-8").splitlines()
                     if json.loads(line)["gate_id"] == "QUERY.SUCCESS")
        self.assertEqual(query["status"], "GATE_STATUS_PASS")
        with self.assertRaises(RunnerInputError):
            run_bundle(ROOT / "configs/evaluation.yaml", source, output, ROOT)

    def test_runner_rejects_tampered_supporting_artifact(self):
        bundle = self._bundle()
        source = self.base / "bundle.json"
        source.write_text(json.dumps(bundle), encoding="utf-8")
        (self.base / "runtime.yaml").write_bytes(b"tampered\n")
        with self.assertRaisesRegex(RunnerInputError, "size/hash mismatch"):
            run_bundle(ROOT / "configs/evaluation.yaml", source, self.base / "bad-result", ROOT)


if __name__ == "__main__":
    unittest.main()
