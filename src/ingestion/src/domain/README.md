# src/ingestion/src/domain

Definisi data dan invariant lintas komponen: dokumen, versi pasal, chunk, canonical entity, relasi, bukti, dan jawaban. Domain menjadi bahasa bersama kedua alur ingestion dan tanya jawab. Representasi lokal Rust mengacu pada src/contracts lintas runtime; definisi wire tidak digandakan. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Tidak mengandung client database, prompt model, routing HTTP, atau orchestration. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../AGENTS.md).

## Peran dan integrasi anak

Semua anak memakai ID stabil, schema version, dan referensi sumber yang eksplisit. Kontrak tidak mengimpor SDK eksternal; perubahan kontrak harus ditinjau terhadap seluruh konsumennya.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Berkas: [chunks.rs](chunks.rs), [documents.rs](documents.rs), [document_wire.rs](document_wire.rs), [entities.rs](entities.rs), [evidence.rs](evidence.rs), [mod.rs](mod.rs), [relations.rs](relations.rs), dan [wire.rs](wire.rs).

## Benchmark dan perhatian performa

**DOMAIN.** Gate deterministik: ID dan referensi sumber/versi tidak ambigu; invalid record ditolak atau dikarantina secara eksplisit. Kompatibilitas schema diperiksa dengan fixtures; belum ada hasil pengukuran.

Ikuti [kebijakan benchmark](../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

`chunks.rs` menyediakan record lokal dan validator provenance/source mapping/token count untuk hasil chunking I01. `document_wire.rs` memproyeksikan structure tree dan chunk batch tervalidasi ke pesan C01 serta mempertahankan source, corpus, provision-version, producer manifest, dan offset normalisasi. Wire validator C01 juga aktif; konstruksi `TextArtifact`/`DocumentBatch`, document versioning, graph/index pipeline, dan worker batch belum diimplementasikan. Fixture unit tidak membuktikan target kualitas atau latency produksi.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [chunks.rs](chunks.rs) | Pertahankan invariant record lokal ketika tokenizer atau kebijakan overlap berkembang; hindari menambahkan schema wire paralel. | Uji boundary Unicode, overlap, overflow, dan alokasi batch besar terhadap konfigurasi produksi. |
| [document_wire.rs](document_wire.rs) | Lengkapi proyeksi `TextArtifact`, page result, dan `DocumentBatch` setelah kontrak versioning menyediakan provision/version yang tervalidasi. | Uji golden wire lintas bahasa, missing refs, hash mismatch, batas jumlah record, dan serialisasi batch besar. |
| [documents.rs](documents.rs) | Construct validated document/source/provision views over generated types; keep observation time separate from legal dates. | Test stable IDs, raw/normalized mappings and historical version ambiguity; avoid redundant conversion/allocation across batches. |
| [entities.rs](entities.rs) | Expose scoped canonical identity/revision and resolution decision helpers without autonomous registry writes. | Test alias ambiguity, merge/split lineage and deterministic identity comparison; avoid redundant conversion/allocation across batches. |
| [evidence.rs](evidence.rs) | Expose snapshot-bound evidence/path operations while retaining primary provenance. | Test corpus/version/snapshot mismatches and missing support hydration; avoid redundant conversion/allocation across batches. |
| [relations.rs](relations.rs) | Expose assertion/support and qualifier invariants without collapsing shared evidence. | Test endpoint types, source withdrawal and negation/condition preservation; avoid redundant conversion/allocation across batches. |

## Penambahan C01 dan panduan verifikasi

Berkas terkait: [wire.rs](wire.rs). Dependency, cara menjalankan dan batas pembuktiannya mengikuti [implementasi C01](../../../../doc/contracts-implementation.md).
