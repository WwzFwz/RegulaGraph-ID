"""
Menjalankan evaluasi E01 dari bundle frozen dan menghasilkan artefak audit yang immutable.

Peran dalam komponen:
Runner menghubungkan dataset, manifest, profile, telemetry produksi, metrik, dan evaluator gate tanpa
menjadi serving path atau menyalin algoritma Go/Rust/C++. Semua record typed berasal dari kontrak C01.

Kontrak integrasi dan perhatian implementasi:
Input JSON memakai unknown-field rejection, hash target/config yang tepat, file artifact yang benar-benar
ada, serta manifest dataset/run yang konsisten. Observation mencakup seluruh scheduled arrival dan dapat
menurunkan latency/success/stage evidence. Evidence manual tetap memerlukan raw artifact terverifikasi.
Output ditulis ke direktori baru secara atomik: input asli, protobuf length-delimited, JSONL GateResult,
dan report JSON. Direktori hasil yang sudah ada tidak ditimpa.

Benchmark dan gate penerimaan:
[EVAL] Setiap run merekam dataset/split, corpus snapshot, model/prompt/config version, seed bila relevan,
serta biaya. Ukuran sampel dan ketidakpastian harus eksplisit; quality, retrieval, graph, answer, latency,
dan biaya tidak digabung menjadi satu skor. Target numerik required tetap configs/benchmark-targets.yaml
dengan status REQUIRED_UNMEASURED sampai run produksi yang sah tersedia. Target hanya boleh diubah dengan
persetujuan pengguna; ikuti doc/benchmark-policy.md.

Status: runner dan evaluator gate E01 aktif untuk bundle artefak/telemetry. Runner tidak menjalankan
pipeline produksi sendiri; adapter benchmark mendatang harus menulis bundle ini dari endpoint/artefak
produksi. Missing evidence menjadi NOT_MEASURED, prasyarat kurang menjadi BLOCKED, threshold terlampaui
menjadi FAIL, dan baseline profile tetap report-only.
Bukti verifikasi: invalid/empty run, artifact/hash mismatch, workload minimum, failed/rejected request,
infinity latency, unit/stat mismatch, threshold boundary, output typed, serta larangan overwrite.
"""
from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
import sys
from typing import Any, Mapping, Sequence

from google.protobuf import json_format
from google.protobuf.message import DecodeError
import evaluation_pb2 as pb
from regulagraph.v1 import common_pb2 as common

from evaluation.config import ConfigError, load_evaluation_config, load_profiles, load_target_suite
from evaluation.datasets.schema import validate
from evaluation.gates import GateInputError, evaluate_gates
from evaluation.telemetry import TelemetryError, derive_observation_measurements, write_delimited


class RunnerInputError(ValueError):
    """The bundle cannot be evaluated without trusting missing or inconsistent evidence."""


_ROOT_FIELDS = {
    "schema_version", "profile_id", "evaluation_config_sha256", "target_suite_sha256",
    "dataset_manifest", "run_manifest", "protocol", "workloads", "measurements",
    "observation_runs", "supporting_artifacts",
}


def _strict_object(value: Any, fields: set[str], path: str) -> Mapping[str, Any]:
    if not isinstance(value, dict):
        raise RunnerInputError(f"{path} must be an object")
    if set(value) != fields:
        raise RunnerInputError(f"{path}: unknown={sorted(set(value) - fields)} missing={sorted(fields - set(value))}")
    return value


def _load_json(path: Path) -> tuple[bytes, Mapping[str, Any]]:
    try:
        raw = path.read_bytes()
        if len(raw) > 256 << 20:
            raise RunnerInputError("input bundle exceeds 256 MiB")
        value = json.loads(raw, parse_constant=lambda value: (_ for _ in ()).throw(ValueError(value)))
    except (OSError, UnicodeError, json.JSONDecodeError, ValueError) as exc:
        raise RunnerInputError(f"cannot load input bundle: {exc}") from exc
    return raw, _strict_object(value, _ROOT_FIELDS, "bundle")


def _parse_message(value: Any, message: Any, path: str) -> Any:
    if not isinstance(value, dict):
        raise RunnerInputError(f"{path} must be a ProtoJSON object")
    try:
        parsed = json_format.ParseDict(value, message, ignore_unknown_fields=False)
        return validate(parsed)
    except (json_format.ParseError, DecodeError, ValueError, UnicodeError) as exc:
        raise RunnerInputError(f"invalid {path}: {exc}") from exc


def _sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for block in iter(lambda: source.read(1 << 20), b""):
            digest.update(block)
    return digest.hexdigest()


