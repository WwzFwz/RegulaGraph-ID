"""Load frozen E01 gold questions and corpus eligibility facts from verified artifacts.

Peran arsitektur:
Loader ini membuktikan bahwa hash dataset menunjuk JSONL ``GoldQuestion`` C01 yang benar-benar dapat
divalidasi dan bahwa hash manifest snapshot menunjuk ukuran corpus referensi. Runner memakai hasilnya
sebelum gate apa pun dapat PASS; file berhash benar tetapi berisi payload lain ditolak.

Kontrak integrasi dan performa:
Dataset adalah UTF-8 NDJSON, satu ProtoJSON GoldQuestion per baris, maksimum 16 MiB per record dan satu
juta record. Corpus manifest JSON memakai schema ketat serta identity snapshot yang sama. Loader berjalan
offline dan streaming untuk membatasi memori byte mentah; daftar question tetap ditahan karena validator
split/gold dan scorer E01 memerlukannya.

Benchmark dan status:
Hitung test/answerable/unanswerable/slice dari record aktual, bukan deklarasi. Minimum dan ukuran corpus
tetap berasal dari configs/benchmark-targets.yaml; perubahan target memerlukan persetujuan pengguna.
Implementasi eligibility E01 aktif, sedangkan gold/corpus produksi tetap REQUIRED_UNMEASURED.
"""
from __future__ import annotations

from dataclasses import dataclass
import json
from pathlib import Path
from typing import Any, Mapping, Sequence

from google.protobuf import json_format
import evaluation_pb2 as pb

from evaluation.datasets.schema import validate, validate_dataset


class DatasetLoadError(ValueError):
    """A frozen dataset/corpus artifact is malformed or inconsistent with C01."""


@dataclass(frozen=True)
class CorpusFacts:
    schema_version: int
    corpus_id: str
    snapshot_id: str
    representation_generation: str
    documents: int
    chunks: int
    canonical_entities: int
    graph_edges: int


def _snapshot_key(snapshot: Any) -> tuple[Any, ...]:
    return (snapshot.corpus_id, snapshot.snapshot_id, snapshot.sequence,
            snapshot.manifest_hash.sha256, snapshot.representation_generation)


def load_gold_questions(path: Path, manifest: pb.DatasetManifest) -> tuple[pb.GoldQuestion, ...]:
    """Stream and validate a UTF-8 NDJSON dataset against its C01 manifest."""
    questions: list[pb.GoldQuestion] = []
    try:
        with path.open("rb") as source:
            for line_number, raw in enumerate(source, 1):
                if len(raw) > 16 << 20:
                    raise DatasetLoadError(f"dataset line {line_number} exceeds 16 MiB")
                if not raw.strip():
                    raise DatasetLoadError(f"dataset line {line_number} is blank")
                if len(questions) >= 1_000_000:
                    raise DatasetLoadError("dataset exceeds one million questions")
                try:
                    value = json.loads(raw)
                    question = json_format.ParseDict(value, pb.GoldQuestion(), ignore_unknown_fields=False)
                    questions.append(validate(question))
                except (UnicodeError, json.JSONDecodeError, json_format.ParseError, TypeError, ValueError) as exc:
                    raise DatasetLoadError(f"invalid GoldQuestion on line {line_number}: {exc}") from exc
    except OSError as exc:
        raise DatasetLoadError(f"cannot read dataset: {exc}") from exc
    if not questions:
        raise DatasetLoadError("dataset is empty")
    try:
        validate_dataset(manifest, questions)
    except (ValueError, UnicodeError) as exc:
        raise DatasetLoadError(f"dataset/manifest mismatch: {exc}") from exc
    manifest_groups = {group.base_question_group for group in manifest.groups}
    question_groups = {question.base_question_group for question in questions}
    if manifest_groups != question_groups:
        raise DatasetLoadError("DatasetManifest groups must exactly match question groups")
    expected_snapshot = _snapshot_key(manifest.corpus_snapshot)
    for question in questions:
        if (question.temporal_scope.HasField("knowledge_snapshot")
                and _snapshot_key(question.temporal_scope.knowledge_snapshot) != expected_snapshot):
            raise DatasetLoadError(f"question {question.meta.record_id} temporal snapshot differs from dataset")
        if any(_snapshot_key(path.snapshot) != expected_snapshot for path in question.required_paths):
            raise DatasetLoadError(f"question {question.meta.record_id} graph path snapshot differs from dataset")
    return tuple(questions)


