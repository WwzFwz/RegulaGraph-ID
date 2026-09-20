"""
Mengukur Recall@k, ranking, dan kelengkapan seluruh bukti pendukung pertanyaan.

Peran dalam komponen:
Membedakan keberhasilan menemukan satu potongan dari keberhasilan menemukan semua bukti multi-hop.

Kontrak integrasi dan perhatian implementasi:
Nyatakan unit evaluasi chunk/pasal/versi dan denominator; hitung kategori factual, temporal, typo,
informal, dan code-switch terpisah. Input memakai ranked production evidence/provision-version ID dan
label relevansi hasil review. Duplicate ID ditolak karena deduplikasi diam-diam mengubah posisi ranking.
Fungsi menghasilkan nilai per pertanyaan agar runner dapat mengagregasi base-question group dan slice
yang telah dibekukan tanpa tuning pada test set.

Benchmark dan gate penerimaan:
[EVAL] Gate: setiap run merekam dataset/split, corpus snapshot, model/prompt/config version, seed bila
relevan, serta biaya. Laporkan ukuran sampel dan ketidakpastian, hindari tuning pada test set, dan
pisahkan kualitas retrieval, graph, jawaban, serta waktu/biaya.

[RETRIEVAL] Ukur Recall@k, nDCG@k, kelengkapan semua bukti multi-hop, serta latency p50/p95 per jenis
pertanyaan. Catat jumlah kandidat, hop, snapshot, dan kondisi cold/warm. Ambang wajib mengikuti
configs/benchmark-targets.yaml; jangan menganggap batas hop/kandidat atau graph selalu meningkatkan
kualitas.
Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED sampai diukur oleh
run yang memenuhi protokol.
Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.

Status: implementasi E01 aktif untuk Recall@k, nDCG@k, dan all-required-evidence/path coverage; hasil
benchmark produksi belum tersedia.
Bukti verifikasi: uji duplicate, tie, multiple acceptable sets, unanswerable question, denominator, dan
truncation policy secara eksplisit.
Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.
"""
from __future__ import annotations

import math
from typing import Mapping, Sequence

from .runtime import MetricError


def recall_at_k(ranked_ids: Sequence[str], acceptable_ids: set[str], k: int) -> float:
    if k <= 0 or not acceptable_ids:
        raise MetricError("recall requires k>0 and non-empty gold")
    if len(set(ranked_ids)) != len(ranked_ids):
        raise MetricError("duplicate retrieved evidence IDs")
    return len(set(ranked_ids[:k]) & acceptable_ids) / len(acceptable_ids)


def ndcg_at_k(ranked_ids: Sequence[str], relevance: Mapping[str, int], k: int) -> float:
    if k <= 0 or not relevance or any(v < 0 or v > 3 for v in relevance.values()):
        raise MetricError("nDCG requires k>0 and relevance grades 0..3")
    if len(set(ranked_ids)) != len(ranked_ids):
        raise MetricError("duplicate retrieved evidence IDs")
    dcg = sum((2 ** relevance.get(item, 0) - 1) / math.log2(rank + 2) for rank, item in enumerate(ranked_ids[:k]))
    ideal = sorted(relevance.values(), reverse=True)[:k]
    idcg = sum((2**grade - 1) / math.log2(rank + 2) for rank, grade in enumerate(ideal))
    if idcg == 0:
        raise MetricError("nDCG gold has no positive relevance")
    return dcg / idcg


def required_set_complete(ranked_ids: Sequence[str], acceptable_sets: Sequence[set[str]], k: int) -> bool:
    if k <= 0 or not acceptable_sets or any(not values for values in acceptable_sets):
        raise MetricError("required evidence sets must be non-empty")
    selected = set(ranked_ids[:k])
    return any(values <= selected for values in acceptable_sets)
