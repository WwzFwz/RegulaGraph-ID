"""Strict E01 configuration, profile, and target-suite loading without import-time I/O.

Peran arsitektur:
Loader membekukan YAML evaluator menjadi dataclass immutable dan SHA-256 yang dipakai RunManifest. Ia
menjaga satu sumber threshold serta mencegah profile atau flag boolean tidak konsisten masuk ke runner.

Kontrak integrasi dan performa:
Path harus berada di root repositori, unknown field ditolak, target relaxation harus false, dan empat
profile eksperimen harus tepat. File baru dibaca ketika fungsi loader dipanggil; import tidak membuka file
atau memuat model. Parsing berlangsung offline dan tidak termasuk request latency.

Benchmark dan status:
Angka hanya dibaca dari configs/benchmark-targets.yaml dan perubahan target memerlukan persetujuan
pengguna. Implementasi loader E01 aktif serta diuji terhadap unknown key, invalid gate/profile, path escape,
dan fingerprint; hasil benchmark produksi tetap REQUIRED_UNMEASURED.
"""
from __future__ import annotations

from dataclasses import dataclass
import hashlib
import math
from pathlib import Path
from types import MappingProxyType
from typing import Any, Mapping

import yaml


class ConfigError(ValueError):
    """A configuration cannot be interpreted without changing benchmark semantics."""


def _mapping(value: Any, path: str) -> dict[str, Any]:
    if not isinstance(value, dict) or any(not isinstance(k, str) for k in value):
        raise ConfigError(f"{path} must be a string-keyed mapping")
    return value


def _keys(value: Mapping[str, Any], allowed: set[str], required: set[str], path: str) -> None:
    unknown = set(value) - allowed
    missing = required - set(value)
    if unknown or missing:
        raise ConfigError(f"{path}: unknown={sorted(unknown)} missing={sorted(missing)}")


def _finite_number(value: Any, path: str) -> float:
    if isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value):
        raise ConfigError(f"{path} must be a finite number")
    return float(value)


@dataclass(frozen=True)
class GateDefinition:
    gate_id: str
    required: bool
    workload: str
    statistic: str
    unit: str
    operator: str
    value: float
    definition: str


@dataclass(frozen=True)
class TargetSuite:
    path: Path
    sha256: str
    suite_id: str
    required_profile: str
    target_status: str
    protocol: Mapping[str, Any]
    reference: Mapping[str, Any]
    workloads: Mapping[str, Any]
    gates: Mapping[str, GateDefinition]


@dataclass(frozen=True)
class EvaluationConfig:
    path: Path
    sha256: str
    dataset: str | None
    corpus_snapshot: str | None
    target_file: Path
    target_suite: str
    release_profile: str
    profiles_file: Path
    artifact_root: Path
    strict_unknown_fields: bool


def _read_yaml(path: Path) -> tuple[bytes, dict[str, Any]]:
    try:
        raw = path.read_bytes()
        loaded = yaml.safe_load(raw)
    except (OSError, yaml.YAMLError) as exc:
        raise ConfigError(f"cannot load {path}: {exc}") from exc
    return raw, _mapping(loaded, str(path))


def load_target_suite(path: str | Path) -> TargetSuite:
    target_path = Path(path).resolve()
    raw, doc = _read_yaml(target_path)
    required_root = {
        "schema_version", "suite_id", "target_status", "defined_on", "required_for_release",
        "other_experiment_profiles", "target_change_authority", "automatic_relaxation",
        "on_gate_failure", "implementation_fixes_require_approval", "benchmark_changes_require_user_approval",
        "runner_implemented", "reference", "protocol", "workloads", "informational_metrics", "gates",
    }
    _keys(doc, required_root, required_root, "target suite")
    if doc["schema_version"] != 1 or doc["target_status"] != "REQUIRED_UNMEASURED":
        raise ConfigError("target suite schema/status is unsupported")
    if doc["target_change_authority"] != "user" or doc["automatic_relaxation"] is not False:
        raise ConfigError("target suite permits unauthorized relaxation")
    if doc["benchmark_changes_require_user_approval"] is not True:
        raise ConfigError("target change approval policy is not enforced")
    gates_raw = _mapping(doc["gates"], "gates")
    if not gates_raw:
        raise ConfigError("target suite has no gates")
    gates: dict[str, GateDefinition] = {}
    fields = {"required", "workload", "statistic", "unit", "operator", "value", "definition"}
    for gate_id, raw_gate in gates_raw.items():
        gate = _mapping(raw_gate, f"gates.{gate_id}")
        _keys(gate, fields, fields, f"gates.{gate_id}")
        if gate["operator"] not in {"lte", "gte"}:
            raise ConfigError(f"gates.{gate_id}: unsupported operator")
        if gate["workload"] not in doc["workloads"]:
            raise ConfigError(f"gates.{gate_id}: unknown workload")
        if not gate_id or any(ord(c) < 33 or ord(c) > 126 for c in gate_id):
            raise ConfigError(f"invalid gate ID {gate_id!r}")
        gates[gate_id] = GateDefinition(
            gate_id, gate["required"] is True, str(gate["workload"]), str(gate["statistic"]),
            str(gate["unit"]), str(gate["operator"]), _finite_number(gate["value"], gate_id), str(gate["definition"]),
        )
    return TargetSuite(
        target_path, hashlib.sha256(raw).hexdigest(), str(doc["suite_id"]),
        str(doc["required_for_release"]), str(doc["target_status"]),
        MappingProxyType(_mapping(doc["protocol"], "protocol")),
        MappingProxyType(_mapping(doc["reference"], "reference")),
        MappingProxyType(_mapping(doc["workloads"], "workloads")), MappingProxyType(gates),
    )


