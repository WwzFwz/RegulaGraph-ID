# Verifikasi graph branch dan fusion empat profil

Laporan ini mencatat komposisi Q01/A01 setelah revision `b9cdcd4c5e57fa5fd66fbca5afc5bec6492823e8` pada 2026-10-09: graph candidates, concurrent fusion, shared hydration, reranking, prepared resources dan draft. Scope serta batasnya dijelaskan di [graph-fusion.md](graph-fusion.md). Automatic linking alami, konfigurasi graph API/CLI, gold/model quality dan required benchmark acceptance belum termasuk hasil ini.

Raw log, toolchain dan fingerprint file berada di `artifacts/verification/20261009-graph-fusion/`. Go 1.26.8 windows/amd64, worker Rust ASSEMBLE, PostgreSQL, Qdrant dan Neo4j lokal nyata. Seed canonical, model extraction/embedding/reranker/generator serta penghitung token adalah fixture sintetis.

| Pemeriksaan | Bukti | Hasil |
| --- | --- | --- |
| Seluruh Go package | `go test ./src/server/...`, `go-test.log` | exit 0 |
| Static checks | `go vet ./src/server/...`, `go-vet.log` | exit 0 |
| Graph branch/fusion/authority | `go test ./src/server/internal/workflows -run 'GraphRAG\|GraphFusion' -count=1 -v`, `graph-unit.log` | exit 0 |
| Final package regression | `unit-final.log`, `independent-regression.log` | exit 0 |
| Native prepared profiles | `go test ./src/server/internal/indexing -run '^TestNativeGraphAssemblyPipeline$' -count=1 -v`, `native.log`, `independent-native.log` | exit 0 |

Expected: GRAPH_RAG menjalankan graph saja tanpa embedding; HYBRID_GRAPH_RAG menggabungkan dense/BM25/graph tanpa kehilangan asal ranking atau source proof. Actual native: satu cabang menghasilkan satu item evidence; tiga cabang menghasilkan dua item evidence. Keduanya menjalankan reranking, merender projection graph pada prompt, dan menghasilkan draft dengan sitasi sumber terverifikasi. Applicability yang belum selesai tetap PARTIAL. Seed kosong mempertahankan missing dependency dan menghasilkan application abstention tanpa memanggil provider.

Unit tests memeriksa callback mutation isolation, snapshot asing, nil entity/unused assertion, graph failure tanpa fallback, serta authority hilang setelah generation. Graph-only source lookup false positive diberi rejection eksplisit; evidence dari BM25 tetap ada walaupun tidak berada di span graph. Fixture lama sempat gagal karena panjang span sintetis tidak sama dengan panjang teks; fixture graph kini memakai panjang byte aktual, tanpa melonggarkan invariant produksi.

Reviewer menemukan ownership clone harus membatasi seluruh traversal, bukan hanya record yang terpilih pada path. Admission kini memeriksa entity/unused record serta aggregate bytes sebelum clone, termasuk batas stop reasons. Regresi memakai aggregate unused entity melebihi 16 MiB. Reviewer juga meminta graph prepared resources tidak hanya terikat binding vector: snapshot graph kini dipin dan rollover menuntut preparation baru. Bind mempunyai salinan metadata index/pin; native test memutasi input caller setelah Bind dan tetap memakai snapshot semula. Rollover test menyamakan copied pin dengan changed snapshot agar tidak sekadar menguji mismatch pin lama.

Verifikasi independen dilakukan oleh `verify_index_jobs` dengan hasil PASS_SCOPED; rerun rollover terakhir lulus pada `independent-rollover.log`. Status akhir, perintah final serta fingerprint ada pada `independent-results.json` dan cocok dengan file implementer sebelum commit. Tidak ada klaim recall/nDCG, entity linking accuracy, semantic/legal correctness, tokenizer parity, p95/p99/throughput/RSS acceptance atau deployment. Target numerik tidak diubah.
