# Native embedding dan reranking

Dokumen ini menjelaskan implementasi M01/N01 untuk model embedding/reranker, bundle terpin,
build lokal, serta cara mengumpulkan bukti dari runtime C++ yang sama dengan serving.
Ini bukan pernyataan bahwa seluruh M01, N01, atau acceptance release telah lulus.

## Komponen dan kontrak

`src/inference` menyediakan `regulagraph_inference_server`, session ONNX yang dimuat sekali,
tokenizer melalui C ABI Rust, serta layanan C01 `EmbedBatch`, `RerankBatch`, dan `GetCapabilities`.
Go `NativeClient` dan Rust `NativeEmbeddingClient` menggunakan koneksi reusable dan memvalidasi
manifest serta korelasi setiap hasil. Binding berasal dari schema yang sudah ada; tidak ada
perubahan baseline C01. Index publication, workflow retrieval, dan generator LLM tetap milik
komponen masing-masing; tersedianya client belum mengaktifkan seluruh pipeline X01/Q01.

Ekspor embedding memakai CLS dan normalisasi L2 FP32 pada keluaran XLM-RoBERTa. Reranker memakai
satu raw logit, tanpa sigmoid atau klaim confidence terkalibrasi. Kandidat yang diuji lokal:

| Task | Model | Revision upstream |
| --- | --- | --- |
| Embedding 1024 dimensi | BAAI/bge-m3 | `5617a9f61b028005a4858fdac845db406aefb181` |
| Reranking | BAAI/bge-reranker-v2-m3 | `953dc6f6f85a1b2dbfca4c34a2796e7dde08d41e` |

Backend bundle adalah ONNX Runtime **1.22.0**, opset 17, FP16/CUDA atau FP32/CPU. Tokenizer
Rust dan Python dipin ke **0.21.4**. Artefak model besar berada dalam `artifacts/models` yang
diabaikan Git. Pemilihan untuk release masih memerlukan gold quality, parity Recall/nDCG,
dan workload reference; model yang berhasil dimuat belum otomatis terpilih untuk release.

## Integritas bundle

Bundle berisi `model.pbjson`, `weights.json`, `model.onnx`, `tokenizer.json`, `export.json`, dan
opsional `model.onnx.data`. Hash manifest diberikan oleh operator melalui argumen proses.
`weights_hash.sha256` mengikat byte katalog; katalog mengikat setiap file bobot. Scanner
streaming memeriksa initializer serta tensor dalam atribut/subgraph, lokasi sidecar, dan
rentang offset/length sebelum ONNX memuatnya. Training graphs, local functions, serta sparse
tensors tidak termasuk profil ekspor ini dan ditolak. Bundle harus immutable selama proses
berjalan dan hanya dapat ditulis oleh operator tepercaya; ini bukan endpoint unggah model.

Tool export membaca snapshot lokal, memeriksa receipt Hugging Face untuk commit dan ETag
(SHA-256 LFS atau Git blob SHA-1), serta mencatat hash seluruh input loader. Receipt adalah
bukti downloader tepercaya, bukan tanda tangan kriptografis penerbit. Penambahan atau perubahan
input loader menyebabkan validasi reference gagal. Fixture lokal harus memakai opsi
`--local-fixture` dan identitas `fixture:`; tidak boleh diklaim sebagai model upstream.

## Batas runtime

Request maksimum 128 item dan 4 MiB, dengan batas model sampai 8192 token per sequence/pair.
Input melampaui kapasitas menghasilkan error per item, tanpa pemotongan diam-diam. Chunking
harus menangani input panjang secara eksplisit. Scheduler membatasi internal batch ke 32 item,
membentuk subbatch menurut panjang aktual dan batas padded tokens (default 16384), serta
memisahkan antrean query dan bulk. Delapan RPC aktif dibatasi lagi menjadi maksimum dua bulk,
sehingga bulk tidak menghabiskan semua slot online. Queue payload token dibatasi 64 MiB.

