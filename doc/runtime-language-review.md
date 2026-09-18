# Kajian pembagian bahasa berdasarkan latency dan throughput

Dokumen ini menilai penempatan Go, Rust, C/C++, dan Python pada komponen RegulaGraph-ID. Perannya adalah menyediakan rekomendasi runtime yang konkret untuk menggantikan asumsi semua komponen produksi berada dalam Python, dengan latency dan throughput sebagai prioritas serta kualitas bukti tetap terukur.

Status: rekomendasi diterima pengguna dan struktur scaffold telah dimigrasikan menurut [keputusan 0002](decisions/0002-polyglot-runtime.md), 2026-09-18. Belum ada implementasi pipeline atau benchmark komparatif. Hardware deployment aktual belum ditentukan; profil hardware, corpus, concurrency, serta anggaran benchmark kini ditetapkan dalam [target wajib](benchmark-targets.md). Rekomendasi bahasa di bawah merupakan penilaian desain, bukan klaim hasil pengukuran bahasa tercepat.

## Pembagian runtime yang disetujui

Gunakan Go untuk API dan orchestration query, Rust untuk worker pemrosesan dokumen dan graph secara batch, serta library/runtime C/C++ yang sudah tersedia untuk parsing native dan inference. Gunakan Python untuk dataset, evaluasi, eksperimen model, dan ekspor model. Jalur query aplikasi tidak membutuhkan proses Python milik proyek; runtime model eksternal dipilih berdasarkan kecocokan model dan perangkat, bukan hanya bahasa implementasinya.

| Komponen saat ini | Target yang direkomendasikan | Pekerjaan dan tujuan performa |
| --- | --- | --- |
| api, CLI operasional | Go | HTTP/streaming, validasi request, deadline, dan connection pooling. |
| workflows/answer | Go | Menjalankan jalur pencarian yang independen secara concurrent dengan budget waktu yang terlihat. |
| retrieval/query | Go | Normalisasi ringan, lookup alias, dan routing dasar tanpa inference tambahan untuk setiap pertanyaan. |
| retrieval/lexical, dense, graph | Go + Qdrant/Neo4j | Driver langsung ke mesin pencarian; query graph dieksekusi di server. |
| retrieval/fusion, filters, pemilihan hasil reranker | Go dalam proses API yang sama | Pengolahan kandidat dan versi tanpa tambahan RPC untuk setiap tahap kecil. |
| answering/context_builder, citations, validasi deterministik | Go dalam proses yang sama | Mengambil parent yang sudah dipersiapkan, menyusun konteks, dan memetakan sumber dengan sedikit penyalinan data. |
| answering/generator | Go client ke inference runtime/provider | Menyiapkan prompt dan mengalirkan token; komputasi model berada di runtime khusus. |
| workflows/ingest dan update, sources | Go coordinator | Penjadwalan job, akuisisi dokumen, checkpoint, batching, dan publikasi snapshot. |
| ingestion/parsing | Rust worker + engine PDF C/C++ | Memproses halaman/dokumen secara paralel sesuai model concurrency engine, mempertahankan layout dan lokasi sumber. |
| ingestion/normalization, chunking, change detection | Rust worker | Transformasi teks, pembentukan rentang/parent, hash, dan manifest dependency dalam batch. |
| versioning dan struktur canonical | Rust untuk transformasi offline; Go untuk pembacaan/filter query | Menyiapkan indeks versi dan lookup identitas agar jalur query hanya melakukan pemilihan yang diperlukan. |
| knowledge_graph/extraction dan summarization | Coordinator/worker memanggil runtime model; Rust memproses output | Komputasi semantik tetap dilakukan model; parsing output dan penyusunan record dilakukan secara native. |
| knowledge_graph/resolution | Rust untuk blocking, exact matching, dan registry transforms; model untuk kasus semantik | Mengurangi jumlah kandidat dengan indeks, lalu memproses batch; kata mirip tidak otomatis digabung. |
| knowledge_graph/assembly dan validation | Rust worker; Go adapter untuk commit Neo4j | Deduplikasi, pembentukan delta graph, validasi endpoint dan provenance, kemudian bulk persistence. |
| indexing dan synchronization | Rust menyiapkan record/batch; Go mengoordinasikan publikasi | Mengurangi per-record round trip dan mempertahankan snapshot lintas backend. |
| infrastructure/models/embeddings dan cross_encoder | Runtime native ONNX Runtime sebagai kandidat awal | Session persisten, graph optimization, accelerator yang sesuai, dan bounded batching. |
| evaluation dan tooling model | Python | Menggunakan ekosistem evaluasi serta reference implementation tanpa berada pada jalur request produksi. |

Coordinator mengirim pekerjaan dengan referensi sumber/snapshot dan opsi proses. Worker mengembalikan artefak atau delta dalam batch, bukan panggilan per token, per edge, atau per chunk kecil. Tanggung jawab persistensi setiap tahap ditetapkan satu pemiliknya agar tidak ada dua implementasi publikasi yang berbeda.