def dataset_eligibility_errors(questions: Sequence[pb.GoldQuestion], workload: Mapping[str, Any]) -> list[str]:
    """Compare counts from actual test records to the existing quality workload minimums."""
    test = [question for question in questions if question.split == pb.DATASET_SPLIT_TEST]
    answerable = [question for question in test if question.answerability == pb.ANSWERABILITY_ANSWERABLE]
    unanswerable = [question for question in test if question.answerability == pb.ANSWERABILITY_UNANSWERABLE]
    errors: list[str] = []
    for label, actual, field in (
        ("test questions", len(test), "test_questions_min"),
        ("answerable questions", len(answerable), "answerable_questions_min"),
        ("unanswerable questions", len(unanswerable), "unanswerable_questions_min"),
    ):
        if actual < workload[field]:
            errors.append(f"{label} {actual} below {workload[field]}")
    required_slices = tuple(workload["slices"])
    minimum = int(workload["questions_per_slice_min"])
    for slice_name in required_slices:
        count = sum(slice_name in question.slice_labels for question in test)
        if count < minimum:
            errors.append(f"slice {slice_name} has {count} questions, requires {minimum}")
    missing_gold = sum(not question.expected_claims or not question.acceptable_evidence_sets for question in answerable)
    if missing_gold:
        errors.append(f"{missing_gold} answerable questions lack claims or acceptable evidence")
    invalid_unanswerable = sum(bool(question.acceptable_evidence_sets) for question in unanswerable)
    if invalid_unanswerable:
        errors.append(f"{invalid_unanswerable} unanswerable questions contain acceptable evidence")
    return errors


def load_corpus_facts(path: Path) -> CorpusFacts:
    """Load the strict JSON document whose bytes are named by SnapshotRef.manifest_hash."""
    fields = {
        "schema_version", "corpus_id", "snapshot_id", "representation_generation",
        "documents", "chunks", "canonical_entities", "graph_edges",
    }
    try:
        raw = path.read_bytes()
        if len(raw) > 1 << 20:
            raise DatasetLoadError("corpus manifest exceeds 1 MiB")
        value = json.loads(raw)
    except (OSError, UnicodeError, json.JSONDecodeError) as exc:
        raise DatasetLoadError(f"cannot load corpus manifest: {exc}") from exc
    if not isinstance(value, dict) or set(value) != fields:
        raise DatasetLoadError("corpus manifest has unknown or missing fields")
    if isinstance(value["schema_version"], bool) or value["schema_version"] != 1:
        raise DatasetLoadError("unsupported corpus manifest schema")
    for field in ("corpus_id", "snapshot_id", "representation_generation"):
        if not isinstance(value[field], str) or not value[field]:
            raise DatasetLoadError(f"corpus manifest {field} must be non-empty")
    for field in ("documents", "chunks", "canonical_entities", "graph_edges"):
        if isinstance(value[field], bool) or not isinstance(value[field], int) or value[field] < 0:
            raise DatasetLoadError(f"corpus manifest {field} must be a non-negative integer")
    return CorpusFacts(**value)


def corpus_eligibility_errors(facts: CorpusFacts, snapshot: Any, target: Mapping[str, Any]) -> list[str]:
    errors: list[str] = []
    if (facts.corpus_id, facts.snapshot_id, facts.representation_generation) != (
            snapshot.corpus_id, snapshot.snapshot_id, snapshot.representation_generation):
        errors.append("corpus manifest identity differs from RunManifest snapshot")
    for field, target_field in (("documents", "documents_min"), ("chunks", "chunks_min"),
                                ("canonical_entities", "canonical_entities_min"),
                                ("graph_edges", "graph_edges_min")):
        actual, minimum = getattr(facts, field), target[target_field]
        if actual < minimum:
            errors.append(f"corpus {field} {actual} below {minimum}")
    return errors


def load_id_inventory(path: Path, fields: Sequence[str]) -> Mapping[str, tuple[str, ...]]:
    """Load strict versioned ID populations for parsing/graph gold eligibility."""
    expected = {"schema_version", *fields}
    try:
        raw = path.read_bytes()
        if len(raw) > 128 << 20:
            raise DatasetLoadError("eligibility inventory exceeds 128 MiB")
        value = json.loads(raw)
    except (OSError, UnicodeError, json.JSONDecodeError) as exc:
        raise DatasetLoadError(f"cannot load eligibility inventory: {exc}") from exc
    if not isinstance(value, dict) or set(value) != expected:
        raise DatasetLoadError("eligibility inventory has unknown or missing fields")
    if isinstance(value["schema_version"], bool) or value["schema_version"] != 1:
        raise DatasetLoadError("unsupported eligibility inventory schema")
    result: dict[str, tuple[str, ...]] = {}
    for field in fields:
        identifiers = value[field]
        if (not isinstance(identifiers, list) or any(not isinstance(item, str) or not item for item in identifiers)
                or len(identifiers) != len(set(identifiers))):
            raise DatasetLoadError(f"eligibility inventory {field} must contain unique non-empty IDs")
        result[field] = tuple(identifiers)
    return result
