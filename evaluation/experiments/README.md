# evaluation/experiments

Definisi eksperimen Vector RAG, Hybrid RAG, GraphRAG, dan Hybrid GraphRAG serta ablation yang diperlukan. Dokumen ini menjadi kontrak cakupan folder dan panduan penempatan komponennya.

## Batas tanggung jawab

Tidak menyalin implementasi retriever atau membuat dataset test menjadi bahan tuning. Komponen baru harus sesuai definisi cakupan di atas. Jika tidak sesuai, jelaskan fungsi serta lokasi yang diusulkan kepada pengguna dan tanyakan apakah perlu folder baru atau perluasan cakupan folder ini sebelum menempatkannya. Ikuti [aturan repositori](../../AGENTS.md).

## Peran dan integrasi anak

Anak menggunakan corpus, generator, budget konteks, dan definisi metrik yang sebanding. Perbedaan reranking atau seed graph harus dinyatakan agar pengaruh graph tidak tertukar dengan faktor lain.

Anak tidak boleh mengubah kontrak input/output secara tersembunyi. Perubahan bentuk data, identitas, versi, atau penanganan error didokumentasikan bersama konsumennya. File implementasi menjelaskan perhatian spesifiknya pada docstring atau komentar pembuka; aturan induk tetap berlaku.

## Isi saat ini

Berkas langsung: [__init__.py](__init__.py), [profiles.yaml](profiles.yaml).

## Benchmark dan perhatian kualitas

**EVAL.** Gate: setiap run merekam dataset/split, corpus snapshot, model/prompt/config version, seed bila relevan, serta biaya. Laporkan ukuran sampel dan ketidakpastian, hindari tuning pada test set, dan pisahkan kualitas retrieval, graph, jawaban, serta waktu/biaya.

Lihat [kebijakan benchmark](../../doc/benchmark-policy.md) untuk protokol pengukuran dan penetapan angka target. Target numerik wajib berada di [target numerik wajib](../../configs/benchmark-targets.yaml) dengan status **REQUIRED_UNMEASURED**; belum ada hasil yang diklaim tercapai. Gate deterministik berlaku pada data yang diterbitkan dan fixtures yang relevan; hasil semantik tetap memerlukan evaluasi.

## Status implementasi

Struktur ini merupakan scaffold dokumentasi. Modul Go, Rust, C++, dan Python masih berupa scaffold; konfigurasi belum dikonsumsi pipeline, dan belum ada layanan aplikasi yang aktif. Build scaffold hanya memverifikasi struktur kode. Referensi pihak ketiga dan dokumen pengguna yang sudah ada dipertahankan; status scaffold tidak mengubah isi sumber tersebut.
