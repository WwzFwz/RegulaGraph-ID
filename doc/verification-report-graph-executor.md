# Verifikasi processor dan executor ASSEMBLE

Dokumen ini mencatat bukti restore inventory, recovery checkpoint, shared batch
lifecycle dan wiring daemon. Scope tidak mencakup Neo4j atau completion K01.

## Revision dan lingkungan

Tanggal 2026-10-09; baseline `baefbf438a266b7f4880532998491aa7b639f672`.
Implementasi: `7eddec1` shared lifecycle, `82053eb` processor/recovery/scoped claim,
`e5da8f9` daemon enablement. Go 1.26.8 windows/amd64; PostgreSQL 16.8-alpine di
127.0.0.1:55448 dan Qdrant 1.18.0 di 127.0.0.1:56348 memakai schema/collection fixture
terisolasi. Raw logs: `artifacts/verification/20261009-graph-executor/`.

Source binding backend menggunakan EXTRACT/RESOLVE kosong sintetis dan ontology hash
JSONC repository asli. Workflow warm/recovery memakai actual Rust-produced rich
fixture dari `artifacts/verification/20261009-graph-output/rich-fixture`; metadata,
storage commit dan RPC ports pada test tersebut sintetis. Kedua run tidak menjadi
bukti gabungan actual RPC plus PostgreSQL.

## Input, proses, output yang diperiksa

Locator menuntut live job/corpus/owner/attempt/fence/expiry. Claim daemon memfilter
scope melalui immutable base source snapshot sebelum mengambil job. Processor
mem-pin snapshot, membaca receipt/input bytes dengan batas, memanggil factory
admission, memakai cache hanya setelah live authorization dan mengulang admission
ketika stamp registry berubah. Input evidence cache tidak disimpan setelah restore.

Recovery menuntut latest checkpoint exact, sukses ASSEMBLE dengan producer/corpus/job
benar dan fence lebih kecil. Output registry/hash/projection dibaca ulang tanpa RPC;
checkpoint baru memakai claim baru, artefak output lama tetap immutable. Error
missing/schema/hash/projection permanen harus FAILED, sedangkan transport timeout
atau backend unavailable tetap retry. Pin dilepas pada success/error; failed cleanup
terlog dan lease tetap mempunyai expiry. Cancellation, deadline, backoff dan late
poll sesudah commit dibagi dengan executor INDEX.

## Hasil yang dijalankan

| Pemeriksaan | Expected dan actual | Bukti |
| --- | --- | --- |
| `go test ./src/server/internal/workflows ./src/server/cmd/ingestion-worker -count=1`, env rich fixture | PASS exit 0: actual output revalidation, warm processor, checkpoint recovery tanpa worker RPC, corrupt output rejection, cancelled cache wait, lifecycle/cancellation/retry, opt-in startup | `recovery-fix.log` |
| `go test ./src/server/internal/indexing -run TestPublishedGraphSourceMembershipAgainstStores -count=1 -v`, PostgreSQL/Qdrant env | PASS exit 0: cold restore reaches worker sentinel, cache reuse, registry stamp refresh, pin count restored, wrong attempt rejected, foreign scope unclaimed, missing checkpoint-bound output becomes FAILED and cannot be reclaimed | `restore-recovery-final.log` |
| `go test ./src/server/...`, env rich fixture tanpa backend env | PASS exit 0: seluruh Go suite, backend-only tests terpisah/skip | `go-all-final.log` |
| `go vet ./src/server/...` | PASS exit 0 | `go-vet-final.log` |
| Independent backend + lifecycle/daemon tests | PASS exit 0 | `independent-final.log` |
| Independent missing/corruption versus availability error classification | PASS exit 0 | `independent-errors.log` |

## Review dan temuan

Reviewer `/root/verify_index_jobs` memberi **scoped PASS** setelah dua temuan ditutup.
Pertama, error corrupt/missing recovery awalnya plain error yang dapat menjadi
RETRY_WAIT tanpa batas karena terminal checkpoint mempertahankan recovery budget.
Perbaikan mengklasifikasikan kehilangan immutable serta kegagalan schema/hash/projection
sebagai ErrPersistentIntegrity. Regression PostgreSQL menjalankan executor nyata
hingga FAILED dan membuktikan tidak ada next claim. Timeout/cancel/unavailable tidak
disamakan dengan corruption. Kedua, daemon berscope tidak boleh mengklaim inventory
scope lain lalu menggagalkannya; scoped claim filter dan test foreign scope ditambahkan.

Run awal `restore.log` gagal karena fixture lama memakai ontology hash rekaan;
fixture diperbaiki menggunakan hash ontology aktual dan `restore-ontology.log` lulus.
Tidak ada validator yang dilonggarkan. `independent-lifecycle.log` menangkap syntax
error saat file masih dalam proses perubahan; log retained, bukan hasil final.
Seluruh log awal dipertahankan agar riwayat temuan tidak hilang.

## Batas yang masih terbuka

Wiring daemon tersedia tetapi belum dibuktikan dengan run gabungan lintas executable
Rust dan PostgreSQL dari source nonempty hingga commit. CLI operator preparation/
scheduling graph, registry reaffirmation lintas revision, canonical CREATE/MERGE/SPLIT,
Neo4j publication/readiness dan graph retrieval belum selesai. Checkpoint recovery
corrupt diuji terhadap backend; recovery sukses dari actual RPC+PG masih diperlukan.
Source ingestion/model gold serta latency/throughput produksi tidak dibuktikan oleh
fixture. Gate required tetap NOT_MEASURED dan configs/benchmark-targets.yaml tidak
berubah. Deployment tetap di luar scope yang diminta pengguna.
