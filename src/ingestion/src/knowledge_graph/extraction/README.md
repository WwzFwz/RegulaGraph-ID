# src/ingestion/src/knowledge_graph/extraction

Ekstraksi penyebutan entitas dan relasi dari konteks dokumen dengan keluaran terstruktur dan bukti sumber. Transformasi batch berjalan dalam Rust; inference semantik menggunakan adapter model. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Tidak menetapkan penggabungan canonical lintas dokumen ataupun menulis langsung ke Neo4j. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../../AGENTS.md).

## Peran dan integrasi anak

Anak memakai schema yang konsisten, mencatat model dan prompt version, serta membawa rentang sumber untuk entitas dan relasi. Pisahkan hubungan eksplisit dari interpretasi dan pertahankan kondisi, negasi, serta pengecualian.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Berkas: [extractor.rs](extractor.rs), [mod.rs](mod.rs), [prompts.rs](prompts.rs).

## Benchmark dan perhatian performa

**EXTRACTION.** Ukur precision/recall/F1 entitas dan relasi dengan predicate, arah, kondisi, serta bukti sumber; catat kegagalan schema, token, biaya/dokumen, dan waktu p50/p95. Target wajib mengikuti [target numerik wajib](../../../../../configs/benchmark-targets.yaml) dan memerlukan gold set valid; schema valid tidak dianggap fakta benar.

Ikuti [kebijakan benchmark](../../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Assembler/validator `ExtractionBatch`, provenance source/version/span, model/prompt identity, dukungan assertion, partial accounting, persistence immutable, executor worker, Semantic Gateway deterministic, serta coordinator durable EXTRACT sudah aktif. Coordinator memeriksa config/model/prompt, source closure, exact mention bytes, UTF-8 evidence boundaries, dan recovery tanpa sampling ulang. Ontology enforcement, provider/model produksi, gold dataset, dan acceptance kualitas belum aktif.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [extractor.rs](extractor.rs) | Pertahankan validator aktif saat menambah ontology constraints dan handoff coordinator; provenance/ID tetap tidak dipercaya dari model. | Uji negation/conditions/exceptions dan unknown ontology terms, lalu ukur precision/recall pada gold set serta biaya/throughput. |
| [prompts.rs](prompts.rs) | Version templates and structured-output instructions against the ontology; include primary spans and treat document content as data. | Test schema-invalid output and injected source instructions; record prompt hash and compare extraction quality on frozen dev splits. |