## Alasan Go untuk jalur query

Jalur online menggabungkan request pengguna, database, dan inference. Go dipilih sebagai orchestration terkompilasi dengan concurrency dan lifecycle request yang eksplisit. Qdrant menyediakan client Go serta antarmuka HTTP/gRPC; Neo4j menyediakan driver Go resmi dengan rekomendasi pool, query, dan fetch size. Dukungan tersebut memungkinkan akses langsung ke kedua backend tanpa perantara Python. Ini merupakan dasar integrasi, bukan bukti Go mengalahkan Rust pada semua workload. [Qdrant clients](https://qdrant.tech/documentation/guides/), [Neo4j Go performance](https://neo4j.com/docs/go-manual/current/performance/)

Fusion, filter, context assembly, dan citation mapping tetap berupa modul dalam proses Go yang sama. Jangan memecah empat fungsi tersebut menjadi empat layanan hanya untuk memakai bahasa berbeda. Gunakan record kompak, kurangi alokasi berulang, dan ukur heap/GC bersama p95/p99. Runtime Go mempunyai garbage collector dan dokumentasi resminya menjelaskan hubungan allocation rate, CPU, serta memori. [Go GC guide](https://go.dev/doc/gc-guide)

Rust juga merupakan pilihan yang valid untuk seluruh serving core. Pemilihan Go di sini didasarkan pada bentuk orchestration dan integrasi driver yang tersedia; bila perbandingan endpoint yang identik menunjukkan Rust memberikan penghematan latency yang material, satu serving core Rust menjadi alternatif yang perlu dicatat, tanpa menambah hop Go-ke-Rust untuk setiap fungsi kecil.

## Alasan Rust untuk pemrosesan data

Normalisasi teks, pembentukan chunk, kandidat resolution, deduplikasi relasi, dan penyusunan batch banyak mengolah struktur data. Rust memberi kendali representasi dan alokasi memori tanpa tracing garbage collector. Manfaat yang dituju adalah throughput preprocessing, peak RSS yang terkendali, dan pemakaian banyak core dengan unit kerja dokumen/batch. Ownership sendiri tidak menjamin sebuah algoritma lebih cepat; implementasi dan bentuk data tetap diukur. [Rust ownership](https://doc.rust-lang.org/book/ch04-00-understanding-ownership.html)

Precompute hubungan parent, canonical alias lookup, dan metadata versi saat ingestion. Saat pengguna bertanya, server hanya membaca data yang siap dicari. Jangan mengulang extraction atau resolution corpus pada request pengguna. Simpan data graph di Neo4j dan jalankan traversal di sana; pemindahan seluruh graph ke objek aplikasi tidak termasuk rancangan ini.

## Penempatan C dan C++

Parsing PDF menggunakan engine yang sudah tersedia, dengan worker Rust mengatur pipeline. MuPDF menyediakan C library serta binding C++; ia merupakan kandidat, bukan pemilihan parser final. Engine dipilih berdasarkan kecepatan serta kualitas urutan baca, tabel, nomor pasal, dan source mapping pada PDF proyek. Tidak ada rencana menulis parser PDF lengkap dari nol. [MuPDF](https://mupdf.readthedocs.io/en/latest/guide/what-is-mupdf.html), [binding C/C++](https://mupdf.readthedocs.io/en/1.27.0/reference/cxx-and-derived-bindings.html)

Untuk embedding dan reranker, ONNX Runtime menyediakan C/C++ API, graph optimization, serta execution providers untuk berbagai perangkat. Gunakan session yang sudah dimuat, input/output buffer yang dikelola, dan batching dengan batas tunggu. Ekspor model harus mempertahankan tokenizer, pooling, normalisasi, truncation, dan skor yang dimaksud. ONNX graph optimization di sini berarti graph komputasi model, berbeda dari knowledge graph regulasi. [ONNX Runtime API](https://onnxruntime.ai/docs/api/c/), [graph optimization](https://onnxruntime.ai/docs/performance/model-optimizations/graph-optimizations.html), [execution providers](https://onnxruntime.ai/docs/execution-providers/)

Implementasi service inference dapat berupa wrapper C++ tipis di atas runtime tersebut bila dibutuhkan. Tidak perlu menulis kernel tensor sendiri untuk memasukkan C++ ke proyek. Untuk LLM lokal, llama.cpp adalah kandidat engine C/C++; model dan perangkat tetap menentukan pemilihannya. Pemakaian API model eksternal juga tetap memungkinkan melalui client Go. [llama.cpp](https://github.com/ggml-org/llama.cpp)

## Alur produksi yang dituju

Query: pengguna ke Go API; Go menyiapkan query dan meminta embedding; Go menjalankan jalur lexical, dense, dan graph yang dependency-nya sudah siap; hasil digabung dan difilter dalam proses yang sama; runtime melakukan reranking; Go membangun konteks dan memanggil generation; token jawaban serta citation disajikan kepada pengguna. Graph seed dari entity linking dapat dimulai lebih awal; seed dari hasil dense harus menunggu hasil dense. Paralelisme mengikuti dependency yang sebenarnya.

Ingestion: Go mengambil dan menjadwalkan sumber; Rust worker membaca sumber melalui engine native, menormalisasi, membentuk chunk serta metadata; model mengekstrak fakta; Rust memproses resolution/assembly sesuai batch yang dijadwalkan; adapter menulis metadata, indeks, dan graph secara batch; coordinator memublikasikan snapshot siap dibaca. Update hanya mengerjakan sumber/dependensi yang berubah.

Alokasi worker/GPU ingestion dipisahkan atau dijadwalkan dengan prioritas agar bulk ingestion tidak menaikkan waktu antre query. Model dimuat terus selama layanan aktif; connection pool dipakai ulang. Cache menyertakan snapshot, tanggal acuan, model version, dan parameter yang memengaruhi hasil. Peningkatan kecepatan terutama diarahkan pada penghapusan kerja berulang, penggunaan batch, paralelisme yang tepat, dan pengurangan data yang dipindahkan.

## Benchmark yang harus disiapkan

| Area | Pengukuran wajib | Kontrol perbandingan |
| --- | --- | --- |
| Serving core Go/Rust | p50/p95/p99 overhead aplikasi, allocation/request, RSS, CPU, request/detik | Endpoint dan kontrak identik, payload/candidate count sama, backend stub stabil untuk isolasi lalu layanan nyata untuk end-to-end. |
| Query lengkap | p50/p95/p99 sampai bukti siap, time-to-first-answer-token, inter-token latency, waktu jawaban lengkap | Concurrency/arrival rate, panjang input/output, corpus, cache hit/miss, perangkat, dan model sama. Respons status/loading tidak dihitung sebagai token jawaban pertama. |
| Preprocessing Rust/native | Halaman/detik, dokumen/detik, p95 waktu job, peak RSS, scaling jumlah core | PDF teks/scan/tabel dibedakan; kualitas parsing, batas pasal, dan provenance tetap diperiksa. |
| Graph preprocessing | Mention/edge per detik, waktu blocking/resolution/assembly, peak RSS | Pasangan kandidat dan hasil canonical/relasi diperiksa; identitas serta predicate tidak dikurangi demi angka throughput. |
| Embedding/reranker | Latency per batch, throughput, waktu antre, RAM/VRAM | Model, tokenizer, panjang sequence, precision, batch size, dan execution provider dicatat. |
| Incremental update | Waktu sampai snapshot baru terlihat dan biaya per perubahan | Bandingkan dengan full rebuild dengan hasil logis setara. |

Profil referensi asumsi dan angka acceptance kini ditetapkan di [benchmark-targets.yaml](../configs/benchmark-targets.yaml); lihat [panduan target](benchmark-targets.md). Sementara itu, sasaran implementasi konkretnya adalah tidak ada pemuatan model per request, tidak ada ekstraksi PDF/graph pada query, tidak ada N+1 query sumber, tidak ada RPC per tahap kecil fusion/filter/context, dan tidak ada pemrosesan ulang sumber yang identik tanpa perubahan dependency. Target numerik bersifat required, belum merupakan hasil yang dicapai, dan tidak boleh diturunkan tanpa persetujuan pengguna.

Tambahkan load test saat ingestion berjalan, pisahkan cold/warm, ukur waktu antre, dan laporkan request yang melewati deadline. Throughput maksimum dan latency minimum dapat memerlukan batch size berbeda; bounded batching mencegah menunggu batch penuh terlalu lama. Optimasi yang mengubah precision model atau candidate coverage tetap diperiksa dengan gold evidence dan kualitas jawaban. Protokol dasarnya mengikuti [benchmark-policy.md](benchmark-policy.md).

## Integrasi dan dampak pada scaffold

Perubahan ini mengganti pilihan runtime untuk fungsi yang sudah ada; domain dan kontrak bukti dipertahankan. FastAPI serta workflow Python tidak menjadi target serving utama, dan LangGraph tidak diperlukan pada jalur request Go. Python tetap dapat menggunakan endpoint/kontrak yang sama untuk evaluasi, tanpa menggandakan implementasi algoritma produksi.

Sebelum migrasi file, susun kontrak lintas bahasa dengan source/canonical/version/snapshot ID, rentang teks yang jelas satuannya, error/status, serta schema version. Offset Unicode perlu konsisten karena byte, code point, dan UTF-16 index tidak setara. Binding atau wire format harus diuji dengan golden fixtures. Pemisahan bahasa mengikuti batas pekerjaan besar dan menghindari penyalinan dokumen berulang.

Scaffold produksi kini berada di src/server (Go), src/ingestion (Rust), dan src/inference (C++); src/contracts menampung kontrak bersama. Source Python produksi lama telah digantikan, sementara evaluation/tooling tetap Python. [Arsitektur scaffold](architecture.md) menjelaskan struktur yang diterapkan; [AGENTS.md](../AGENTS.md) tetap mengatur penambahan komponen berikutnya.
