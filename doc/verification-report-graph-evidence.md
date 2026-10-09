# Verifikasi pemetaan bukti graph

Laporan ini mencatat boundary discovery graph ke teks sumber terautentikasi, setelah revision `b41ec9d536e28e3eeb23249d33e4a761435e73d3` pada 2026-10-09. Status scope ini PASS; integrasi graph-to-answer dan acceptance keseluruhan belum selesai. Kontrak ada di [graph-evidence.md](graph-evidence.md).

Raw logs, perintah, toolchain dan SHA256 file kode berada di `artifacts/verification/20261009-graph-evidence/`, termasuk `independent-results.json`. Go 1.26.8 windows/amd64; backend lokal PostgreSQL/Qdrant/Neo4j dan worker Rust yang sama dengan [traversal](verification-report-graph-traversal.md). Model/extraction/review/embedding/generator tetap fixture sintetis.

| Pemeriksaan | Perintah / bukti | Hasil |
| --- | --- | --- |
| Seluruh Go package | `go test ./src/server/...`, `go-test.log` | exit 0 |
| Static checks | `go vet ./src/server/...`, `go-vet.log` | exit 0 |
| Scope/span/resource regressions | `go test ./src/server/internal/retrieval/graph -run GraphEvidence -count=1`, `guards-final.log` | exit 0 |
| Native integration awal | `go test ./src/server/internal/indexing -run '^TestNativeGraphAssemblyPipeline$' -count=1 -v`, `native.log` | exit 0 |
| Independent final, termasuk pin revoke | `go test ./src/server/internal/retrieval/graph ./src/server/internal/adapters/qdrant ./src/server/internal/indexing -run 'GraphEvidence\|SourceChunkLookup\|^TestNativeGraphAssemblyPipeline$' -count=1 -v`, `independent-final.log` | exit 0 |
| Existing search setelah decoder reuse | `go test ./src/server/internal/adapters/qdrant -run 'Search' -count=1 -v`, `independent-search.log` | exit 0 |

Expected: source/version/regulation harus berpasangan tepat, setiap span support tertutup tanpa gap/rune split, alternative support tetap tersedia, dan authority akhir tetap aktif. Actual: fixture Rust menghasilkan satu path yang dipetakan ke satu item teks terautentikasi melalui Qdrant dan PostgreSQL/artefak. Applicability fixture belum terselesaikan sehingga bundle tetap PARTIAL. Wrong scope serta pencabutan pin nyata pada pemeriksaan akhir ditolak. Tes juga meliputi split chunk, gap, source/version/regulation mismatch, foreign/closed/old visibility, qualifier/inferred assertion dan source alternatif.

Reviewer `verify_index_jobs` menemukan risiko clone sebelum budget, pertumbuhan salinan path dan pemindaian support berulang. Perbaikan memeriksa ukuran sebelum clone/append, menghitung aggregate path/assertion/support, serta mempersiapkan membership assertion-to-evidence sekali. Regresi membedakan input yang muat tetapi tambahan path tidak muat, dan input gabungan melebihi 16 MiB meskipun tiap path wire-valid. Verifikasi independen final tidak menemukan blocker dalam scope ini. Iterasi test awal sempat gagal karena nama field qualifier dan fixture ID melebihi batas C01; keduanya diperbaiki sebelum hasil final.

Input traversal wajib berasal dari admitted reader internal; admission tidak membuktikan arbitrary DTO buatan caller. Consumer masa depan wajib menjaga seluruh `PathEvidence` saat ranking/truncation, bukan hanya memeriksa satu salinan path ID. Seed linking, graph branch, renderer konteks, applicability/exception resolution dan jawaban graph belum dibuktikan oleh laporan ini. Gold, legal correctness, Recall/nDCG, latency/throughput/RSS acceptance tetap NOT_MEASURED; angka benchmark tidak berubah.
