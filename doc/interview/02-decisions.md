# Mengapa memilih arsitektur ini?

Dokumen ini menjelaskan alasan desain dan alternatifnya untuk interview. Alasan
adalah pertimbangan engineering, bukan klaim hasil benchmark komparatif yang belum
dilakukan. Tiap pilihan harus dapat ditinjau ulang ketika bukti workload tersedia.

## 1. Bahasa dan batas proses

| Pilihan | Alasan dalam proyek | Alternatif dan kelebihannya | Harga pilihan kita / kapan ditinjau ulang |
| --- | --- | --- | --- |
| Go untuk API, workflow, retrieval orchestration | Menyatukan lifecycle request, concurrency, deadline, driver backend dan deployment executable | Python/FastAPI: eksperimen ML cepat; Rust: kendali memori lebih ketat | Go tetap memiliki GC; multi-bahasa memperbesar biaya integrasi. Jika tim kecil dan bottleneck seluruhnya model, all-Python bisa cukup masuk akal. |
| Rust untuk transformasi dokumen/index batch | Ownership, kontrol alokasi, transformasi data dan concurrency dengan unit dokumen/batch | Go: satu bahasa lebih sederhana; C++: integrasi native langsung; Python: library parsing kaya | Compile dan pengembangan lebih kompleks. Tidak otomatis lebih cepat daripada Python yang hanya membungkus engine native. |
| C++ sebagai wrapper ONNX inference | Memakai runtime tensor yang sudah ada, warm session, buffer dan batching eksplisit | Python ONNX/PyTorch: lebih mudah iterasi; inference server siap pakai: operasi lebih standar | Build/C ABI/CUDA lebih rumit. Tidak menulis kernel sendiri; accelerator dan shape sering lebih menentukan daripada bahasa wrapper. |
| Python offline | Ekosistem dataset, model export/reference, evaluasi | Seluruh tooling di Go/Rust menyederhanakan toolchain bahasa | Memerlukan environment tambahan; manfaatnya pada eksperimen, bukan otomatis mempercepat query. |
| gRPC/Protobuf pada boundary batch | Satu schema lintas Go/Rust/C++, typed messages, ID/error/accounting konsisten | HTTP/JSON mudah di-debug; FFI menghindari network hop | Codegen, versioning, serialisasi dan copy tetap berbiaya. Batching mengamortisasi biaya boundary. |
| Fusion/filter/context/citation tetap satu proses Go | Fungsi kecil tidak memerlukan round trip terpisah | Microservice masing-masing fungsi dapat diskalakan/dirilis sendiri | Satu proses lebih erat terkait; pecah hanya bila kebutuhan scaling/isolation membenarkannya. |

