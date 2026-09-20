# src/server/internal/domain

Definisi data dan invariant lintas komponen: dokumen, versi pasal, chunk, canonical entity, relasi, bukti, dan jawaban. Domain menjadi bahasa bersama kedua alur ingestion dan tanya jawab. Implementasi runtime berada di Go. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Tidak mengandung client database, prompt model, routing HTTP, atau orchestration. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../AGENTS.md).

## Peran dan integrasi anak

Semua anak memakai ID stabil, schema version, dan referensi sumber yang eksplisit. Kontrak tidak mengimpor SDK eksternal; perubahan kontrak harus ditinjau terhadap seluruh konsumennya. Sumber wire schema adalah src/contracts/proto; representasi Go tidak membuat schema paralel. Offset wire mengikuti byte UTF-8 end-exclusive.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Berkas: [answers.go](answers.go), [chunks.go](chunks.go), [documents.go](documents.go), [document_validation.go](document_validation.go), [entities.go](entities.go), [evidence.go](evidence.go), [relations.go](relations.go), [registry.go](registry.go), serta [operations.go](operations.go) untuk boundary job/publication S01. `document_validation.go` memeriksa closure referensi dan span pada artifact worker sebelum registrasi. [registry_test.go](registry_test.go) memverifikasi exact-key planning, sedangkan [documents_test.go](documents_test.go) memverifikasi binding regulation/provision, provenance, completeness, dan structural closure. Validator wire dan boundary lintas record dijelaskan pada bagian C01 di bawah.

## Benchmark dan perhatian performa

**DOMAIN.** Gate deterministik: ID dan referensi sumber/versi tidak ambigu; invalid record ditolak atau dikarantina secara eksplisit. Kompatibilitas schema diperiksa dengan fixtures; belum ada hasil pengukuran.

Ikuti [kebijakan benchmark](../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Kontrak/validator C01, tipe boundary job/publication S01, planner exact regulation identity K01, serta transform assignment registry menjadi `Regulation`, `DocumentEdition`, `Provision`, dan `ProvisionVersion` sudah aktif; lihat [cakupan implementasi C01](../../../../doc/contracts-implementation.md). Binding memeriksa ulang observation dan structural closure, mempertahankan legal date/status sebagai unknown, serta menandai source tanpa structured text sebagai partial. Boundary artifact worker kini menambah validasi semantic closure, struktur acyclic, dan containment span dengan indeks berbatas sebelum registrasi. Workflow BIND dan CHUNK registry-bound sudah tersambung; helper domain graph/retrieval/answer lain masih mengikuti paket pemiliknya. Build dan fixture tidak membuktikan target kualitas atau latency.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [answers.go](answers.go) | Construct claim/citation/stream state helpers over authoritative wire types; preserve semantic vs transport completion. | Test byte-accurate claim spans, unknown evidence refs and exactly one terminal stream event. |
| [chunks.go](chunks.go) | Construct chunk and parent views retaining provision version, source spans and tokenizer identity. | Test missing parents, split Unicode and overlong units without source loss; avoid redundant conversion/allocation across batches. |
| [documents.go](documents.go) | Pertahankan planner/materializer registry-bound dan hubungkan melalui workflow durable tanpa melemahkan provenance, structural coverage, atau uncertainty temporal. | Uji persistence/replay, batch parsial, cross-language semantic closure, workload besar, p95/p99, dan peak RSS. |
| [entities.go](entities.go) | Expose scoped canonical identity/revision and resolution decision helpers without autonomous registry writes. | Test alias ambiguity, merge/split lineage and deterministic identity comparison; avoid redundant conversion/allocation across batches. |
| [registry.go](registry.go) | Pertahankan exact-key planning dan handoff issuer→regulation fail-closed; berikutnya bangun keputusan merge/split reversible di atas registry revision. | Uji false merge/split, stale assignment, duplicate/foreign provenance, concurrent replay, serta throughput batch; semantic merge tetap membutuhkan gold set. |
| [evidence.go](evidence.go) | Expose snapshot-bound evidence/path operations while retaining primary provenance. | Test corpus/version/snapshot mismatches and missing support hydration; avoid redundant conversion/allocation across batches. |
| [relations.go](relations.go) | Expose assertion/support and qualifier invariants without collapsing shared evidence. | Test endpoint types, source withdrawal and negation/condition preservation; avoid redundant conversion/allocation across batches. |
| [operations.go](operations.go) | Pertahankan tipe domain job/publication tanpa dependency SDK storage. | Compile-time interface checks dan integration test adapter/workflow saat field berkembang. |

## Penambahan C01 dan panduan verifikasi

Berkas terkait: [wire.go](wire.go), [wire_test.go](wire_test.go), [boundaries.go](boundaries.go), [boundaries_test.go](boundaries_test.go). Dependency, cara menjalankan dan batas pembuktiannya mengikuti [implementasi C01](../../../../doc/contracts-implementation.md).
