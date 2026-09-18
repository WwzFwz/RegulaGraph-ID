# tests/integration

Pengujian kontrak antara komponen aplikasi dan adapter layanan yang benar-benar dijalankan untuk test. Dokumen ini menjadi kontrak cakupan folder dan panduan penempatan komponennya.

## Batas tanggung jawab

Bukan benchmark model atau pengujian unit dengan nama integrasi. Komponen baru harus sesuai definisi cakupan di atas. Jika tidak sesuai, jelaskan fungsi serta lokasi yang diusulkan kepada pengguna dan tanyakan apakah perlu folder baru atau perluasan cakupan folder ini sebelum menempatkannya. Ikuti [aturan repositori](../../AGENTS.md).

## Peran dan integrasi anak

Anak memakai resource khusus test, setup/teardown terkontrol, dan penanda kebutuhan layanan. Uji idempotensi, transaksi lokal, payload, serta penanganan timeout.

Anak tidak boleh mengubah kontrak input/output secara tersembunyi. Perubahan bentuk data, identitas, versi, atau penanganan error didokumentasikan bersama konsumennya. File implementasi menjelaskan perhatian spesifiknya pada docstring atau komentar pembuka; aturan induk tetap berlaku.

## Isi saat ini

Folder ini baru menyediakan kontrak dokumentasi. File implementasi ditambahkan ketika pekerjaannya dimulai; tidak ada perilaku runtime yang dijanjikan oleh keberadaan folder.

## Benchmark dan perhatian kualitas

**TEST.** Pengujian harus memverifikasi perilaku dan kegagalan yang bermakna. Unit test tidak membutuhkan jaringan; integration/end-to-end menyatakan layanan yang diperlukan. Mock hanya memvalidasi alur, bukan akurasi model. Tidak ada klaim coverage atau test lulus sebelum dijalankan.

**STORAGE.** Ukur latency p50/p95/p99, throughput batch, pool saturation, retry, dan error rate pada concurrency serta volume data yang disebutkan. Gate: timeout terlapor, resource dilepas, dan operasi tulis idempotent sesuai kontrak; tidak ada asumsi transaksi atomik lintas layanan.

Lihat [kebijakan benchmark](../../doc/benchmark-policy.md) untuk protokol pengukuran dan penetapan angka target. Target numerik wajib berada di [target numerik wajib](../../configs/benchmark-targets.yaml) dengan status **REQUIRED_UNMEASURED**; belum ada hasil yang diklaim tercapai. Gate deterministik berlaku pada data yang diterbitkan dan fixtures yang relevan; hasil semantik tetap memerlukan evaluasi.

## Status implementasi

Struktur ini merupakan scaffold dokumentasi. Modul Go, Rust, C++, dan Python masih berupa scaffold; konfigurasi belum dikonsumsi pipeline, dan belum ada layanan aplikasi yang aktif. Build scaffold hanya memverifikasi struktur kode. Referensi pihak ketiga dan dokumen pengguna yang sudah ada dipertahankan; status scaffold tidak mengubah isi sumber tersebut.
