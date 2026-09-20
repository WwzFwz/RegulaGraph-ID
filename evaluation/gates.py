"""Evaluate every configured E01 gate from strict, per-run raw measurement evidence.

Peran arsitektur:
Modul ini menjadi satu-satunya penerjemah target ``configs/benchmark-targets.yaml`` menjadi status
``GateResult`` C01. Inputnya adalah suite tervalidasi, manifest dataset/run, fakta workload, dan evidence
mentah per run; outputnya satu hasil untuk setiap gate tanpa menjalankan ulang algoritma produksi.

Kontrak integrasi dan performa:
Setiap run dinilai sendiri dan nilai terburuk dilaporkan, sehingga averaging tidak dapat menyembunyikan
run gagal. ``sample_count`` wajib memenuhi ukuran workload sebenarnya. Prasyarat tidak lengkap menjadi
BLOCKED, evidence absen menjadi NOT_MEASURED, dan threshold terlampaui menjadi FAIL. Target numerik tetap
bersumber dari configs/benchmark-targets.yaml dan hanya boleh berubah dengan persetujuan pengguna.

Status: implementasi evaluator E01 aktif; hasil benchmark produksi tetap REQUIRED_UNMEASURED.
Bukti verifikasi: threshold boundary, sampel kurang, denominator kosong, NaN/infinity, manifest mismatch,
rejected/unfinished arrival, unit salah, run kurang, dan required gate tanpa evidence.
"""
from __future__ import annotations

from dataclasses import dataclass
import hashlib
import math
from typing import Any, Mapping, Sequence

import evaluation_pb2 as pb
from regulagraph.v1 import common_pb2 as common

from evaluation.config import GateDefinition, TargetSuite
from evaluation.datasets.schema import validate
from evaluation.metrics.runtime import Estimate, MetricError, estimate


class GateInputError(ValueError):
    """The measurement bundle cannot be evaluated without guessing semantics."""


@dataclass(frozen=True)
class EvaluationSummary:
    results: tuple[pb.GateResult, ...]
    release_status: str
    counts: Mapping[str, int]


_STATUS = {
    "PASS": pb.GATE_STATUS_PASS,
    "FAIL": pb.GATE_STATUS_FAIL,
    "BLOCKED": pb.GATE_STATUS_BLOCKED,
    "NOT_MEASURED": pb.GATE_STATUS_NOT_MEASURED,
    "NOT_APPLICABLE": pb.GATE_STATUS_NOT_APPLICABLE,
}


def _strict_mapping(value: Any, fields: set[str], required: set[str], path: str) -> Mapping[str, Any]:
    if not isinstance(value, dict):
        raise GateInputError(f"{path} must be an object")
    unknown, missing = set(value) - fields, required - set(value)
    if unknown or missing:
        raise GateInputError(f"{path}: unknown={sorted(unknown)} missing={sorted(missing)}")
    return value


def _compare_fact(actual: Any, expected: Any, key: str) -> bool:
    if key.endswith("_min") or key.endswith("_min_ratio"):
        return isinstance(actual, (int, float)) and not isinstance(actual, bool) and actual >= expected
    if key.endswith("_max"):
        return isinstance(actual, (int, float)) and not isinstance(actual, bool) and actual <= expected
    return actual == expected


def workload_errors(target: Mapping[str, Any], actual: Mapping[str, Any]) -> list[str]:
    """Compare all machine-readable workload facts; notes are explanatory only."""
    if not isinstance(actual, dict):
        return ["workload facts must be an object"]
    errors: list[str] = []
    expected_keys = {key for key in target if key != "note"}
    if set(actual) != expected_keys:
        errors.append(f"workload facts keys expected={sorted(expected_keys)} actual={sorted(actual)}")
        return errors
    for key in sorted(expected_keys):
        if not _compare_fact(actual[key], target[key], key):
            errors.append(f"workload fact {key} expected {target[key]!r}, got {actual[key]!r}")
    return errors


