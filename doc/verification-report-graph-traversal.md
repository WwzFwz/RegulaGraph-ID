# Verifikasi discovery traversal graph

Status akhir scope discovery: PASS. Seluruh Go suite dan `go vet ./src/server/...`
selesai exit 0 (`go-test.log`, `go-vet.log`). Full Neo4j/graph package dengan backend
nyata juga exit 0 pada `backend-suite.log`. Reviewer `verify_index_jobs` mengulang
regresi serta native pipeline: PASS tanpa blocker tersisa; lihat
`independent-fixed.log`, `independent-native.log` dan `independent-results.json`.

Laporan ini mencatat implementasi setelah revision `dea353fabf022f67bf316c5c088d5c772429f1ff` pada 2026-10-09. Raw log dan fingerprint berada di `artifacts/verification/20261009-graph-traversal/`. Scope meliputi snapshot-bound neighborhood, directed/sourced path discovery dan resource accounting; bukan graph-to-answer atau acceptance kualitas.

`regression-final.log` menjalankan `go test ./src/server/internal/retrieval/graph ./src/server/internal/adapters/neo4j -run 'TestTraversal|TestSealedGraphReaderAgainstNeo4j' -count=1 -v`, exit 0. Fixture diamond/cycle mempertahankan alternatif jalur, tidak membalik predicate, mempertahankan semua alternate support, dan menghasilkan output deterministik. Hop/path/assertion/read budget menjadi status eksplisit; invalid snapshot/endpoint/support/visibility/byte accounting dan cancellation ditolak.

Regresi 65 frontier menguji reader yang memakai ulang buffer antar-batch. Backend Neo4j nyata menguji outgoing/incoming seed, corrupted edge sequence, extra edge, duplicate subject menggantikan object, hilangnya seluruh endpoint adjacency, dan hilangnya satu dari dua support link. Sesudah setiap corruption test, fixture dipulihkan lalu harus dapat dibaca lagi; ini memperbaiki helper lama yang sebelumnya merestorasi record dari posisi array, bukan ID entity yang dimutasi. Metadata type-substitution guard juga diuji ulang.

Reviewer menemukan celah multiplicity, pointer reuse, dan missing support link. Implementasi memakai exact distinct multiplicity, owned assertion clones, serta union adjacency dengan projection membership berindeks. Overflow byte valid dipisahkan dari corruption melalui `ErrGraphReadBudget`; raw payload size dipakai untuk aggregate budget, bukan hanya ukuran protobuf setelah decode. Tes awal juga menangkap kewajiban C01 satu selected support per edge; seluruh alternate support tetap disimpan terpisah.

`native.log`: `go test ./src/server/internal/indexing -run '^TestNativeGraphAssemblyPipeline$' -count=1 -v`, exit 0. Rust ASSEMBLE aktual melewati PostgreSQL/Qdrant/Neo4j publication; traversal produksi kemudian menghasilkan satu discovery path dengan exact support dan snapshot yang sama. EXTRACT/review/vector/reranker/generator tetap fixture sintetis. Draft bersitasi pada fixture yang sama masih menggunakan dense/BM25, bukan bukti bahwa branch graph sudah terhubung ke jawaban.

Go 1.26.8 windows/amd64, Neo4j 5.26.0-community, worker Rust SHA256 `867e3a0bc33480ee08ed526a53e8aea97ad1af38576bd35a842b1e567fd9e314`. Hasil independent review, seluruh Go suite/vet dan fingerprints disimpan pada raw results setelah final checks. Gold, Recall/nDCG, legal correctness, latency/throughput/RSS acceptance tetap NOT_MEASURED. Target benchmark tidak diubah.