Go mempunyai trade-off CPU/memori pada GC; pengurangan allocation rate lebih
bermakna daripada menyebut bahasa sebagai jaminan latency.
[Rujukan resmi Go GC](https://go.dev/doc/gc-guide).
ONNX Runtime juga menyediakan pengaturan thread, dan menambah parallel execution
dapat merugikan model tertentu. Kita perlu profiling, bukan sekadar menambah thread.
[Rujukan resmi ONNX Runtime](https://onnxruntime.ai/docs/performance/tune-performance/threading.html).

**Mengapa bukan LangGraph untuk mempercepat?** Workflow saat ini eksplisit dalam
Go. Framework orchestration bukan pengganti komputasi inference. Menambahnya tidak
otomatis mengurangi waktu token generation. Sebuah framework tetap bisa berguna
bila manfaat workflow/observability-nya melebihi tambahan dependency dan boundary;
proyek ini sudah mempunyai scheduler, checkpoint dan state handling sendiri.

## 2. Representasi dokumen dan graph

| Pilihan | Manfaat yang dituju | Mengapa alternatif tetap dipertimbangkan | Trade-off / pengujian |
| --- | --- | --- | --- |
| Structural chunks + parent context | Menjaga batas pasal/ayat, label, syarat dan pengecualian | Fixed window mudah, murah dan cocok untuk demo awal | Parser struktur dapat salah; ukur boundary accuracy dan retrieval evidence coverage. Parent menambah token. |
| Canonical ID terpisah dari alias | Nama berbeda dapat menunjuk entitas sama tanpa mengubah ID; alias ambigu tidak dipaksa unik | String matching sederhana berguna untuk exact scoped identity | Registry/revision/review lebih kompleks; ukur false merge dan candidate recall. |
| Exact rule + model proposal untuk ambiguity | Aturan menjaga invariant; model mempertimbangkan konteks semantik yang sulit ditulis sebagai rule | Semua hardcode reproducible; semua LLM lebih fleksibel | Model bisa salah dan berubah antar-run. Proposal harus punya sumber, candidate scope, validation dan jalur defer/review. |
| Provenance pada assertion/support | Edge dapat ditelusuri dan dukungan satu sumber bisa dicabut tanpa menghapus bukti lain | Graph triple sederhana lebih ringan | Lebih banyak record/index dan biaya traversal; perlu uji consistency/rebuild. |
| Dua sumbu: legal date dan snapshot | Jawaban tentang tanggal hukum dan tentang pengetahuan sistem tidak tertukar | Satu `updated_at` lebih sederhana | Interval unknown/conflict memerlukan policy; “dokumen terbaru” bukan resolver universal. |
| Incremental berbasis dependency | Menghindari pengulangan model/parse untuk bagian yang tidak berubah | Full rebuild lebih mudah dijadikan referensi kebenaran | Invalidation lintas dokumen sulit; bandingkan hasil incremental dengan rebuild penuh. |

Canonicalization bukan “semua kata yang maknanya mirip menjadi satu entitas”.
Kesamaan bahasa hanya sinyal kandidat. Tipe entitas, penerbit, nomor/tahun, sumber,
dan konteks tetap dibutuhkan. Hubungan dua entitas tidak berarti identitasnya sama.

## 3. Retrieval dan model

| Bagian | Mengapa dipakai | Alternatif | Trade-off yang perlu dijelaskan |
| --- | --- | --- | --- |
| BM25 | Cocok untuk nomor, istilah hukum, frasa eksplisit; mudah diaudit | Dense-only lebih fleksibel terhadap parafrasa | BM25 sensitif kosakata; tidak memahami sinonim secara otomatis. |
| Dense BGE-M3 | Kandidat multilingual untuk parafrasa dan pencocokan semantik | Model lebih kecil murah; model lain mungkin lebih baik pada domain Indonesia | Kualitas domain/typo tetap perlu gold. Model card bukan hasil benchmark corpus kita. |
| Graph branch | Mengikuti rujukan/relasi untuk bukti lintas dokumen | Vector/hybrid tanpa graph lebih sederhana | Salah edge atau hop berlebih menambah noise; ukur all-required-evidence, bukan jumlah edge saja. |
| RRF fusion | Menggabungkan peringkat tanpa menjumlahkan skor yang skalanya berbeda | Weighted score sum setelah kalibrasi; learned ranker | RRF mengabaikan besar selisih skor; bobot dan konstanta perlu ablation. |
| Cross-encoder reranker | Memeriksa interaksi query–passage setelah kandidat diperkecil | Bi-encoder saja cepat; LLM reranker lebih fleksibel | Biaya inference per pasangan lebih besar; tidak dapat menyelamatkan dokumen yang tidak masuk kandidat. |
| Generation terikat evidence | Jawaban dapat ditelusuri ke sumber retrieval | LLM-only sederhana; extractive-only mudah diverifikasi | Citation ID valid belum membuktikan makna jawaban benar. Abstention mengurangi jawaban rekaan tetapi juga mengurangi answer rate. |
| Qwen lokal pada demo | Model sudah tersedia; demo tidak membutuhkan API key | API model bisa memindahkan beban hardware; model lokal lebih kecil bisa lebih cepat | VRAM/CPU laptop membatasi latency. Ini pilihan demo, bukan pemenang seleksi model produksi. |

BGE-M3 mendukung beberapa bentuk retrieval menurut model card pembuatnya. Namun
implementasi proyek memakai **dense embedding BGE-M3 dan BM25 terpisah**; jangan
mengatakan seluruh kemampuan sparse/multi-vector BGE-M3 sudah diaktifkan.
[Model card BAAI](https://huggingface.co/BAAI/bge-m3).

## 4. Storage dan publication

Qdrant dipilih untuk jalur dense/sparse serta filter payload. Mesin lain seperti
pgvector atau mesin pencarian lexical/vector terpadu tetap mungkin; satu storage
dapat mengurangi operasi, tetapi perlu menguji filtering, recall dan biaya pada
workload yang sama. Qdrant menyediakan fasilitas hybrid query/fusion; proyek tetap
memiliki fusion Go untuk mempertahankan provenance dan menggabungkan calon branch
graph dari backend berbeda. [Dokumentasi Qdrant](https://qdrant.tech/documentation/search/hybrid-queries/).

Neo4j direncanakan untuk adjacency dan path query. Alternatifnya tabel edge
PostgreSQL dengan recursive query atau adjacency di aplikasi. Yang menentukan
bukan “graph database selalu lebih cepat”, melainkan pola traversal, filter versi,
volume support, operasi, dan pengukuran. Integrasi Neo4j produksi belum lengkap.

PostgreSQL dipertahankan sebagai otoritas job/registry/publication karena search
index saja tidak cukup untuk menjalankan semua invariant itu. Blob storage
mempertahankan byte sumber agar kutipan, replay dan pemeriksaan integrity tidak
bergantung pada URL portal yang bisa berubah.

Publication marker menambah latency ingestion dan kompleksitas recovery, tetapi
mencegah pembaca mencampur indeks baru dengan graph/metadata lama. Tidak ada klaim
distributed ACID lintas backend. Detail protokol tetap mengikuti
[storage-consistency](../storage-consistency.md).
