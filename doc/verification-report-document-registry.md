# Verifikasi identitas BIND pada registry target

Laporan ini merekam pemeriksaan exact identity BIND sebelum graph preparation;
hasilnya tidak meluluskan K01 keseluruhan atau benchmark release. Kontrak konsumennya
ada di [input ASSEMBLE](graph-assembly-inputs.md).

## Revision dan lingkungan

Basis kode `c8d5ca8` ditambah perubahan komponen document registry pada commit yang
memperkenalkan laporan ini. Tanggal 2026-10-09; Go 1.26.8 windows/amd64,
PostgreSQL 16.8-alpine pada port 55448 dan Qdrant 1.18.0 pada port 56348.
Schema/collection fixture terisolasi dan dibersihkan oleh tes. Raw logs berada di
`artifacts/verification/20261009-document-registry/`.

## Input, proses dan hasil

Unit fixture berasal dari `BindDocumentBatch` produksi. Integrasi workflow memakai
binder exact-key dan allocator PostgreSQL produksi pada input STRUCTURE sintetis;
tidak menjalankan scheduler BIND. Fixture indexing memakai artefak Rust-derived yang
diperkaya source metadata/edition dan identity rows sintetis sebelum registrasi.
Publication PostgreSQL/Qdrant, registrasi byte dan graph preparation dijalankan nyata;
EXTRACT/RESOLVE kosong tetap fixture, bukan keluaran model live.

| Pemeriksaan | Expected dan actual | Bukti/status |
| --- | --- | --- |
| Rekonstruksi key BIND | Sama dengan claim issuer/regulasi/pasal asli, deterministik dan input tidak dimutasi | PASS |
| Closure dan budget | Duplicate dependency/provision, foreign corpus, source/edition/revision hilang, scope tidak dikenal dan reference budget ditolak | PASS |
| Autentikasi byte | Hash byte palsu ditolak meskipun metadata referensi disalin | PASS |
| Revision tak terkait | Identitas yang lifetime-nya masih utuh diterima sesudah penambahan lain | PASS |
| Lifetime identity | Revision historis sebelum closure diterima; tepat pada closure atau sesudahnya ditolak; target masa depan ditolak | PASS |
| Preparation integration | Registry key berubah ditolak meskipun source receipt/hash tetap utuh; replay valid tetap identik | PASS |

Perintah `go test ./src/server/internal/domain ./src/server/internal/workflows
./src/server/internal/indexing -run '^(TestDocumentRegistry|TestPublishedGraphSourceMembership)'
-count=1 -v` exit 0 (`targeted-final.log`). Seluruh `go test ./src/server/...`
exit 0 (`go-all.log`) dan `go vet ./src/server/...` exit 0 (`go-vet.log`).
Status PASS berlaku pada tes yang dijalankan; paket/tes dengan prasyarat lain yang
skip tidak dianggap terverifikasi. Log percobaan awal yang gagal karena cache izin
atau fixture belum lengkap tetap disimpan, bukan dihitung sebagai PASS.

Reviewer terpisah `verify_index_jobs` memeriksa diff dan menjalankan domain, BIND
PostgreSQL dan graph preparation PostgreSQL/Qdrant secara independen. Exit 0,
`independent.log`; tidak ditemukan blocker dalam scope. Perubahan dokumentasi
sesudah review tidak mengubah perilaku kode.

## Batas dan pekerjaan berikutnya

Read-only RepeatableRead membuktikan view historis, bukan authority yang bertahan
sampai job dijadwalkan. Admission harus mengulang predicate di transaksi yang memegang
lock publikasi/corpus/source. Pemeriksaan ini tidak membuktikan alias/candidate context,
reaffirmation keputusan lintas revision, Neo4j publication, mutu model atau hukum.
Guard RESOLVE revision sama dengan target belum dilonggarkan.

Ukuran record/reference dan byte dibatasi; p95/p99 SQL/queue, RSS, throughput serta
kualitas release **REQUIRED_UNMEASURED**, tetap mengikuti
[target numerik](../configs/benchmark-targets.yaml). Tidak ada target yang diubah.
