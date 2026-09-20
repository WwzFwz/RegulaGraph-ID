# tests/unit

Pengujian terisolasi untuk kontrak domain dan perilaku tiap komponen yang deterministik. Dokumen ini menjadi kontrak cakupan folder dan panduan penempatan komponennya.

## Batas tanggung jawab

Tidak memulai database atau model eksternal. Komponen baru harus sesuai definisi cakupan di atas. Jika tidak sesuai, jelaskan fungsi serta lokasi yang diusulkan kepada pengguna dan tanyakan apakah perlu folder baru atau perluasan cakupan folder ini sebelum menempatkannya. Ikuti [aturan repositori](../../AGENTS.md).

## Peran dan integrasi anak

Test executable bahasa mengikuti package/crate/target build masing-masing; folder ini menjadi panduan dan data kasus bersama. Anak menguji kasus bermakna seperti ID, version filtering, pemetaan sumber, merge, dan invalidasi. Gunakan fake pada batas infrastruktur, bukan menyalin implementasi ke dalam assert.

Anak tidak boleh mengubah kontrak input/output secara tersembunyi. Perubahan bentuk data, identitas, versi, atau penanganan error didokumentasikan bersama konsumennya. File implementasi menjelaskan perhatian spesifiknya pada docstring atau komentar pembuka; aturan induk tetap berlaku.

## Isi saat ini

File Python aktif mencakup validasi kontrak C01, loader konfigurasi E01, primitive metrik, status gate, dan
derivasi telemetry. Semua fixture bersifat sintetis dan tidak membuktikan kualitas/latency produksi.

## Benchmark dan perhatian kualitas

**TEST.** Pengujian harus memverifikasi perilaku dan kegagalan yang bermakna. Unit test tidak membutuhkan jaringan; integration/end-to-end menyatakan layanan yang diperlukan. Mock hanya memvalidasi alur, bukan akurasi model. Tidak ada klaim coverage atau test lulus sebelum dijalankan.

Lihat [kebijakan benchmark](../../doc/benchmark-policy.md) untuk protokol pengukuran dan penetapan angka target. Target numerik wajib berada di [target numerik wajib](../../configs/benchmark-targets.yaml) dengan status **REQUIRED_UNMEASURED**; belum ada hasil yang diklaim tercapai. Gate deterministik berlaku pada data yang diterbitkan dan fixtures yang relevan; hasil semantik tetap memerlukan evaluasi.

## Status implementasi

E01 memiliki pengujian aktif pada [test_evaluation_config.py](test_evaluation_config.py),
[test_evaluation_dataset_loader.py](test_evaluation_dataset_loader.py),
[test_evaluation_metrics.py](test_evaluation_metrics.py), [test_evaluation_gates.py](test_evaluation_gates.py),
dan [test_evaluation_telemetry.py](test_evaluation_telemetry.py). Kontrak C01 tetap diperiksa oleh
[test_evaluation_contracts.py](test_evaluation_contracts.py). Profiler corpus M01 diperiksa oleh
[test_corpus_profile.py](test_corpus_profile.py). Gunakan generated bindings pada PYTHONPATH.
PASS test tersebut membuktikan perilaku deterministik evaluator, bukan target benchmark.