def _artifact_path(repo_root: Path, artifact: common.ArtifactRef) -> Path:
    path = (repo_root / Path(artifact.storage_key)).resolve()
    if path != repo_root and repo_root not in path.parents:
        raise RunnerInputError(f"artifact escapes repository: {artifact.artifact_id}")
    if not path.is_file():
        raise RunnerInputError(f"artifact missing: {artifact.storage_key}")
    if path.stat().st_size != artifact.byte_size or _sha256(path) != artifact.content_hash.sha256:
        raise RunnerInputError(f"artifact size/hash mismatch: {artifact.artifact_id}")
    return path


def _validate_supporting_artifacts(repo_root: Path, values: Any, manifest: pb.RunManifest,
                                   dataset: pb.DatasetManifest) -> tuple[common.ArtifactRef, ...]:
    if not isinstance(values, list):
        raise RunnerInputError("supporting_artifacts must be a list")
    artifacts = tuple(_parse_message(value, common.ArtifactRef(), f"supporting_artifacts[{index}]")
                      for index, value in enumerate(values))
    hashes: set[str] = set()
    artifact_ids: set[str] = set()
    for artifact in artifacts:
        _artifact_path(repo_root, artifact)
        if artifact.artifact_id in artifact_ids:
            raise RunnerInputError("supporting artifact IDs must be unique")
        artifact_ids.add(artifact.artifact_id)
        hashes.add(artifact.content_hash.sha256)
    required = {dataset.dataset_hash.sha256, manifest.config_hash.sha256, manifest.workload.content_hash.sha256}
    if not required <= hashes:
        raise RunnerInputError("dataset, runtime config, and workload artifacts must all be supplied and verified")
    if not any(artifact == manifest.workload for artifact in artifacts):
        raise RunnerInputError("RunManifest workload ArtifactRef must be supplied exactly")
    return artifacts


def _parse_observation_runs(values: Any) -> tuple[list[tuple[str, str, list[pb.Observation]]], list[pb.Observation]]:
    if not isinstance(values, list):
        raise RunnerInputError("observation_runs must be a list")
    parsed: list[tuple[str, str, list[pb.Observation]]] = []
    flat: list[pb.Observation] = []
    seen: set[tuple[str, str]] = set()
    for index, value in enumerate(values):
        item = _strict_object(value, {"run_id", "workload", "observations"}, f"observation_runs[{index}]")
        if not isinstance(item["run_id"], str) or not item["run_id"] or not isinstance(item["workload"], str) or not item["workload"]:
            raise RunnerInputError("observation run ID/workload must be non-empty strings")
        if not isinstance(item["observations"], list):
            raise RunnerInputError("observations must be a list")
        observations = [_parse_message(raw, pb.Observation(), f"observation_runs[{index}].observations[{offset}]")
                        for offset, raw in enumerate(item["observations"])]
        key = (item["run_id"], item["workload"])
        if key in seen:
            raise RunnerInputError("duplicate observation run/workload")
        seen.add(key)
        parsed.append((item["run_id"], item["workload"], observations))
        flat.extend(observations)
    return parsed, flat


def _artifact_ref(path: Path, repo_root: Path, artifact_id: str, media_type: str,
                  storage_path: Path | None = None) -> common.ArtifactRef:
    try:
        storage_key = (storage_path or path).resolve().relative_to(repo_root).as_posix()
    except ValueError as exc:
        raise RunnerInputError("output must remain inside repository root") from exc
    artifact = common.ArtifactRef(
        artifact_id=artifact_id,
        content_hash=common.ContentHash(sha256=_sha256(path)),
        storage_key=storage_key,
        media_type=media_type,
        byte_size=path.stat().st_size,
        schema_version=1,
    )
    return validate(artifact)


def _json_message(message: Any) -> Mapping[str, Any]:
    return json_format.MessageToDict(message, preserving_proto_field_name=True)


