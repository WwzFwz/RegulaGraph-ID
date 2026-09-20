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
import math
from pathlib import Path
import sys
from typing import Any, Mapping, Sequence

from google.protobuf import json_format
from google.protobuf.message import DecodeError
import evaluation_pb2 as pb
from regulagraph.v1 import common_pb2 as common

from evaluation.config import ConfigError, load_evaluation_config, load_profiles, load_target_suite
from evaluation.datasets.loader import (DatasetLoadError, corpus_eligibility_errors,
                                        dataset_eligibility_errors, load_corpus_facts,
                                        load_gold_questions, load_id_inventory)
from evaluation.datasets.schema import validate
from evaluation.gates import GateInputError, evaluate_gates
from evaluation.telemetry import (TELEMETRY_GATES, TelemetryError, derive_observation_measurements,
                                  validate_observation_protocol, write_delimited)


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
    except (json_format.ParseError, DecodeError, TypeError, ValueError, UnicodeError) as exc:
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
    required = {dataset.dataset_hash.sha256, manifest.config_hash.sha256, manifest.workload.content_hash.sha256,
                manifest.corpus_snapshot.manifest_hash.sha256}
    if not required <= hashes:
        raise RunnerInputError("dataset, corpus manifest, runtime config, and workload artifacts must all be supplied and verified")
    if not any(artifact == manifest.workload for artifact in artifacts):
        raise RunnerInputError("RunManifest workload ArtifactRef must be supplied exactly")
    return artifacts


def _artifact_with_hash(artifacts: Sequence[common.ArtifactRef], sha256: str,
                        media_type: str) -> common.ArtifactRef:
    matches = [artifact for artifact in artifacts if artifact.content_hash.sha256 == sha256]
    if len(matches) != 1:
        raise RunnerInputError(f"expected exactly one supporting artifact for hash {sha256}")
    if matches[0].media_type != media_type:
        raise RunnerInputError(f"artifact {matches[0].artifact_id} must use media type {media_type}")
    return matches[0]


def _artifact_with_id(artifacts: Sequence[common.ArtifactRef], artifact_id: str) -> common.ArtifactRef:
    matches = [artifact for artifact in artifacts if artifact.artifact_id == artifact_id]
    if len(matches) != 1 or matches[0].media_type != "application/json":
        raise RunnerInputError(f"eligibility artifact {artifact_id!r} is missing or has the wrong media type")
    return matches[0]


def _plain(value: Any) -> Any:
    if isinstance(value, Mapping):
        return {key: _plain(item) for key, item in value.items()}
    if isinstance(value, (list, tuple)):
        return [_plain(item) for item in value]
    return value