def protocol_errors(target: Mapping[str, Any], actual: Mapping[str, Any]) -> list[str]:
    expected = {key: value for key, value in target.items() if key not in {"pass_rule", "eligibility", "quality_floor", "confidence_reporting"}}
    if set(actual) != set(expected):
        return [f"protocol keys expected={sorted(expected)} actual={sorted(actual)}"]
    return [f"protocol {key} expected {value!r}, got {actual[key]!r}" for key, value in expected.items() if actual[key] != value]


def manifest_errors(manifest: pb.RunManifest, suite: TargetSuite, dataset_manifest: pb.DatasetManifest | None) -> list[str]:
    errors: list[str] = []
    try:
        validate(manifest)
    except (ValueError, UnicodeError) as exc:
        return [f"invalid RunManifest: {exc}"]
    if manifest.target_suite_hash.sha256 != suite.sha256:
        errors.append("target-suite hash mismatch")
    if dataset_manifest is None:
        errors.append("dataset manifest missing")
    else:
        try:
            validate(dataset_manifest)
        except (ValueError, UnicodeError) as exc:
            errors.append(f"invalid DatasetManifest: {exc}")
        if manifest.dataset_hash != dataset_manifest.dataset_hash:
            errors.append("dataset hash mismatch")
        if manifest.corpus_snapshot != dataset_manifest.corpus_snapshot:
            errors.append("dataset/run corpus snapshot mismatch")
    reference = suite.reference["application_node"]
    hardware = manifest.hardware
    if hardware.cpu_limit < reference["cpu_physical_cores"]:
        errors.append("CPU limit below reference")
    gib = 1 << 30
    if hardware.ram_bytes < reference["ram_gib"] * gib or hardware.memory_limit_bytes < reference["ram_gib"] * gib:
        errors.append("RAM/resource limit below reference")
    if len(hardware.gpus) < reference["gpu_count"] or not hardware.vram_bytes or max(hardware.vram_bytes) < reference["gpu_vram_gib"] * gib:
        errors.append("GPU/VRAM below reference")
    if hardware.operating_system != reference["reference_os"]:
        errors.append("operating system differs from reference")
    if not manifest.models:
        errors.append("model identities not pinned")
    return errors


def _result(manifest: pb.RunManifest, gate: GateDefinition, status: str, reason: str, denominator: int = 0,
            value: float | None = None, uncertainty: float | None = None,
            raw_artifacts: Sequence[common.ArtifactRef] = ()) -> pb.GateResult:
    record_id = f"{manifest.meta.record_id}.{gate.gate_id}"
    if len(record_id) > 256:
        record_id = f"gate-result-{hashlib.sha256(record_id.encode('ascii')).hexdigest()}"
    result = pb.GateResult(
        meta=common.RecordMeta(schema_version=1, corpus_id=manifest.meta.corpus_id,
                               record_id=record_id),
        gate_id=gate.gate_id, applicable_workload=gate.workload, denominator=denominator,
        threshold_reference=f"{gate.gate_id}:{gate.operator}:{gate.value:g}:{gate.unit}",
        status=_STATUS[status], reason=reason, raw_artifacts=raw_artifacts,
    )
    if value is not None:
        result.estimate = value
    if uncertainty is not None:
        result.uncertainty = uncertainty
    validate(result)
    return result


def _minimum_samples(gate: GateDefinition, workload: Mapping[str, Any]) -> float:
    """Return the primary declared unit required for one run of this gate."""
    special = {
        "PARSING.CRITICAL_TOKENS": "annotated_critical_tokens_min",
        "PARSING.OCR_CER": "standard_scan_pages_min",
        "EXTRACTION.PRECISION": "labeled_relations_min",
        "EXTRACTION.RECALL": "labeled_relations_min",
        "RESOLUTION.PAIR_PRECISION": "same_entity_pairs_min",
        "RESOLUTION.PAIR_RECALL": "same_entity_pairs_min",
        "RESOLUTION.BLOCKING_RECALL": "same_entity_pairs_min",
    }
    candidates = [special.get(gate.gate_id, ""), "requests_per_run_min", "test_questions_min",
                  "annotated_pages_min", "pages_min", "input_mib_min", "resolved_relations", "edges",
                  "chunks", "events_min"]
    for key in candidates:
        if key and key in workload:
            value = workload[key]
            if isinstance(value, (int, float)) and not isinstance(value, bool):
                return float(value)
    return 1.0


