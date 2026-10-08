# Verifikasi authority awal ASSEMBLE

Laporan ini mencatat pengujian adapter PostgreSQL pada revision `df4df09`, tanggal
2026-10-09. Raw logs berada di `artifacts/verification/20261009-assembly-coordinator`.
Cakupan ialah reader receipt historis dan gate source/publication, bukan workflow
dispatch lengkap atau transaksi output/publication graph.

Windows amd64 dan Go 1.26.8; PostgreSQL 16.8 disposable port 55448 dan Qdrant 1.18.0
port 56348 dipakai oleh suite regresi. Tes authority memakai receipt/proposal sintetis,
source artefak nyata, serta reservation/publication PostgreSQL. Tidak ada approval
pengguna untuk fakta corpus yang dibuat otomatis oleh tes ini.

| Perintah / pemeriksaan | Expected dan actual | Log / exit |
| --- | --- | --- |
| `go test ./internal/adapters/postgres -run '^TestSemanticRegistryAgainstPostgres$' -count=1 -v` | Receipt committed terbaca tanpa mutasi operasi; missing/changed/corrupt ditolak | `receipt.log`, 0 |
| Focused `TestSemanticRegistryAgainstPostgres` dan `TestGraphAssemblyPublicationAgainstPostgres` | Authority valid diterima; mismatch parent/hash/generation/sequence/revision/fence dan aborted ditolak | `authority-fixed.log`, 0 |
| Reviewer menjalankan ulang kedua tes setelah final hardening | PASS independen termasuk empty LINK, superseded checkpoint fence dan cancellation | `independent-authority-final.log`, 0 |
| `go test ./... -count=1` dari src/server dengan DSN/URL disposable | Semua package PASS, backend integration aktif | `go-all.log`, 0 |
| `go vet ./...` | Tidak ada temuan | `go-vet.log`, 0 |

Historical read memverifikasi request hash, candidate/source registration dan bytes,
review, setiap decision payload serta deterministic receipt. Hasil tetap sama setelah
registry mengalami write berikutnya. Operasi yang belum di-commit tidak dibuat oleh
pembaca; jumlah row tetap nol. Approval berubah dan decision hash rusak ditolak.
Reader memakai transaksi read-only; helper review memakai query tanpa row lock,
sedangkan write path mempertahankan `FOR SHARE`.

Source gate memeriksa latest RESOLVE checkpoint sukses pada job STAGED/SUCCEEDED,
registered output hash, corpus, schema dan kesamaan checkpoint fence dengan job fence.
Regression memeriksa checkpoint ID/hash salah, fence superseded, dan cancellation.
Publication gate memeriksa published active base dan open target dari satu query;
ia tidak menjanjikan authority bertahan setelah method kembali.

Reviewer `verify_index_jobs` menemukan kebutuhan eksplisit mengikat checkpoint fence
ke job fence; perbaikan yang sama sedang diterapkan implementer, lalu diperiksa ulang
independen. Self-review juga menemukan approval inspector membaca kandidat LINK
sebelum pemeriksaan cardinality. Validator proposal kini dipanggil lebih dahulu;
empty-candidate regression lulus. Tidak ada blocker tersisa pada scope gate awal.

`authority.log` awal gagal compile karena nama enum terminal job salah; diperbaiki
ke enum schema yang sebenarnya dan diuji ulang. Log gagal tidak dianggap lulus.
`git diff --check` dan tautan lokal dokumen baru diperiksa tanpa temuan.

Integrasi berikutnya dijelaskan pada [coordinator graph](graph-assembly-coordinator.md):
binding snapshot sumber, revalidasi dependensi lintas revision, plan/view/inventory,
dispatch, commit output atomik dan writer Neo4j. Pemeriksaan awal ini harus diulang
dalam transaksi scheduling/commit; source/receipt valid bukan bukti live fence saat
RPC selesai. Benchmark kualitas/latency/throughput tetap **REQUIRED_UNMEASURED**.