def _load_workload_evidence(path: Path, suite: Any, profile_id: str, protocol: Any,
                            workloads: Any, manifest: pb.RunManifest) -> tuple[
                                Mapping[tuple[str, str], Mapping[str, Any]], list[str], Mapping[str, str]]:
    """Validate the immutable workload/environment document referenced by RunManifest."""
    fields = {"schema_version", "suite_id", "profile_id", "protocol", "workloads", "environment",
              "eligibility_artifacts", "runs"}
    try:
        raw = path.read_bytes()
        if len(raw) > 16 << 20:
            raise RunnerInputError("workload evidence exceeds 16 MiB")
        value = json.loads(raw)
    except (OSError, UnicodeError, json.JSONDecodeError) as exc:
        raise RunnerInputError(f"cannot load workload evidence: {exc}") from exc
    value = _strict_object(value, fields, "workload artifact")
    if (isinstance(value["schema_version"], bool) or value["schema_version"] != 1
            or value["suite_id"] != suite.suite_id or value["profile_id"] != profile_id):
        raise RunnerInputError("workload artifact schema/suite/profile mismatch")
    if value["protocol"] != _plain(protocol) or value["workloads"] != _plain(workloads):
        raise RunnerInputError("workload artifact does not pin the bundle protocol/workloads")
    environment_fields = {
        "storage", "database_placement", "generator_placement", "generator_network_rtt_p95_ms",
        "generator_model_identity", "generator_capacity_sustained",
        "generation_timing_includes_provider_and_network", "runtime_versions",
    }
    environment = _strict_object(value["environment"], environment_fields, "workload artifact.environment")
    for field in ("storage", "database_placement", "generator_placement", "generator_model_identity"):
        if not isinstance(environment[field], str) or not environment[field]:
            raise RunnerInputError(f"workload environment {field} must be a non-empty string")
    for field in ("generator_capacity_sustained", "generation_timing_includes_provider_and_network"):
        if not isinstance(environment[field], bool):
            raise RunnerInputError(f"workload environment {field} must be boolean")
    versions = environment["runtime_versions"]
    required_versions = {"go", "rust", "cpp", "postgresql", "neo4j", "qdrant"}
    if (not isinstance(versions, dict) or not required_versions <= set(versions)
            or any(not isinstance(key, str) or not isinstance(item, str) or not item for key, item in versions.items())):
        raise RunnerInputError("runtime_versions must pin Go, Rust, C++, PostgreSQL, Neo4j, and Qdrant")
    errors: list[str] = []
    application = suite.reference["application_node"]
    generator = suite.reference["generator"]
    if environment["storage"] != application["storage"]:
        errors.append("storage differs from reference")
    if environment["database_placement"] != application["database_placement"]:
        errors.append("database placement differs from reference")
    if environment["generator_placement"] != generator["placement"]:
        errors.append("generator placement differs from reference")
    rtt = environment["generator_network_rtt_p95_ms"]
    try:
        finite_rtt = float(rtt)
    except (TypeError, ValueError, OverflowError) as exc:
        raise RunnerInputError("generator_network_rtt_p95_ms must be finite and non-negative") from exc
    if isinstance(rtt, bool) or not math.isfinite(finite_rtt) or finite_rtt < 0:
        raise RunnerInputError("generator_network_rtt_p95_ms must be finite and non-negative")
    if finite_rtt > generator["network_rtt_p95_ms_max"]:
        errors.append("generator network RTT exceeds reference")
    if environment["generator_capacity_sustained"] is not True:
        errors.append("generator capacity was not sustained")
    if environment["generation_timing_includes_provider_and_network"] is not generator["timing_includes_provider_and_network"]:
        errors.append("generation timing boundary differs from reference")
    generator_ids = {f"{model.model_id}@{model.version}" for model in manifest.models
                     if model.task == common.MODEL_TASK_GENERATE}
    if environment["generator_model_identity"] not in generator_ids:
        errors.append("generator environment identity differs from RunManifest")
    if not isinstance(value["runs"], list):
        raise RunnerInputError("workload artifact.runs must be a list")
    eligibility_artifacts = value["eligibility_artifacts"]
    if (not isinstance(eligibility_artifacts, dict)
            or any(key not in {"parsing_quality", "graph_quality", "invariants"} or not isinstance(item, str) or not item
                   for key, item in eligibility_artifacts.items())):
        raise RunnerInputError("eligibility_artifacts must map supported workloads to artifact IDs")
    for workload_name in ("parsing_quality", "graph_quality", "invariants"):
        declaration = workloads.get(workload_name) if isinstance(workloads, dict) else None
        if isinstance(declaration, dict) and declaration.get("status") == "measured" and workload_name not in eligibility_artifacts:
            errors.append(f"{workload_name} measured without a gold eligibility artifact")
    run_fields = {"run_id", "workload", "warmup_seconds", "measured_seconds", "sample_counts"}
    runs: dict[tuple[str, str], Mapping[str, Any]] = {}
    for index, raw_run in enumerate(value["runs"]):
        run = _strict_object(raw_run, run_fields, f"workload artifact.runs[{index}]")
        if (not isinstance(run["run_id"], str) or not run["run_id"]
                or not isinstance(run["workload"], str) or run["workload"] not in suite.workloads):
            raise RunnerInputError("workload run ID/workload is invalid")
        for field in ("warmup_seconds", "measured_seconds"):
            if isinstance(run[field], bool) or not isinstance(run[field], int) or run[field] < 0:
                raise RunnerInputError(f"workload run {field} must be a non-negative integer")
        sample_counts = run["sample_counts"]
        if not isinstance(sample_counts, dict) or not sample_counts:
            raise RunnerInputError("workload run sample_counts must be a non-empty object")
        for gate_id, count in sample_counts.items():
            if (gate_id not in suite.gates or suite.gates[gate_id].workload != run["workload"]
                    or isinstance(count, bool) or not isinstance(count, int) or count <= 0):
                raise RunnerInputError("workload run sample_counts contains an invalid gate/count")
        key = (run["run_id"], run["workload"])
        if key in runs:
            raise RunnerInputError("duplicate workload run declaration")
        runs[key] = run
    return runs, errors, eligibility_artifacts


