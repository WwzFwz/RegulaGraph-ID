"""
Mengukur waktu per tahap, end-to-end latency, throughput, token, biaya, dan memori.

Peran dalam komponen:
Menilai trade-off kualitas terhadap penggunaan sumber daya melalui primitive statistik deterministik
yang dipakai evaluator E01, tanpa melakukan I/O atau mengubah evidence mentah.

Kontrak integrasi dan perhatian implementasi:
Ukur p95/p99, waktu antre, time-to-first-answer-token, dan inter-token latency. Pisahkan ingestion/query
dan cold/warm; nyatakan hardware, concurrency, sample size, error rate, serta satuan biaya. Gunakan timer
monotonic untuk durasi. Fungsi menerima sampel mentah atau count eksplisit dan mengembalikan estimate
beserta denominator. Request yang tidak selesai direpresentasikan sebagai positive infinity agar gate
latency gagal dan tidak hilang dari statistik.

Benchmark dan gate penerimaan:
[EVAL] Gate: setiap run merekam dataset/split, corpus snapshot, model/prompt/config version, seed bila
relevan, serta biaya. Laporkan ukuran sampel dan ketidakpastian, hindari tuning pada test set, dan
pisahkan kualitas retrieval, graph, jawaban, serta waktu/biaya.

[MODEL] Ukur cold load terpisah dari inference warm, throughput token/embedding/pasangan, p50/p95,
RAM/VRAM, truncation, dan biaya. Catat model/version, precision, hardware, dan batch size. Cache harus
memasukkan versi model serta input yang relevan; target numerik wajib mengikuti
configs/benchmark-targets.yaml.
Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED sampai diukur oleh
run yang memenuhi protokol.
Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.

Status: implementasi E01 aktif untuk nearest-rank quantile, maximum, throughput, ratio, macro mean,
minimum slice, micro F1, corpus error rate, count/difference, dan validasi bentuk evidence. Hasil
benchmark produksi belum tersedia.
Bukti verifikasi: uji percentile, ketidaksesuaian satuan clock, request timeout/missing, denominator,
dan non-finite value; hindari coordinated-omission bias serta success-only latency.
Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.
"""
from __future__ import annotations

from dataclasses import dataclass
import math
from typing import Iterable, Mapping, Sequence


class MetricError(ValueError):
    """Raw metric evidence is missing, ambiguous, or inconsistent."""


@dataclass(frozen=True)
class Estimate:
    value: float
    denominator: int


def _numbers(values: Iterable[float], *, allow_infinity: bool = False) -> list[float]:
    result: list[float] = []
    for value in values:
        if isinstance(value, bool) or not isinstance(value, (int, float)):
            raise MetricError("sample must be numeric")
        number = float(value)
        if math.isnan(number) or (math.isinf(number) and not allow_infinity):
            raise MetricError("sample must be finite")
        result.append(number)
    if not result:
        raise MetricError("empty samples")
    return result


def nearest_rank(values: Iterable[float], percentile: float) -> Estimate:
    if not 0 < percentile <= 1:
        raise MetricError("percentile must be in (0, 1]")
    samples = sorted(_numbers(values, allow_infinity=True))
    if any(value < 0 for value in samples):
        raise MetricError("duration samples must be non-negative")
    rank = max(1, math.ceil(percentile * len(samples)))
    return Estimate(samples[rank - 1], len(samples))


def maximum(values: Iterable[float]) -> Estimate:
    samples = _numbers(values)
    if any(value < 0 for value in samples):
        raise MetricError("resource samples must be non-negative")
    return Estimate(max(samples), len(samples))


def ratio(numerator: int, denominator: int) -> Estimate:
    if isinstance(numerator, bool) or isinstance(denominator, bool) or not isinstance(numerator, int) or not isinstance(denominator, int):
        raise MetricError("ratio counts must be integers")
    if denominator <= 0 or numerator < 0 or numerator > denominator:
        raise MetricError("invalid ratio counts")
    return Estimate(numerator / denominator, denominator)


def quotient(numerator: int, denominator: int) -> Estimate:
    """Return a non-negative ratio that may exceed one for performance comparisons."""
    if isinstance(numerator, bool) or isinstance(denominator, bool) or not isinstance(numerator, int) or not isinstance(denominator, int):
        raise MetricError("quotient counts must be integers")
    if denominator <= 0 or numerator < 0:
        raise MetricError("invalid quotient counts")
    return Estimate(numerator / denominator, denominator)


def throughput(completed: int, duration_seconds: float) -> Estimate:
    if isinstance(completed, bool) or not isinstance(completed, int) or completed <= 0:
        raise MetricError("throughput count must be positive")
    duration = _numbers([duration_seconds])[0]
    if duration <= 0:
        raise MetricError("throughput duration must be positive")
    return Estimate(completed / duration, completed)


