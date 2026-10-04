# Verifikasi reader snapshot dan hidrasi evidence

Laporan ini mencatat pemeriksaan library Q01/A01 pada 2026-10-04. Reader katalog,
hidrasi teks dan lifecycle request terpin **lulus pemeriksaan fungsional yang
dijalankan serta review independen**. Ini bukan status seluruh milestone Q01/A01
atau penerimaan produksi. Kontrak ada di [pinned-evidence.md](pinned-evidence.md).

## Revision dan lingkungan

Baseline `4b23304`; perubahan kode terverifikasi:

- `8a9bcbb`: reader katalog/record PostgreSQL dengan lease hidup.
- `5dae49c`: hidrasi sumber, temporal policy dan proyeksi versi Qdrant unik.
- `e9e58c6`: lease sepanjang RAG, konteks status dan tes draft bersitasi.

Windows amd64, Go 1.26.8; PostgreSQL 16.8 Alpine disposable pada loopback 55442;
Qdrant 1.18.0 disposable pada loopback 56342, image
`sha256:b3063c673f3973877c038eeecc392bad5011f072ee7892b56c9a8e204a3bdea9`.
Setiap fixture writer memakai schema dan collection unik serta membersihkannya.
Kedua container milik run dihentikan sesudah verifikasi; layanan lain tidak diubah.
Raw output: `artifacts/verification/20261004-pinned-evidence/`.

Fixture sumber tetap `tests/fixtures/index-source-v1.pb` (ekspor Rust sintetis),
SHA-256 `a1899b13a5eac25e8dbef08dd29c83cf45c17b2b144ac3b9f65a1cfb9fcf631f`.
Tes menambahkan normalized text terverifikasi dan observation URL fixture,
merebind identitas, serta memakai dua chunk dengan closure sumber lengkap.
Byte artefak disajikan dari map, tetapi hash/size/registry/closure diperiksa kode
produksi. Vector, checkpoint awal, generator dan token counter adalah sintetis.
PostgreSQL, Qdrant, publication, admission, retrieval, hydration, context dan
validasi citation memakai implementasi produksi. Tidak ada run PDF pengguna atau
model nyata yang diklaim dalam laporan ini.

## Perintah dan hasil

Perintah dijalankan dari root dengan GOCACHE lokal dan
`REGULAGRAPH_TEST_POSTGRES_DSN=postgres://postgres@127.0.0.1:55442/regulagraph_read_test?sslmode=disable`,
`REGULAGRAPH_TEST_QDRANT_ENDPOINT=http://127.0.0.1:56342` pada database disposable.

| Pemeriksaan | Expected dan actual | Exit / status |
| --- | --- | --- |
| `go test -count=1 ./src/server/...` | Semua paket Go lulus, termasuk integrasi PostgreSQL/Qdrant yang prasyaratnya disetel | 0 / PASS cakupan dijalankan |
| `go vet ./src/server/...` | Tidak ada diagnostic | 0 / PASS |
| `go test -count=1 ./src/server/internal/indexing -run TestInitialIndexPublicationAgainstStores -v` | Publication Qdrant terisolasi dapat dibaca; required receipt Neo4j yang hilang tetap memblokir publication | 0 / PASS |
| Reader dan hydration pada tes tersebut | Urutan batch tetap; duplicate/missing ID, byte budget, owner/sequence salah, point asing, corruption teks dan lease dilepas ditolak | 0 / PASS |
| Bundle byte cap | Batas satu byte di bawah ukuran serialized bundle ditolak meskipun teks item sendiri muat | 0 / PASS |
| `TestHydrationTemporalAdmission` | `[start,end)`, unresolved REPORT/EXCLUDE/REQUIRE_REVIEW, status campuran dan tanggal yang tidak bisa diproyeksikan menghasilkan outcome eksplisit | 0 / PASS |
| `TestStoreDeduplicatesVersionProjectionAcrossSourcePairs` | Satu versi dari dua pasangan sumber diproyeksikan sekali; tes duplicate pair sebelumnya tetap menolak pair identik | 0 / PASS |
| `TestRAGSessionOwnsLeaseThroughDraft` | Pin dipertahankan, caller tidak dimutasi, cleanup saat gagal/cancel, historical mismatch dan catalog invalid ditolak; revocation sesudah generation tidak menghasilkan sukses | 0 / PASS |
| Komposisi published RAG dalam tes indexing | Query dense Qdrant mencapai hydration dan draft; citation merujuk evidence, version dan URL fixture yang tepat; hasil UNREVIEWED/PARTIAL; lease dilepas | 0 / PASS |
| Tes context | Status snapshot pengetahuan tampil dan ikut budget; dependency hilang tetap PARTIAL | 0 / PASS |
| Race detector | Tidak dijalankan ulang; compiler Cygwin tidak kompatibel dengan build Windows cgo pada run sebelumnya | BLOCKED toolchain, bukan PASS |
| Required kualitas/latency/throughput/memori | Gold, model, corpus dan workload penerimaan belum dijalankan | NOT_MEASURED |

Log utama: `go-test.log`, `go-vet.log`, `integration-final.log`, `toolchain.log`
dan `reviewer.log`. Run integrasi terakhir lulus setelah assertion citation
diperkuat; seluruh tes Go dijalankan sebelum tambahan assertion itu. Perubahan
berikutnya hanya dokumentasi. Opt-in integrasi lain tanpa prasyarat tetap bukan
bukti lulus layanan eksternal. Keterbatasan race dicatat pada
[laporan writer](verification-report-initial-index.md).

## Review independen dan temuan

Agent `verify_candidate_contract` meninjau kode secara read-only dan menjalankan
ulang tes integrasi layanan nyata serta tes temporal/session/Qdrant/context.
Temuan diperbaiki: versi campuran tidak boleh menghapus bukti valid diam-diam;
budget harus mencakup bundle lengkap; status konflik harus tetap terlihat dan
PARTIAL; daftar versi hit tidak mengulang pasangan sumber. Plan terdekode dan
lookup item kini di-cache per request untuk menghindari kerja berulang per hit.
Kompilasi fixture tanggal sempat gagal karena tipe uint32, lalu diperbaiki menjadi
int32 dan diuji ulang. Review final tidak menemukan blocker kode terkonfirmasi
dalam cakupan library snapshot awal. Pemeriksaan tambahan citation ditinjau
kembali dan run final lulus.

## Batas penerimaan dan kelanjutan

Factory integrasi menggunakan Qdrant store fixture yang sudah dibuat dan cocok
dengan katalog; pemilihan route/credential produksi dari binding belum diuji.
Caller tetap pemilik authorization corpus. Read lease tidak membuktikan GC atau
rollover publication serentak sudah diuji. Hydrator hanya mendukung source snapshot
awal yang sama dengan target; parent/exception belum diekspansi dan temporal
projection campuran belum aktif. Query API/CLI, coordinator INDEX penuh, inventory
otoritatif, profil backend wajib, tokenizer generator nyata, reranker menyeluruh,
graph dan streaming tetap terbuka.

Tidak ada klaim dukungan semantik model, akurasi hukum, Recall/nDCG atau p95/p99.
Target tetap [benchmark-targets.yaml](../configs/benchmark-targets.yaml) tanpa
perubahan angka/workload. Paket library ini memungkinkan integrasi berikutnya;
belum berarti aplikasi RAG produksi atau keseluruhan proyek selesai.
