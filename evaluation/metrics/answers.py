"""
Mengukur ketepatan jawaban, ketepatan versi, faithfulness, dan kemampuan menjawab terbatas.

Peran dalam komponen:
Menggabungkan label/manual review dengan adapter metrik seperti RAGAS jika dipilih.

Kontrak integrasi dan perhatian implementasi:
Judge, prompt, dan bahasa evaluasi dicatat serta dikalibrasi; faithful terhadap konteks tidak berarti konteks tersebut versi yang benar.

Benchmark dan gate penerimaan:
[EVAL] Gate: setiap run merekam dataset/split, corpus snapshot, model/prompt/config version, seed bila relevan, serta biaya. Laporkan ukuran sampel dan ketidakpastian, hindari tuning pada test set, dan pisahkan kualitas retrieval, graph, jawaban, serta waktu/biaya.

[ANSWER] Ukur ketepatan jawaban, citation precision/coverage, ketepatan versi, abstention pada pertanyaan tanpa bukti, faithfulness, latency p50/p95, dan token/biaya. Gate deterministik hanya validitas referensi; dukungan semantik harus dievaluasi tersendiri dengan review manusia terkalibrasi.
Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.

Status: scaffold dokumentasi; perilaku modul belum diimplementasikan.
Rekomendasi implementasi berikutnya (belum merupakan fitur aktif):
Score correctness, temporal correctness, faithfulness and abstention separately; calibrate any judge against human labels.
Bukti verifikasi: Test ambiguous/no-answer/conflict cases and invalid denominator; report category counts and uncertainty without tuning on test labels.
Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.
"""
