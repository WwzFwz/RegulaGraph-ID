"""C01 evaluation schemas and descriptor-driven validation of production wire records.

Generated types come from scripts/generate_contracts.py; add .cache/contracts/python to PYTHONPATH.
No schema is redefined in dataclasses. Validation checks structure, never model quality.
Wire sizes/depth/items are bounded; external references and semantic gold labels require their owners.
Rekomendasi implementasi berikutnya (belum merupakan fitur aktif):
Extend the active contract validator with versioned dataset loaders and reviewed group/split provenance; keep generated bindings authoritative.
Bukti verifikasi: Test duplicate/group leakage, incomplete human reviews and snapshot/model drift; bound large dataset memory and reject lossy conversions.
Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.
"""
import datetime
import math
import posixpath
from google.protobuf import json_format
from regulagraph.v1 import common_pb2 as common
import evaluation_pb2 as artifacts

DatasetManifest = artifacts.DatasetManifest
GoldQuestion = artifacts.GoldQuestion
RunManifest = artifacts.RunManifest
Observation = artifacts.Observation
GateResult = artifacts.GateResult


def check_utf8_span(text, start, end):
    raw = text.encode("utf-8") if isinstance(text, str) else text
    raw.decode("utf-8")
    if not 0 <= start <= end <= len(raw):
        raise ValueError("span out of bounds")
    raw[:start].decode("utf-8")
    raw[:end].decode("utf-8")


def validate(message, max_bytes=16 << 20, max_depth=64, max_items=100000):
    if min(max_bytes, max_depth, max_items) <= 0 or message.ByteSize() > max_bytes:
        raise ValueError("invalid limits or oversized message")
    remaining = max_items

    def corpus_of(m):
        own = getattr(m, "corpus_id", "")
        if own:
            return own
        for name in ("meta", "context", "batch"):
            if name in m.DESCRIPTOR.fields_by_name and m.HasField(name):
                own = corpus_of(getattr(m, name))
                if own:
                    return own
        return ""

    def walk(m, depth, corpus=""):
        own = corpus_of(m)
        if own:
            if corpus and own != corpus:
                raise ValueError("nested corpus mismatch")
            corpus = own
        nonlocal remaining
        if depth > max_depth:
            raise ValueError("depth limit")
        populated = {f.name for f, _ in m.ListFields()}
        for f in m.DESCRIPTOR.fields:
            rules = f.GetOptions().Extensions[common.rules]
            value = getattr(m, f.name)
            if rules.required and f.name not in populated:
                raise ValueError(f"{f.full_name}: required")
            if f.is_repeated:
                values = value
                remaining -= len(values)
                if len(values) < rules.min_items:
                    raise ValueError(f"{f.full_name}: min_items")
            else:
                values = [value] if not f.has_presence or m.HasField(f.name) else []
            if remaining < 0:
                raise ValueError("item limit")
            if rules.unique and len(set(values)) != len(values):
                raise ValueError(f"{f.full_name}: duplicate")
            for v in values:
                if f.message_type:
                    remaining -= 1
                    if remaining < 0:
                        raise ValueError("item limit")
                    walk(v, depth + 1, corpus)
                    continue
                if rules.ascii_id and (not v or len(v) > 256 or any(ord(c) < 33 or ord(c) > 126 for c in v)):
                    raise ValueError(f"{f.full_name}: ID")
                if rules.sha256 and (len(v) != 64 or any(c not in "0123456789abcdef" for c in v)):
                    raise ValueError(f"{f.full_name}: hash")
                if f.enum_type and rules.required and (v == 0 or v not in f.enum_type.values_by_number):
                    raise ValueError(f"{f.full_name}: enum")
                if rules.positive or rules.finite or rules.probability:
                    if not math.isfinite(v) or (rules.positive and v <= 0) or (rules.probability and not 0 <= v <= 1):
                        raise ValueError(f"{f.full_name}: numeric")
                if f.name == "schema_version" and v != 1:
                    raise ValueError("unsupported schema version")
        for o in m.DESCRIPTOR.oneofs:
            synthetic = len(o.fields) == 1 and o.name == "_" + o.fields[0].name
            if not synthetic and m.WhichOneof(o.name) is None:
                raise ValueError(f"{o.full_name}: oneof required")
        semantic(m)
    walk(message, 0)
    return message


