# doc

Dokumentasi desain, keputusan arsitektur, kontrak data, dan referensi proyek. Folder ini menjelaskan sistem secara menyeluruh serta alasan pemilihan desain, tanpa menjalankan pipeline. Dokumen ini menjadi kontrak cakupan folder dan panduan penempatan komponennya.

## Batas tanggung jawab

Kode produksi, dataset benchmark, dan hasil eksperimen berada di folder masing-masing. Komponen baru harus sesuai definisi cakupan di atas. Jika tidak sesuai, jelaskan fungsi serta lokasi yang diusulkan kepada pengguna dan tanyakan apakah perlu folder baru atau perluasan cakupan folder ini sebelum menempatkannya. Ikuti [aturan repositori](../AGENTS.md).

## Peran dan integrasi anak

Dokumen anak harus membedakan keputusan yang disepakati, hipotesis yang akan diuji, dan implementasi yang benar-benar tersedia. Tautkan perubahan kontrak ke komponen terdampak; pertahankan sumber referensi asli.

Anak tidak boleh mengubah kontrak input/output secara tersembunyi. Perubahan bentuk data, identitas, versi, atau penanganan error didokumentasikan bersama konsumennya. File implementasi menjelaskan perhatian spesifiknya pada docstring atau komentar pembuka; aturan induk tetap berlaku.

## Isi saat ini

Subfolder yang dikelola: [decisions/](decisions/README.md).

Berkas langsung: [acquisition.md](acquisition.md), [system-design.md](system-design.md), [system-contracts.md](system-contracts.md), [storage-consistency.md](storage-consistency.md), [corpus-plan.md](corpus-plan.md), [development-plan.md](development-plan.md), [benchmark-targets.md](benchmark-targets.md), [evaluation-runner.md](evaluation-runner.md), [Graph-Engineering-Athropic-Playbook.pdf](Graph-Engineering-Athropic-Playbook.pdf), [architecture.md](architecture.md), [benchmark-policy.md](benchmark-policy.md), [data-model.md](data-model.md), [reference.md](reference.md), [runtime-language-review.md](runtime-language-review.md), [verification-report-d01-inventory.md](verification-report-d01-inventory.md), [verification-report-e01.md](verification-report-e01.md), [verification-report-i01-pdf-parser.md](verification-report-i01-pdf-parser.md), [verification-report-i01-text-normalization.md](verification-report-i01-text-normalization.md), [verification-report-m01-pdf-profile.md](verification-report-m01-pdf-profile.md), dan [verification-report-s01.md](verification-report-s01.md).

Mulai dari system-design untuk membaca keseluruhan rancangan, lalu system-contracts dan storage-consistency untuk semantik integrasi. Corpus-plan mengikat sumber pilihan pengguna dan prosedur gold dataset. Development-plan mengurutkan seluruh implementasi menurut dependency beserta bukti kelulusannya. Dokumen desain tidak berarti pipeline atau benchmark sudah aktif; status kontrak C01 dijelaskan terpisah.

## Benchmark dan perhatian kualitas

**DOC.** Dokumentasi harus konsisten dengan status scaffold/implementasi dan tidak menyatakan hasil eksperimen yang belum dijalankan. Tautan lokal dan rujukan kontrak diperiksa.

Lihat [kebijakan benchmark](benchmark-policy.md) untuk protokol pengukuran dan penetapan angka target. Target numerik wajib berada di [target numerik wajib](../configs/benchmark-targets.yaml) dengan status **REQUIRED_UNMEASURED**; belum ada hasil yang diklaim tercapai. Gate deterministik berlaku pada data yang diterbitkan dan fixtures yang relevan; hasil semantik tetap memerlukan evaluasi.

## Status implementasi

Collector dan audit inventory D01, kontrak/validator C01, evaluator offline E01, fondasi control-plane
storage/publication S01, serta parser PDFium, normalizer teks, parser struktur hukum, dan parent-aware chunk builder I01 sudah aktif. Profiler M01 telah membandingkan tiga
engine PDF. Worker/batch wire I01 berikutnya, graph/retrieval, mutasi backend Qdrant/Neo4j,
layanan model, gold dataset, serta acceptance run produksi belum aktif. Status anak dijelaskan pada header
masing-masing; profil parser, audit integrity, test correctness S01, dan evaluator sintetis tidak membuktikan
target kualitas atau latency produksi.

## Penambahan C01 dan panduan verifikasi

Berkas terkait: [implementation-guide.md](implementation-guide.md), [contracts-implementation.md](contracts-implementation.md), [verification.md](verification.md), [verification-contracts.md](verification-contracts.md), [verification-pipeline.md](verification-pipeline.md), [verification-quality.md](verification-quality.md). Mulai dari verification.md untuk reviewer dan implementation-guide.md untuk implementer. Dependency, cara menjalankan dan batas pembuktiannya mengikuti [implementasi C01](contracts-implementation.md).

Ringkasan milestone: [verification-report-c01.md](verification-report-c01.md),
[verification-report-d01-inventory.md](verification-report-d01-inventory.md),
[verification-report-e01.md](verification-report-e01.md),
[verification-report-i01-pdf-parser.md](verification-report-i01-pdf-parser.md),
[verification-report-i01-text-normalization.md](verification-report-i01-text-normalization.md),
[verification-report-m01-pdf-profile.md](verification-report-m01-pdf-profile.md), dan
[verification-report-s01.md](verification-report-s01.md). Laporan S01 mengikat implementasi
storage/publication ke PostgreSQL aktual dan audit agent independen.
