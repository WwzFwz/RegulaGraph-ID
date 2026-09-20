"""
Mengukur Recall@k, ranking, dan kelengkapan seluruh bukti pendukung pertanyaan.

Peran dalam komponen:
Membedakan keberhasilan menemukan satu potongan dari keberhasilan menemukan semua bukti multi-hop.

Kontrak integrasi dan perhatian implementasi:
Nyatakan unit evaluasi chunk/pasal/versi dan denominator; hitung kategori factual, temporal, typo, informal, dan code-switch terpisah.

Benchmark dan gate penerimaan:
[EVAL] Gate: setiap run merekam dataset/split, corpus snapshot, model/prompt/config version, seed bila relevan, serta biaya. Laporkan ukuran sampel dan ketidakpastian, hindari tuning pada test set, dan pisahkan kualitas retrieval, graph, jawaban, serta waktu/biaya.

[RETRIEVAL] Ukur Recall@k, nDCG@k, kelengkapan semua bukti multi-hop, serta latency p50/p95 per jenis pertanyaan. Catat jumlah kandidat, hop, snapshot, dan kondisi cold/warm. Ambang wajib mengikuti configs/benchmark-targets.yaml; jangan menganggap batas hop/kandidat atau graph selalu meningkatkan kualitas.
Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.

Status: scaffold dokumentasi; perilaku modul belum diimplementasikan.
Rekomendasi implementasi berikutnya (belum merupakan fitur aktif):
Compute Recall@k, nDCG@k and all-required-evidence/path coverage using production ranked output.
Bukti verifikasi: Test duplicates, ties, multiple acceptable sets and unanswerable questions; keep denominators and truncation policy explicit.
Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.
"""
