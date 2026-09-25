# scripts

Entry point operasional yang tipis untuk setup lokal, validasi, dan pekerjaan pemeliharaan yang berulang. Dokumen ini menjadi kontrak cakupan folder dan panduan penempatan komponennya.

## Batas tanggung jawab

Tidak menampung implementasi bisnis yang seharusnya dapat digunakan workflow. Komponen baru harus sesuai definisi cakupan di atas. Jika tidak sesuai, jelaskan fungsi serta lokasi yang diusulkan kepada pengguna dan tanyakan apakah perlu folder baru atau perluasan cakupan folder ini sebelum menempatkannya. Ikuti [aturan repositori](../AGENTS.md).

## Peran dan integrasi anak

Anak memanggil modul aplikasi, mendokumentasikan prasyarat dan efek samping, serta membatasi target operasi. Script harus melaporkan kegagalan dengan exit code yang tepat.

Anak tidak boleh mengubah kontrak input/output secara tersembunyi. Perubahan bentuk data, identitas, versi, atau penanganan error didokumentasikan bersama konsumennya. File implementasi menjelaskan perhatian spesifiknya pada docstring atau komentar pembuka; aturan induk tetap berlaku.

## Isi saat ini

File C01 di bawah sudah dapat dijalankan dengan prasyarat toolchain pada dokumentasi. Pipeline produksi dan benchmark model belum aktif.

## Benchmark dan perhatian kualitas

**CONFIG.** Konfigurasi yang tidak valid harus ditolak secara eksplisit saat loader diimplementasikan. Import modul tidak boleh memicu I/O atau inference. Ukur startup dan waktu validasi bila menjadi bottleneck; ambang wajib mengikuti [target numerik wajib](../configs/benchmark-targets.yaml); hasil belum diukur.

Lihat [kebijakan benchmark](../doc/benchmark-policy.md) untuk protokol pengukuran dan penetapan angka target. Target numerik wajib berada di [target numerik wajib](../configs/benchmark-targets.yaml) dengan status **REQUIRED_UNMEASURED**; belum ada hasil yang diklaim tercapai. Gate deterministik berlaku pada data yang diterbitkan dan fixtures yang relevan; hasil semantik tetap memerlukan evaluasi.

## Status implementasi

[generate_lexical_letters.go](generate_lexical_letters.go) menghasilkan tabel kategori
huruf dan properti stream-safe NFC Unicode 15 untuk analyzer Rust dari tabel
`unicode.IsLetter` serta x/text NFC Go. Properti privat x/text dibaca hanya saat
generasi offline dan susunan field diperiksa agar perubahan dependency gagal jelas. Jalankan
`go run scripts/generate_lexical_letters.go --check` dari root repo untuk memeriksa
artefak terpin; `--write` hanya saat mengubah analyzer generation setelah review.
Generator bekerja offline dan tidak berada di jalur query/ingestion produksi.

Status lintas repositori: collector PDF dan kontrak/validator C01 sudah tersedia; lihat [cakupan implementasi C01](../doc/contracts-implementation.md). Pipeline parsing/graph/retrieval, adapter storage, layanan model dan evaluator benchmark masih belum aktif. Status anak dijelaskan pada header masing-masing; build dan fixture tidak membuktikan target kualitas atau latency.

## Penambahan C01 dan panduan verifikasi

Berkas terkait: [generate_contracts.py](generate_contracts.py), [check_contracts.py](check_contracts.py). Dependency, cara menjalankan dan batas pembuktiannya mengikuti [implementasi C01](../doc/contracts-implementation.md).

[build_native.ps1](build_native.ps1) menjalankan build tokenizer Rust, CMake service C++, dan CTest pada SDK lokal terpin. Script tidak mengunduh model atau membuka endpoint; prasyarat ada pada [panduan native](../doc/native-inference.md).