def _repo_path(root: Path, value: Any, field: str) -> Path:
    if not isinstance(value, str) or not value:
        raise ConfigError(f"{field} must be a non-empty repository-relative path")
    path = (root / value).resolve()
    if path != root and root not in path.parents:
        raise ConfigError(f"{field} escapes repository root")
    return path


def load_evaluation_config(path: str | Path, repo_root: str | Path | None = None) -> EvaluationConfig:
    config_path = Path(path).resolve()
    root = Path(repo_root).resolve() if repo_root else config_path.parents[1]
    raw, doc = _read_yaml(config_path)
    fields = {
        "schema_version", "status", "dataset", "corpus_snapshot", "target_file", "target_suite",
        "target_status", "release_profile", "automatic_target_relaxation", "profiles_file",
        "artifact_root", "strict_unknown_fields",
    }
    _keys(doc, fields, fields, "evaluation config")
    if doc["schema_version"] != 1 or doc["status"] != "active":
        raise ConfigError("evaluation config must be schema 1 and active")
    if doc["automatic_target_relaxation"] is not False or doc["target_status"] != "REQUIRED_UNMEASURED":
        raise ConfigError("evaluation config attempts to relax or misstate targets")
    for optional in ("dataset", "corpus_snapshot"):
        if doc[optional] is not None and not isinstance(doc[optional], str):
            raise ConfigError(f"{optional} must be null or a path")
    return EvaluationConfig(
        config_path, hashlib.sha256(raw).hexdigest(), doc["dataset"], doc["corpus_snapshot"],
        _repo_path(root, doc["target_file"], "target_file"), str(doc["target_suite"]),
        str(doc["release_profile"]), _repo_path(root, doc["profiles_file"], "profiles_file"),
        _repo_path(root, doc["artifact_root"], "artifact_root"), doc["strict_unknown_fields"] is True,
    )


def load_profiles(path: str | Path) -> Mapping[str, Mapping[str, Any]]:
    _, doc = _read_yaml(Path(path).resolve())
    _keys(doc, {"schema_version", "status", "profiles", "acceptance"}, {"schema_version", "status", "profiles", "acceptance"}, "profiles")
    if doc["schema_version"] != 1 or doc["status"] != "active":
        raise ConfigError("experiment profiles must be schema 1 and active")
    profiles = _mapping(doc["profiles"], "profiles.profiles")
    if set(profiles) != {"vector_rag", "hybrid_rag", "graph_rag", "hybrid_graphrag"}:
        raise ConfigError("exactly four named experiment profiles are required")
    profile_fields = {"retrievers", "graph_traversal", "lexical", "dense", "release_required"}
    checked: dict[str, Mapping[str, Any]] = {}
    for name, raw_profile in profiles.items():
        profile = _mapping(raw_profile, f"profiles.{name}")
        _keys(profile, profile_fields, profile_fields, f"profiles.{name}")
        retrievers = profile["retrievers"]
        if (not isinstance(retrievers, list) or not retrievers
                or any(not isinstance(value, str) for value in retrievers)
                or len(retrievers) != len(set(retrievers))
                or any(value not in {"lexical", "dense", "graph"} for value in retrievers)):
            raise ConfigError(f"profiles.{name}.retrievers is invalid")
        for field in ("graph_traversal", "lexical", "dense", "release_required"):
            if not isinstance(profile[field], bool):
                raise ConfigError(f"profiles.{name}.{field} must be boolean")
        expected = {
            "lexical": "lexical" in profile["retrievers"],
            "dense": "dense" in profile["retrievers"],
            "graph_traversal": "graph" in profile["retrievers"],
        }
        if any(profile[field] != value for field, value in expected.items()):
            raise ConfigError(f"profiles.{name} retriever flags disagree")
        if profile["release_required"] != (name == "hybrid_graphrag"):
            raise ConfigError(f"profiles.{name}.release_required disagrees with acceptance policy")
        checked[name] = MappingProxyType(profile)
    acceptance = _mapping(doc["acceptance"], "profiles.acceptance")
    if acceptance.get("required_profile") != "hybrid_graphrag" or acceptance.get("baseline_policy") != "report_only":
        raise ConfigError("experiment acceptance policy changed")
    return MappingProxyType(checked)
