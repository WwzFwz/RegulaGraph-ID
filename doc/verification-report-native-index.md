# Verifikasi indeks dengan inference native

Dokumen ini mencatat integrasi Rust population/worker, Go coordinator, C++ BGE-M3,
PostgreSQL dan Qdrant pada 2026-10-09 di atas revision `ecba96b`. Input sumber
adalah fixture CHUNK Rust, dengan checkpoint/source job sintetis; hasil embedding
dan statistik dihitung executable sebenarnya. Ini bukan run seluruh corpus PDF,
anotasi gold, generation jawaban, atau acceptance benchmark release.

`TestNativeIndexPipeline` melakukan source snapshot preparation, vocabulary Rust,
dictionary PostgreSQL, freeze statistik Rust, bootstrap inventory, claim/executor
Go, RPC worker Rust, inference C++ BGE-M3, admission output, publication Qdrant,
query embedding native, fusion dense/BM25 dan hidrasi sumber. Enam chunk menjadi
tiga child job dan enam evidence items; semua hasil tetap terikat snapshot serta
source refs. Collection dan schema database terisolasi dan dibersihkan oleh tes.

Run pertama gagal: Go mengharuskan media typed protobuf generik untuk
DocumentBatch, sedangkan Rust memakai vendor media. Perbaikan menerima vendor
`application/vnd.regulagraph.document-batch+protobuf` dan alias typed persis
`application/x-protobuf; message=regulagraph.v1.DocumentBatch` pada reader kedua
bahasa. Writer Rust tetap vendor; source binding Go mempertahankan label sumber
untuk replay metadata identik. Label umum atau tipe lain tidak diterima; checks
hash, bytes, schema, registry authority dan semantik tetap wajib.

| Pemeriksaan | Hasil |
| --- | --- |
| Native cross-runtime pipeline setelah perbaikan | PASS, `pipeline-second.log` |
| Reviewer independen `/root/verify_index_jobs` | PASS, `independent.log`, termasuk run native aktual |
| Seluruh paket Go dengan PostgreSQL/Qdrant | PASS, `go-all.log`; native opt-in dijalankan terpisah |
| Rust reader media compatibility | PASS, `rust-media-final.log`, 3 tes |
| Kualitas hukum, Recall/nDCG gold, required latency/throughput | NOT_MEASURED |

Raw logs berada di `artifacts/verification/20261009-native-index/`. Go1.26.8
Windows amd64; PostgreSQL16.8-alpine, Qdrant1.18.0; Rust worker dev build; model
BAAI/bge-m3 revision `5617a9f61b028005a4858fdac845db406aefb181`, ONNX Runtime1.22.0
CUDA FP16 bundle lokal. Lama keseluruhan tes bukan latency inference maupun SLA.
Native stdout mencatat readiness; model tidak dimuat ulang per child/query.

Untuk reproduksi, hidupkan disposable PostgreSQL/Qdrant, native model terpin dan
worker Rust dengan root artefak khusus pengujian mengikuti [native](native-inference.md)
dan [worker](index-build.md). Gunakan fixture population Rust (lihat
[laporan population](verification-report-lexical-population.md)), kemudian set:

```text
REGULAGRAPH_TEST_NATIVE_INDEX=1
REGULAGRAPH_INDEX_FIXTURE_DIR=<folder fixture Rust dengan source.pb dan objects/>
REGULAGRAPH_TEST_INDEX_ARTIFACT_ROOT=<root yang sama dengan worker>
REGULAGRAPH_TEST_LEXICAL_EXE=<absolute regulagraph-lexical executable>
REGULAGRAPH_TEST_NATIVE_ENDPOINT=<loopback host:port>
REGULAGRAPH_TEST_WORKER_ENDPOINT=<loopback host:port>
REGULAGRAPH_TEST_POSTGRES_DSN=<disposable database>
REGULAGRAPH_TEST_QDRANT_ENDPOINT=<disposable Qdrant URL>
REGULAGRAPH_TEST_NATIVE_EMBED_MANIFEST=<native model.pbjson>
REGULAGRAPH_TEST_NATIVE_EMBED_SHA256=<SHA-256 exact JSON bytes>
```

Jalankan `go test ./src/server/internal/indexing -run '^TestNativeIndexPipeline$'
-count=1 -v`. Opt-in tanpa prasyarat lengkap gagal; tanpa opt-in tes dilewati.
Skip bukan PASS native. Worker harus memakai manifest model sama dalam C01 biner.
Binding job/checkpoint dalam tes tidak menggantikan verifikasi ingestion corpus.
