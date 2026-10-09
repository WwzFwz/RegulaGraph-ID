# Verifikasi operator persiapan graph

Dokumen ini mencatat bukti `prepare-graph` dari published source membership sampai
penjadwalan ASSEMBLE, serta integrasinya dengan worker/publication/retrieval.
Baseline sebelum perubahan: `7dd982261a389731e27d3104868ee3fd3c8ffd71`.
Fingerprint kode, perintah, exit code dan raw log berada di
`artifacts/verification/20261009-graph-prepare/`; laporan ini bukan acceptance release.

## Hasil 2026-10-09

| Pemeriksaan | Expected dan actual | Status |
| --- | --- | --- |
| Seluruh sumber | Sumber kedua belum RESOLVE atau revision berbeda menggagalkan semua sebelum reservation | PASS |
| Budget kandidat | Ref kandidat melewati budget ditolak sebelum candidate I/O/reservation | PASS |
| Membership SQL | 257 receipt nonmember tidak menyembunyikan sumber indeks; member oversized ditolak | PASS |
| CLI | Invalid scope/base/hash/deadline, cancellation, error dan output invalid tidak menghasilkan sukses; credential tidak dicetak | PASS |
| Preparation native | Executable memulai sebelum reservation/envelope graph; source binding, plan, child job dan exact replay durable; pin tidak bocor | PASS |
| Native pipeline | Rust ASSEMBLE aktual, checkpoint recovery tanpa RPC ulang, Neo4j/Qdrant publication dan retrieval/draft fixture bersitasi | PASS |
| Recovery publication | Qdrant readback gagal setelah graph write mempertahankan parent; retry CLI memulihkan publication | PASS |
| Go regression | `go test ./src/server/...` dan `go vet ./src/server/...` exit 0 | PASS |
| Review independen | Agent `/root/verify_index_jobs` membaca diff dan menjalankan ulang unit/native dari executable baru; tiga temuan ditutup | PASS_SCOPED |
| Gold, model quality, ketepatan hukum, benchmark required | Tidak dijalankan pada corpus/workload acceptance | NOT_MEASURED |

Run memakai Windows, Go toolchain lokal, worker Rust ASSEMBLE yang sudah dibangun,
PostgreSQL/Qdrant/Neo4j lokal dengan schema/generation fixture terisolasi. Tidak
ada deployment. Source text, EXTRACT/review labels dan vektor indeks adalah fixture;
generator pada run ini juga fixture. Ini bukan bukti seluruh PDF telah diproses
model ataupun kualitas jawaban hukum nyata.

## Temuan dan batas

Reviewer menemukan bahwa reference SQL semula dibatasi sesudah transfer, receipt
nonmember dapat menghabiskan LIMIT sebelum member terbaca, dan budget aggregate
belum menghitung candidate refs. Perbaikan memakai CASE SQL sebelum transfer,
seleksi exact source job IDs dan candidate budget sebelum pembacaan. Regression
independen melewati seluruh kasus. Reader checkpoint umum juga membatasi payload
16 MiB di SQL sebelum hash/decode.

Budget 64 MiB menghitung declared metadata sumber/kandidat, bukan total byte I/O
seluruh workflow: sebagian input dibaca ulang oleh validator/admission. Required
latency/RSS/throughput tetap harus diukur. Batas 256 plan bukan perubahan workload
benchmark. Preparation dapat meninggalkan reservation atau artefak sesudah gagal
di tahap mutation, tetapi tidak subset child jobs; retry wajib dengan identitas sama.

Sumber lintas registry revision masih ditolak sampai durable reaffirmation ada.
Graph incremental pada snapshot yang mewarisi indeks juga belum dicakup command.
Status proyek keseluruhan dan seluruh benchmark tetap belum selesai.
