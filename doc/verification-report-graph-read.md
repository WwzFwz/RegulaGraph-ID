# Verifikasi admission dan record reader graph

Seluruh Go suite (`go test ./src/server/...`) dan `go vet ./src/server/...`
selesai exit 0; log `go-test.log` dan `go-vet.log`. Tes integrasi opt-in dijalankan
terpisah sebagaimana perintah di bawah, bukan dianggap tercakup dari suite default.

Laporan ini melacak boundary pembacaan graph setelah revision `7f7851959ff9f1f9b05267c6ab411b6f30dbd789` pada 2026-10-09. Raw log dan fingerprint berada di `artifacts/verification/20261009-graph-read/`. Cakupan adalah authority serta integritas record, bukan relevansi traversal atau penerimaan seluruh GraphRAG.

| Pemeriksaan | Expected / actual |
| --- | --- |
| Generation belum ada / belum sealed | OpenReader menolak keduanya |
| Cold reader | Berhasil tanpa bootstrap schema |
| Typed batch | Urutan request dipertahankan; protobuf sama dengan sumber |
| Missing, duplicate, wrong kind, byte budget | Error tanpa hasil parsial |
| Hash, payload, projection, sequence korup | Pembacaan ditolak |
| Hash/payload diganti string list besar | Query metadata/payload mengembalikan null sebelum transfer |
| Payload diganti integer list | Bounded transfer, ditolak oleh tipe Go |
| Expired reader | Deadline menghentikan pembacaan |
| Snapshot index-only / wrong scope / released lease | Admission PostgreSQL menolak |
| Snapshot gabungan terbit | Entity/assertion/support/mention/decision sama dengan output Rust aktual |

`neo4j-final.log`: `go test ./src/server/internal/adapters/neo4j -run '^TestSealedGraphReaderAgainstNeo4j$' -count=1 -v`, exit 0. Tes memeriksa hasil query DB langsung untuk regresi type substitution; error Go saja tidak membuktikan batas transfer. Reviewer menemukan masalah ini pada versi awal, lalu guard tipe ditambahkan pada kedua fase.

`native.log`: `go test ./src/server/internal/indexing -run '^TestNativeGraphAssemblyPipeline$' -count=1 -v`, exit 0. Worker Rust ASSEMBLE aktual, PostgreSQL/Qdrant/Neo4j lokal, publication gabungan dan typed reader digunakan. EXTRACT/review/vector/reranker/generator tetap fixture; cited draft masih menggunakan retrieval dense/BM25. Test paging sebelumnya menambahkan catalog sintetis sesudah query; graph reader tidak menyatakan ulang konsistensi seluruh indeks setelah mutation fixture itu.

Go 1.26.8 windows/amd64, Neo4j 5.26.0-community, worker SHA256 `867e3a0bc33480ee08ed526a53e8aea97ad1af38576bd35a842b1e567fd9e314`. Reviewer terpisah `verify_index_jobs` mengulang Neo4j dan native test: PASS pada `independent-fixed.log` dan `independent-native.log`, tanpa blocker tersisa; fingerprint di `independent-results.json`. Suite/check lanjutan dicatat dalam raw results. Traversal, graph-to-answer, corpus/model/gold acceptance dan seluruh required benchmark belum selesai/NOT_MEASURED. Target tidak berubah.