def _inventory_evidence(repo_root: Path, artifacts: Sequence[common.ArtifactRef],
                        artifact_ids: Mapping[str, str], suite: Any,
                        workloads: Mapping[str, Any], corpus_facts: Any) -> tuple[Mapping[str, set[str]], list[str]]:
    populations: dict[str, set[str]] = {}
    errors: list[str] = []
    if "parsing_quality" in artifact_ids:
        reference = _artifact_with_id(artifacts, artifact_ids["parsing_quality"])
        inventory = load_id_inventory(_artifact_path(repo_root, reference), (
            "annotated_page_ids", "standard_scan_page_ids", "difficult_scan_page_ids", "critical_token_ids"))
        target = suite.workloads["parsing_quality"]
        for field, target_field in (("annotated_page_ids", "annotated_pages_min"),
                                    ("standard_scan_page_ids", "standard_scan_pages_min"),
                                    ("difficult_scan_page_ids", "difficult_scan_pages_min_report_only"),
                                    ("critical_token_ids", "annotated_critical_tokens_min")):
            if len(inventory[field]) < target[target_field]:
                errors.append(f"{field} population {len(inventory[field])} below {target[target_field]}")
        populations.update({
            "PARSING.STRUCTURE_F1": set(inventory["annotated_page_ids"]),
            "PARSING.OCR_CER": set(inventory["standard_scan_page_ids"]),
            "PARSING.CRITICAL_TOKENS": set(inventory["critical_token_ids"]),
        })
    else:
        parsing_declaration = workloads.get("parsing_quality") if isinstance(workloads, dict) else None
        if isinstance(parsing_declaration, dict) and parsing_declaration.get("status") == "measured":
            errors.append("parsing quality inventory missing")
    if "graph_quality" in artifact_ids:
        reference = _artifact_with_id(artifacts, artifact_ids["graph_quality"])
        inventory = load_id_inventory(_artifact_path(repo_root, reference), (
            "relation_ids", "mention_ids", "same_entity_pair_ids", "confusing_different_pair_ids"))
        target = suite.workloads["graph_quality"]
        for field, target_field in (("relation_ids", "labeled_relations_min"),
                                    ("mention_ids", "labeled_mentions_min"),
                                    ("same_entity_pair_ids", "same_entity_pairs_min"),
                                    ("confusing_different_pair_ids", "confusing_different_entity_pairs_min")):
            if len(inventory[field]) < target[target_field]:
                errors.append(f"{field} population {len(inventory[field])} below {target[target_field]}")
        pair_universe = set(inventory["same_entity_pair_ids"]) | set(inventory["confusing_different_pair_ids"])
        populations.update({
            "EXTRACTION.PRECISION": set(inventory["relation_ids"]),
            "EXTRACTION.RECALL": set(inventory["relation_ids"]),
            "RESOLUTION.PAIR_PRECISION": pair_universe,
            "RESOLUTION.PAIR_RECALL": set(inventory["same_entity_pair_ids"]),
            "RESOLUTION.BLOCKING_RECALL": set(inventory["same_entity_pair_ids"]),
        })
    else:
        graph_declaration = workloads.get("graph_quality") if isinstance(workloads, dict) else None
        if isinstance(graph_declaration, dict) and graph_declaration.get("status") == "measured":
            errors.append("graph quality inventory missing")
    if "invariants" in artifact_ids:
        reference = _artifact_with_id(artifacts, artifact_ids["invariants"])
        inventory = load_id_inventory(_artifact_path(repo_root, reference),
                                      ("published_chunk_ids", "published_edge_ids", "published_citation_ids",
                                       "retrieval_request_ids", "update_event_ids", "wire_fixture_ids"))
        if len(inventory["published_chunk_ids"]) != corpus_facts.chunks:
            errors.append("published chunk inventory differs from corpus manifest")
        if len(inventory["published_edge_ids"]) != corpus_facts.graph_edges:
            errors.append("published edge inventory differs from corpus manifest")
        if not inventory["published_citation_ids"]:
            errors.append("published citation inventory is empty")
        if len(inventory["retrieval_request_ids"]) < suite.workloads["retrieval"]["requests_per_run_min"]:
            errors.append("snapshot invariant request inventory is below retrieval minimum")
        if len(inventory["update_event_ids"]) < suite.workloads["update"]["events_min"]:
            errors.append("update invariant inventory is below event minimum")
        if not inventory["wire_fixture_ids"]:
            errors.append("wire fixture inventory is empty")
        source_population = {
            *(f"chunk:{item}" for item in inventory["published_chunk_ids"]),
            *(f"edge:{item}" for item in inventory["published_edge_ids"]),
            *(f"citation:{item}" for item in inventory["published_citation_ids"]),
        }
        populations["INVARIANT.SOURCE_MAPPING"] = source_population
        populations["INVARIANT.ORPHAN_EDGES"] = {
            f"edge:{item}" for item in inventory["published_edge_ids"]
        }
        populations["INVARIANT.SNAPSHOT_MISMATCH"] = set(inventory["retrieval_request_ids"])
        populations["INVARIANT.UNCHANGED_REEXTRACTION"] = set(inventory["update_event_ids"])
        populations["INVARIANT.REPLAY_DIFF"] = set(inventory["update_event_ids"])
        populations["INVARIANT.WIRE_PARITY"] = set(inventory["wire_fixture_ids"])
    else:
        invariant_declaration = workloads.get("invariants") if isinstance(workloads, dict) else None
        if isinstance(invariant_declaration, dict) and invariant_declaration.get("status") == "measured":
            errors.append("published-record invariant inventory missing")
    return populations, errors


