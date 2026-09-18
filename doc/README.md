# doc

Dokumentasi desain, keputusan arsitektur, kontrak data, dan referensi proyek. Folder ini menjelaskan sistem secara menyeluruh serta alasan pemilihan desain, tanpa menjalankan pipeline. Dokumen ini menjadi kontrak cakupan folder dan panduan penempatan komponennya.

## Batas tanggung jawab

Kode produksi, dataset benchmark, dan hasil eksperimen berada di folder masing-masing. Komponen baru harus sesuai definisi cakupan di atas. Jika tidak sesuai, jelaskan fungsi serta lokasi yang diusulkan kepada pengguna dan tanyakan apakah perlu folder baru atau perluasan cakupan folder ini sebelum menempatkannya. Ikuti [aturan repositori](../AGENTS.md).

## Peran dan integrasi anak

Dokumen anak harus membedakan keputusan yang disepakati, hipotesis yang akan diuji, dan implementasi yang benar-benar tersedia. Tautkan perubahan kontrak ke komponen terdampak; pertahankan sumber referensi asli.

Anak tidak boleh mengubah kontrak input/output secara tersembunyi. Perubahan bentuk data, identitas, versi, atau penanganan error didokumentasikan bersama konsumennya. File implementasi menjelaskan perhatian spesifiknya pada docstring atau komentar pembuka; aturan induk tetap berlaku.

## Isi saat ini

Subfolder yang dikelola: [decisions/](decisions/README.md).

Berkas langsung: [benchmark-targets.md](benchmark-targets.md), [Graph-Engineering-Athropic-Playbook.pdf](Graph-Engineering-Athropic-Playbook.pdf), [architecture.md](architecture.md), [benchmark-policy.md](benchmark-policy.md), [data-model.md](data-model.md), [reference.md](reference.md), [runtime-language-review.md](runtime-language-review.md).

## Benchmark dan perhatian kualitas

**DOC.** Dokumentasi harus konsisten dengan status scaffold/implementasi dan tidak menyatakan hasil eksperimen yang belum dijalankan. Tautan lokal dan rujukan kontrak diperiksa.

Lihat [kebijakan benchmark](benchmark-policy.md) untuk protokol pengukuran dan penetapan angka target. Target numerik wajib berada di [target numerik wajib](../configs/benchmark-targets.yaml) dengan status **REQUIRED_UNMEASURED**; belum ada hasil yang diklaim tercapai. Gate deterministik berlaku pada data yang diterbitkan dan fixtures yang relevan; hasil semantik tetap memerlukan evaluasi.

## Status implementasi

Struktur ini merupakan scaffold dokumentasi. Modul Go, Rust, C++, dan Python masih berupa scaffold; konfigurasi belum dikonsumsi pipeline, dan belum ada layanan aplikasi yang aktif. Build scaffold hanya memverifikasi struktur kode. Referensi pihak ketiga dan dokumen pengguna yang sudah ada dipertahankan; status scaffold tidak mengubah isi sumber tersebut.