def run_bundle(config_path: str | Path, input_path: str | Path, output_path: str | Path | None = None,
               repo_root: str | Path | None = None) -> Mapping[str, Any]:
    """Validate and evaluate one immutable bundle, returning its report dictionary."""
    config_file = Path(config_path).resolve()
    root = Path(repo_root).resolve() if repo_root else config_file.parents[1]
    config = load_evaluation_config(config_file, root)
    suite = load_target_suite(config.target_file)
    profiles = load_profiles(config.profiles_file)
    if suite.suite_id != config.target_suite or suite.required_profile != config.release_profile:
        raise RunnerInputError("evaluation config and target suite disagree")
    raw, bundle = _load_json(Path(input_path).resolve())
    if isinstance(bundle["schema_version"], bool) or bundle["schema_version"] != 1:
        raise RunnerInputError("unsupported bundle schema_version")
    profile_id = bundle["profile_id"]
    if not isinstance(profile_id, str) or profile_id not in profiles:
        raise RunnerInputError("unknown profile_id")
    if bundle["evaluation_config_sha256"] != config.sha256 or bundle["target_suite_sha256"] != suite.sha256:
        raise RunnerInputError("frozen config/target hash mismatch")
    dataset = _parse_message(bundle["dataset_manifest"], pb.DatasetManifest(), "dataset_manifest")
    manifest = _parse_message(bundle["run_manifest"], pb.RunManifest(), "run_manifest")
    _validate_supporting_artifacts(root, bundle["supporting_artifacts"], manifest, dataset)
    observation_runs, observations = _parse_observation_runs(bundle["observation_runs"])
    unknown_observation_workloads = {workload for _, workload, _ in observation_runs} - set(suite.workloads)
    if unknown_observation_workloads:
        raise RunnerInputError(f"unknown observation workloads: {sorted(unknown_observation_workloads)}")
    try:
        derived = derive_observation_measurements(observation_runs)
    except TelemetryError as exc:
        raise RunnerInputError(f"invalid telemetry: {exc}") from exc
    if not isinstance(bundle["measurements"], dict):
        raise RunnerInputError("measurements must be an object")
    overlap = set(bundle["measurements"]) & set(derived)
    if overlap:
        raise RunnerInputError(f"manual measurements conflict with telemetry-derived gates: {sorted(overlap)}")
    measurements = {**bundle["measurements"], **derived}

    run_id = manifest.meta.record_id
    final = Path(output_path).resolve() if output_path else (config.artifact_root / run_id).resolve()
    if final != root and root not in final.parents:
        raise RunnerInputError("output path escapes repository root")
    partial = final.with_name(final.name + ".partial")
    if final.exists() or partial.exists():
        raise RunnerInputError("output directory already exists")
    partial.mkdir(parents=True)
    try:
        input_copy = partial / "input.json"
        input_copy.write_bytes(raw)
        input_artifact = _artifact_ref(input_copy, root, f"{run_id}.input", "application/json",
                                       final / "input.json")
        summary = evaluate_gates(suite, manifest, profile_id, dataset, bundle["protocol"],
                                 bundle["workloads"], measurements, input_artifact)
        write_delimited(partial / "observations.pb", observations)
        write_delimited(partial / "gate-results.pb", summary.results)
        with (partial / "gate-results.jsonl").open("w", encoding="utf-8", newline="\n") as output:
            for result in summary.results:
                output.write(json.dumps(_json_message(result), sort_keys=True, ensure_ascii=False) + "\n")
        release_profile = profile_id == suite.required_profile
        report = {
            "schema_version": 1,
            "run_id": run_id,
            "profile_id": profile_id,
            "acceptance_mode": "release" if release_profile else "report_only",
            "acceptance_status": summary.release_status if release_profile else "REPORT_ONLY",
            "evaluated_status": summary.release_status,
            "gate_counts": dict(summary.counts),
            "gate_total": len(summary.results),
            "evaluation_config_sha256": config.sha256,
            "target_suite": suite.suite_id,
            "target_suite_sha256": suite.sha256,
            "input_sha256": hashlib.sha256(raw).hexdigest(),
            "result_files": ["input.json", "observations.pb", "gate-results.pb", "gate-results.jsonl"],
            "limitations": [
                "Synthetic/unit verification does not prove production quality or performance.",
                "A PASS claim requires every applicable required gate on the frozen reference workload.",
            ],
        }
        (partial / "report.json").write_text(json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8")
        partial.replace(final)
        return report
    except Exception:
        # Preserve the partial directory as failure evidence; a future run must use a new run ID/path.
        raise


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Evaluate a frozen RegulaGraph E01 bundle")
    parser.add_argument("--config", default="configs/evaluation.yaml")
    parser.add_argument("--input", required=True)
    parser.add_argument("--output")
    args = parser.parse_args(argv)
    try:
        report = run_bundle(args.config, args.input, args.output)
    except (RunnerInputError, ConfigError, GateInputError, OSError) as exc:
        print(f"evaluation input error: {exc}", file=sys.stderr)
        return 2
    print(json.dumps(report, sort_keys=True))
    return 0 if report["acceptance_status"] in {"PASS", "REPORT_ONLY"} else 1


if __name__ == "__main__":
    raise SystemExit(main())