def _quality_populations(questions: Sequence[pb.GoldQuestion]) -> Mapping[str, set[str]]:
    test = [question for question in questions if question.split == pb.DATASET_SPLIT_TEST]
    answerable = {question.meta.record_id for question in test if question.answerability == pb.ANSWERABILITY_ANSWERABLE}
    unanswerable = {question.meta.record_id for question in test if question.answerability == pb.ANSWERABILITY_UNANSWERABLE}
    multi_hop = {question.meta.record_id for question in test
                 if question.answerability == pb.ANSWERABILITY_ANSWERABLE and "multi_hop" in question.slice_labels}
    temporal = {question.meta.record_id for question in test
                if question.answerability == pb.ANSWERABILITY_ANSWERABLE and "temporal" in question.slice_labels}
    populations = {
        gate_id: set(answerable) for gate_id in (
            "QUALITY.RECALL_AT_20", "QUALITY.NDCG_AT_10", "QUALITY.CONTEXT_COMPLETE",
            "QUALITY.ANSWER_CORRECT", "QUALITY.SLICE_MIN", "QUALITY.CITATION_PRECISION",
            "QUALITY.CITATION_COVERAGE", "QUALITY.FALSE_ABSTENTION", "PARITY.RECALL_DROP", "PARITY.NDCG_DROP")
    }
    populations["QUALITY.MULTIHOP_COMPLETE"] = multi_hop
    populations["QUALITY.TEMPORAL_ACCURACY"] = temporal
    populations["QUALITY.ABSTENTION_RECALL"] = unanswerable
    return populations


