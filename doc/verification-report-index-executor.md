# Verifikasi executor INDEX durable

Dokumen ini mencatat integrasi processor INDEX, lifecycle workflow dan daemon
opt-in pada 2026-10-09 di atas revision `8e6a635`. Fingerprint, perintah dan log
berada di `artifacts/verification/20261009-index-executor/`. Scope runtime adalah
Go; schema wire C01 dan target benchmark tidak berubah.

`TestInitialIndexProcessorAgainstPostgres` memakai PostgreSQL16.8 disposable,
inventory dan sumber fixture terverifikasi, serta worker/vector sintetis. Jalur
yang diuji adalah claim, preflight authority, restore seluruh plan, request C01,
admission output, registration, checkpoint/STAGED atomik dan pelepasan lease.
Error disuntikkan setelah commit database sukses; processor mengonfirmasi exact
checkpoint dan STAGED lalu mengembalikan hasil tanpa inference kedua. Checkpoint
dengan fence berbeda tidak boleh menjadi bukti sukses; job selesai tidak diklaim
lagi dan claim lama ditolak sebelum pemanggilan worker.

`TestIndexExecutorLifecycle` menguji dengan fake terkontrol: sukses, I/O sementara,
authority yang membutuhkan replan, cancellation, deadline, response kosong,
balapan poll setelah commit, antrean kosong, dan batas backoff. Ini pembuktian
control flow, bukan database atau model. `TestIndexEnablement` memeriksa flag
daemon disabled, invalid, dan dependency wajib ketika enabled. Test scope
inventory juga menolak nilai yang tidak bisa dibawa sebagai ascii_id C01.

Perintah `go test ./src/server/... -count=1` dengan PostgreSQL PASS (`go-all.log`),
tes terfokus lost acknowledgement PASS (`lost-ack.log`), dan `go vet` terhadap
workflow/indexing/PostgreSQL/daemon PASS (`vet.log`). Perbaikan validasi auth scope
sesudah broad run diuji terfokus PASS (`auth-scope.log`). Toolchain Go1.26.8
Windows amd64; opt-in Qdrant/native tidak dijalankan pada paket ini.

Review independen integrasi paket ini masih menunggu hasil akhir; review output
commit sebelumnya tercatat terpisah pada
[laporan inventory](verification-report-index-jobs.md). Daemon tersambung tetapi
run berikut belum dibuktikan oleh fixture ini: corpus pengguna melalui Rust dan
model sungguhan, otomatisasi CLI persiapan inventory, pengumpulan seluruh output
dan publication. Required performance dan model/legal quality NOT_MEASURED;
seluruh Hybrid GraphRAG belum dinyatakan selesai.