Satu worker compute dibagi antar-model dan bergiliran antar-lane. Query mendapat prioritas
dengan fairness bulk. GPU kernel yang sudah berjalan tidak dipreempt oleh query baru; biaya
batch bulk tetap harus diprofilkan pada beban gabungan. Cancellation menghapus item antrean;
ONNX run dibatalkan ketika seluruh item aktif sudah batal/kedaluwarsa. `inference.total`
mencakup tokenisasi dan antrean; `queue_ns` adalah maksimum waktu antre item setelah tokenisasi,
bukan hanya delay sengaja pembentukan batch. Jangan memakai field ini langsung sebagai
`MODEL.BATCH_WAIT_P99` tanpa memisahkan definisi workload/telemetry yang tepat.

Listener saat ini hanya loopback tanpa TLS. Runtime memanaskan model sebelum mengumumkan
ready dan menolak model/version/hash berbeda. Shutdown SIGINT/SIGTERM memberi masa drain RPC;
packaging, TLS, supervisor, serta remote deployment tetap berada pada O01 dan di luar tugas ini.

Arena CUDA memakai `kSameAsRequested` agar kapasitas yang ditahan tidak tumbuh menurut pangkat
dua pada tiap session untuk input dengan panjang berubah. Ini memperbaiki OOM pada pengulangan
workload lokal; bukan jaminan seluruh ukuran batch/sequence muat pada setiap GPU. Fault eksekusi
atau validasi model menonaktifkan readiness seluruh layanan dan request berikutnya menerima
UNAVAILABLE sampai restart. Cancellation terkonfirmasi memakai exception tersendiri; OOM yang
bertepatan dengan cancel tetap fault. Pengenalan termination mengikuti kode/pesan tepat pada
[ORT 1.22.0](https://github.com/microsoft/onnxruntime/blob/v1.22.0/onnxruntime/core/framework/stream_execution_context.cc#L214),
sehingga upgrade backend harus memverifikasi ulang perilaku ini.

## Build dan menjalankan

Prasyarat: Rust, C++17/CMake, protobuf/protoc **34.1**, gRPC C++ **1.76.0**, serta SDK ONNX Runtime
**1.22.0** sesuai arsitektur. Build dependency protobuf dan gRPC dengan toolchain/ABI yang sama.
Header SDK, import/static library, serta shared library ONNX harus berasal dari versi yang sama.
Untuk CUDA, sediakan library CUDA/cuDNN yang cocok; pengujian lokal memakai CUDA 12.4/cuDNN dari
instalasi PyTorch. DLL ONNX diletakkan di samping executable agar Windows tidak memuat DLL
sistem lain lebih dahulu. API ONNX yang terlalu lama sekarang ditolak sebelum session dibuat.

PowerShell, sesudah SDK tersedia:

Script memakai `cargo --locked --offline`; isi cache dependency dari lockfile terlebih dahulu
dengan `cargo fetch --locked` pada lingkungan yang memiliki akses registry. SDK path pada contoh
adalah instalasi lokal yang disediakan operator, bukan file yang ikut di Git.

```powershell
./scripts/build_native.ps1 -OrtRoot .cache/ort-wheel-sdk `
  -DependencyPrefixes .cache/protobuf-install,.cache/grpc-install `
  -Protoc .cache/contracts-tools/protoc/bin/protoc.exe
```

Untuk toolchain lain, gunakan `cargo build -p regulagraph-inference-tokenizer --release`, lalu
configure CMake dengan `REGULAGRAPH_MODEL_RUNTIME=ON`, `CMAKE_PREFIX_PATH`, `REGULAGRAPH_ORT_ROOT`,
`REGULAGRAPH_TOKENIZER_LIBRARY`, dan `REGULAGRAPH_PROTOC`. Build default tanpa opsi tersebut tetap
menyediakan scheduler tanpa SDK/model. Build tidak mengunduh weights atau memulai endpoint.

Contoh menjalankan bundle lokal (ganti pin dengan SHA-256 byte manifest aktual):

```powershell
./.cache/native-model-build/Release/regulagraph_inference_server.exe `
  --bundle artifacts/models/bge-m3-fp16-ort1220 --manifest-sha256 <embedding-pin> `
  --reranker artifacts/models/bge-reranker-v2-m3-fp16-ort1220 --reranker-sha256 <reranker-pin> `
  --listen 127.0.0.1:50054 --threads 4 --batch-tokens 16384
```

## Ekspor dan verifikasi

Gunakan virtual environment offline tooling dengan extras `models`; `model-tests` menambah
ORT Python CPU untuk tes exporter. Runtime produk tidak memanggil Python. Generate C01 Python
bindings dan tambahkan folder generated serta root repo ke `PYTHONPATH`. Hindari mencampur paket
ML global dengan versi lain. Pilihan wheel PyTorch CUDA harus sesuai mesin reference.

```text
python -m tooling.models.export --source <pinned-local-snapshot> --output <new-bundle> --model-id BAAI/bge-m3 --revision 5617a9f61b028005a4858fdac845db406aefb181 --task embed --precision fp16 --provider cuda --max-tokens 8192
python -m tooling.models.parity reference --source <snapshot> --bundle <bundle> --cases <cases.json> --output <reference.json>
regulagraph_token_probe <bundle> <manifest-sha256> <cases.json> <tokens.json>
python -m tooling.models.parity native --reference <reference.json> --endpoint 127.0.0.1:50054 --token-probe <tokens.json> --output <parity.json>
```

Cases berupa array `{id,text}` untuk embedding atau `{id,query,text}` untuk reranking. Reference
menyimpan token IDs, FP32, serving precision, source/model hashes, toolchain, dan GPU. Input JSON
dan hash dibekukan dari byte pembacaan yang sama. Parity menolak tensor nonfinite/nol, model drift,
ID hilang/duplikat, dan token accounting salah; hasil numerik tetap diagnostik tanpa human gold.

`tooling.models.load` menjalankan arrival berjadwal, mencatat seluruh arrival termasuk local
capacity rejection, error RPC/item, durasi native, dan latency dari arrival. Contoh diagnostik:

```text
python -m tooling.models.load --reference <reference.json> --endpoint 127.0.0.1:50054 --output <load.json> --requests 100 --rps 10 --purpose query
```

`time_to_outcome_all_arrivals_ms` mencakup error yang cepat dan tidak sama dengan latency
penyelesaian. Untuk `completion_latency_all_arrivals_ms`, arrival yang gagal/tidak selesai
bernilai infinity; JSON menuliskan `null` disertai `INFINITE_NONCOMPLETION` pada quantile terkait.
Deadline dihitung sejak arrival terjadwal, termasuk keterlambatan dispatch load generator.

Ini bukan pengganti harness E01 acceptance: input mix, batch 50 reranker, jumlah sampel, hardware,
quality, dan isolation workload wajib sesuai YAML untuk run yang dinilai. Tidak ada target yang
diubah atau status release PASS yang diberikan oleh tool ini.

Tes native opt-in memakai `REGULAGRAPH_NATIVE_BINARY` untuk
`pytest tests/integration/test_native_runtime.py`; fixture ONNX nyata tetap bukan BGE quality.
Tes Go memakai `REGULAGRAPH_TEST_NATIVE_ENDPOINT`, `REGULAGRAPH_TEST_NATIVE_EMBED_MANIFEST`, dan
`REGULAGRAPH_TEST_NATIVE_RERANK_MANIFEST`. Tes Rust ignored `real_native_embedding_transport`
memakai endpoint yang sama serta `REGULAGRAPH_TEST_NATIVE_EMBED_BINARY` (manifest protobuf biner).
Lihat [laporan bukti](verification-report-native-models.md) untuk hasil dan batas pengujian.
