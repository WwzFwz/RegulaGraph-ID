# tests

Verifikasi perilaku implementasi melalui pengujian unit, integrasi, dan alur end-to-end. Pengujian melindungi kontrak dan regresi deterministik. Dokumen ini menjadi kontrak cakupan folder dan panduan penempatan komponennya.

## Batas tanggung jawab

Pengukuran kualitas model dan perbandingan metode berada di evaluation. Komponen baru harus sesuai definisi cakupan di atas. Jika tidak sesuai, jelaskan fungsi serta lokasi yang diusulkan kepada pengguna dan tanyakan apakah perlu folder baru atau perluasan cakupan folder ini sebelum menempatkannya. Ikuti [aturan repositori](../AGENTS.md).

## Peran dan integrasi anak

Unit test Go/Rust/C++ nantinya mengikuti toolchain dan dapat berada di dekat source runtime; folder unit pusat menyimpan panduan serta kasus lintas kontrak. Pengujian integration/end-to-end di sini memakai boundary nyata Go/Rust/C++ dan evaluator Python. Anak memakai fixtures kecil, membedakan layanan nyata dari fake, dan tidak mengandalkan jaringan untuk unit test. Jangan mengklaim kualitas model dari keluaran mock.

Anak tidak boleh mengubah kontrak input/output secara tersembunyi. Perubahan bentuk data, identitas, versi, atau penanganan error didokumentasikan bersama konsumennya. File implementasi menjelaskan perhatian spesifiknya pada docstring atau komentar pembuka; aturan induk tetap berlaku.

## Isi saat ini

Subfolder yang dikelola: [unit/](unit/README.md), [integration/](integration/README.md), [end_to_end/](end_to_end/README.md), [fixtures/](fixtures/README.md).

Folder ini baru menyediakan kontrak dokumentasi. File implementasi ditambahkan ketika pekerjaannya dimulai; tidak ada perilaku runtime yang dijanjikan oleh keberadaan folder.

## Benchmark dan perhatian kualitas

**TEST.** Pengujian harus memverifikasi perilaku dan kegagalan yang bermakna. Unit test tidak membutuhkan jaringan; integration/end-to-end menyatakan layanan yang diperlukan. Mock hanya memvalidasi alur, bukan akurasi model. Tidak ada klaim coverage atau test lulus sebelum dijalankan.

Lihat [kebijakan benchmark](../doc/benchmark-policy.md) untuk protokol pengukuran dan penetapan angka target. Target numerik wajib berada di [target numerik wajib](../configs/benchmark-targets.yaml) dengan status **REQUIRED_UNMEASURED**; belum ada hasil yang diklaim tercapai. Gate deterministik berlaku pada data yang diterbitkan dan fixtures yang relevan; hasil semantik tetap memerlukan evaluasi.

## Status implementasi

Struktur ini merupakan scaffold dokumentasi. Modul Go, Rust, C++, dan Python masih berupa scaffold; konfigurasi belum dikonsumsi pipeline, dan belum ada layanan aplikasi yang aktif. Build scaffold hanya memverifikasi struktur kode. Referensi pihak ketiga dan dokumen pengguna yang sudah ada dipertahankan; status scaffold tidak mengubah isi sumber tersebut.
