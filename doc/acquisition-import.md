# Impor acquisition ke ingestion produksi

Dokumen ini menjelaskan verifikasi/copy PDF collector ke shared artifact store,
registration PostgreSQL dan submit job PARSE. Hasil enqueue bukan publication
atau bukti kualitas parsing/model.

## Pemakaian

Siapkan database dengan migrasi proyek dan konfigurasi `submit`: DSN PostgreSQL,
ontology path/hash, candidate policy path/hash, serta optional producer RESOLVE
path/hash. Set `REGULAGRAPH_ARTIFACTS_DIR` ke root yang sama dengan
`REGULAGRAPH_WORKER_ARTIFACT_ROOT` milik worker Rust.

Request ProtoJSON memuat corpusId, operation `JOB_OPERATION_INGEST`, idempotencyKey
dan configManifest aktual yang cocok dengan pipeline. Kosongkan sources dan
observations; command mengisinya dari record. Jangan membuat hash producer asal
untuk melewati admission.

```powershell
go run ./src/server/cmd/cli submit `
  -request data/ingestion-request.json `
  -job-id job:regulation-example `
  -acquisition-record data/acquisition/records/RECORD_HASH.json `
  -acquisition-root data/acquisition
```

Pakai record individual dalam `records/`, bukan inventory.json atau handoff.json.
Record harus schema v1, complete, tanpa error, dan mempunyai receipt sukses untuk
seluruh PDF utama yang ditemukan. Semua receipt diimpor termasuk lampiran;
command tidak melakukan download atau inference.

## Kontrak dan resource

Loader membatasi request/record 16 MiB dan menolak unknown fields/trailing JSON.
Workflow membatasi 64 receipt dan 256 MiB PDF unik per record. Sebelum cloning,
template dibatasi 8 MiB dan estimasi ekspansi metadata 8 MiB; field receipt
dibatasi 8 KiB. Ini batas admission operasi, bukan penurunan benchmark release.

Source reader terkurung pada root, menolak symlink/traversal, memeriksa ukuran
dan SHA-256, lalu signature PDF mengikuti header collector. Destination menyimpan
immutable bytes dengan hash verification dan atomic no-replace. Registration
baru dimulai setelah seluruh salinan berhasil; scheduler kemudian menyimpan
sources/observations dan policy pins dalam satu request durable.

Artifact ID dan storage key diturunkan dari corpus ID dan hash PDF karena registry
memiliki ownership corpus serta uniqueness global. Corpus berbeda memakai alamat
tujuan berbeda; source-blob identity/checksum tetap content-addressed. Mirror
byte-identik dalam corpus yang sama berbagi artefak dan mempertahankan observations
terpisah. Metadata portal tetap assertion bersumber, bukan identitas canonical
atau keputusan tanggal berlaku.

## Replay, kegagalan dan tahap berikutnya

Output sama dengan `submit`: job ID, corpus, state, stage PARSE dan reused.
Jika PDF hilang/corrupt, tidak ada job baru. Salinan yang sudah berhasil boleh
tertinggal tanpa registration. Jika registration/enqueue gagal, metadata yang
sudah committed boleh tertinggal; input identik dapat diulang tanpa menghapus
artefak. Perubahan request dengan idempotency key lama ditolak sebagai conflict.
Replay memverifikasi PDF asal lagi walaupun destination tersedia.

Root/credential dimiliki operator lokal tepercaya; command bukan endpoint upload
publik. Worker melanjutkan PARSE, STRUCTURE, BIND, CHUNK dan EXTRACT dengan producer
terpin. Resolusi, indexing dan publication tetap mengikuti kontraknya sendiri.
Ukur throughput copy/hash, cancellation dan latency antrean mengikuti
configs/benchmark-targets.yaml; required targets tetap REQUIRED_UNMEASURED.
Verifikasi hash/header tidak membuktikan signature digital atau kebenaran hukum.
