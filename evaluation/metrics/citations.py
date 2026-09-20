"""
Mengukur validitas referensi, dukungan semantik klaim, dan kelengkapan citation.

Peran dalam komponen:
Memisahkan citation yang menunjuk sumber nyata dari citation yang benar-benar mendukung klaim.

Kontrak integrasi dan perhatian implementasi:
Definisikan unit klaim dan coverage; periksa pasal/ayat/versi, bukan hanya kecocokan URL atau nama dokumen.

Benchmark dan gate penerimaan:
[EVAL] Gate: setiap run merekam dataset/split, corpus snapshot, model/prompt/config version, seed bila relevan, serta biaya. Laporkan ukuran sampel dan ketidakpastian, hindari tuning pada test set, dan pisahkan kualitas retrieval, graph, jawaban, serta waktu/biaya.

[ANSWER] Ukur ketepatan jawaban, citation precision/coverage, ketepatan versi, abstention pada pertanyaan tanpa bukti, faithfulness, latency p50/p95, dan token/biaya. Gate deterministik hanya validitas referensi; dukungan semantik harus dievaluasi tersendiri dengan review manusia terkalibrasi.
Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.

Status: scaffold dokumentasi; perilaku modul belum diimplementasikan.
Rekomendasi implementasi berikutnya (belum merupakan fitur aktif):
Score claim-level citation precision/coverage and correct version/locator against acceptable evidence sets.
Bukti verifikasi: Distinguish syntactic references from actual semantic support; test multiple valid evidence sets, unsupported claims and missing annotations.
Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.
"""
