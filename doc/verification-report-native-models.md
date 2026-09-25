# Verifikasi native embedding dan reranking

Dokumen ini mengikat implementasi M01/N01 untuk embedding/reranker ke bukti build,
integrasi lintas bahasa, output model nyata, dan pengujian kegagalan. Hasil diagnostik
lokal tidak menyatakan seluruh M01/N01 atau benchmark release telah lulus.

## Identitas dan ruang lingkup

Tanggal: 2026-09-25. Baseline sebelum pekerjaan: `361801f2c31b5f368826a38601c9dc34367bd3c1`.
Revision implementasi yang diverifikasi: `f189095` (termasuk commit komponen sebelumnya).
Raw log, reference, output, serta manifest fingerprint disimpan di
`artifacts/verification/20260925-native-models/`; direktori artefak diabaikan Git.
`run-manifest.json` mencatat hash source, binary, suite, input, dan laporan pada mesin ini.
Dokumentasi/status diperbarui dalam commit terpisah sesudah revision implementasi tersebut.

Aktif: export XLM-RoBERTa ke ONNX; validasi sumber/bundle terpin; tokenizer Rust C ABI;
session C++ warm; C01 gRPC embedding/reranking/capabilities; admission query/bulk, batching,
deadline/cancel, readiness setelah fault; client Go/Rust; reference/parity/load tooling.
Runtime request tidak menjalankan Python. Schema C01 dan target benchmark tidak diubah.
Integrasi indeks X01, retrieval menyeluruh Q01, dan generator A01 tidak ditutup oleh paket ini.

## Lingkungan dan model

Windows 10.0.26200, Ryzen 9 6900HS (8 core/16 logical), RAM 16,388,050,944 byte,
RTX 3060 Laptop 6144 MiB, driver 555.97. Ini berbeda dari profil Linux/16 core/64 GiB/
GPU 24 GiB pada suite. SDK: ONNX Runtime GPU 1.22.0, protobuf/protoc 34.1, gRPC C++
1.76.0, MSVC 2019 v14.29. Tokenizers Python/Rust 0.21.4, Transformers 4.51.3,
PyTorch 2.6.0+cu124, ONNX 1.18.0. Detail versi compiler lain ada dalam manifest run.

| Model | Revision sumber | Bundle |
| --- | --- | --- |
| BAAI/bge-m3 | `5617a9f61b028005a4858fdac845db406aefb181` | `artifacts/models/bge-m3-fp16-ort1220` |
| BAAI/bge-reranker-v2-m3 | `953dc6f6f85a1b2dbfca4c34a2796e7dde08d41e` | `artifacts/models/bge-reranker-v2-m3-fp16-ort1220` |

Keduanya memakai FP16 CUDA dan output FP32; embedding memakai CLS/L2, 1024 dimensi,
reranker menghasilkan satu raw logit. Receipt sumber, file SHA, katalog weights,
tokenizer, dan manifest diverifikasi; tidak ada API key atau model jarak jauh pada run.

SHA-256 byte manifest embedding:
`ac099586f048edb5caa1e13fa10ccbaaad5aef3e13f07c1b9bc88e1f279ca35d`.
SHA-256 byte manifest reranker:
`8a991fedf10dc7209be04a0bd9d3c5ce9ab10802fc452986509c5cb4aae6cb4a`.
Reference embedding `embedding-reference-v2.json`:
`2c6e13ab3b6eb2f4528ffe57fe42011d6a5b48a27df16ca8f59b42d9a9e74a0d`.
Reference reranker `rerank-reference.json`:
`4a84bc18f3b79902d6cee3f3b8a8bfeabf4e664f8def2effaea34d1baf8affd1`.

## Pemeriksaan correctness

Semua perintah pada tabel selesai dengan exit code 0. Prasyarat dan environment lengkap
untuk reproduksi berada pada [panduan runtime](native-inference.md).

