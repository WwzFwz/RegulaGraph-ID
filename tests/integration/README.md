# tests/integration

Pengujian kontrak antara komponen aplikasi dan adapter layanan yang benar-benar dijalankan untuk test. Dokumen ini menjadi kontrak cakupan folder dan panduan penempatan komponennya.

## Batas tanggung jawab

Bukan benchmark model atau pengujian unit dengan nama integrasi. Komponen baru harus sesuai definisi cakupan di atas. Jika tidak sesuai, jelaskan fungsi serta lokasi yang diusulkan kepada pengguna dan tanyakan apakah perlu folder baru atau perluasan cakupan folder ini sebelum menempatkannya. Ikuti [aturan repositori](../../AGENTS.md).

## Peran dan integrasi anak

Anak memakai resource khusus test, setup/teardown terkontrol, dan penanda kebutuhan layanan. Uji idempotensi, transaksi lokal, payload, serta penanganan timeout.

Anak tidak boleh mengubah kontrak input/output secara tersembunyi. Perubahan bentuk data, identitas, versi, atau penanganan error didokumentasikan bersama konsumennya. File implementasi menjelaskan perhatian spesifiknya pada docstring atau komentar pembuka; aturan induk tetap berlaku.

## Isi saat ini

File C01 di bawah dan [test_evaluation_runner.py](test_evaluation_runner.py) dapat dijalankan dengan
prasyarat toolchain pada dokumentasi. Test runner memakai filesystem dan protobuf nyata, tetapi tetap
fixture sintetis tanpa pipeline/model produksi.

## Benchmark dan perhatian kualitas

**TEST.** Pengujian harus memverifikasi perilaku dan kegagalan yang bermakna. Unit test tidak membutuhkan jaringan; integration/end-to-end menyatakan layanan yang diperlukan. Mock hanya memvalidasi alur, bukan akurasi model. Tidak ada klaim coverage atau test lulus sebelum dijalankan.

**STORAGE.** Ukur latency p50/p95/p99, throughput batch, pool saturation, retry, dan error rate pada concurrency serta volume data yang disebutkan. Gate: timeout terlapor, resource dilepas, dan operasi tulis idempotent sesuai kontrak; tidak ada asumsi transaksi atomik lintas layanan.

Lihat [kebijakan benchmark](../../doc/benchmark-policy.md) untuk protokol pengukuran dan penetapan angka target. Target numerik wajib berada di [target numerik wajib](../../configs/benchmark-targets.yaml) dengan status **REQUIRED_UNMEASURED**; belum ada hasil yang diklaim tercapai. Gate deterministik berlaku pada data yang diterbitkan dan fixtures yang relevan; hasil semantik tetap memerlukan evaluasi.

## Status implementasi

E01 runner offline serta C01 contract round-trip aktif dan diuji. Pipeline parsing/graph/retrieval, adapter
storage, layanan model, gold dataset, dan acceptance run produksi belum aktif; target numerik tetap
**REQUIRED_UNMEASURED**.

## Penambahan C01 dan panduan verifikasi

Berkas terkait: [wire_roundtrip.py](wire_roundtrip.py), [wire_cpp.cpp](wire_cpp.cpp). Dependency, cara menjalankan dan batas pembuktiannya mengikuti [implementasi C01](../../doc/contracts-implementation.md).

[native_fixture.py](native_fixture.py) menghasilkan model ONNX sintetis; [test_model_export.py](test_model_export.py) membandingkan tiny Transformers/ORT; [test_native_runtime.py](test_native_runtime.py) menguji executable nyata untuk admission, cancellation, deadline, serta bobot eksternal. Prasyarat dan opt-in environment ada pada [panduan native](../../doc/native-inference.md).
