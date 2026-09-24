# src

Folder ini menampung implementasi produk RegulaGraph-ID dan kontrak data bersama yang diperlukan untuk menghubungkan komponennya. Perannya adalah mengelompokkan kode produksi berdasarkan fungsi: server, ingestion, inference, dan contracts. Evaluation, tooling model, pengujian lintas komponen, dokumentasi, dan pengaturan deployment memiliki folder sendiri di root.

## Peran dan integrasi anak

[server](server/README.md) adalah modul Go untuk API/CLI, workflow, retrieval, answering, serta penjadwalan job dan publikasi storage. [ingestion](ingestion/README.md) adalah crate Rust yang menghasilkan batch dokumen, graph, dan indeks; coordinator Go memiliki commit dan publication. [inference](inference/README.md) menyediakan library C++ untuk eksekusi embedding/reranker serta pengelolaan batch. [contracts](contracts/README.md) menjadi sumber schema wire bersama, termasuk identitas sumber, canonical entity, versi pasal, snapshot, status, dan offset teks.

Anak harus mempertahankan batas tanggung jawab tersebut. Routing dan kebijakan jawaban berada di server; transformasi batch berada di ingestion; eksekusi tensor berada di inference. Binding hasil codegen mengikuti contracts dan tidak menjadi schema independen. Fusion, filtering, context builder, serta citation tetap satu proses Go; boundary worker/inference memakai operasi batch yang jelas, bukan RPC untuk tiap langkah kecil. Model tidak dimuat per request.

## Build dan deployment

[go.work](../go.work) dan [Cargo.toml](../Cargo.toml) di root menghubungkan workspace komponen. CMake milik inference berada di komponennya sendiri. Folder src di dalam ingestion dan inference memisahkan implementasi komponen dari manifest build dan header. Nama module Go, crate Rust, namespace C++, dan package Protobuf tetap sama setelah pemindahan lokasi.

[deployment](../deployment/README.md) mengatur packaging dan wiring deployment. Lokasi dalam src tidak berarti semua source harus disalin ke image runtime: build menghasilkan binary, library, serta binding yang relevan. Rust memiliki executable worker PARSE berbasis Tonic/PDFium dan Go memiliki daemon dispatch durable; stage lanjutan serta layanan inference C++ masih bertahap.

## Benchmark dan batas cakupan

Semua anak mengikuti [target required](../configs/benchmark-targets.yaml) dan [kebijakan benchmark](../doc/benchmark-policy.md). Ukur latency p95/p99 termasuk antrean, throughput batch, memori, kualitas bukti, serta konsistensi versi/snapshot. Pemindahan folder bukan hasil pengukuran performa. Target tetap REQUIRED_UNMEASURED; kegagalan tes memicu perbaikan implementasi dan uji ulang, sedangkan perubahan benchmark memerlukan persetujuan pengguna.

Setiap anak wajib mendokumentasikan cakupan dan kontrak integrasinya dalam README serta fungsi file pada komentar pembuka. Ikuti [AGENTS.md](../AGENTS.md). Acquisition PDF/metadata, transformasi dokumen sampai CHUNK, worker EXTRACT, Semantic Gateway, commit durable EXTRACT, ontology EXTRACT bersama, blocking kandidat, dan helper alias LINK sourced tersedia. Stage RESOLVE, graph/index, retrieval/answering, serta provider/model produksi belum aktif.

Gateway dan workflow proposal RESOLVE kontekstual tersedia pada server. Status stage belum aktif di atas merujuk dispatch ingestion end-to-end; lihat [integrasi resolusi](../doc/semantic-resolution.md).
