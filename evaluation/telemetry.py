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


TELEMETRY_GATES = frozenset({
    "QUERY.EVIDENCE_P95", "QUERY.EVIDENCE_P99", "QUERY.SUCCESS",
    "QUERY.TTFT_P95", "QUERY.TTFT_P99", "QUERY.FULL_P95", "QUERY.FULL_P99",
    "QUERY.ANSWER_SUCCESS", "GO.OVERHEAD_P95", "SEARCH.LEXICAL_P95", "SEARCH.DENSE_P95",
    "SEARCH.GRAPH_P95", "MODEL.QUERY_EMBED_P95", "MODEL.RERANK_P95", "MODEL.BATCH_WAIT_P99",
    "CONTEXT.BUILD_P95",
})


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


def _timestamp_ns(observation: pb.Observation) -> int:
    return observation.scheduled_arrival.seconds * 1_000_000_000 + observation.scheduled_arrival.nanos


def validate_observation_protocol(run_id: str, workload: str, observations: list[pb.Observation],
                                  corpus_id: str, target: Mapping[str, Any],
                                  protocol: Mapping[str, Any]) -> None:
    """Verify provenance, open-loop schedule, duration, deadlines, and token-length workload facts."""
    validate_observation_run(run_id, observations)
    if any(observation.meta.corpus_id != corpus_id for observation in observations):
        raise TelemetryError("observation corpus differs from RunManifest")
    required = target.get("requests_per_run_min")
    rate = target.get("requests_per_second")
    if not isinstance(required, int) or not isinstance(rate, int) or required <= 0 or rate <= 0:
        raise TelemetryError("observation workload lacks integer request count/rate")
    if len(observations) < required:
        raise TelemetryError("observation run is below requests_per_run_min")
    expected_interval = 1_000_000_000 // rate
    if expected_interval * rate != 1_000_000_000:
        raise TelemetryError("request rate cannot be represented exactly in nanoseconds")
    scheduled = [_timestamp_ns(observation) for observation in observations]
    if any(right - left != expected_interval for left, right in zip(scheduled, scheduled[1:])):
        raise TelemetryError("scheduled arrivals do not follow the constant open-loop rate")
    if len(observations) / rate < protocol["measured_seconds_min"]:
        raise TelemetryError("observation measurement window is too short")
    deadline_ns = int(target["completion_deadline_ms"]) * 1_000_000
    for observation in observations:
        if observation.outcome == common.COMPLETION_STATUS_SUCCEEDED:
            if observation.completion_ns <= 0 or observation.completion_ns > deadline_ns:
                raise TelemetryError("successful observation violates completion deadline")
            for stage in observation.stage_durations:
                if stage.duration_ns + stage.queue_ns > observation.completion_ns:
                    raise TelemetryError("stage duration/queue exceeds end-to-end completion")
        if workload == "retrieval" and observation.HasField("first_substantive_token_ns"):
            raise TelemetryError("retrieval observation cannot contain answer TTFT")
        if (workload == "answer" and observation.outcome == common.COMPLETION_STATUS_SUCCEEDED
                and (not observation.HasField("first_substantive_token_ns")
                     or observation.first_substantive_token_ns == 0)):
            raise TelemetryError("successful answer observation requires substantive TTFT")
    if workload == "answer":
        succeeded = [observation for observation in observations
                     if observation.outcome == common.COMPLETION_STATUS_SUCCEEDED and not observation.rejected_arrival]
        if any(not observation.HasField("tokens") for observation in succeeded):
            raise TelemetryError("successful answer observation requires token usage")
        if any(observation.tokens.input_tokens > target["model_input_tokens_max"]
               or observation.tokens.output_tokens > target["output_tokens_max"] for observation in succeeded):
            raise TelemetryError("answer token usage exceeds workload maximum")
        if succeeded:
            output_tokens = sorted(observation.tokens.output_tokens for observation in succeeded)
            median = output_tokens[max(0, math.ceil(0.50 * len(output_tokens)) - 1)]
            p90 = output_tokens[max(0, math.ceil(0.90 * len(output_tokens)) - 1)]
            if median < target["output_tokens_p50_min"] or p90 < target["output_tokens_p90_min"]:
                raise TelemetryError("answer output-token distribution is below workload minimum")


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