def _assess_run(gate: GateDefinition, run: Mapping[str, Any], required_slices: Sequence[str],
                workload: Mapping[str, Any]) -> tuple[Estimate, float | None]:
    run = _strict_mapping(run, {"run_id", "sample_count", "evidence", "uncertainty"},
                          {"run_id", "sample_count", "evidence"}, f"{gate.gate_id}.run")
    if not isinstance(run["run_id"], str) or not run["run_id"]:
        raise MetricError("run_id must be non-empty")
    sample_count = run["sample_count"]
    if isinstance(sample_count, bool) or not isinstance(sample_count, (int, float)) or not math.isfinite(sample_count) or sample_count <= 0:
        raise MetricError("sample_count must be finite and positive")
    minimum = _minimum_samples(gate, workload)
    if sample_count < minimum:
        raise MetricError(f"sample_count {sample_count:g} below workload minimum {minimum:g}")
    value = estimate(gate.statistic, run["evidence"], required_slices)
    uncertainty = run.get("uncertainty")
    if gate.workload == "quality" and uncertainty is None:
        raise MetricError("quality run requires grouped-bootstrap uncertainty")
    if uncertainty is not None and (isinstance(uncertainty, bool) or not isinstance(uncertainty, (int, float)) or not math.isfinite(uncertainty) or uncertainty < 0):
        raise MetricError("uncertainty must be finite and non-negative")
    return value, float(uncertainty) if uncertainty is not None else None


def _passes(gate: GateDefinition, value: float) -> bool:
    return value <= gate.value if gate.operator == "lte" else value >= gate.value