def _string_id_set(value: Any) -> set[str] | None:
    """Return validated IDs without allowing unhashable malformed values to escape as TypeError."""
    if not isinstance(value, list) or any(not isinstance(item, str) or not item for item in value):
        return None
    return set(value)


def _measurement_population_errors(measurements: Mapping[str, Any], populations: Mapping[str, set[str]],
                                   questions: Sequence[pb.GoldQuestion]) -> list[str]:
    errors: list[str] = []
    answerable_questions = {question.meta.record_id: question for question in questions
                            if question.split == pb.DATASET_SPLIT_TEST
                            and question.answerability == pb.ANSWERABILITY_ANSWERABLE}
    for gate_id, expected in populations.items():
        measurement = measurements.get(gate_id)
        if not isinstance(measurement, dict) or not isinstance(measurement.get("runs"), list):
            continue
        for run in measurement["runs"]:
            if not isinstance(run, dict):
                continue
            actual = run.get("population_ids")
            if (not isinstance(actual, list) or any(not isinstance(item, str) for item in actual)
                    or set(actual) != expected or len(actual) != len(expected)):
                errors.append(f"{gate_id} population IDs do not match the frozen gold population")
                continue
            evidence = run.get("evidence")
            if gate_id == "PARSING.STRUCTURE_F1" and isinstance(evidence, dict):
                counts = evidence.get("page_counts")
                if not isinstance(counts, dict) or set(counts) != expected:
                    errors.append("PARSING.STRUCTURE_F1 page outcomes do not cover annotated pages")
            if gate_id == "PARSING.OCR_CER" and isinstance(evidence, dict):
                counts = evidence.get("page_counts")
                if not isinstance(counts, dict) or set(counts) != expected:
                    errors.append("PARSING.OCR_CER page outcomes do not cover standard scan pages")
            if gate_id == "PARSING.CRITICAL_TOKENS" and isinstance(evidence, dict):
                correct, incorrect = evidence.get("correct_token_ids"), evidence.get("incorrect_token_ids")
                correct_ids, incorrect_ids = _string_id_set(correct), _string_id_set(incorrect)
                if (correct_ids is None or incorrect_ids is None
                        or correct_ids | incorrect_ids != expected or correct_ids & incorrect_ids):
                    errors.append("PARSING.CRITICAL_TOKENS outcomes do not partition critical tokens")
            if gate_id in {"EXTRACTION.PRECISION", "EXTRACTION.RECALL"} and isinstance(evidence, dict):
                matched, missed = evidence.get("matched_gold_relation_ids"), evidence.get("missed_gold_relation_ids")
                matched_ids, missed_ids = _string_id_set(matched), _string_id_set(missed)
                if (matched_ids is None or missed_ids is None
                        or matched_ids | missed_ids != expected or matched_ids & missed_ids):
                    errors.append(f"{gate_id} outcomes do not partition labeled gold relations")
            if gate_id in {"RESOLUTION.PAIR_PRECISION", "RESOLUTION.PAIR_RECALL"} and isinstance(evidence, dict):
                matched, missed = evidence.get("matched_same_pair_ids"), evidence.get("missed_same_pair_ids")
                same_pairs = populations.get("RESOLUTION.PAIR_RECALL", set())
                pair_universe = populations.get("RESOLUTION.PAIR_PRECISION", set())
                predicted = evidence.get("predicted_same_pair_ids")
                matched_ids, missed_ids, predicted_ids = (
                    _string_id_set(matched), _string_id_set(missed), _string_id_set(predicted))
                if (matched_ids is None or missed_ids is None or predicted_ids is None
                        or matched_ids | missed_ids != same_pairs or matched_ids & missed_ids
                        or not predicted_ids <= pair_universe):
                    errors.append(f"{gate_id} outcomes do not cover the frozen pair population")
            if gate_id == "RESOLUTION.BLOCKING_RECALL" and isinstance(evidence, dict):
                blocked, missed = evidence.get("blocked_same_pair_ids"), evidence.get("missed_same_pair_ids")
                blocked_ids, missed_ids = _string_id_set(blocked), _string_id_set(missed)
                if (blocked_ids is None or missed_ids is None
                        or blocked_ids | missed_ids != expected or blocked_ids & missed_ids):
                    errors.append("RESOLUTION.BLOCKING_RECALL outcomes do not partition same-entity pairs")
            invariant_fields = {
                "INVARIANT.SOURCE_MAPPING": ("mapped_record_ids", "unmapped_record_ids"),
                "INVARIANT.ORPHAN_EDGES": ("valid_edge_ids", "orphan_edge_ids"),
                "INVARIANT.SNAPSHOT_MISMATCH": ("compatible_request_ids", "mismatched_request_ids"),
                "INVARIANT.UNCHANGED_REEXTRACTION": (
                    "unchanged_without_reextraction_ids", "unexpectedly_reextracted_ids"),
                "INVARIANT.REPLAY_DIFF": ("identical_event_ids", "differing_event_ids"),
                "INVARIANT.WIRE_PARITY": ("matching_fixture_ids", "mismatching_fixture_ids"),
            }
            if gate_id in invariant_fields and isinstance(evidence, dict):
                good, bad = (evidence.get(field) for field in invariant_fields[gate_id])
                good_ids, bad_ids = _string_id_set(good), _string_id_set(bad)
                if (good_ids is None or bad_ids is None
                        or good_ids | bad_ids != expected or good_ids & bad_ids):
                    errors.append(f"{gate_id} outcomes do not partition its verified inventory")
            if gate_id in {"QUALITY.RECALL_AT_20", "QUALITY.NDCG_AT_10"} and isinstance(evidence, dict):
                scores = evidence.get("group_scores")
                if not isinstance(scores, dict) or set(scores) != expected:
                    errors.append(f"{gate_id} score IDs do not match its population IDs")
            exact_denominator_gates = {
                "QUALITY.MULTIHOP_COMPLETE", "QUALITY.CONTEXT_COMPLETE", "QUALITY.ANSWER_CORRECT",
                "QUALITY.TEMPORAL_ACCURACY", "QUALITY.ABSTENTION_RECALL", "QUALITY.FALSE_ABSTENTION",
                "PARITY.RECALL_DROP", "PARITY.NDCG_DROP",
            }
            if gate_id in exact_denominator_gates and isinstance(evidence, dict):
                denominator = evidence.get("denominator")
                if denominator != len(expected):
                    errors.append(f"{gate_id} denominator differs from its frozen gold population")
            if gate_id == "QUALITY.SLICE_MIN" and isinstance(evidence, dict):
                counts = evidence.get("slice_counts")
                if isinstance(counts, dict):
                    for slice_name, item in counts.items():
                        expected_count = sum(slice_name in question.slice_labels for question in answerable_questions.values())
                        if not isinstance(item, dict) or item.get("denominator") != expected_count:
                            errors.append(f"QUALITY.SLICE_MIN denominator for {slice_name} differs from gold")
    for left, right in (("EXTRACTION.PRECISION", "EXTRACTION.RECALL"),
                        ("RESOLUTION.PAIR_PRECISION", "RESOLUTION.PAIR_RECALL")):
        left_runs = measurements.get(left, {}).get("runs", []) if isinstance(measurements.get(left), dict) else []
        right_runs = measurements.get(right, {}).get("runs", []) if isinstance(measurements.get(right), dict) else []
        if not isinstance(left_runs, list):
            left_runs = []
        if not isinstance(right_runs, list):
            right_runs = []
        left_evidence = {run["run_id"]: run.get("evidence") for run in left_runs
                         if isinstance(run, dict) and isinstance(run.get("run_id"), str)}
        right_evidence = {run["run_id"]: run.get("evidence") for run in right_runs
                          if isinstance(run, dict) and isinstance(run.get("run_id"), str)}
        if left_evidence and right_evidence and left_evidence != right_evidence:
            errors.append(f"{left} and {right} must use identical per-run outcomes")
    return errors