| Perintah / cakupan | Expected dan actual | Raw log |
| --- | --- | --- |
| `scripts/build_native.ps1` dengan SDK lokal sesuai panduan | Binary terbangun; CTest scheduler dan classifier termination **2/2 PASS** | `final-health-build.log` |
| `pytest tests/integration/test_native_runtime.py tests/unit/test_model_provenance.py tests/integration/test_model_export.py -q -p no:cacheprovider` | **20 PASS**: native process 7, provenance/load guard 10, export/reference 3 | `acceptance-code-final.log` |
| `cargo test -p regulagraph-inference-tokenizer --offline --target-dir .cache/native-tokenizer` | **2 PASS**: UTF-8/buffer ownership, no hidden truncation, hashing dan invalid handles | `tokenizer-tests.log` |
| `go test ./src/server/internal/adapters/inference ./src/server/internal/domain -count=1` dengan endpoint dan manifest BGE | Kedua paket **PASS**, termasuk integration opt-in ke model nyata | `go-bge-verified.log` |
| `go test ./src/server/internal/adapters/inference -run TestNativeExecutableIntegration -v -count=1` dengan environment yang sama | Explicit **RUN/PASS**, bukan SKIP, pada model BGE nyata | `go-bge-integration-verbose.log` |
| `cargo test -p regulagraph-ingestion real_native_embedding_transport --offline --target-dir .cache/cargo-target -- --ignored` dengan manifest BGE biner | **1 PASS, 0 ignored**: readiness, embedding, partial overlength | `rust-bge-verified.log` |
| Client Rust response guards (filter `native_inference`) | Batch 128 x 1024 diterima; missing/duplicate/nonfinite/drift ditolak | `rust-native-client-final.log` |
| Native tokenizer probe dibanding reference, termasuk input tepat 8192 token | IDs identik; **PASS** untuk tokenisasi, bukan inference tensor sepanjang 8192 | `boundary-result.log`, `boundary-*-tokens.json` |

Tests ONNX fixture memakai proses C++ dan transport gRPC sungguhan. Mereka membuktikan
admission saat bulk penuh, deadline tua, cancellation, fault/readiness, missing/tampered
sidecar, serta startup valid; fixture tidak mengukur kualitas BGE. Native integration Go
juga memeriksa korelasi concurrent request, manifest drift, duplikat ID, dan overlength
tanpa memotong input secara tersembunyi.

Log test Python berisi dua Torch TracerWarning dan diagnostic Windows/pyarrow ketika
mengimpor dependency global; proses selesai exit 0 dengan seluruh tes lulus. Environment
lokal memakai venv dengan system packages. Reproduksi bersih dianjurkan memakai environment
terisolasi sesuai versi terpin; hasil ini tidak menyatakan kompatibilitas semua paket global.

## Parity numerik model nyata

Enam input diagnostik per task mencakup bahasa formal, informal, typo, code-switch,
Unicode, dan teks panjang. Panjang embedding 10–983 token; pair reranker 20–992 token.
Reference menyimpan hasil FP32 dan serving precision dari model sumber yang sama.
Input ini belum dataset gold; tidak mengukur Recall/nDCG atau kebenaran jawaban hukum.

| Pemeriksaan | Hasil akhir |
| --- | --- |
| Embedding native vs reference | 6/6 hasil, 0 error; token IDs identik |
| Minimum cosine embedding terhadap FP32 | 0.9999978035 |
| Maksimum absolute error embedding terhadap FP32 / serving precision | 0.0004215986 / 0.0005204976 |
| Reranker native vs reference | 6/6 hasil, 0 error; token IDs identik |
| Maksimum absolute error raw logit terhadap FP32 / serving precision | 0.0040540695 / 0.0078125 |

Perintah: `python -m tooling.models.parity native --reference <reference.json> --endpoint
127.0.0.1:50055 --token-probe <native-tokens.json> --output <verified.json>`.
Raw hasil: `embedding-verified.json`, `reranker-verified.json`; status
**MEASURED_DIAGNOSTIC**, required gates **NOT_MEASURED**. Angka absolute error di atas
dilaporkan sebagai pengukuran, bukan toleransi baru yang menggantikan suite.

## Beban lokal dan perbaikan kegagalan

Dua session tetap dimuat dalam satu proses, tanpa cache hasil. Load generator open-loop
mempertahankan semua arrival dan menghitung deadline/latency sejak arrival terjadwal.
Run akhir memakai satu item/pair per request, max-in-flight 32, deadline 2000 ms,
serta siklus keenam input diagnostik yang sama. Embedding 100 request pada 10 RPS;
reranker 40 request pada 2 RPS, dijalankan berurutan.

| Run akhir | Berhasil / ditawarkan | p50 | p95 | p99 |
| --- | --- | --- | --- | --- |
| Embedding | 100 / 100 | 27.79 ms | 88.45 ms | 93.23 ms |
| Reranker | 40 / 40 | 30.27 ms | 326.33 ms | 330.00 ms |

