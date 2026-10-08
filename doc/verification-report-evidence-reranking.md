# Verifikasi reranking bukti terhidrasi

Dokumen ini mencatat bukti integrasi reranker pada jalur retrieval-answer dan
CLI operator. Ia membedakan tes invariant, integrasi storage, dan inference nyata
dari kualitas gold serta acceptance benchmark yang belum diukur.

Paket 2026-10-09 berbasis revision `15f9db5`; fingerprint kode final dan raw log
berada di `artifacts/verification/20261009-evidence-reranking/results.json`.
Toolchain Go 1.26.8 windows/amd64. PostgreSQL 16.8-alpine dan Qdrant 1.18.0
disposable memakai port loopback 55448/56348, tanpa volume corpus pengguna.

## Input, proses dan output

Input adalah EvidenceBundle terhidrasi dan RequestContext dengan snapshot/corpus
yang sama, model RERANK terpin, serta budget kandidat/pasangan/byte. Semua halaman
request dibentuk dan diperiksa sebelum RPC pertama. Proses mempertahankan semua
bukti, provenance cabang, missing dependency dan status completeness, menggunakan
deadline caller, dan mengorelasikan hasil berdasarkan pasangan/model/request.
Output menyusun ulang evidence dan skor secara stabil; skor seri mengikuti fusion.
Truncation, hasil parsial/hilang dan kegagalan inference menghentikan generation.

CLI memuat `model.pbjson` native yang SHA-256 byte-nya dibekukan operator, memeriksa
capabilities dan mengekspor skor/model beserta jumlah batch dan durasi. Tanpa
konfigurasi reranker, profil tetap baseline fusion eksplisit. File konfigurasi
tidak mengubah wire schema C01, model weights atau target benchmark.

## Pemeriksaan

| Pemeriksaan | Perintah / bukti | Hasil |
| --- | --- | --- |
| Invariant dan integrasi storage | `go test ./src/server/internal/retrieval ./src/server/internal/workflows ./src/server/internal/config ./src/server/cmd/cli ./src/server/internal/indexing -count=1`, `targeted.log` | PASS, exit 0 |
| Seluruh Go dengan PostgreSQL/Qdrant tersedia | `go test ./src/server/... -count=1`, `go-all.log` | PASS, exit 0; tes opt-in lain tanpa prasyarat dapat SKIP |
| Loader format native final dan CLI | `go test ./src/server/internal/config ./src/server/cmd/cli -count=1`, `config-final.log` | PASS, exit 0 |
| Static analysis komponen terdampak | `go vet` pada lima paket di atas, `vet.log` | PASS, exit 0 |
| Reranker native nyata | `go test ./src/server/internal/retrieval -run TestEvidenceRerankingNativeIntegration -count=1 -v`, `native-integration.log` | PASS, exit 0 |
| Review independen | Agent `verify_evidence_reranking`, diff dan tes terfokus, termasuk loader ProtoJSON final | Tidak ada temuan substantif terbuka |
| Kualitas gold / workload required | Belum dijalankan | NOT_MEASURED |

Expected vs actual: input invalid ditolak sebelum inference, skor hilang/duplikat/
salah model tidak menjadi evidence, request tetap dalam budget protobuf, ties
stabil lintas batch, generation tidak dipanggil sesudah failure, dan lease storage
dilepas setelah query. Tes published RAG memakai PostgreSQL/Qdrant nyata dan
embedding/reranker/generator sintetis; jalur answer dan evidence-only keduanya
memanggil reranker. Reviewer memeriksa tes tersebut dan raw log tetapi tidak
menjalankan ulang database integration secara independen.

Tes native terpisah menggunakan BAAI/bge-reranker-v2-m3 revision
`953dc6f6f85a1b2dbfca4c34a2796e7dde08d41e`, ONNX Runtime 1.22.0 CUDA FP16,
manifest SHA-256 `8a991fedf10dc7209be04a0bd9d3c5ce9ab10802fc452986509c5cb4aae6cb4a`.
Tiga teks fixture Indonesia diproses dua batch dengan token accounting nyata,
identitas model cocok dan urutan skor menurun. Durasi diagnostik satu panggilan
tercatat pada raw log; bukan p95/p99, bukan ukuran nDCG, dan bukan hasil workload
referensi. Model nyata belum digabungkan dengan corpus pengguna dalam tes ini.

## Batas yang masih terbuka

Graph retrieval/path completion, perluasan konteks parent/exception, tokenizer
prompt generator, API/CLI jawaban, dan scheduler INDEX durable masih pekerjaan
lanjutan. Pengujian ini tidak menutup seluruh Q01/A01. Gold tetap mengikuti
urutan yang disetujui pengguna; target required pada benchmark-targets.yaml
tidak berubah. Lihat [konfigurasi query](query-evidence.md) untuk penggunaan.
