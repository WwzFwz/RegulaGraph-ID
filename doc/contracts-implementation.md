# Implementasi kontrak C01

Dokumen ini menjelaskan bentuk kontrak yang sudah tersedia, cara membangun dan memverifikasinya, serta batas pemeriksaan C01. Ia menghubungkan rancangan di [system-contracts](system-contracts.md) dengan schema dan helper aktual; keberadaan message/service descriptor bukan layanan produksi yang berjalan.

## Sumber dan pemetaan

Tujuh proto produksi pada [v1](../src/contracts/proto/regulagraph/v1/README.md) dan [evaluation.proto](../evaluation/datasets/evaluation.proto) mendefinisikan 164 message, 32 enum, dan empat service descriptor. `common` memiliki primitive/presence/ID/provenance/dependency; `documents` memiliki sumber, struktur, versi dan chunk; `graph` memiliki identity/assertion/support, artefak EXTRACT/RESOLVE, path, dan registry; `evidence` memiliki indeks, retrieval dan context; `answers` memiliki claim/citation/stream; `jobs` memiliki worker/ledger/publication; `inference` memiliki model batch dan semantic gateway. Evaluasi merujuk tipe produksi yang sama.

`RecordMeta.record_id` adalah primary ID record pemiliknya, misalnya canonical ID pada CanonicalEntity, mention ID pada Mention, dan provision-version ID pada ProvisionVersion. ID referensi tetap memakai nama semantiknya. Jangan membuat ID baru ketika mengonversi bahasa. `DependencyManifest` ditempatkan di common agar import documents/graph/jobs tidak melingkar; Go jobs tetap pemilik pelaksanaan dependency dan publication.

Baseline pertama ada di [schema-lock.json](../src/contracts/schema-lock.json), turunan descriptor, bukan schema lain untuk diedit. `check_contracts.py` menolak perubahan/penghapusan tag, tipe, presence, aturan field, nilai enum dan signature RPC yang sudah tercatat; penambahan masih memerlukan review semantik. BIND dan CHUNK memakai nilai enum append-only 8/9 untuk kompatibilitas; rank domain menetapkannya setelah STRUCTURE dan sebelum EXTRACT. Jangan menjalankan `--write-baseline` untuk menyembunyikan perubahan tidak kompatibel. Baseline ini belum merupakan matriks kompatibilitas dua versi rilis historis; tambahkan producer/consumer lama-baru ketika ada revisi kedua.

Refresh baseline X01 setelah review kompatibilitas mencatat penambahan paired
filter, konteks resolusi K01, dan `IndexGeneration.embedding_input_policy` tag 8.
Descriptor baru tidak mengubah tag/tipe/presence lama. Reader binary lama tetap
dapat mengabaikan tag baru; publication generation baru mensyaratkan upgrade
atau isolasi semua route pembaca. Bukti dan daftar addition ada di
[laporan policy X01](verification-report-x01-input-policy.md).

## Pembagian validator

Go, Rust, C++ dan Python memiliki validator aturan descriptor serta invariant dasar bersama. Fixture lintas bahasa menguji hasil valid/invalid, unknown binary fields, optional presence, uint64, tanggal, offset Unicode, vector, ukuran, graph path dan corpus scope. Validasi tidak berarti semua aturan bisnis identik diterapkan di setiap runtime.

Go memiliki guard tambahan sesuai ownership: korelasi worker/model batch, publication receipts, evidence/context/answer dan urutan stream. Client Worker memanggil validasi wire dan `VerifyWorkerResponse`; guard menolak tipe artefak yang tidak cocok dengan stage, termasuk `ExtractionBatch` untuk EXTRACT dan `ResolutionBatch` untuk RESOLVE. Coordinator durable PARSE/STRUCTURE/CHUNK/EXTRACT membaca ulang artifact checksum-bound serta memeriksa stage records, dependency manifest, accounting, source/version evidence, model/prompt pin, exact mention bytes, dan boundary UTF-8 sebelum checkpoint; executor durable untuk RESOLVE dan stage graph berikutnya masih harus diimplementasikan. Response worker baru wajib mengisi `Checkpoint.terminal_status` sama dengan status response. Nilai `UNSPECIFIED` hanya dibaca untuk kompatibilitas checkpoint legacy dan tidak boleh diinfer sebagai sukses. Adapter lain wajib memanggil guard sebelum efek samping. Pemeriksaan authoritative registry, akses pengguna, visibilitas storage, dan keberlakuan hukum tetap memerlukan pemilik S01/I01/K01/A01.

`VerifyCitationEvidence` membutuhkan lookup metadata sumber tepercaya yang dipin ke snapshot. URL dan page locator harus berasal dari source blob yang cocok dengan versi citation. Containment SourceSpan saat ini memeriksa ID artefak dan batas rentang pada evidence; pemetaan artefak teks ke blob/versi dan UTF-8 teks aktual harus disediakan I01/A01 sebelum penerbitan citation. Pemeriksaan struktur tidak membuktikan bahwa isi pasal mendukung klaim.