Raw: `embedding-load-verified.json`, `reranker-load-verified.json`, log senama.
Perintah `python -m tooling.models.load` memakai `--requests 100 --rps 10 --purpose query`
untuk embedding dan `--requests 40 --rps 2` untuk reranker, dengan endpoint/reference di atas.
Ini **MEASURED_DIAGNOSTIC**, bukan PASS benchmark: sampel/durasi, hardware, panjang input,
dan jumlah pair berbeda dari workload required. Tidak ada confidence interval atau peak
memory acceptance yang disimpulkan dari run singkat ini.

Kegagalan sebelumnya tetap disimpan: `embedding-load-final.json` hanya 71/100 berhasil,
lalu CUDA OOM; `reranker-load-final.json` 0/40 karena context sudah gagal. Exit code load
saat itu 1. Quantile completion untuk arrival yang gagal tetap infinity (JSON null dengan
status eksplisit), tidak dihitung sebagai latency cepat yang lulus. `final-models.err.log`
mencatat OOM pada operasi Transpose dan kegagalan CUDA lanjutan.

Perbaikan mengganti pertumbuhan arena CUDA pangkat dua dengan `kSameAsRequested`.
Workload embedding yang sama diulang **tiga kali pada satu proses tanpa restart**, masing-masing
100/100 berhasil (`embedding-memory-1/2/3.json`), lalu kedua task diuji lagi pada build final
sebagaimana tabel. Sampling `nvidia-smi` sesudah load final menunjukkan 3143 MiB total GPU used;
angka ini termasuk proses lain dan bukan peak VRAM khusus layanan.

Perbaikan recovery menonaktifkan readiness kedua lane setelah fault eksekusi/validasi dan
menolak kerja berikutnya hingga restart. Temuan reviewer bahwa cancel bersamaan dengan
OOM dapat menyembunyikan fault telah diperbaiki: hanya termination ORT terkonfirmasi menjadi
exception cancellation; exception lain selalu menonaktifkan readiness. Regression CTest
menguji kombinasi OOM+cancel dan pesan/status yang tidak cocok; process test menguji
invalid tensor setelah warmup serta readiness/UNAVAILABLE sesudahnya.

## Review independen dan status tersisa

Agent `verify_native_client` meninjau Go boundary dan menjalankan integration fixture.
Agent `verify_model_tooling` memeriksa sumber/hash/reference, denominators dan status load;
temuan source attribution, nonfinite/empty inputs, snapshot pembacaan, serta quantile gagal
ditutup dengan regression tests. Agent `verify_native_runtime` meninjau session, tokenizer,
Rust client, queues, scanner, dan failure handling. Review terakhir menjalankan ulang
CTest **2/2 PASS** serta native process **7/7 PASS**; tidak ada blocker tersisa dalam scope
tersebut. Scanner juga diuji dengan path traversal, offset/range invalid, dan dependency
tersembunyi dalam atribut/subgraph (`verifier-scanner-adversarial.log`, 5 pemeriksaan PASS).

| Area | Status |
| --- | --- |
| Implementasi embedding/reranker native dan correctness yang diuji | **PASS** sesuai cakupan di atas |
| Numerical parity dan load singkat GPU lokal | **MEASURED_DIAGNOSTIC** |
| PARITY Recall/nDCG dan absolute model quality pada human gold | **BLOCKED**: dataset gold dan indeks pembanding frozen belum tersedia |
| MODEL latency/throughput/queue, workload batch 50 reranker, indexing batch 32, serta isolation acceptance | **NOT_MEASURED** pada profil required; resource mesin berbeda dan workload lengkap belum dijalankan |
| Numerical inference seluruh rentang hingga 8192 token, cold-start dan peak memory | **NOT_MEASURED**; tokenization boundary saja telah diuji |
| M01 menyeluruh termasuk OCR/parser/model lain | Tetap mengikuti [development plan](development-plan.md); belum ditutup oleh pekerjaan embedding ini |
| Deployment/O01 | Tidak dikerjakan, sesuai instruksi pengguna |

Target lama tetap berlaku. Langkah berikutnya adalah memakai client native untuk indeks X01,
menghubungkan gold frozen ke evaluasi retrieval/reference, dan menjalankan suite penuh pada
hardware yang memenuhi asumsi. Build/parity fixture maupun diagnostik di sini tidak boleh
diubah menjadi status release PASS tanpa bukti tersebut.
