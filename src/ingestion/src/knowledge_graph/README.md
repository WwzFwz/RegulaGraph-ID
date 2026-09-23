# src/ingestion/src/knowledge_graph

Pembangunan knowledge graph melalui extraction, resolution, assembly, summarization, dan validation. Graph menyimpan entitas serta hubungan yang dapat ditelusuri ke sumber. Transformasi batch berjalan dalam Rust; inference semantik menggunakan adapter model. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Traversal untuk pertanyaan pengguna berada di retrieval/graph; koneksi Neo4j berada di infrastructure. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../AGENTS.md).

## Peran dan integrasi anak

Anak meneruskan mention ID, canonical ID, jenis relasi, sumber teks, versi, dan status validasi. Ringkasan atau hasil interpretasi tidak menggantikan bukti primer; ekstraksi terstruktur belum menjamin kebenaran semantik.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Subfolder: [assembly/](assembly/README.md), [extraction/](extraction/README.md), [resolution/](resolution/README.md), [summarization/](summarization/README.md), [validation/](validation/README.md).

Berkas: [mod.rs](mod.rs), [schema.rs](schema.rs).

## Benchmark dan perhatian performa

**EXTRACTION.** Ukur precision/recall/F1 entitas dan relasi dengan predicate, arah, kondisi, serta bukti sumber; catat kegagalan schema, token, biaya/dokumen, dan waktu p50/p95. Target wajib mengikuti [target numerik wajib](../../../../configs/benchmark-targets.yaml) dan memerlukan gold set valid; schema valid tidak dianggap fakta benar.

**RESOLUTION.** Ukur pairwise precision/recall/F1, false merge, false split, mention yang hilang, waktu per batch, dan biaya. Gate: semua mention tetap terlacak; pasal dari peraturan berbeda tidak digabung hanya karena nama sama. Blocking harus diukur juga terhadap pasangan benar yang terlewat.

**GRAPH.** Ukur validitas endpoint dan provenance, ketepatan predicate/arah, kelengkapan jalur bukti, waktu assembly/traversal, dan penggunaan memori. Gate: graph yang dipublikasikan tidak memiliki endpoint/bukti wajib yang hilang. Connectivity adalah diagnosis, bukan target memaksa satu komponen.

Ikuti [kebijakan benchmark](../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

EXTRACT memiliki validator, artefak immutable terikat `DocumentBatch`, executor worker, Semantic Gateway deterministic, coordinator durable, dan ontology JSONC bersama yang memvalidasi tipe/predicate/qualifier sebelum persistence. Resolution memiliki blocking kandidat, proposal LINK/DEFER dari pilihan eksplisit, dan helper alias LINK sourced sebagai library; stage RESOLVE, receipt registry terintegrasi, merge/split, assembly, summarization, serta publication graph belum aktif. Provider/model produksi dan kualitas semantik/latency belum dibuktikan.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [schema.rs](schema.rs) | Pertahankan compiler ontology bersama dan integrasikan aturan temporal/evidence tambahan saat gold set menuntutnya; jangan buat wire schema paralel. | Uji drift versi/hash, endpoint, explicit/inferred, unknown predicate, serta kualitas semantic pada gold split. |
