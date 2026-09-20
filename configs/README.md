# configs

Konfigurasi deklaratif untuk ingestion, retrieval, dan eksperimen. Konfigurasi memisahkan parameter percobaan dari logika aplikasi. Dokumen ini menjadi kontrak cakupan folder dan panduan penempatan komponennya.

## Batas tanggung jawab

Rahasia berada dalam environment; implementasi algoritma berada di src/server, src/ingestion, dan src/inference. Komponen baru harus sesuai definisi cakupan di atas. Jika tidak sesuai, jelaskan fungsi serta lokasi yang diusulkan kepada pengguna dan tanyakan apakah perlu folder baru atau perluasan cakupan folder ini sebelum menempatkannya. Ikuti [aturan repositori](../AGENTS.md).

## Peran dan integrasi anak

Anak folder ini harus menggunakan nama parameter yang divalidasi loader Go pada src/server/internal/config, menyatakan versi konfigurasi, dan menghasilkan fingerprint untuk reproduksi. Nilai eksperimen tidak boleh diam-diam menjadi keputusan permanen.

Anak tidak boleh mengubah kontrak input/output secara tersembunyi. Perubahan bentuk data, identitas, versi, atau penanganan error didokumentasikan bersama konsumennya. File implementasi menjelaskan perhatian spesifiknya pada docstring atau komentar pembuka; aturan induk tetap berlaku.

## Isi saat ini

Berkas langsung: [benchmark-targets.yaml](benchmark-targets.yaml), [evaluation.yaml](evaluation.yaml), [ingestion.yaml](ingestion.yaml), [retrieval.yaml](retrieval.yaml), [sources.txt](sources.txt), [listings.txt](listings.txt).

sources.txt adalah input collector PDF yang sudah dapat dijalankan; konfigurasi YAML pipeline/evaluator tetap scaffold. Format seed berupa satu URL per baris dengan komentar #. [Panduan akuisisi](../doc/acquisition.md) menjelaskan download, resume, refresh, dan batas discovery.

listings.txt berisi seed halaman katalog untuk perintah discover, sedangkan sources.txt berisi URL detail untuk collect. Perintah discover menghasilkan daftar panjang di data/acquisition/queue.txt; hasil crawl tidak dimasukkan ke konfigurasi Git.

## Benchmark dan perhatian kualitas

**CONFIG.** Konfigurasi yang tidak valid harus ditolak secara eksplisit saat loader diimplementasikan. Import modul tidak boleh memicu I/O atau inference. Ukur startup dan waktu validasi bila menjadi bottleneck; ambang wajib mengikuti [target numerik wajib](benchmark-targets.yaml); hasil belum diukur.

Lihat [kebijakan benchmark](../doc/benchmark-policy.md) untuk protokol pengukuran dan penetapan angka target. Target numerik wajib berada di [target numerik wajib](benchmark-targets.yaml) dengan status **REQUIRED_UNMEASURED**; belum ada hasil yang diklaim tercapai. Gate deterministik berlaku pada data yang diterbitkan dan fixtures yang relevan; hasil semantik tetap memerlukan evaluasi.

## Status implementasi

Struktur ini merupakan scaffold dokumentasi. Modul Go, Rust, C++, dan Python masih berupa scaffold; konfigurasi belum dikonsumsi pipeline, dan belum ada layanan aplikasi yang aktif. Build scaffold hanya memverifikasi struktur kode. Referensi pihak ketiga dan dokumen pengguna yang sudah ada dipertahankan; status scaffold tidak mengubah isi sumber tersebut.