def semantic(m):
    name = m.DESCRIPTOR.name
    def require(ok):
        if not ok:
            raise ValueError(f"{name}: inconsistent fields")
    if name == "CalendarDate":
        datetime.date(m.year, m.month, m.day)
    elif name == "DateAssertion":
        require((m.knowledge == common.DATE_KNOWLEDGE_KNOWN) == m.HasField("value"))
        require(len(m.alternatives) >= 2 if m.knowledge == common.DATE_KNOWLEDGE_CONFLICT else not m.alternatives)
    elif name == "Visibility":
        require(not m.HasField("to_seq") or m.to_seq > m.from_seq)
    elif name in ("TextSpan", "AnswerTextSpan"):
        require(m.end_byte >= m.start_byte)
    elif name == "BoundingBox":
        require(m.x1 >= m.x0 and m.y1 >= m.y0)
    elif name == "LegalInterval" and m.start.HasField("value") and m.end.HasField("value"):
        date = lambda d: (d.year, d.month, d.day)
        require(date(m.start.value) < date(m.end.value))
    elif name == "SparseVector":
        require(len(m.indices) == len(m.values) and all(a < b for a, b in zip(m.indices, m.indices[1:])))
    elif name == "DenseVector":
        require(len(m.values) == m.dimensions)
    elif name == "Counts":
        require(m.accepted + m.rejected == m.expected)
    elif name == "TruncationInfo":
        require(m.retained_tokens <= m.original_tokens and (m.truncated or m.retained_tokens == m.original_tokens))
    elif name == "RequestContext":
        require(not m.HasField("snapshot_ref") or m.corpus_id == m.snapshot_ref.corpus_id)
    elif name == "Timestamp":
        require(-62135596800 <= m.seconds <= 253402300799 and 0 <= m.nanos <= 999999999)
    elif name == "ModelManifest":
        require(m.task != common.MODEL_TASK_EMBED or m.HasField("dimensions"))
    elif name in ("EmbedBatchRequest", "RerankBatchRequest"):
        items = m.items if name == "EmbedBatchRequest" else m.pairs
        ids = [x.item_id if name == "EmbedBatchRequest" else x.pair_id for x in items]
        require(len(ids) == len(set(ids)))
        require(m.model.task == (common.MODEL_TASK_EMBED if name == "EmbedBatchRequest" else common.MODEL_TASK_RERANK))
    elif name == "ArtifactRef":
        key = m.storage_key
        require(not key.startswith(("/", "../")) and key not in ("..", ".") and ":" not in key and "\\" not in key and posixpath.normpath(key) == key)
    elif name == "TemporalScope":
        require(m.mode != common.TEMPORAL_MODE_AS_OF or m.HasField("effective_at"))
        require((len(m.compare_dates) >= 2 and not m.HasField("effective_at")) if m.mode == common.TEMPORAL_MODE_COMPARE else not m.compare_dates)
    elif name == "GraphPath":
        require(len(m.ordered_node_ids) == len(m.ordered_assertion_ids) + 1 and len(m.selected_support_ids) == len(m.ordered_assertion_ids))
    elif name == "SourceBlob":
        require(m.byte_size == m.artifact_ref.byte_size and m.raw_sha256 == m.artifact_ref.content_hash)
    elif name == "SourceObservation":
        require(m.status != 1 or (m.HasField("source_blob_id") and not m.HasField("error")))
    elif name == "DatasetManifest":
        groups = [g.base_question_group for g in m.groups]
        require(len(groups) == len(set(groups)))
    elif name == "GateResult" and m.status == artifacts.GATE_STATUS_PASS:
        require(m.denominator > 0 and m.HasField("estimate") and bool(m.raw_artifacts))
    elif name == "Observation":
        require(not m.HasField("first_substantive_token_ns") or m.first_substantive_token_ns <= m.completion_ns)
        require(not m.rejected_arrival or m.outcome != common.COMPLETION_STATUS_SUCCEEDED)


def decode(raw, message_type, **limits):
    if len(raw) > limits.get("max_bytes", 16 << 20):
        raise ValueError("byte limit")
    message = message_type()
    message.ParseFromString(raw)
    return validate(message, **limits)


def to_json(message):
    """Reject a lossy JSON bridge when binary unknown fields exist."""
    clean = type(message)()
    clean.CopyFrom(message)
    clean.DiscardUnknownFields()
    if clean.SerializeToString(deterministic=True) != message.SerializeToString(deterministic=True):
        raise ValueError("unknown fields cannot be preserved by ProtoJSON")
    validate(message)
    return json_format.MessageToJson(message, preserving_proto_field_name=True)


def from_json(raw, message_type):
    if len(raw.encode("utf-8")) > 16 << 20:
        raise ValueError("byte limit")
    return validate(json_format.Parse(raw, message_type(), ignore_unknown_fields=False))


def validate_dataset(manifest, questions):
    validate(manifest)
    groups = {g.base_question_group: g.split for g in manifest.groups}
    ids = set()
    for q in questions:
        validate(q)
        if q.meta.record_id in ids or groups.get(q.base_question_group) != q.split or q.meta.corpus_id != manifest.meta.corpus_id:
            raise ValueError("duplicate question, split leakage, or corpus mismatch")
        ids.add(q.meta.record_id)
