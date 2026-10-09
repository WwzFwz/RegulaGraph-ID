# Verifikasi snapshot graph dengan indeks yang dipakai ulang

Laporan ini mencatat milestone setelah revision `980f893` pada 2026-10-09. Fingerprint, perintah dan raw log berada di `artifacts/verification/20261009-index-reuse/`. Ruang lingkup adalah publication/read admission/hydration, bukan graph traversal relevance atau penerimaan kualitas model.

Run native menjalankan Rust ASSEMBLE aktual, recovery checkpoint tanpa RPC tambahan, PostgreSQL catalog/intent/receipt, Neo4j exact readback, Qdrant full readback dan reuse receipt, CAS snapshot aktif, reader snapshot baru/lama, lalu workflow dense+BM25/reranking/generation/citation. Neo4j berisi 8 record dan 5 edge. EXTRACT/review/vector awal serta reranker/generator tetap fixture sintetis; jawaban bersitasi membuktikan integrasi identitas, bukan correctness hukum atau kualitas retrieval.

| Pemeriksaan | Expected / actual |
| --- | --- |
| Readback Qdrant gagal | Tidak ada mapping atau receipt sukses |
| Insert receipt gagal setelah mapping | Mapping rollback bersama transaksi |
| Replay dan mutation mapping | Receipt identik; UPDATE mapping ditolak |
| Final source-index child dibatalkan | CAS ditolak walaupun graph siap |
| Final graph cancellation/registry/lock checks | Tetap ditolak sebelum aktivasi |
| Publication nyata dan replay | Berhasil; snapshot baru aktif dan source envelope tetap snapshot asal |
| Query pada snapshot gabungan | Dense/BM25, hydration dan draft bersitasi berhasil melalui workflow produksi |
| Parent historis | Tetap bisa dibaca dengan pin lama |
| Inherited serving | Sequence sesudah awal diterima; future point dan extra point ditolak |
| Paging | 67 record tercakup tanpa duplikasi dalam beberapa halaman; EOF kosong |
| Lease | Pin kedaluwarsa ditolak; query yang tertahan lock dibatalkan pada deadline pin |

`native.log` membuktikan publication nyata awal, `final-regression.log` mencakup paging dan `TestInitialServingAgainstQdrant`, dan `deadline-regression.log` menambahkan table-lock deadline. Perintah utama: `go test ./src/server/internal/indexing ./src/server/internal/adapters/qdrant -run '^(TestNativeGraphAssemblyPipeline|TestInitialServingAgainstQdrant)$' -count=1 -v`. Seluruh Go suite dan vet dicatat pada `go-test.log` / `go-vet.log`.

Test paging menambahkan 65 record catalog sintetis hanya setelah query produksi berhasil. Record itu tidak ditulis ke Qdrant atau diberi receipt; schema fixture dibuang setelah pemeriksaan paging. Test stale-authority masih memakai trigger rollback untuk mengisolasi guard, tetapi receipt Qdrant kini diperoleh dari readback nyata; setelah trigger dihapus, CAS benar-benar berhasil.

Reviewer terpisah memeriksa batas authority, provenance reader dan query. Temuan helper paging semula belum membatasi pool query dengan deadline pin diperbaiki dengan context deadline pada seluruh metode, disertai uji lock nyata. Hasil final serta fingerprint reviewer berada pada `independent-results.json`.

Hasil akhir tabel: PASS. Native/readiness regression, seluruh Go suite dan vet
selesai exit 0. Reviewer `verify_index_jobs` mengulang native/deadline, inherited
Qdrant, publication foundation dan superseded-fence: PASS, tanpa temuan blocking
tersisa. Log `independent-deadline.log`, `independent-final.log` dan
`independent-publication.log` menyimpan hasil aktual.

Runtime Go 1.26.8 windows/amd64, worker Rust SHA256 `867e3a0bc33480ee08ed526a53e8aea97ad1af38576bd35a842b1e567fd9e314`, PostgreSQL/Qdrant disposable dan Neo4j 5.26.0-community lokal. Migration diuji pada schema baru; rehearsal upgrade produksi belum diuji. Graph traversal/graph-to-answer, operator wiring dan corpus/model/gold acceptance belum selesai. Seluruh benchmark required tetap NOT_MEASURED dan target tidak berubah.
