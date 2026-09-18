# evaluation/metrics

Implementasi ukuran kualitas retrieval, ekstraksi, resolution, jawaban, citation, latency, dan biaya. Dokumen ini menjadi kontrak cakupan folder dan panduan penempatan komponennya.

## Batas tanggung jawab

Tidak memperlakukan skor LLM judge sebagai kebenaran hukum atau benchmark sebagai generator jawaban produksi. Komponen baru harus sesuai definisi cakupan di atas. Jika tidak sesuai, jelaskan fungsi serta lokasi yang diusulkan kepada pengguna dan tanyakan apakah perlu folder baru atau perluasan cakupan folder ini sebelum menempatkannya. Ikuti [aturan repositori](../../AGENTS.md).

## Peran dan integrasi anak

Anak menyatakan denominator, unit, kasus tanpa label, dan agregasi per kategori. Predicate, versi pasal, kelengkapan bukti multi-hop, serta abstention dinilai tersendiri; kalibrasikan judge terhadap penilaian manusia.

Anak tidak boleh mengubah kontrak input/output secara tersembunyi. Perubahan bentuk data, identitas, versi, atau penanganan error didokumentasikan bersama konsumennya. File implementasi menjelaskan perhatian spesifiknya pada docstring atau komentar pembuka; aturan induk tetap berlaku.

## Isi saat ini

Berkas langsung: [__init__.py](__init__.py), [answers.py](answers.py), [citations.py](citations.py), [graph.py](graph.py), [retrieval.py](retrieval.py), [runtime.py](runtime.py).

## Benchmark dan perhatian kualitas

**EVAL.** Gate: setiap run merekam dataset/split, corpus snapshot, model/prompt/config version, seed bila relevan, serta biaya. Laporkan ukuran sampel dan ketidakpastian, hindari tuning pada test set, dan pisahkan kualitas retrieval, graph, jawaban, serta waktu/biaya.

Lihat [kebijakan benchmark](../../doc/benchmark-policy.md) untuk protokol pengukuran dan penetapan angka target. Target numerik wajib berada di [target numerik wajib](../../configs/benchmark-targets.yaml) dengan status **REQUIRED_UNMEASURED**; belum ada hasil yang diklaim tercapai. Gate deterministik berlaku pada data yang diterbitkan dan fixtures yang relevan; hasil semantik tetap memerlukan evaluasi.

## Status implementasi

Struktur ini merupakan scaffold dokumentasi. Modul Go, Rust, C++, dan Python masih berupa scaffold; konfigurasi belum dikonsumsi pipeline, dan belum ada layanan aplikasi yang aktif. Build scaffold hanya memverifikasi struktur kode. Referensi pihak ketiga dan dokumen pengguna yang sudah ada dipertahankan; status scaffold tidak mengubah isi sumber tersebut.
