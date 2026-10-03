# Verifikasi branch retrieval dan workflow draft jawaban

Dokumen ini mencatat bukti integrasi library Q01/A01 untuk pencarian dense/BM25,
hidrasi kandidat, dan draft jawaban bersitasi. Perannya membedakan kode yang sudah
diuji dari jalur PDF produksi, kualitas model, serta target latency yang belum
dibuktikan. Ini bukan pernyataan bahwa seluruh RAG siap digunakan.

## Revision dan lingkungan

Tanggal: 2026-10-03. Baseline `076939a`; implementasi berada pada `65966df`
(branch retrieval dan fusion paralel), `0369b74` (generator/citation), serta
`5011324` (workflow retrieval-answer). Tes/fixture disimpan bersama kode tersebut.
Raw log: `artifacts/verification/20261003-rag-workflow/`; manifest file/hash dan
toolchain: `run-manifest.json` dalam direktori itu. Lingkungan Windows amd64,
Go 1.26.8, GOCACHE lokal `.cache/go-build`.

Qdrant memakai container disposable tanpa mount data pengguna, loopback port
56333, versi 1.18.0, digest image
`sha256:b3063c673f3973877c038eeecc392bad5011f072ee7892b56c9a8e204a3bdea9`.
Tes membuat dan menghapus collection unik `regulagraph_test_...`.
Vektor, metadata lexical, corpus dan jawaban model tes bersifat sintetis.
Native BGE, Ollama, PostgreSQL dan PDF pengguna tidak menjadi sumber jawaban run ini.

## Hasil dan reproduksi

Perintah dari root repositori. Exit 0 berlaku untuk tes yang dijalankan; tes
opt-in tanpa endpoint/DSN tetap dilewati.

| Pemeriksaan | Perintah / log | Exit | Hasil aktual |
| --- | --- | --- | --- |
| Seluruh paket Go | `go test ./src/server/... -count=1`; `go-all-final.log` | 0 | PASS suite lokal; integrasi opt-in PostgreSQL/native/Qdrant dilewati pada perintah ini. |
| Analisis Go | `go vet ./src/server/internal/retrieval ./src/server/internal/workflows ./src/server/internal/answering ./src/server/internal/adapters/inference ./src/server/internal/adapters/qdrant`; `go-vet-final.log` | 0 | PASS, tanpa diagnostik. |
| Qdrant nyata | Set `REGULAGRAPH_TEST_QDRANT_ENDPOINT=http://127.0.0.1:56333`, lalu `go test ./src/server/internal/retrieval -run TestRetrieveBranchesAgainstQdrant -count=1 -v`; `qdrant-branches-real.log` | 0 | PASS: create/upsert/readback, dense dan BM25 menemukan record pada seq 7; keduanya kosong pada seq 6. |
| Regresi audit generator | `go test ./src/server/internal/answering -count=1`; `go-audit-regressions.log` | 0 | PASS: token output nol, JSON duplikat/terlalu dalam, hash request, producer immutable dan pembatalan antrean. |
| Race detector | `go test -race` pada retrieval/workflows/answering; `go-race-unsandboxed.log` | 1 | BLOCKED: compiler Cygwin tidak mendukung cgo native Windows; perlu toolchain MinGW sesuai. Tidak ada klaim race PASS. |
| Kualitas dan performa release | Belum ada run corpus/gold/workload eligible | — | NOT_MEASURED; angka required tetap `configs/benchmark-targets.yaml`. |

Uji Qdrant membuktikan transport/visibility pada backend nyata. Vector dan bobot
sparse sintetis tidak membuktikan kualitas atau autentikasi generation produksi.
Durasi suite bukan benchmark latency request produksi.

## Input, proses, output dan review

Dense memeriksa model/generation, tujuan QUERY, truncation dan normalisasi vector
sebelum search. BM25 mempertahankan dictionary frozen; semua-OOV menghasilkan
kosong sah tanpa backend. Error tidak menjadi kosong/fallback. Workflow hybrid
menjalankan branch paralel, membatalkan sibling saat gagal, dan mempertahankan
rank/provenance RRF. Callback branch wajib menghormati cancellation; workflow
menunggu penyelesaiannya.

Hidrasi mewajibkan setiap kandidat diterima atau ditolak dengan alasan, snapshot
dan versi konsisten, serta provenance dari search. Reviewer independen
`/root/verify_candidate_contract` menemukan callback dapat memutasi hasil search
yang menjadi acuan validasi. Perbaikan memberikan salinan mendalam ke callback.
Kasus adversarial mengubah versi callback dan evidence sekaligus; hasil ditolak
sebelum model. Reviewer memeriksa ulang perbaikan dan menjalankan tes RAG.

Generator hanya menghasilkan abstention aplikasi atau draft PARTIAL dengan claim
UNREVIEWED. URL berasal dari lookup tepercaya; aplikasi menghitung span byte
UTF-8. JSON ambigu/berlebih, evidence asing, token accounting tidak cocok, dan
output di atas budget ditolak. Hash payload/schema dicatat tanpa memutasi producer
bersama. Slot admission meliputi preflight dan generation. Reviewer delta terakhir
tidak menemukan blocker dan menjalankan tes DraftGenerator. Regresi tambahan
token nol/hash/nesting dijalankan implementer sesudah review; tidak ada perubahan
production code sesudah review. Sitasi valid secara struktur bukan bukti entailment.

## Batas kesiapan dan pekerjaan berikutnya

Paket integrasi library telah diuji; Q01/A01 keseluruhan masih terbuka.
`CandidateHydrator` masih port, belum implementasi storage otoritatif. Counter
token fixture sintetis tidak boleh dipakai pada produksi. Jalur runnable dari PDF
menuju jawaban masih membutuhkan:

1. Writer INDEX X01 dengan artefak generation terpin, readback dan publication.
2. Admission snapshot serta katalog generation tepercaya yang memegang read lease.
3. Hidrasi batch storage yang memverifikasi hash, membership, span, URL sumber,
   dan kebijakan tanggal sebelum menerima evidence.
4. Tokenizer/template generator yang tepat, model lokal terpin, serta wiring API/CLI.

Reranking penuh, graph profile, streaming, support assessment, gold dan acceptance
tetap mengikuti rencana menyeluruh. Jalur pertama Vector/Hybrid RAG tidak mengubah
kriteria release Hybrid GraphRAG. Model tidak dapat menyatakan klaimnya sendiri
SUPPORTED; kualitas jawaban dan kebenaran hukum belum dibuktikan oleh tes ini.
