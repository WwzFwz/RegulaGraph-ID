# Verifikasi konteks kandidat lintas revisi

Dokumen ini mencatat pembuktian reader revalidasi kandidat untuk persiapan ASSEMBLE.
Scope-nya konsistensi konteks RESOLVE pada registry historis; bukan kelulusan seluruh
K01, validitas hukum, atau acceptance Hybrid GraphRAG. Kontrak berada di
[coordinator graph](graph-assembly-coordinator.md).

## Revisi, lingkungan, dan bukti

Implementasi dan tes: commit `3a6e385`, 2026-10-09, Go 1.26.8 windows/amd64,
PostgreSQL 16.8 disposable lokal. Fixture sintetis dari semantic registry integration
test menyimpan EXTRACT/kandidat berhash, alias, profil dan keputusan ke database nyata.
Raw log: `artifacts/verification/20261009-candidate-revalidation/`.

| Pemeriksaan | Expected dan actual | Bukti / exit |
| --- | --- | --- |
| `go test ./src/server/internal/adapters/postgres -run 'TestRegistryCandidateView\|TestSemanticRegistryAgainstPostgres' -count=1 -v` | Konteks sama diterima; lookup/profile/alias berubah, bytes palsu, corpus salah, scope berlebih dan revision tidak sah ditolak; PASS | `context-elevated.log`, 0 |
| Reviewer independen `verify_index_jobs`, targeted tests yang sama | Inspeksi boundary dan rerun PostgreSQL; tidak ada blocker ditemukan; PASS | `independent.log`, 0 |
| `go test ./... -count=1` dari src/server | Regresi seluruh package Go lulus dengan DSN PostgreSQL aktif; tes Qdrant gated belum aktif pada run ini | `go-all.log`, 0 |
| `go vet ./...` dari src/server | Tidak ada temuan; PASS | `go-vet.log`, 0 |
| `go test ./internal/adapters/qdrant ./internal/indexing -count=1 -v` dengan DSN dan `REGULAGRAPH_TEST_QDRANT_ENDPOINT` | Qdrant readiness serta PostgreSQL/Qdrant publication nyata lulus; native opt-in dan fixture Rust tidak dijalankan | `backends.log`, 0 |

Kasus integrasi membekukan candidate batch, membuat canonical identity lain yang
tidak memengaruhi lookup, lalu menambahkan alias pada lookup yang semula kosong.
Revision baru yang tidak terkait tetap diterima. Penambahan alias mengembalikan
`ErrResolutionReplan`, sementara read revision sebelumnya tetap diterima. Unit cases
memeriksa perubahan label/revision profil, kandidat tidak terpilih, support alias,
hilangnya kandidat/alias/scope, duplicate/mismatched scope, serta urutan record yang
berbeda tanpa perubahan isi. Candidate batch asli tidak diubah.

Run awal `context.log` gagal karena akses cache compiler dalam sandbox; bukan hasil
pengujian produk. Rerun dengan akses toolchain yang diperlukan lulus. Pada regresi
Go awal, variabel Qdrant memakai nama yang tidak dibaca runner; karena itu hasil tersebut
tidak diklaim sebagai bukti tes Qdrant nyata. Native model/worker dan gold tidak dijalankan
untuk perubahan reader Go ini.

## Batas dan integrasi berikutnya

Reader melakukan satu lookup historis batch setelah autentikasi dua artefak dan
validasi domain. Semua profil kandidat dibandingkan, bukan hanya canonical ID yang
dipilih. Revision scope exact juga dibandingkan sehingga perubahan lalu pengembalian
isi tidak tersamarkan. Caller harus memasok revision publication yang telah dibuktikan.

Dependency EXTRACT/BIND, snapshot membership, committed resolution receipt dan live
publication fence tetap merupakan kewajiban terpisah. Scope luas `canonical-registry`
tidak dianggap selesai oleh pembandingan alias-key ini. Guard equality revision pada
GraphDelta belum dilonggarkan. Jika commit RESOLVE sendiri mengubah konteks kandidat,
replan/reaffirmation eksplisit tetap diperlukan. Tidak ada klaim end-to-end graph baru.
Required kualitas, latency, throughput, dan biaya tetap **REQUIRED_UNMEASURED** sesuai
[target benchmark](../configs/benchmark-targets.yaml).