def macro_mean(group_scores: Mapping[str, float]) -> Estimate:
    if not isinstance(group_scores, Mapping) or not group_scores:
        raise MetricError("empty group scores")
    scores = _numbers(group_scores.values())
    if any(not 0 <= value <= 1 for value in scores):
        raise MetricError("group score outside [0, 1]")
    return Estimate(sum(scores) / len(scores), len(scores))


def minimum_slice_ratio(slice_counts: Mapping[str, Mapping[str, int]], required_slices: Sequence[str]) -> Estimate:
    if not isinstance(slice_counts, Mapping):
        raise MetricError("slice_counts must be a mapping")
    if set(slice_counts) != set(required_slices):
        raise MetricError("slice evidence does not match required slices")
    estimates: list[Estimate] = []
    for value in slice_counts.values():
        if not isinstance(value, Mapping) or set(value) != {"numerator", "denominator"}:
            raise MetricError("each slice requires exactly numerator and denominator")
        estimates.append(ratio(value.get("numerator"), value.get("denominator")))
    return Estimate(min(value.value for value in estimates), sum(value.denominator for value in estimates))


def micro_f1(true_positive: int, false_positive: int, false_negative: int) -> Estimate:
    counts = (true_positive, false_positive, false_negative)
    if any(isinstance(v, bool) or not isinstance(v, int) or v < 0 for v in counts):
        raise MetricError("confusion counts must be non-negative integers")
    denominator = 2 * true_positive + false_positive + false_negative
    if denominator == 0:
        raise MetricError("empty F1 denominator")
    return Estimate(2 * true_positive / denominator, true_positive + false_negative)


def corpus_error_rate(edits: int, gold_units: int) -> Estimate:
    if any(isinstance(v, bool) or not isinstance(v, int) for v in (edits, gold_units)) or edits < 0 or gold_units <= 0:
        raise MetricError("invalid corpus error counts")
    return Estimate(edits / gold_units, gold_units)


def scalar(value: float, denominator: int, *, non_negative: bool = False) -> Estimate:
    values = _numbers([value])
    if non_negative and values[0] < 0:
        raise MetricError("scalar value must be non-negative")
    if isinstance(denominator, bool) or not isinstance(denominator, int) or denominator <= 0:
        raise MetricError("scalar evidence needs a positive denominator")
    return Estimate(values[0], denominator)


def estimate(statistic: str, evidence: Mapping[str, object], required_slices: Sequence[str] = ()) -> Estimate:
    """Calculate only the evidence shape assigned to the target statistic."""
    if not isinstance(evidence, Mapping):
        raise MetricError("evidence must be a mapping")
    shapes = {
        "p50": {"samples"}, "p95": {"samples"}, "p99": {"samples"}, "max": {"samples"},
        "throughput": {"completed", "duration_seconds"}, "ratio": {"numerator", "denominator"},
        "micro_ratio": {"numerator", "denominator"}, "pairwise_ratio": {"numerator", "denominator"},
        "macro_mean": {"group_scores"}, "minimum_slice_ratio": {"slice_counts"},
        "micro_f1": {"true_positive", "false_positive", "false_negative"},
        "corpus_cer": {"edits", "gold_units"}, "count": {"value", "denominator"},
        "difference": {"value", "denominator"},
    }
    if statistic not in shapes:
        raise MetricError(f"unsupported statistic: {statistic}")
    if set(evidence) != shapes[statistic]:
        raise MetricError(f"{statistic} evidence fields must be {sorted(shapes[statistic])}")
    if statistic in {"p50", "p95", "p99"}:
        return nearest_rank(evidence.get("samples", []), int(statistic[1:]) / 100)
    if statistic == "max":
        return maximum(evidence.get("samples", []))
    if statistic == "throughput":
        return throughput(evidence.get("completed"), evidence.get("duration_seconds"))
    if statistic == "ratio":
        return quotient(evidence.get("numerator"), evidence.get("denominator"))
    if statistic in {"micro_ratio", "pairwise_ratio"}:
        return ratio(evidence.get("numerator"), evidence.get("denominator"))
    if statistic == "macro_mean":
        return macro_mean(evidence.get("group_scores", {}))
    if statistic == "minimum_slice_ratio":
        return minimum_slice_ratio(evidence.get("slice_counts", {}), required_slices)
    if statistic == "micro_f1":
        return micro_f1(evidence.get("true_positive"), evidence.get("false_positive"), evidence.get("false_negative"))
    if statistic == "corpus_cer":
        return corpus_error_rate(evidence.get("edits"), evidence.get("gold_units"))
    if statistic == "count":
        value = evidence.get("value")
        if isinstance(value, bool) or not isinstance(value, int):
            raise MetricError("count value must be an integer")
        return scalar(value, evidence.get("denominator"), non_negative=True)
    if statistic == "difference":
        return scalar(evidence.get("value"), evidence.get("denominator"))
    raise AssertionError("validated statistic was not dispatched")
