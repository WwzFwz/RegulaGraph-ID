"""
Mengukur kualitas ekstraksi relasi graph dan resolusi identitas canonical.

Peran dalam komponen:
Menilai apakah knowledge graph mempertahankan endpoint, arah, predicate, qualifier, provenance, dan
identitas lintas sumber secara benar, bukan sekadar menghasilkan record yang valid secara schema.

Kontrak integrasi dan perhatian implementasi:
Caller harus menetapkan kecocokan relasi lengkap melalui gold review sebelum mengirim TP/FP/FN.
Entity-resolution dinilai dengan pasangan joined yang benar terhadap pasangan prediksi dan gold.
Precision, recall, dan micro F1 dihitung dari count eksplisit agar denominator dapat diaudit.

Benchmark dan gate penerimaan:
[EVAL] Gate: setiap run merekam dataset/split, corpus snapshot, model/prompt/config version, seed bila
relevan, serta biaya. Laporkan ukuran sampel dan ketidakpastian, hindari tuning pada test set, dan
pisahkan kualitas retrieval, graph, jawaban, serta waktu/biaya.

[GRAPH] Ukur precision/recall/F1 relasi lengkap dan pairwise precision/recall canonical identity.
Laporkan per tipe relasi, jenis dokumen, dan sumber; mismatch endpoint, arah, qualifier, versi, atau
dukungan sumber harus dihitung sebagai error. Ambang wajib mengikuti configs/benchmark-targets.yaml.
Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED sampai diukur oleh
run yang memenuhi protokol.
Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.

Status: implementasi E01 aktif untuk relation precision/recall/micro-F1 dan canonical pairwise
precision/recall; hasil benchmark produksi belum tersedia.
Bukti verifikasi: uji zero denominator, count negatif, correct pair melebihi predicted/gold, serta
matching yang membedakan arah, qualifier, versi, dan provenance.
Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.
"""
from __future__ import annotations

from .runtime import Estimate, MetricError, micro_f1, ratio


def precision_recall(true_positive: int, false_positive: int, false_negative: int) -> tuple[Estimate, Estimate, Estimate]:
    if any(isinstance(v, bool) or not isinstance(v, int) or v < 0 for v in (true_positive, false_positive, false_negative)):
        raise MetricError("graph counts must be non-negative integers")
    precision = ratio(true_positive, true_positive + false_positive)
    recall = ratio(true_positive, true_positive + false_negative)
    return precision, recall, micro_f1(true_positive, false_positive, false_negative)


def pair_precision_recall(correct_joined: int, predicted_joined: int, gold_joined: int) -> tuple[Estimate, Estimate]:
    if correct_joined > min(predicted_joined, gold_joined):
        raise MetricError("correct pairs exceed predicted/gold pairs")
    return ratio(correct_joined, predicted_joined), ratio(correct_joined, gold_joined)
