"""
Mengukur akurasi jawaban, perilaku abstention, dan kualitas terburuk pada slice wajib.

Peran dalam komponen:
Menilai hasil akhir grounded generation tanpa membiarkan skor agregat menutupi kegagalan pada query
temporal, typo-heavy, informal, code-switched, atau multi-hop.

Kontrak integrasi dan perhatian implementasi:
Scorer menerima keputusan hasil adjudication dan tidak memanggil LLM judge sendiri. Jika judge membantu
review, caller merekam identitas, versi, prompt, calibration, dan disagreement. Abstention pada pertanyaan
answerable dihitung salah; jawaban tanpa dukungan pada pertanyaan unanswerable tidak dihitung berhasil.

Benchmark dan gate penerimaan:
[EVAL] Gate: setiap run merekam dataset/split, corpus snapshot, model/prompt/config version, seed bila
relevan, serta biaya. Laporkan ukuran sampel dan ketidakpastian, hindari tuning pada test set, dan
pisahkan kualitas retrieval, graph, jawaban, serta waktu/biaya.

[ANSWER] Ukur answer accuracy pada pertanyaan answerable, correct abstention pada unanswerable, false
abstention pada answerable, serta worst required slice. Laporkan macro base-question aggregation dan
sample size per slice. Ambang wajib mengikuti configs/benchmark-targets.yaml.
Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED sampai diukur oleh
run yang memenuhi protokol.
Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.

Status: implementasi E01 aktif untuk answer accuracy, correct/false abstention, dan worst answerable
slice; hasil benchmark produksi belum tersedia.
Bukti verifikasi: uji slice hilang/kurang sampel, answerable abstention, unsupported answer, base-question
grouping, dan zero denominator.
Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.
"""
from __future__ import annotations

from typing import Mapping, Sequence

from .runtime import Estimate, minimum_slice_ratio, ratio


def answer_accuracy(correct_answerable: int, answerable_questions: int) -> Estimate:
    return ratio(correct_answerable, answerable_questions)


def abstention_metrics(correct_abstentions: int, unanswerable_questions: int, false_abstentions: int, answerable_questions: int) -> tuple[Estimate, Estimate]:
    return ratio(correct_abstentions, unanswerable_questions), ratio(false_abstentions, answerable_questions)


def worst_answerable_slice(slice_counts: Mapping[str, Mapping[str, int]], slices: Sequence[str]) -> Estimate:
    return minimum_slice_ratio(slice_counts, slices)