def evaluate_gates(suite: TargetSuite, manifest: pb.RunManifest, profile_id: str,
                   dataset_manifest: pb.DatasetManifest | None, protocol: Mapping[str, Any],
                   workloads: Mapping[str, Any], measurements: Mapping[str, Any],
                   input_artifact: common.ArtifactRef) -> EvaluationSummary:
    """Return one result per configured gate; invalid/missing evidence never becomes PASS."""
    if not isinstance(workloads, dict) or not isinstance(measurements, dict) or not isinstance(protocol, dict):
        raise GateInputError("protocol, workloads and measurements must be objects")
    unknown_workloads = set(workloads) - set(suite.workloads)
    if unknown_workloads:
        raise GateInputError(f"unknown workloads: {sorted(unknown_workloads)}")
    global_errors = manifest_errors(manifest, suite, dataset_manifest) + protocol_errors(suite.protocol, protocol)
    unknown_measurements = set(measurements) - set(suite.gates)
    if unknown_measurements:
        raise GateInputError(f"unknown measurement gates: {sorted(unknown_measurements)}")
    release_profile = profile_id == suite.required_profile
    expected_runs = int(suite.protocol["performance_runs"])
    required_slices = [value for value in suite.workloads["quality"].get("slices", []) if value != "unanswerable"]
    results: list[pb.GateResult] = []
    for gate in suite.gates.values():
        workload = workloads.get(gate.workload)
        if workload is None:
            results.append(_result(manifest, gate, "BLOCKED", "workload declaration missing"))
            continue
        try:
            workload = _strict_mapping(workload, {"status", "reason", "facts"}, {"status", "reason", "facts"}, f"workloads.{gate.workload}")
            if not isinstance(workload["status"], str) or not isinstance(workload["reason"], str):
                raise GateInputError(f"workloads.{gate.workload} status/reason must be strings")
            if not isinstance(workload["facts"], dict):
                raise GateInputError(f"workloads.{gate.workload}.facts must be an object")
        except GateInputError as exc:
            results.append(_result(manifest, gate, "BLOCKED", str(exc)))
            continue
        if workload["status"] == "not_applicable":
            if not workload["reason"]:
                results.append(_result(manifest, gate, "BLOCKED", "not_applicable workload requires a reason"))
                continue
            if release_profile and gate.required:
                results.append(_result(manifest, gate, "BLOCKED", "required release workload cannot be not_applicable"))
            else:
                results.append(_result(manifest, gate, "NOT_APPLICABLE", str(workload["reason"])))
            continue
        if workload["status"] == "blocked":
            results.append(_result(manifest, gate, "BLOCKED", workload["reason"] or "workload blocked without a reason"))
            continue
        if workload["status"] != "measured":
            results.append(_result(manifest, gate, "BLOCKED", "unknown workload status"))
            continue
        prerequisite_errors = global_errors + workload_errors(suite.workloads[gate.workload], workload["facts"])
        if prerequisite_errors:
            results.append(_result(manifest, gate, "BLOCKED", "; ".join(prerequisite_errors)))
            continue
        measurement = measurements.get(gate.gate_id)
        if measurement is None:
            results.append(_result(manifest, gate, "NOT_MEASURED", "no measurement supplied"))
            continue
        try:
            measurement = _strict_mapping(measurement, {"workload", "statistic", "unit", "runs"},
                                          {"workload", "statistic", "unit", "runs"}, gate.gate_id)
            if (measurement["workload"], measurement["statistic"], measurement["unit"]) != (gate.workload, gate.statistic, gate.unit):
                raise MetricError("workload/statistic/unit mismatch")
            runs = measurement["runs"]
            if not isinstance(runs, list) or not runs:
                raise MetricError("empty runs")
            if gate.required and len(runs) < expected_runs:
                raise MetricError(f"requires at least {expected_runs} valid runs")
            if len({run.get("run_id") for run in runs if isinstance(run, dict)}) != len(runs):
                raise MetricError("duplicate run IDs")
            assessed = [_assess_run(gate, run, required_slices, suite.workloads[gate.workload]) for run in runs]
        except (GateInputError, MetricError, TypeError) as exc:
            results.append(_result(manifest, gate, "BLOCKED", f"invalid measurement: {exc}"))
            continue
        failed = [value for value, _ in assessed if not _passes(gate, value.value)]
        worst = max((value for value, _ in assessed), key=lambda item: item.value) if gate.operator == "lte" else min((value for value, _ in assessed), key=lambda item: item.value)
        uncertainties = [uncertainty for _, uncertainty in assessed if uncertainty is not None]
        finite_value = worst.value if math.isfinite(worst.value) else None
        status = "FAIL" if failed else "PASS"
        reason = f"{len(assessed)} independent runs; worst {gate.operator} threshold {gate.value:g} {gate.unit}"
        if finite_value is None:
            reason += "; unfinished arrival produced infinite latency"
        results.append(_result(manifest, gate, status, reason, sum(item.denominator for item, _ in assessed),
                               finite_value, max(uncertainties) if uncertainties else None, (input_artifact,)))
    counts = {name: 0 for name in _STATUS}
    reverse = {value: name for name, value in _STATUS.items()}
    for result in results:
        counts[reverse[result.status]] += 1
    required = [result for result, gate in zip(results, suite.gates.values()) if gate.required]
    if any(result.status == pb.GATE_STATUS_FAIL for result in required):
        release_status = "FAIL"
    elif any(result.status == pb.GATE_STATUS_BLOCKED for result in required):
        release_status = "BLOCKED"
    elif any(result.status == pb.GATE_STATUS_NOT_MEASURED for result in required):
        release_status = "NOT_MEASURED"
    elif all(result.status == pb.GATE_STATUS_PASS for result in required):
        release_status = "PASS"
    else:
        release_status = "BLOCKED"
    return EvaluationSummary(tuple(results), release_status, counts)
