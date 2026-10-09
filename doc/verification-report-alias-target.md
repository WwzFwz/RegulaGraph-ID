# Verifikasi alias untuk target provisional existing

Laporan ini mencatat boundary review dua occurrence pada 2026-10-09, baseline
`3a0c720`. Jalur baru menambah alias ke canonical provisional melalui receipt
pembentukan asli tanpa membuat identity atau mengganti profil. Ini bukti integrasi
registry/kandidat, bukan penilaian kesetaraan hukum atau full GraphRAG acceptance.

## Hasil dan lingkungan

Go 1.26.8 Windows/amd64 dan PostgreSQL lokal; integration test menggunakan schema
`review_<timestamp>` yang dihapus setelah test. Corpus operasional tidak dimigrasikan.
Source sintetis memiliki dua occurrence yang direview menuju satu ID. BIND,
FileStore, catalog checkpoint, registry dan candidate hydration memakai kode
produksi. Tes ini belum merupakan pembuktian lintas dokumen/corpus nyata.

| Pemeriksaan | Expected dan actual | Status |
| --- | --- | --- |
| Review dua sisi | Mention baru dan occurrence pembentukan target tampil terpisah, dengan konteks sumber | PASS |
| Profile/identity | Alias kedua menunjuk ID yang sama; label, profile revision dan UNREVIEWED tidak diganti | PASS |
| Original approval | Hash plan asli direkonstruksi memakai original policy fingerprint, revision, artefak dan profil | PASS |
| Corruption | Receipt hilang, operation payload berubah, plan hash atau policy hash berubah ditolak | PASS |
| Domain proof | Missing/forged profile, extra identity key, changed label/type, future revision, wrong artifact/occurrence, incompatible mode ditolak | PASS |
| Atomicity/replay | Late review-insert failure tidak menyisakan alias; replay setelah restart tidak membuat identity baru | PASS |
| Kandidat | Dua alias menjadi satu kandidat dengan dua support context yang bisa dihidrasi | PASS |
| CLI | Explicit target mode; canonical wajib; create/document flags dan missing approval ditolak | PASS |
| Existing paths | Provisional creation dan BIND existing-target regressions tetap lulus | PASS |
| Package suites / vet | Empat package terdampak exit 0 | PASS |
| Full durable LINK sampai Rust ASSEMBLE | Belum dijalankan sebagai satu rantai dari fixture ini | NOT_MEASURED |
| Model/gold/required performance | Tidak dijalankan; target tidak berubah | NOT_MEASURED |

Sembilan skenario target-provisional PostgreSQL, enam skenario creation, dan
delapan skenario BIND existing-target lulus. Raw log:
`artifacts/verification/20261009-alias-target/integration-final.log`.
Perintah utama dengan DSN hanya untuk schema test terisolasi:

```text
go test ./src/server/internal/workflows -run 'Test(ExistingProvisionalAlias|ProvisionalAlias|SourcedAlias)' -count=1 -v
```

Regresi umum tanpa DSN (DB opt-in SKIP pada run ini, bukan pengganti run di atas):

```text
go test ./src/server/internal/domain ./src/server/internal/adapters/postgres ./src/server/internal/workflows ./src/server/cmd/cli -count=1
go vet ./src/server/internal/domain ./src/server/internal/adapters/postgres ./src/server/internal/workflows ./src/server/cmd/cli
```

Keduanya exit 0, log `packages.log` dan `vet.log`. Fingerprint file/perintah disimpan
di `manifest.json` pada direktori artefak yang sama. Migration 0027 diuji lewat
runner migrasi pada schema terisolasi, tanpa menulis ulang migration lama.

## Review dan tindak lanjut

Agent `verify_index_abort` melakukan review read-only dan menjalankan unit/CLI
sendiri, exit 0. Reviewer menemukan bahwa implementasi awal hanya mengautentikasi
payload lineage, belum original plan/policy hash. Perbaikan menambahkan kedua
pin tersebut ke proof, merekonstruksi original hash, dan memeriksa ulang request
ingest asal serta receipt dalam transaksi. Regresi korupsi kedua kolom telah
ditambahkan dan dijalankan terhadap PostgreSQL. Review akhir **PASS terbatas untuk
existing-provisional alias**, tanpa blocker scope ini.

DB di sesi reviewer SKIP karena DSN tidak tersedia; reviewer memeriksa kode dan
log DB implementer. Klaim PASS DB berasal dari run implementer yang dicatat di atas.
Budget file bersama 64 MiB; konteks source/target masing-masing 4 MiB, maksimal
8 MiB tampilan. Ini batas resource, bukan hasil benchmark latency.

Semantic CREATE model, merge/split, corpus nyata dan durable LINK sampai ASSEMBLE
masih perlu dilanjutkan. [Panduan](provisional-entity-review.md) menjelaskan cara
inspect/accept serta replan kandidat; [kondisi model](extraction-trial-limitations.md)
membedakan gap integrasi dari keterbatasan percobaan Qwen lokal.