Decoder Go/Rust/C++ membatasi bytes dan kedalaman sebelum/selama parse; Python membatasi bytes sebelum parse dan memeriksa kedalaman/jumlah item setelah parse menggunakan batas parser runtime bawaannya. Batas operasional ini bukan benchmark required. ProtoJSON menolak bridge yang akan kehilangan unknown binary fields; gunakan binary untuk round-trip data tersebut.

## Dependency dan build

Versi yang dipakai: Go toolchain 1.26.8, protoc 34.1, protobuf Go/protoc-gen-go 1.36.12, Python protobuf 7.34.1, Rust protobuf/protobuf-codegen 3.7.2 (Rust 1.87.0, Cargo.lock memin home 0.5.11), C++ protobuf 34.1.0 dan Abseil 20250512.1. C++ diuji dengan CMake dan Visual Studio 2019 x64/C++17. Jangan memasangkan generated C++ dengan runtime protobuf versi berbeda.

Dengan dependency terpasang, dari root:

```powershell
python scripts/generate_contracts.py
python scripts/check_contracts.py
go test ./src/server/...
go vet ./src/server/...
$env:CARGO_TARGET_DIR = "$PWD/.cache/rust-target"
cargo test --workspace --locked --offline
cmake -S src/contracts -B .cache/contracts-build -G "Visual Studio 16 2019" -A x64 -DCMAKE_PREFIX_PATH="$PWD/.cache/protobuf-install" -DREGULAGRAPH_PROTOC="$PWD/.cache/contracts-tools/protoc/bin/protoc.exe"
cmake --build .cache/contracts-build --config Release --parallel 4
python tests/integration/wire_roundtrip.py
```

`generate_contracts.py` menerima `--protoc` dan `--go-plugin`; default menunjuk cache lokal Windows. Siapkan plugin melalui `go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12` dengan GOBIN pada `.cache/contracts-tools/bin`. CLI Go yang dipakai harness dapat dipilih lewat `--go`; probe C++ lewat `--cpp`. Python perlu dependency pyproject serta `.cache/contracts/python` pada PYTHONPATH untuk memakai schema di luar harness. Instalasi package Python saja belum menghasilkan binding ini.

Generated Go dilacak di src/server/gen; Rust dihasilkan build.rs pada OUT_DIR; Python dan descriptor dihasilkan di .cache/contracts; C++ dihasilkan CMake pada build/generated. File generated tidak diedit tangan. Belum ada generated gRPC transport stub atau server HTTP/RPC aktif; integrasi transport berada pada paket runtime pemiliknya. Target inference C++ masih scaffold terpisah dari library kontrak.

Untuk membangun runtime C++ lokal, gunakan source protobuf resmi 34.1 dan Abseil 20250512.1 pada `.cache/contracts-tools`. Konfigurasi yang diuji:

```powershell
cmake -S .cache/contracts-tools/protobuf-34.1 -B .cache/protobuf-build -G "Visual Studio 16 2019" -A x64 -Dprotobuf_BUILD_TESTS=OFF -Dprotobuf_BUILD_PROTOC_BINARIES=OFF -Dprotobuf_BUILD_LIBPROTOC=OFF -Dprotobuf_BUILD_SHARED_LIBS=OFF -Dprotobuf_WITH_ZLIB=OFF -Dprotobuf_MSVC_STATIC_RUNTIME=OFF -Dprotobuf_BUILD_LIBUPB=OFF -DCMAKE_CXX_STANDARD=17 -DFETCHCONTENT_SOURCE_DIR_ABSL="$PWD/.cache/contracts-tools/abseil-cpp-20250512.1" -DCMAKE_INSTALL_PREFIX="$PWD/.cache/protobuf-install"
cmake --build .cache/protobuf-build --config Release --target install --parallel 4
```

LIBUPB dinonaktifkan karena tidak dipakai binding C++ ini dan build C library tersebut membutuhkan header yang tidak tersedia pada toolchain MSVC lokal. Runtime libprotobuf tetap dibangun. Arsip resmi yang digunakan: [protoc Windows](https://github.com/protocolbuffers/protobuf/releases/download/v34.1/protoc-34.1-win64.zip), [protobuf source](https://github.com/protocolbuffers/protobuf/releases/download/v34.1/protobuf-34.1.tar.gz), dan [Abseil](https://github.com/abseil/abseil-cpp/archive/refs/tags/20250512.1.tar.gz). Hash SHA-256 hasil unduhan masing-masing adalah `6d7ebdc75e9c1f0026d4fb28f17ef1d0aae77d36744d83a9e052d79ba493724f`, `e4e6ff10760cf747a2decd1867741f561b216bd60cc4038c87564713a6da1848`, dan `9b7a064305e9fd94d124ffa6cc358592eb42b5da588fb4e07d09254aa40086db`.

## Pekerjaan berikutnya

Ikuti [implementation-guide](implementation-guide.md) dan [verification](verification.md). E01 membangun evaluator required gate; S01 menghubungkan guard ke storage dan transaksi nyata. Transport Worker PARSE/STRUCTURE adalah konsumen awal C01, sedangkan N01/A01 masih perlu inference dan citation resolver. C01 belum membuktikan quality model, end-to-end performance atau seluruh invariant pada data produksi.