def _measurement_run_errors(measurements: Mapping[str, Any], suite: Any,
                            declared_runs: Mapping[tuple[str, str], Mapping[str, Any]]) -> list[str]:
    errors: list[str] = []
    performance_workloads = {gate.workload for gate in suite.gates.values()
                             if gate.statistic in {"p50", "p95", "p99", "max", "throughput"}}
    performance_workloads.add("mixed_load")
    for gate_id, measurement in measurements.items():
        if gate_id not in suite.gates or not isinstance(measurement, dict) or not isinstance(measurement.get("runs"), list):
            continue
        workload = measurement.get("workload")
        if not isinstance(workload, str):
            errors.append(f"{gate_id} measurement workload is invalid")
            continue
        for run in measurement["runs"]:
            if not isinstance(run, dict) or not isinstance(run.get("run_id"), str):
                continue
            declared = declared_runs.get((run["run_id"], workload))
            if declared is None:
                errors.append(f"{gate_id} run {run['run_id']} absent from workload artifact")
                continue
            if run.get("sample_count") != declared["sample_counts"].get(gate_id):
                errors.append(f"{gate_id} run {run['run_id']} sample_count differs from workload artifact")
            if workload in performance_workloads:
                if declared["warmup_seconds"] < suite.protocol["warmup_seconds"]:
                    errors.append(f"{gate_id} run {run['run_id']} warmup is too short")
                if declared["measured_seconds"] < suite.protocol["measured_seconds_min"]:
                    errors.append(f"{gate_id} run {run['run_id']} measurement window is too short")
            evidence = run.get("evidence")
            gate = suite.gates[gate_id]
            if gate.statistic == "throughput" and isinstance(evidence, dict):
                duration = evidence.get("duration_seconds")
                if (isinstance(duration, bool) or not isinstance(duration, (int, float))
                        or duration != declared["measured_seconds"]):
                    errors.append(f"{gate_id} run {run['run_id']} throughput duration differs from measured window")
            if gate_id == "ISOLATION.INGESTION_RATIO" and isinstance(evidence, dict):
                if (evidence.get("mixed_duration_seconds") != declared["measured_seconds"]
                        or evidence.get("alone_duration_seconds") != declared["measured_seconds"]):
                    errors.append(f"{gate_id} run {run['run_id']} comparison durations differ from measured window")
            request_denominator = (
                gate_id == "ISOLATION.QUERY_P95_RATIO"
                or (workload in {"retrieval", "answer"} and gate_id != "QUERY.INTER_TOKEN_P95")
            )
            rate_target = (suite.workloads["retrieval"] if gate_id == "ISOLATION.QUERY_P95_RATIO"
                           else suite.workloads.get(workload, {}))
            rate = rate_target.get("requests_per_second")
            sample_count = run.get("sample_count")
            if (request_denominator and rate is not None and isinstance(sample_count, int)
                    and not isinstance(sample_count, bool)
                    and sample_count != declared["measured_seconds"] * rate):
                errors.append(f"{gate_id} run {run['run_id']} arrival population differs from measured window")
    return errors


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
    artifacts = _validate_supporting_artifacts(root, bundle["supporting_artifacts"], manifest, dataset)
    dataset_artifact = _artifact_with_hash(artifacts, dataset.dataset_hash.sha256, "application/x-ndjson")
    corpus_artifact = _artifact_with_hash(
        artifacts, manifest.corpus_snapshot.manifest_hash.sha256, "application/json")
    workload_artifact = _artifact_with_hash(
        artifacts, manifest.workload.content_hash.sha256, "application/json")
    questions = load_gold_questions(_artifact_path(root, dataset_artifact), dataset)
    corpus_facts = load_corpus_facts(_artifact_path(root, corpus_artifact))
    declared_runs, environment_errors, eligibility_artifact_ids = _load_workload_evidence(
        _artifact_path(root, workload_artifact), suite, profile_id, bundle["protocol"],
        bundle["workloads"], manifest)
    inventory_populations, inventory_errors = _inventory_evidence(
        root, artifacts, eligibility_artifact_ids, suite, bundle["workloads"], corpus_facts)
    observation_runs, observations = _parse_observation_runs(bundle["observation_runs"])
    unknown_observation_workloads = {workload for _, workload, _ in observation_runs} - set(suite.workloads)
    if unknown_observation_workloads:
        raise RunnerInputError(f"unknown observation workloads: {sorted(unknown_observation_workloads)}")
    try:
        for observation_run_id, observation_workload, run_observations in observation_runs:
            if observation_workload not in {"retrieval", "answer"}:
                raise TelemetryError("Observation runs are supported only for retrieval and answer workloads")
            declared = declared_runs.get((observation_run_id, observation_workload))
            expected_gates = [gate_id for gate_id in TELEMETRY_GATES
                              if gate_id in suite.gates and suite.gates[gate_id].workload == observation_workload]
            if (declared is None or any(declared["sample_counts"].get(gate_id) != len(run_observations)
                                        for gate_id in expected_gates)):
                raise TelemetryError("Observation count differs from workload run declaration")
            validate_observation_protocol(observation_run_id, observation_workload, run_observations,
                                          manifest.meta.corpus_id, suite.workloads[observation_workload],
                                          suite.protocol)
        derived = derive_observation_measurements(observation_runs)
    except TelemetryError as exc:
        raise RunnerInputError(f"invalid telemetry: {exc}") from exc
    if not isinstance(bundle["measurements"], dict):
        raise RunnerInputError("measurements must be an object")
    manual_telemetry = set(bundle["measurements"]) & TELEMETRY_GATES
    if manual_telemetry:
        raise RunnerInputError(f"telemetry-owned gates cannot use manual evidence: {sorted(manual_telemetry)}")
    overlap = set(bundle["measurements"]) & set(derived)
    if overlap:
        raise RunnerInputError(f"manual measurements conflict with telemetry-derived gates: {sorted(overlap)}")
    measurements = {**bundle["measurements"], **derived}
    populations = {**_quality_populations(questions), **inventory_populations}
    population_requirements = {
        "UPDATE.REBUILD_RATIO": math.ceil(
            corpus_facts.documents * suite.workloads["incremental"]["changed_document_ratio"]),
    }
    for gate_id in (
        "INVARIANT.SOURCE_MAPPING", "INVARIANT.ORPHAN_EDGES", "INVARIANT.SNAPSHOT_MISMATCH",
        "INVARIANT.UNCHANGED_REEXTRACTION", "INVARIANT.REPLAY_DIFF", "INVARIANT.WIRE_PARITY",
    ):
        if gate_id in populations:
            population_requirements[gate_id] = len(populations[gate_id])
    eligibility_errors = (
        dataset_eligibility_errors(questions, suite.workloads["quality"])
        + corpus_eligibility_errors(corpus_facts, manifest.corpus_snapshot, suite.reference["corpus"])
        + environment_errors
        + inventory_errors
        + _measurement_run_errors(measurements, suite, declared_runs)
        + _measurement_population_errors(measurements, populations, questions)
    )

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
                                 bundle["workloads"], measurements, input_artifact, eligibility_errors,
                                 population_requirements)
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
    except (RunnerInputError, DatasetLoadError, ConfigError, GateInputError, OSError) as exc:
        print(f"evaluation input error: {exc}", file=sys.stderr)
        return 2
    print(json.dumps(report, sort_keys=True))
    return 0 if report["acceptance_status"] in {"PASS", "REPORT_ONLY"} else 1


if __name__ == "__main__":
    raise SystemExit(main())
