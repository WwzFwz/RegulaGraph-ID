# Verifikasi rendering graph sampai draft bersitasi

Laporan ini mencatat boundary A01 setelah revision `f3262b4c0f55dd827f1a08917ada2bf5a6392427` pada 2026-10-09: immutable graph rendering, packing, generator preflight, citation mapping dan validasi akhir. Status scope ini PASS, termasuk review independen `verify_index_jobs`. Scope ini tidak menyelesaikan graph query routing/fusion atau quality acceptance. Kontrak ada pada [graph-context.md](graph-context.md).

Raw log, fingerprint SHA256, toolchain dan hasil reviewer berada di `artifacts/verification/20261009-graph-context/`. Go 1.26.8 windows/amd64; fixture native menggunakan Rust worker, PostgreSQL, Qdrant dan Neo4j nyata. Model EXTRACT/embedding/generator serta token counter tetap sintetis.

| Pemeriksaan | Bukti | Hasil |
| --- | --- | --- |
| Seluruh package Go | `go test ./src/server/...`, `go-test.log` | exit 0 |
| Static checks | `go vet ./src/server/...`, `go-vet.log` | exit 0 |
| Context/generator/claim membership | `go test ./src/server/internal/answering -run 'GraphContext\|GraphPaths' -count=1 -v`, `claim-members.log` | exit 0 |
| Native integration | `go test ./src/server/internal/indexing -run '^TestNativeGraphAssemblyPipeline$' -count=1 -v`, `native.log` | exit 0 |
| Independent unit/native | `independent-unit.log`, `independent-final.log`, `independent-native.log`, `independent-results.json` | exit 0 |
| Final claim-path recheck | `independent-answer-paths.log`, `independent-claim-members.log` | exit 0 |

Expected: dua chunk yang menutup satu support wajib tetap ada untuk path lengkap; graph direction/support/labels harus hadir dalam prompt; penghilangan metadata kewajiban atau perubahan byte tidak boleh meloloskan COMPLETE. Actual: split-support dengan budget penuh menghasilkan context COMPLETE untuk fixture applicability yang eksplisit; budget satu chunk menghasilkan PARTIAL dengan path omitted. Metadata, teks dan rendered block yang diubah ditolak sebelum provider. Reordering item tetap sah, sedangkan mutasi traversal setelah plan dibuat tidak mengubah projection. Missing member dan entity foreign/closed ditolak oleh constructor.

Draft generator menerima projection graph dan mengembalikan sitasi sumber melalui mapper produksi. Klaim tetap UNREVIEWED/PARTIAL. Aplikasi hanya menambahkan path lengkap bila satu klaim menyebut seluruh anggota bukti; dua klaim terpisah yang union-nya mencakup path tidak cukup. Regresi memeriksa missing member, split claims, abstention serta injeksi path manual. Exact path yang sah diterima dan perubahan node ditolak.

Native pipeline menghasilkan satu path Rust aktual, menerbitkannya ke snapshot gabungan, menelusuri Neo4j, mengambil source chunks melalui Qdrant, mengautentikasi teks via PostgreSQL/artefak, lalu merender graph pada prompt generator dan menghasilkan cited fixture draft. Applicability fixture belum selesai sehingga context tetap PARTIAL. Ini memperluas bukti sebelumnya yang hanya menghasilkan draft lewat dense/BM25; seed query dan graph fusion belum dipasang pada endpoint publik.

Reviewer menemukan pinning item saja belum cukup: caller dapat menghapus RequiredPathSets/MissingDependencies dan mengganti completeness. Plan kini mem-pin metadata bundle juga. Repeated anchor concatenation diganti penampungan fragment dan join sekali; repeated membership scan diganti indeks path-to-members. Unit awal menangkap citation mapper yang masih hanya mengenali rendering teks biasa; plan sekarang diteruskan ke mapper tanpa melonggarkan exact byte check. Perubahan final diverifikasi ulang oleh reviewer.

Tidak ada klaim legal/semantic correctness atau hasil required benchmark. Real generator tokenizer parity, model quality/gold, latency/throughput/RSS acceptance, routing/fusion graph, applicability/exception resolution dan streaming tetap belum terverifikasi. Angka benchmark tidak diubah.
