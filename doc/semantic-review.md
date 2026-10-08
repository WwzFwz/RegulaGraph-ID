# Review dan resume proposal resolusi

Dokumen ini menjelaskan boundary operator lokal untuk memeriksa hasil model RESOLVE,
menyetujui seluruh batch LINK/DEFER yang tidak berubah, dan melanjutkan executor durable.
Ini merupakan bagian K01, bukan persetujuan otomatis model atau kelulusan graph lengkap.

## Input, keputusan, dan hasil

Job nonempty yang sudah selesai menghasilkan proposal berada pada `WAITING_REVIEW`.
Queue mengikat checkpoint EXTRACT dan empat artefak: sumber, kandidat registry,
request model yang berisi konteks, dan response model. Workflow membaca byte terverifikasi
dengan budget gabungan, lalu memeriksa producer/model/prompt/schema, corpus, auth scope,
snapshot, mention exact, seluruh kandidat/revisi, serta alias support dan coverage.
Konteks model berasal dari audit ingestion; bukan pencarian ulang pada registry terbaru.

Operator memeriksa konteks dua sisi LINK dan alasan DEFER. `accept` membutuhkan hash
response, expected registry revision, dan alasan eksplisit. Seluruh proposal harus sama
dengan response immutable. Jika sebagian salah, jangan accept batch itu. Revisi proposal,
rejection workflow, dan pembuatan job replan otomatis belum tersedia pada perintah ini.

Dalam satu transaksi PostgreSQL, corpus lalu job dikunci, revision/current checkpoint dan
cancellation diperiksa, LINK reviews ditulis dalam batch, intent output dan audit batch
disimpan, lalu state menjadi `RETRY_WAIT`. Hanya stage attempt direset untuk fase sesudah
review; global attempt/fence tetap monotonik. Registry belum dimutasi pada transaksi ini.
Executor RESOLVE membaca intent yang sama, melakukan CAS registry, menyimpan output
dan checkpoint, lalu berakhir `STAGED` tanpa memanggil model lagi.

Exact retry mengakui review historis, termasuk setelah registry maju, job selesai atau
dibatalkan. Ia tidak menghidupkan kembali job atau mereset retry budget. Actor, reason,
hash atau intent yang berubah ditolak. Jika revision berubah sebelum approval, approval
ditolak; jika berubah sesudahnya, executor mengikuti jalur terminal/replan yang ada.
DEFER tetap unresolved, bukan canonical rekaan. Approval tidak menjadikan graph published.

## Menjalankan secara lokal

Terapkan migration sampai `0017_semantic_review_resume.up.sql` lewat mekanisme migrasi
repositori sebelum memakai CLI. Gunakan PostgreSQL/artifact root, auth scope dan pinned
producer yang sama dengan [daemon resolusi](semantic-resolution.md). Konfigurasi `.env`
tidak dimuat otomatis oleh executable Go.

```powershell
$env:REGULAGRAPH_REVIEW_CORPUS_ID = 'corpus:pilihan-anda'
# Variabel berikut harus sudah berisi konfigurasi operator yang benar:
# REGULAGRAPH_POSTGRES_DSN, REGULAGRAPH_ARTIFACTS_DIR
# REGULAGRAPH_COORDINATOR_AUTH_SCOPE
# REGULAGRAPH_RESOLUTION_PRODUCER_PATH, REGULAGRAPH_RESOLUTION_PRODUCER_SHA256
go run ./src/server/cmd/cli review-resolution -job 'job:pilihan-anda' > proposal-review.json
```

Output inspeksi memuat `model_input`, `model_response`, `output_sha256`, dan
`expected_revision`. Baca keseluruhan konteks dan keputusan sebelum menjalankan:

```powershell
go run ./src/server/cmd/cli review-resolution -action accept `
  -job 'job:pilihan-anda' -output-sha256 '<hash-hasil-inspeksi>' `
  -expected-revision 3 -reason 'Alasan hasil pemeriksaan manusia'
```

Angka 3 hanya contoh; gunakan revision dari inspeksi. `accepted_for_execution` berarti
review baru disimpan dan job dapat diklaim daemon. `already_accepted` berarti exact replay
approval historis; keduanya tidak mengklaim registry commit telah selesai. Kesalahan
menulis stdout setelah commit ditangani dengan mengulang invocation exact untuk rekonsiliasi.
Jalankan daemon RESOLVE dengan konfigurasi yang sama untuk melanjutkan execution.

Identitas audit diturunkan dari akun OS asli (UID/SID, dipisahkan per host), bukan `USERNAME`,
argumen actor, isi model atau body request. Kewenangan berasal dari akses operator lokal
ke konfigurasi dan kredensial database. Ini belum RBAC multi-user, approval HTTP, atau
jaminan melawan administrator yang dapat mengubah database. Jaga kredensial tersebut.

CLI memiliki timeout default 30 detik, maksimum 5 menit, budget artefak gabungan 64 MiB,
100000 referensi dan 128 kandidat per mention; ukuran setiap payload tetap mengikuti
validator wire. Oversize gagal, bukan dipotong. Simpan hasil inspeksi sebagai data sumber
terkontrol karena memuat kutipan dokumen. CLI tidak menjalankan service sehingga tidak
ada proses server baru yang perlu dimatikan; Ctrl+C membatalkan invocation aktif.

## Peta implementasi dan verifikasi

`internal/workflows/semantic_review.go` memiliki admission/preview; `internal/domain/semantic_review.go`
memiliki nilai internal tanpa schema wire baru. `internal/adapters/postgres/semantic_review.go`
memiliki transaction/replay. `cmd/cli/semantic_review.go` merakit principal, config dan workflow.
Executor/intent/checkpoint existing tetap menjadi pemilik commit registry dan recovery.

[Laporan verifikasi](verification-report-semantic-review.md) membedakan integrasi DEFER
sampai STAGED dari LINK storage dan tes writer yang terpisah. Ukur hash/validation,
lock wait, queue/commit p95/p99, konflik/retry, RSS dan kualitas resolution terhadap
[target required](../configs/benchmark-targets.yaml). Belum ada acceptance benchmark
atau legal-quality gold; angka tidak berubah. Canonical CREATE/MERGE/SPLIT, snapshot
registry view, ASSEMBLE dan Neo4j masih pekerjaan K01 berikutnya.
