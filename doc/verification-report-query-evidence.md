# Verifikasi query evidence dan factory katalog

Dokumen ini mencatat hasil verifikasi 2026-10-04 untuk CLI operator evidence-only,
resource query berbasis katalog dan admission Qdrant tanpa bootstrap. Paket
**lulus tes fungsional yang dijalankan serta review independen**; aplikasi RAG
penuh dan required benchmark belum selesai. Cara penggunaan ada pada
[query-evidence.md](query-evidence.md).

## Revision, fixture dan lingkungan

Baseline `5e2444b`; implementasi terverifikasi:

- `450c51b`: admission existing collection tanpa mutasi pada jalur query.
- `77a8442`: factory hybrid berbasis katalog, reuse binding dan sesi evidence.
- `6833727`: CLI, profil/tanggal eksplisit, output guard dan redaction.

Windows amd64, Go 1.26.8. PostgreSQL 16.8 Alpine disposable pada loopback 55443,
Qdrant 1.18.0 pada loopback 56343 dengan image
`sha256:b3063c673f3973877c038eeecc392bad5011f072ee7892b56c9a8e204a3bdea9`.
Raw log berada di `artifacts/verification/20261004-query-evidence/`. Container run
ini dihentikan setelah verifikasi; container pengguna tidak disentuh.

Live fixture memakai schema/collection unik miliknya dan membersihkannya.
Sumber `tests/fixtures/index-source-v1.pb` mempunyai SHA-256
`a1899b13a5eac25e8dbef08dd29c83cf45c17b2b144ac3b9f65a1cfb9fcf631f`.
Publication, registry, PostgreSQL, Qdrant, BM25 artefak, fusion, hydration, context
dan citation adalah implementasi produksi. Source/job seed, embedding, generator
dan token counter tetap sintetis. File artefak pada integrasi disajikan melalui
map dengan pemeriksaan registry/hash produksi. Tidak ada klaim uji CLI lengkap
dengan FileStore corpus pengguna dan proses native model nyata pada run ini.

## Hasil aktual

Perintah dari root memakai GOCACHE lokal serta endpoint disposable
`REGULAGRAPH_TEST_POSTGRES_DSN=postgres://postgres@127.0.0.1:55443/regulagraph_query_test?sslmode=disable`
dan `REGULAGRAPH_TEST_QDRANT_ENDPOINT=http://127.0.0.1:56343`.

| Pemeriksaan | Expected dan actual | Exit/status |
| --- | --- | --- |
| `go test -count=1 ./src/server/...` | Semua paket Go lulus; termasuk integrasi PostgreSQL/Qdrant dengan prasyarat yang disetel | 0 / PASS cakupan dijalankan |
| `go vet ./src/server/...` | Tidak ada diagnostic | 0 / PASS |
| `TestReadOnlyAdmissionNeverBootstraps` | 404, payload index hilang dan layout drift ditolak; hanya GET, tidak membuat/repair collection | PASS |
| `TestPublishedQuerySelectsOnlyAuthorizedCatalogRoute` | Origin asing ditolak sebelum HTTP; route/key berasal dari konfigurasi exact-origin dan katalog; model/route drift ditolak; Bind ulang tanpa I/O persiapan atau mutable weights bersama | PASS |
| `TestSearchSessionReturnsEvidenceWithoutGenerator` | Evidence dikembalikan tanpa generator, lease dilepas, profile drift ditolak | PASS |
| `TestInitialIndexPublicationAgainstStores` | Factory membuka store berdasarkan katalog, memuat BM25 terdaftar dan mencapai draft bersitasi; resource prepared dipakai kembali untuk evidence-only; receipt graph hilang tetap memblokir publication | 0 / PASS |
| `go test -count=1 ./src/server/cmd/cli` setelah perbaikan final | Argumen/config invalid gagal sebelum I/O; profil wajib; output PARTIAL dipertahankan; cancellation, failed completion dan foreign corpus tidak exit 0; credential tidak masuk error | 0 / PASS |
| `go run ./src/server/cmd/cli query-evidence -help` | Executable menerima command baru dan menampilkan flag tanpa memerlukan koneksi | 0 / PASS |
| Native model/CLI corpus nyata | Belum dijalankan pada paket ini; capabilities dan transport native adalah codepath nyata, bukan bukti model run baru | NOT_MEASURED |
| Race detector | Tidak dijalankan ulang; keterbatasan compiler Windows/Cygwin dari run sebelumnya tetap berlaku | BLOCKED toolchain |
| Required kualitas/performa | Workload/model/gold penerimaan belum dijalankan | NOT_MEASURED |

Log: `go-test.log`, `go-vet.log`, `cli-final.log`, `cli-help.log`,
`toolchain.log`, dan `reviewer.log`. Full suite dijalankan sebelum pengetatan
output/profil dan penyesuaian nama environment CLI; paket CLI diuji ulang setelah
perubahan terakhir. Kode workflow/backend tidak berubah setelah full suite.
Opt-in lain tanpa prasyarat tidak menjadi bukti layanan eksternal lulus. Riwayat
kegagalan toolchain race ada pada [laporan writer](verification-report-initial-index.md).

## Review dan batas cakupan

Agent `verify_candidate_contract` memeriksa diff secara read-only, menjalankan
ulang integrasi PostgreSQL/Qdrant dan tes CLI/workflow. Dua temuan antarmuka telah
ditutup: profil yang semula default hybrid kini wajib eksplisit, dan CLI tidak
mencetak bundle failed/foreign sebagai sukses. Tidak ada blocker kode tersisa
yang terkonfirmasi dalam cakupan query-evidence. Tes Qdrant spesifik dijalankan
implementer; command unit terarah reviewer untuk paket itu tidak mempunyai
matching test, sehingga tidak diklaim sebagai pengulangan independen tes tersebut.

CLI bersifat operator-local, tidak menyediakan authorization multi-user/API.
Semua koneksi dan lexical load dibuat per invocation, sedangkan native server
yang memuat weights tetap hidup di luar CLI. Reuse warm hanya tersedia pada
PreparedQuery library dan diuji terpisah. Generation rollover memerlukan resource
baru; cache/API lifecycle belum dibuat. Factory awal belum mendukung checked
dictionary ancestry lintas generation. Receipt/profil backend penuh dan corpus
inventory tetap tanggung jawab coordinator publication.

Masih perlu coordinator indeks corpus pengguna, generator/tokenizer prompt nyata,
API jawaban, graph, parent/exception hydration, temporal projection, reranking penuh
dan incremental update. Gold/kualitas serta p95/p99/throughput/RSS belum dibuktikan.
Target [benchmark-targets.yaml](../configs/benchmark-targets.yaml) tidak diubah.
