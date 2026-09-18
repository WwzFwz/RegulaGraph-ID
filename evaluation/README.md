# evaluation

Benchmark offline untuk menilai empat pendekatan RAG, kualitas graph, citation, latency, dan biaya. Evaluasi menggunakan komponen produksi yang sama dengan konfigurasi berbeda. Dokumen ini menjadi kontrak cakupan folder dan panduan penempatan komponennya.

## Batas tanggung jawab

Tidak berada pada critical path request pengguna dan tidak menjadi pipeline produksi kedua. Komponen baru harus sesuai definisi cakupan di atas. Jika tidak sesuai, jelaskan fungsi serta lokasi yang diusulkan kepada pengguna dan tanyakan apakah perlu folder baru atau perluasan cakupan folder ini sebelum menempatkannya. Ikuti [aturan repositori](../AGENTS.md).

## Peran dan integrasi anak

Runner Python menggunakan endpoint Go atau artefak terstruktur dari runtime produksi sesuai src/contracts, bukan import regulagraph Python. Anak memakai corpus snapshot, dataset version, konfigurasi, dan model version yang tercatat. Pisahkan development/test, cegah kebocoran variasi pertanyaan, dan simpan hasil di artifacts.

Anak tidak boleh mengubah kontrak input/output secara tersembunyi. Perubahan bentuk data, identitas, versi, atau penanganan error didokumentasikan bersama konsumennya. File implementasi menjelaskan perhatian spesifiknya pada docstring atau komentar pembuka; aturan induk tetap berlaku.

## Isi saat ini

Subfolder yang dikelola: [datasets/](datasets/README.md), [experiments/](experiments/README.md), [metrics/](metrics/README.md).

Berkas langsung: [__init__.py](__init__.py), [runner.py](runner.py).

## Benchmark dan perhatian kualitas

**EVAL.** Gate: setiap run merekam dataset/split, corpus snapshot, model/prompt/config version, seed bila relevan, serta biaya. Laporkan ukuran sampel dan ketidakpastian, hindari tuning pada test set, dan pisahkan kualitas retrieval, graph, jawaban, serta waktu/biaya.

Lihat [kebijakan benchmark](../doc/benchmark-policy.md) untuk protokol pengukuran dan penetapan angka target. Target numerik wajib berada di [target numerik wajib](../configs/benchmark-targets.yaml) dengan status **REQUIRED_UNMEASURED**; belum ada hasil yang diklaim tercapai. Gate deterministik berlaku pada data yang diterbitkan dan fixtures yang relevan; hasil semantik tetap memerlukan evaluasi.

## Status implementasi

Struktur ini merupakan scaffold dokumentasi. Modul Go, Rust, C++, dan Python masih berupa scaffold; konfigurasi belum dikonsumsi pipeline, dan belum ada layanan aplikasi yang aktif. Build scaffold hanya memverifikasi struktur kode. Referensi pihak ketiga dan dokumen pengguna yang sudah ada dipertahankan; status scaffold tidak mengubah isi sumber tersebut.
