# Verifikasi writer indeks awal

Laporan ini mencatat pemeriksaan katalog, admission, dispatch Qdrant dan receipt
publication pada 2026-10-04. Paket library **lulus pemeriksaan fungsional yang
dijalankan dan review independen**. Race detector belum berhasil dijalankan;
X01 keseluruhan, kualitas model, benchmark dan aplikasi RAG belum selesai.

## Revision, lingkungan dan bukti

Baseline `250cea35fc88611157d14736e4884e8ba9774daf`; implementasi hasil:

- `cb91b90`: katalog generation/point immutable dan primitive intent/authority.
- `8bcb81e`: pemeriksaan publisher epoch sebelum snapshot activation.
- `32e1c3a`: Qdrant exact count, topology dan probe dense/BM25.
- `52b5cee2bd1b97112977a40f5d0474485c74100f`: preparation/writer dan tes integrasi.

Windows amd64, Go 1.26.8; PostgreSQL 16.8 Alpine image
`sha256:3b057e1c2c6dfee60a30950096f3fab33be141dbb0fdd7af3d477083de94166c`;
Qdrant 1.18.0 image
`sha256:b3063c673f3973877c038eeecc392bad5011f072ee7892b56c9a8e204a3bdea9`.
Kedua container disposable memakai port loopback 55441/56341. Tes writer membuat
schema PostgreSQL dan collection unik miliknya, lalu membersihkannya. Layanan
ini bukan deployment aplikasi. Raw log: `artifacts/verification/20261004-initial-index/`.

Fixture `tests/fixtures/index-source-v1.pb` berasal dari ekspor CHUNK Rust sintetis;
SHA-256 `a1899b13a5eac25e8dbef08dd29c83cf45c17b2b144ac3b9f65a1cfb9fcf631f`.
Plan/batch berasal dari wire-cases.json. Tes merebind identitas dan memilih dua
chunk dengan closure sumber utuh. Checkpoint SQL, statistik dan vector dua dimensi
adalah seed sintetis. Reader artefak memakai map in-memory; hash/size tetap diperiksa
kode produksi. PostgreSQL, Qdrant, katalog, gate, writer dan coordinator memakai
implementasi nyata. Tidak ada klaim run model atau PDF pengguna pada paket ini.

## Hasil aktual

| Pemeriksaan | Expected dan actual | Exit / status |
| --- | --- | --- |
| `go test -count=1 ./src/server/...` dengan kedua endpoint disposable | Semua package Go lulus; termasuk opt-in PostgreSQL, writer dan Qdrant. Opt-in lain yang prasyaratnya tidak disetel bukan bukti lulus integrasi eksternal. | 0 / PASS cakupan dijalankan |
| `go vet ./src/server/...` | Tidak ada diagnostic | 0 / PASS |
| `TestIndexCatalogAgainstPostgres` | Replay identik; route/fence drift, vector invalid, collision UUID, mutation SQL dan batch konflik ditolak tanpa partial allocation | 0 / PASS |
| `TestPublicationRejectsSupersededPublisherAgainstPostgres` | DB menolak dua open publication; epoch drift yang diinjeksi ditolak saat activation; published replay tetap sah | 0 / PASS |
| `TestInitialIndexPublicationAgainstStores` | Artefak corrupt, job salah, batch duplikat, source coverage parsial dan write set berbeda ditolak; lock eksklusif; lost reply tidak publish/abort tanpa compensation; retry sukses | 0 / PASS |
| Subkasus required Neo4j | Qdrant siap tidak menggantikan receipt Neo4j yang belum tersedia; publication ditolak | 0 / PASS |
| `TestInitialServingAgainstQdrant` | Upsert/readback/probe nyata lulus; count kosong/kurang/lebih dan probe tidak visible ditolak | 0 / PASS |
| `TestInitialServingRejectsUnprovedRoute` | HTTP fixtures menolak multi-shard/replica, health/optimizer gagal, write consistency salah, count hilang/lebih dan empty dense route | 0 / PASS |
| `go test -race ./src/server/internal/indexing ./src/server/internal/adapters/qdrant ./src/server/internal/domain` | Tidak mencapai eksekusi tes: compiler Cygwin gagal signal pipe dalam sandbox; retry elevated gagal compile `runtime/cgo` dengan warning format DWORD diperlakukan sebagai error | 1 / BLOCKED toolchain |
| Required quality/performance | Corpus/model/workload/gold penerimaan belum dijalankan | NOT_MEASURED |

Raw output tes dan vet ada di `go-test.log` dan `go-vet.log`; kegagalan race
tetap disimpan dalam `go-race.log` dan `go-race-elevated.log`. Jangan menafsirkan
build gagal sebagai laporan data race, atau menandai race detector PASS. Ulangi
pada compiler C Windows yang kompatibel atau lingkungan Linux yang sesuai.

Run awal lintas package sempat gagal karena tes PostgreSQL lama melakukan
`TRUNCATE` pada schema bersama saat writer masih berjalan. Tes writer kemudian
diisolasi per schema; run bersama dan run seluruh package sesudah perbaikan lulus.
Fixture juga diperbaiki memakai ID artefak unik agar run ulang tidak berbenturan
dengan registry immutable. Ini perubahan fixture, bukan kelonggaran invariant.

## Review independen dan batas

Agent `verify_candidate_contract` meninjau implementasi secara read-only dan
menjalankan ulang tes terarah pada layanan nyata. Review final menyetujui cakupan
writer awal tanpa blocker kode terkonfirmasi. Validasi probe/corpus, bentuk vector,
dictionary membership dan boundary checkpoint telah diperiksa kembali setelah
perbaikan. Temuan awal tentang dua publication aktif ditarik: unique partial index
DB sudah menolak skenario itu. Pemeriksaan epoch tambahan merupakan pertahanan
eksplisit, bukan klaim eksploit sebelumnya terbukti.

Kelengkapan hanya untuk daftar sumber yang dinyatakan pemanggil. Production
coordinator masih perlu membekukan inventory, menetapkan backend wajib, mengikat
job/lease/cancellation dan memasang reader/hydration sebelum route aplikasi aktif.
Test publication Qdrant saja memakai corpus terisolasi; tidak ada receipt graph
palsu. Topology terdistribusi, incremental/closure, recovery bootstrap, GC,
performance reference, akurasi dan PDF-ke-jawaban tetap terbuka. Kontrak lebih
rinci ada pada [writer awal](initial-index-writer.md); target tetap pada
[benchmark-targets.yaml](../configs/benchmark-targets.yaml), tanpa perubahan angka.
