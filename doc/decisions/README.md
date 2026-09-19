# doc/decisions

Catatan keputusan yang memengaruhi batas komponen, kontrak, atau perilaku lintas pipeline. Catatan menyimpan konteks, pilihan, konsekuensi, dan status keputusan. Dokumen ini menjadi kontrak cakupan folder dan panduan penempatan komponennya.

## Batas tanggung jawab

Bukan tempat menyimpan log eksekusi atau mengganti referensi sumber. Komponen baru harus sesuai definisi cakupan di atas. Jika tidak sesuai, jelaskan fungsi serta lokasi yang diusulkan kepada pengguna dan tanyakan apakah perlu folder baru atau perluasan cakupan folder ini sebelum menempatkannya. Ikuti [aturan repositori](../../AGENTS.md).

## Peran dan integrasi anak

Setiap keputusan anak merujuk komponen yang terkena dampak dan menjelaskan migrasi bila kontrak berubah. Perubahan cakupan harus mengikuti AGENTS.md.

Anak tidak boleh mengubah kontrak input/output secara tersembunyi. Perubahan bentuk data, identitas, versi, atau penanganan error didokumentasikan bersama konsumennya. File implementasi menjelaskan perhatian spesifiknya pada docstring atau komentar pembuka; aturan induk tetap berlaku.

## Isi saat ini

Berkas langsung: [0001-scaffold-boundaries.md](0001-scaffold-boundaries.md), [0002-polyglot-runtime.md](0002-polyglot-runtime.md), [0003-required-benchmark-targets.md](0003-required-benchmark-targets.md), [0004-product-source-layout.md](0004-product-source-layout.md), [0005-complete-system-design.md](0005-complete-system-design.md).

## Benchmark dan perhatian kualitas

**DOC.** Dokumentasi harus konsisten dengan status scaffold/implementasi dan tidak menyatakan hasil eksperimen yang belum dijalankan. Tautan lokal dan rujukan kontrak diperiksa.

Lihat [kebijakan benchmark](../benchmark-policy.md) untuk protokol pengukuran dan penetapan angka target. Target numerik wajib berada di [target numerik wajib](../../configs/benchmark-targets.yaml) dengan status **REQUIRED_UNMEASURED**; belum ada hasil yang diklaim tercapai. Gate deterministik berlaku pada data yang diterbitkan dan fixtures yang relevan; hasil semantik tetap memerlukan evaluasi.

## Status implementasi

Struktur ini merupakan scaffold dokumentasi. Modul lintas bahasa masih berupa scaffold, konfigurasi belum dikonsumsi aplikasi, dan belum ada layanan atau pipeline yang aktif. Referensi pihak ketiga dan dokumen pengguna yang sudah ada dipertahankan; status scaffold tidak mengubah isi sumber tersebut.
