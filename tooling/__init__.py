"""
Paket tooling Python offline.

Peran dalam komponen:
Mengelompokkan alat pengembangan model di luar request produksi.

Integrasi dan perhatian performa:
Import tidak memuat model atau mengakses jaringan.

Benchmark dan gate penerimaan:
Ukur p50/p95/p99, throughput, waktu antre, serta RSS/VRAM sesuai workload. Ambang wajib ada di configs/benchmark-targets.yaml (REQUIRED_UNMEASURED); ukur cold/warm terpisah dan pertahankan kualitas sumber/versi.
Bandingkan hasil terhadap referensi Python serta gold evidence; ekspor yang dapat
dimuat belum menjamin kesetaraan kualitas retrieval/reranking.

Status: scaffold dokumentasi; belum ada tooling ekspor yang aktif.
"""
