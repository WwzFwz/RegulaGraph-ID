# src/server/internal/domain

Definisi data dan invariant lintas komponen: dokumen, versi pasal, chunk, canonical entity, relasi, bukti, dan jawaban. Domain menjadi bahasa bersama kedua alur ingestion dan tanya jawab. Implementasi runtime berada di Go. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Tidak mengandung client database, prompt model, routing HTTP, atau orchestration. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../AGENTS.md).

## Peran dan integrasi anak

Semua anak memakai ID stabil, schema version, dan referensi sumber yang eksplisit. Kontrak tidak mengimpor SDK eksternal; perubahan kontrak harus ditinjau terhadap seluruh konsumennya. Sumber wire schema adalah src/contracts/proto; representasi Go tidak membuat schema paralel. Offset wire mengikuti byte UTF-8 end-exclusive.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Berkas: [answers.go](answers.go), [chunks.go](chunks.go), [documents.go](documents.go), [entities.go](entities.go), [evidence.go](evidence.go), [relations.go](relations.go).

## Benchmark dan perhatian performa

**DOMAIN.** Gate deterministik: ID dan referensi sumber/versi tidak ambigu; invalid record ditolak atau dikarantina secara eksplisit. Kompatibilitas schema diperiksa dengan fixtures; belum ada hasil pengukuran.

Ikuti [kebijakan benchmark](../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Status lintas repositori: collector PDF dan kontrak/validator C01 sudah tersedia; lihat [cakupan implementasi C01](../../../../doc/contracts-implementation.md). Pipeline parsing/graph/retrieval, adapter storage, layanan model dan evaluator benchmark masih belum aktif. Status anak dijelaskan pada header masing-masing; build dan fixture tidak membuktikan target kualitas atau latency.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [answers.go](answers.go) | Construct claim/citation/stream state helpers over authoritative wire types; preserve semantic vs transport completion. | Test byte-accurate claim spans, unknown evidence refs and exactly one terminal stream event. |
| [chunks.go](chunks.go) | Construct chunk and parent views retaining provision version, source spans and tokenizer identity. | Test missing parents, split Unicode and overlong units without source loss; avoid redundant conversion/allocation across batches. |
| [documents.go](documents.go) | Construct validated document/source/provision views over generated types; keep observation time separate from legal dates. | Test stable IDs, raw/normalized mappings and historical version ambiguity; avoid redundant conversion/allocation across batches. |
| [entities.go](entities.go) | Expose scoped canonical identity/revision and resolution decision helpers without autonomous registry writes. | Test alias ambiguity, merge/split lineage and deterministic identity comparison; avoid redundant conversion/allocation across batches. |
| [evidence.go](evidence.go) | Expose snapshot-bound evidence/path operations while retaining primary provenance. | Test corpus/version/snapshot mismatches and missing support hydration; avoid redundant conversion/allocation across batches. |
| [relations.go](relations.go) | Expose assertion/support and qualifier invariants without collapsing shared evidence. | Test endpoint types, source withdrawal and negation/condition preservation; avoid redundant conversion/allocation across batches. |

## Penambahan C01 dan panduan verifikasi

Berkas terkait: [wire.go](wire.go), [wire_test.go](wire_test.go), [boundaries.go](boundaries.go), [boundaries_test.go](boundaries_test.go). Dependency, cara menjalankan dan batas pembuktiannya mengikuti [implementasi C01](../../../../doc/contracts-implementation.md).
