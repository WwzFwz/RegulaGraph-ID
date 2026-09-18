"""
Menandai paket evaluation.metrics dan mendokumentasikan batas ekspornya.

Peran dalam komponen:
Implementasi ukuran kualitas retrieval, ekstraksi, resolution, jawaban, citation, latency, dan biaya.

Kontrak integrasi dan perhatian implementasi:
Ekspor publik belum ditetapkan. Import tidak membuka koneksi, memuat model, membaca rahasia, atau menjalankan pipeline. Anak menyatakan denominator, unit, kasus tanpa label, dan agregasi per kategori. Predicate, versi pasal, kelengkapan bukti multi-hop, serta abstention dinilai tersendiri; kalibrasikan judge terhadap penilaian manusia.

Benchmark dan gate penerimaan:
[CONFIG] Konfigurasi yang tidak valid harus ditolak secara eksplisit saat loader diimplementasikan. Import modul tidak boleh memicu I/O atau inference. Ukur startup dan waktu validasi bila menjadi bottleneck; ambang wajib mengikuti configs/benchmark-targets.yaml; hasil belum diukur.
Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.

Status: scaffold dokumentasi; perilaku modul belum diimplementasikan.
"""
