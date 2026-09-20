# Arsitektur RegulaGraph-ID

Dokumen ini menjelaskan arsitektur monorepo Go/Rust/C++/Python yang telah disetujui pengguna dengan prioritas latency dan throughput. Struktur, storage/control-plane dasar, dan transport Worker PARSE sudah direalisasikan; pipeline stage lanjutan, query serving, serta model belum lengkap.

Dokumen ini menjadi ringkasan pemilik komponen. Rancangan menyeluruh terdapat pada [system-design](system-design.md), [system-contracts](system-contracts.md), [storage-consistency](storage-consistency.md), dan [development-plan](development-plan.md), sesuai keputusan 0005. Bila ringkasan tidak memuat rincian failure/field, gunakan spesifikasi tersebut; jangan menganggap rincian itu di luar cakupan produk.

## Pemilik komponen

| Runtime/lokasi | Tanggung jawab |
| --- | --- |
| [src/server](../src/server/README.md), Go | API/CLI, orchestration query dan job, retrieval, fusion, filter, context builder, citation, adapter database, commit/publikasi snapshot. |
| [src/ingestion](../src/ingestion/README.md), Rust | Parsing melalui engine native, normalisasi, chunking, version transforms, extraction postprocessing, resolution, GraphDelta, dan persiapan index batch. |
| [src/inference](../src/inference/README.md), C++ | Lifecycle session model, wrapper embedding/reranker, serta bounded batching; backend aktual belum dipilih. |
| [src/contracts](../src/contracts/README.md) | Sumber schema wire untuk ID, dokumen, job, graph delta, bukti, jawaban, serta inference. |
| [evaluation](../evaluation/README.md) dan [tooling](../tooling/README.md), Python | Benchmark offline, datasets, metrik, ekspor model, dan pemeriksaan parity. |

Pembagian bahasa mengikuti pekerjaan besar. Fusion, filtering, context builder, serta citation tidak dijadikan layanan terpisah. Type lokal mengikuti kontrak bersama; wire schema tidak diduplikasi secara independen.

## Alur query

Pengguna masuk melalui Go API. Go menyiapkan query dan alias linking; jalur lexical dan graph yang seed-nya sudah diketahui bisa berjalan sebelum embedding selesai. Dense menunggu embedding, sedangkan graph expansion berbasis hasil dense menunggu kandidat tersebut. Paralelisme mengikuti dependency yang nyata.

Client Go mengakses Qdrant serta Neo4j secara langsung. Traversal dieksekusi di Neo4j, bukan memuat seluruh graph ke memori aplikasi. Go memfilter versi/snapshot, menggabungkan kandidat, meminta reranking ke runtime inference, lalu membangun konteks beserta parent dan rantai bukti. Generator client memanggil runtime/provider model, mengalirkan jawaban, dan mempertahankan citation. Status loading tidak dihitung sebagai token jawaban pertama.

Pemeriksaan citation deterministik berada di Go; evaluator semantik yang memakai model merupakan tahap tersendiri yang budget latency-nya perlu dicatat. Model/session dan pool koneksi dipakai ulang, bukan dibuat tiap request.

## Alur ingestion dan update

Go memperoleh sumber dan menjadwalkan job dengan source locator, content hash, config/model/schema version, serta snapshot. Rust worker menggunakan engine parsing native, membersihkan artefak teks, membentuk chunk struktural dan relasi parent, serta menyiapkan data versi. Worker memanggil adapter inference untuk ekstraksi/resolution semantik ketika diperlukan.

Rust menghasilkan artefak dokumen, GraphDelta dengan canonical ID dan provenance, serta record indeks dalam batch. Adapter Go menjadi pemilik penulisan database dan publication marker; worker bukan penulis state produksi kedua. Coordinator menyimpan checkpoint, memeriksa kesiapan tiap artefak, kemudian memublikasikan snapshot konsisten.

Perubahan isi maupun dependency fingerprint dapat menginvalidasi chunk, relasi, canonical mapping, ringkasan, dan indeks lintas dokumen. Dukungan satu sumber yang dihapus tidak menghapus seluruh relasi bila sumber lain masih mendukungnya. Hilangnya sumber dari corpus bukan pernyataan pencabutan hukum. Sumber yang tidak berubah tidak diproses ulang tanpa alasan dependency yang tercatat.

## Penyimpanan dan boundary

PostgreSQL direncanakan untuk metadata/versi/manifest, Qdrant untuk indeks pencarian, Neo4j untuk graph dengan provenance, dan storage berkas untuk dokumen asli serta artefak batch. Tidak ada asumsi transaksi atomik lintas backend; staging/checkpoint/idempotensi dan publication marker menjaga pembacaan snapshot.

Go-Rust dan Go/Rust-inference memakai gRPC berukuran job/batch, bukan RPC per edge atau token. Worker Go-Rust memiliki generated binding, implementasi PARSE berbasis referensi artefak, dan coordinator Go yang menyerahkan job durable serta menyimpan checkpoint. Semantic/inference C++ masih berupa target static library. Transport Worker hanya loopback sampai termination TLS deployment tersedia.

Offset wire menggunakan rencana byte UTF-8 start-inclusive/end-exclusive dengan identitas teks terkait. Parser mempertahankan mapping ke teks asli bila normalisasi mengubah posisi. Source/canonical/provision-version/snapshot ID diteruskan tanpa perubahan identitas berdasarkan nama tampilan.

## Desain performa

Pisahkan resource/prioritas ingestion dari query agar bulk job tidak menghabiskan kapasitas request. Precompute parent, canonical lookup, dan metadata versi. Hindari N+1 query sumber dan pemindahan dokumen penuh berulang. Gunakan bounded batching inference dan tulis database secara batch, sambil mengukur waktu antre.

Ukur p50/p95/p99, time-to-first-answer-token, waktu jawaban lengkap, throughput dokumen/halaman/edge, serta RAM/VRAM. Perangkat deployment belum ditentukan, tetapi [target benchmark wajib](benchmark-targets.md) telah ditetapkan pada profil referensi asumsi. Angka merupakan sasaran desain yang belum diukur. Ketepatan versi, teks, predicate, dan dukungan citation tetap diuji sebagai syarat optimasi.

## Status keputusan

Keputusan runtime tercatat di [0002](decisions/0002-polyglot-runtime.md). [Kajian bahasa](runtime-language-review.md) menyimpan alasan pemilihannya. FastAPI dan workflow LangGraph Python tidak menjadi jalur produksi utama. Model/provider dan engine PDF spesifik tetap dipilih sebelum run. Workload acceptance serta target wajib berada di configs/benchmark-targets.yaml; parameter benchmark tidak menjadi hard limit kemampuan produk.

Lihat [kontrak data](data-model.md) serta [kebijakan benchmark](benchmark-policy.md) untuk rincian integrasi dan pengukuran.
