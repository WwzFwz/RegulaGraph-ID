"""
Menandai paket evaluation.datasets dan mendokumentasikan batas ekspornya.

Peran dalam komponen:
Dataset berlabel untuk pertanyaan, jawaban rujukan, bukti pasal/versi, hubungan graph, dan pertanyaan tanpa dukungan corpus.

Kontrak integrasi dan perhatian implementasi:
Ekspor publik belum ditetapkan. Import tidak membuka koneksi, memuat model, membaca rahasia, atau menjalankan pipeline. Anak memiliki schema dan provenance label, reviewer, group ID, serta split. Parafrasa, typo, dan code-switch dari pertanyaan dasar yang sama harus berada pada split yang sama.

Benchmark dan gate penerimaan:
[CONFIG] Konfigurasi yang tidak valid harus ditolak secara eksplisit saat loader diimplementasikan. Import modul tidak boleh memicu I/O atau inference. Ukur startup dan waktu validasi bila menjadi bottleneck; ambang wajib mengikuti configs/benchmark-targets.yaml; hasil belum diukur.
Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.

Status: scaffold dokumentasi; perilaku modul belum diimplementasikan.
"""
