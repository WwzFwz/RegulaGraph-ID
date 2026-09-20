"""Validate, persist, and derive E01 measurements from C01 Observation records.

Peran arsitektur:
Modul telemetry mengubah observasi typed dari load generator/production trace menjadi evidence mentah yang
dapat dinilai ``evaluation.gates`` serta menyimpan protobuf length-delimited untuk audit dan replay.

Kontrak integrasi dan perhatian performa:
Satu run harus memuat setiap scheduled arrival, termasuk rejected, failed, timed-out, dan unfinished.
Latency dimulai pada arrival terjadwal; completion/TTFT yang hilang menjadi positive infinity agar request
gagal tidak hilang. Stage gate hanya diturunkan bila seluruh observasi memiliki stage terkait. Penulisan
artefak atomik dan tidak berada pada request path produksi.

Benchmark dan status:
Gunakan p95/p99 nearest-rank, queue time, denominator seluruh arrival, dan minimum workload dari
configs/benchmark-targets.yaml. Target hanya boleh diubah dengan persetujuan pengguna. Implementasi E01
aktif untuk evidence/query latency, answer TTFT/full latency, success ratio, dan stage latency; hasil
benchmark produksi tetap REQUIRED_UNMEASURED. Inter-token latency masih harus diberikan sebagai evidence
eksplisit karena kontrak Observation belum menyimpan timestamp tiap token.
"""
from __future__ import annotations

import math
from pathlib import Path
from typing import Any, Iterable, Mapping

from google.protobuf.message import Message

import evaluation_pb2 as pb
from regulagraph.v1 import common_pb2 as common

from evaluation.datasets.schema import validate


class TelemetryError(ValueError):
    """Observation telemetry is incomplete, duplicated, or inconsistent."""


def _varint(value: int) -> bytes:
    output = bytearray()
    while value > 0x7F:
        output.append((value & 0x7F) | 0x80)
        value >>= 7
    output.append(value)
    return bytes(output)


def write_delimited(path: Path, messages: Iterable[Message]) -> None:
    """Atomically write protobuf messages with varint length prefixes."""
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(path.name + ".partial")
    with temporary.open("wb") as output:
        for message in messages:
            raw = message.SerializeToString(deterministic=True)
            output.write(_varint(len(raw)))
            output.write(raw)
        output.flush()
    temporary.replace(path)


def validate_observation_run(run_id: str, observations: list[pb.Observation]) -> None:
    if not run_id or not observations:
        raise TelemetryError("observation run must have an ID and scheduled arrivals")
    request_ids: set[str] = set()
    for observation in observations:
        validate(observation)
        if observation.run_id != run_id:
            raise TelemetryError("observation run_id mismatch")
        if observation.request_id in request_ids:
            raise TelemetryError("duplicate request_id in observation run")
        request_ids.add(observation.request_id)
        stages = [stage.stage for stage in observation.stage_durations]
        if len(stages) != len(set(stages)):
            raise TelemetryError("duplicate stage name in observation")


def _measurement(workload: str, statistic: str, unit: str, runs: list[dict[str, Any]]) -> dict[str, Any]:
    return {"workload": workload, "statistic": statistic, "unit": unit, "runs": runs}


def derive_observation_measurements(observation_runs: list[tuple[str, str, list[pb.Observation]]]) -> Mapping[str, Any]:
    """Derive end-to-end and fully-instrumented stage gates from typed observations."""
    derived: dict[str, dict[str, Any]] = {}
    stage_gates = {
        "go.overhead": ("GO.OVERHEAD_P95", "p95", "ms", False),
        "search.lexical": ("SEARCH.LEXICAL_P95", "p95", "ms", True),
        "search.dense": ("SEARCH.DENSE_P95", "p95", "ms", True),
        "search.graph": ("SEARCH.GRAPH_P95", "p95", "ms", True),
        "model.query_embed": ("MODEL.QUERY_EMBED_P95", "p95", "ms", True),
        "model.rerank": ("MODEL.RERANK_P95", "p95", "ms", True),
        "model.batch_wait": ("MODEL.BATCH_WAIT_P99", "p99", "ms", False),
        "context.build": ("CONTEXT.BUILD_P95", "p95", "ms", True),
    }
    for run_id, workload, observations in observation_runs:
        validate_observation_run(run_id, observations)
        success = sum(o.outcome == common.COMPLETION_STATUS_SUCCEEDED and not o.rejected_arrival for o in observations)
        if workload == "retrieval":
            latency = [o.completion_ns / 1_000_000 if o.completion_ns and o.outcome == common.COMPLETION_STATUS_SUCCEEDED and not o.rejected_arrival else math.inf for o in observations]
            for gate_id, statistic in (("QUERY.EVIDENCE_P95", "p95"), ("QUERY.EVIDENCE_P99", "p99")):
                derived.setdefault(gate_id, _measurement(workload, statistic, "ms", []))["runs"].append(
                    {"run_id": run_id, "sample_count": len(observations), "evidence": {"samples": latency}})
            derived.setdefault("QUERY.SUCCESS", _measurement(workload, "ratio", "ratio", []))["runs"].append(
                {"run_id": run_id, "sample_count": len(observations),
                 "evidence": {"numerator": success, "denominator": len(observations)}})
        elif workload == "answer":
            ttft = [o.first_substantive_token_ns / 1_000_000 if o.HasField("first_substantive_token_ns") and o.outcome == common.COMPLETION_STATUS_SUCCEEDED and not o.rejected_arrival else math.inf for o in observations]
            completion = [o.completion_ns / 1_000_000 if o.completion_ns and o.outcome == common.COMPLETION_STATUS_SUCCEEDED and not o.rejected_arrival else math.inf for o in observations]
            for gate_id, statistic, samples in (("QUERY.TTFT_P95", "p95", ttft), ("QUERY.TTFT_P99", "p99", ttft),
                                                 ("QUERY.FULL_P95", "p95", completion), ("QUERY.FULL_P99", "p99", completion)):
                derived.setdefault(gate_id, _measurement(workload, statistic, "ms", []))["runs"].append(
                    {"run_id": run_id, "sample_count": len(observations), "evidence": {"samples": samples}})
            derived.setdefault("QUERY.ANSWER_SUCCESS", _measurement(workload, "ratio", "ratio", []))["runs"].append(
                {"run_id": run_id, "sample_count": len(observations),
                 "evidence": {"numerator": success, "denominator": len(observations)}})
        if workload == "retrieval":
            stages_per_observation = [{stage.stage: stage for stage in observation.stage_durations} for observation in observations]
            for stage_name, (gate_id, statistic, unit, include_queue) in stage_gates.items():
                if all(stage_name in stages for stages in stages_per_observation):
                    samples = [
                        (stages[stage_name].duration_ns + (stages[stage_name].queue_ns if include_queue else 0)) / 1_000_000
                        for stages in stages_per_observation
                    ]
                    derived.setdefault(gate_id, _measurement(workload, statistic, unit, []))["runs"].append(
                        {"run_id": run_id, "sample_count": len(observations), "evidence": {"samples": samples}})
    return derived
