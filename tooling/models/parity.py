"""
Membandingkan output model referensi dengan runtime native.

Peran dalam komponen:
Menilai kesetaraan embedding/skor sebelum model dipakai serving.

Integrasi dan perhatian performa:
Toleransi numerik ditentukan per precision; nilai Recall@k/nDCG dan kualitas jawaban harus diperiksa terpisah.

Benchmark dan gate penerimaan:
Ukur p50/p95/p99, throughput, waktu antre, serta RSS/VRAM sesuai workload. Ambang wajib ada di configs/benchmark-targets.yaml (REQUIRED_UNMEASURED); ukur cold/warm terpisah dan pertahankan kualitas sumber/versi.
Bandingkan hasil terhadap referensi Python serta gold evidence; ekspor yang dapat
dimuat belum menjamin kesetaraan kualitas retrieval/reranking.

Status: scaffold dokumentasi; belum ada tooling ekspor yang aktif.
"""
