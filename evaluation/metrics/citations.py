"""
Mengukur ketepatan citation, cakupan claim, dan pemilihan versi regulasi yang berlaku.

Peran dalam komponen:
Memastikan jawaban dapat ditelusuri ke bukti tingkat pasal yang benar dan tidak memakai versi temporal
yang salah.

Kontrak integrasi dan perhatian implementasi:
Count harus berasal dari review claim-level terhadap acceptable evidence sets. URL valid atau judul
dokumen yang cocok tidak cukup sebagai bukti dukungan. Precision, coverage, dan temporal accuracy
memakai denominator terpisah agar satu metrik tidak menyamarkan kegagalan metrik lain.

Benchmark dan gate penerimaan:
[EVAL] Gate: setiap run merekam dataset/split, corpus snapshot, model/prompt/config version, seed bila
relevan, serta biaya. Laporkan ukuran sampel dan ketidakpastian, hindari tuning pada test set, dan
pisahkan kualitas retrieval, graph, jawaban, serta waktu/biaya.

[CITATION] Ukur claim-evidence precision, coverage claim yang wajib bersumber, dan temporal/version
accuracy. Penilaian harus memverifikasi bahwa evidence mendukung claim, bukan hanya bahwa citation
terbuka. Ambang wajib mengikuti configs/benchmark-targets.yaml.
Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED sampai diukur oleh
run yang memenuhi protokol.
Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.

Status: implementasi E01 aktif untuk citation precision, citation coverage, dan temporal/version
accuracy dari reviewed counts; hasil benchmark produksi belum tersedia.
Bukti verifikasi: uji unsupported citation, claim tanpa citation, acceptable evidence alternatif,
temporal unknown yang dibenarkan, dan zero denominator.
Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.
"""
from __future__ import annotations

from .runtime import Estimate, ratio


def citation_precision(supported_pairs: int, cited_claim_pairs: int) -> Estimate:
    return ratio(supported_pairs, cited_claim_pairs)


def citation_coverage(sourced_claims: int, claims_requiring_sources: int) -> Estimate:
    return ratio(sourced_claims, claims_requiring_sources)


def temporal_accuracy(correct_versions_or_unknown: int, temporal_questions: int) -> Estimate:
    return ratio(correct_versions_or_unknown, temporal_questions)
