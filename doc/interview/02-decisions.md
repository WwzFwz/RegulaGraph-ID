# Mengapa memilih arsitektur ini?

Dokumen ini menjelaskan pilihan sistem lengkap sesuai asumsi pada [panduan](README.md).
Status aktual ada di [dokumen 7](07-implementation-status.md). Alasan
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

**Mengapa bukan LangGraph untuk mempercepat?** Workflow eksplisit dalam
Go. Framework orchestration bukan pengganti komputasi inference. Menambahnya tidak
otomatis mengurangi waktu token generation. Sebuah framework tetap bisa berguna
bila manfaat workflow/observability-nya melebihi tambahan dependency dan boundary;
proyek ini sudah mempunyai scheduler, checkpoint dan state handling sendiri.

## 2. Representasi dokumen dan graph

| Pilihan | Manfaat yang dituju | Mengapa alternatif tetap dipertimbangkan | Trade-off / pengujian |
| --- | --- | --- | --- |
| Structural chunks + parent context | Menjaga batas pasal/ayat, label, syarat dan pengecualian | Fixed window mudah, murah dan mudah dijadikan baseline | Parser struktur dapat salah; ukur boundary accuracy dan retrieval evidence coverage. Parent menambah token. |
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
| Generator melalui adapter provider | Model lokal atau API memakai kontrak evidence/output yang sama | Mengikat aplikasi langsung ke satu SDK lebih sederhana | Lokal memberi kendali data dan kapasitas; API mengurangi pengelolaan hardware tetapi menambah biaya dan ketergantungan jaringan. Provider/model dibekukan per run. |

BGE-M3 mendukung beberapa bentuk retrieval menurut model card pembuatnya. Namun
arsitektur ini memakai **dense embedding BGE-M3 dan BM25 terpisah**; kemampuan sparse/multi-vector BGE-M3 bukan bagian wajib konfigurasi ini.
[Model card BAAI](https://huggingface.co/BAAI/bge-m3).

## 4. Storage dan publication

Qdrant dipilih untuk jalur dense/sparse serta filter payload. Mesin lain seperti
pgvector atau mesin pencarian lexical/vector terpadu tetap mungkin; satu storage
dapat mengurangi operasi, tetapi perlu menguji filtering, recall dan biaya pada
workload yang sama. Qdrant menyediakan fasilitas hybrid query/fusion; proyek tetap
memiliki fusion Go untuk mempertahankan provenance dan menggabungkan branch
graph dari backend berbeda. [Dokumentasi Qdrant](https://qdrant.tech/documentation/search/hybrid-queries/).

Neo4j dipakai untuk adjacency dan path query. Alternatifnya tabel edge
PostgreSQL dengan recursive query atau adjacency di aplikasi. Yang menentukan
bukan “graph database selalu lebih cepat”, melainkan pola traversal, filter versi,
volume support, operasi, dan pengukuran.

PostgreSQL dipertahankan sebagai otoritas job/registry/publication karena search
index saja tidak cukup untuk menjalankan semua invariant itu. Blob storage
mempertahankan byte sumber agar kutipan, replay dan pemeriksaan integrity tidak
bergantung pada URL portal yang bisa berubah.

Publication marker menambah latency ingestion dan kompleksitas recovery, tetapi
mencegah pembaca mencampur indeks baru dengan graph/metadata lama. Tidak ada klaim
distributed ACID lintas backend. Detail protokol tetap mengikuti
[storage-consistency](../storage-consistency.md).

## 5. Alasan bisnis: dari masalah pengguna ke keputusan arsitektur

Alasan bisnis menjawab **pekerjaan pengguna apa yang terbantu, biaya apa yang
berubah, dan bagaimana manfaatnya dibuktikan**. “Menggunakan graph” adalah keputusan
teknis; “membantu pengguna menemukan seluruh rujukan untuk menjawab satu pertanyaan”
adalah tujuan produk. Keduanya perlu dihubungkan, bukan disamakan.

Hipotesis pengguna awal adalah orang yang melakukan riset regulasi, misalnya tim
compliance, analis kebijakan atau peneliti. Hipotesis ini belum menyatakan bahwa
pelanggan telah diwawancarai, bersedia membayar, atau bahwa ROI sudah terbukti.
Detail contoh input/output setiap keputusan ada di [dokumen 8](08-input-output-examples.md).

### Pekerjaan pengguna yang ingin dipermudah

Pengguna datang dengan pertanyaan, menemukan beberapa sumber, menentukan mana yang
relevan untuk tanggal/situasinya, mengikuti rujukan, lalu menyusun jawaban yang bisa
dipertanggungjawabkan. Produk membantu rangkaian itu dengan sumber yang dapat dibuka.
Jawaban cepat yang membutuhkan pemeriksaan ulang total belum tentu menghemat waktu.

| Masalah pengguna/operasi | Keputusan arsitektur | Manfaat bisnis yang dituju | Biaya dan alternatif | Bukti yang perlu dikumpulkan |
| --- | --- | --- | --- | --- |
| Dokumen tersebar dan perlu diperiksa berkala | Connector, receipt, audit, incremental ingestion | Mengurangi pekerjaan mengunduh/menata sumber dan jeda pembaruan | Crawling perlu pemeliharaan; curated upload bisa cukup pada scope kecil | Coverage, freshness lag, waktu kurasi dan biaya per dokumen yang usable |
| Sulit menemukan lokasi ketentuan dan pengecualian | Struktur pasal/ayat, source spans, parent hydration | Mempercepat pemeriksaan sumber dan mengurangi bagian penting yang terlewat | Parser lebih rumit daripada window; retrieval-only tanpa hierarchy lebih sederhana | Ketepatan locator, completeness, waktu pengguna membuka/memverifikasi sumber |
| Bahasa pertanyaan berbeda dari istilah resmi | BM25 + dense | Melayani exact lookup dan parafrasa dalam satu produk | Dua representasi menambah compute/storage; lexical-only bisa cukup pada sebagian workload | Recall per slice bahasa, keberhasilan tugas dan latency |
| Jawaban tersebar di rujukan/aturan perubahan | Graph dengan support dan traversal terarah | Mengurangi pekerjaan mengikuti hubungan dokumen secara manual | Extraction, resolution dan graph store menambah biaya serta kemungkinan error | All-required-evidence dan kualitas multi-hop dibanding hybrid tanpa graph |
| Nama mirip dan nomor sama mengacu ke objek berbeda | Registry berscope, canonical ID, model proposal + validasi | Mencegah pencarian/relasi tercampur dan mengurangi biaya koreksi data | Registry/review lebih mahal daripada string matching | False merge, candidate recall, antrean review dan dampak koreksi |
| Pengguna membutuhkan kondisi pada tanggal tertentu | Provision versions + temporal filtering | Memisahkan aturan historis dan perubahan parsial secara dapat diaudit | Memerlukan lineage/tanggal bersumber; latest-only lebih murah | Ketepatan pemilihan versi dan penanganan unknown/conflict |
| Banyak hasil relevan tetapi sulit disintesis | Reranking, context builder dan generation | Mengurangi waktu menyusun jawaban sambil mempertahankan rujukan | Inference menambah latency/biaya; pencarian bersumber saja dapat mencukupi pengguna tertentu | Waktu penyelesaian tugas, kualitas jawaban, token/cost dan tingkat koreksi |
| Jawaban model terlihat meyakinkan meskipun bukti kurang | Pemeriksaan evidence, claim/citation validation, abstention | Memperjelas batas jawaban dan memudahkan review | Validasi semantik mahal dan tidak sempurna; abstention menurunkan answer rate | Unsupported claims, salah menolak pertanyaan yang bisa dijawab, dan dukungan citation |
| Pengguna menunggu terlalu lama atau membatalkan pertanyaan | Warm model, parallel branch, batch terukur, deadline/cancel | Respons lebih dapat diprediksi dan kapasitas tidak terbuang | Kapasitas hangat tetap berbiaya; batch besar dapat memperlambat satu pengguna | p95/p99, antrean, time-to-first-output, timeout, cancellation dan throughput |
| Pembaruan menghasilkan backend tidak konsisten | Staging, readiness, snapshot pin, recovery | Mengurangi jawaban campuran versi dan beban penanganan insiden | Publication lebih lambat dan operasi lebih kompleks | Uji crash/retry, mixed-snapshot invariant dan waktu pemulihan |

### Mengapa memakai beberapa bahasa dan database dari sisi bisnis?

Pembagian Go/Rust/C++/Python adalah investasi pada kontrol compute, concurrency,
dan ekosistem tooling. Manfaat baru ada jika throughput, latency, atau produktivitas
eksperimen membaik cukup untuk mengimbangi biaya build, debugging, perekrutan dan
pemeliharaan. Memakai lebih banyak bahasa bukan nilai produk dengan sendirinya.
Untuk tim kecil, integrasi lebih sederhana bisa memberi waktu rilis lebih pendek;
perbandingan harus memakai workload dan kualitas yang sama.

Demikian juga Qdrant untuk pencarian, Neo4j untuk traversal, PostgreSQL untuk state
otoritatif, dan blob storage untuk sumber. Masing-masing punya tanggung jawab jelas,
tetapi backup, observability dan publication lintas backend menambah beban operasi.
Jika pengukuran menunjukkan satu storage dapat memenuhi kebutuhan dengan biaya
lebih rendah, konsolidasi layak diusulkan. Itu keputusan arsitektur terpisah, bukan
perubahan yang dilakukan oleh dokumen ini.

Provider lokal memberi kontrol eksekusi dan kapasitas, tetapi perangkat harus cukup
dan operasi model menjadi tanggung jawab tim. API memindahkan sebagian operasi
model ke provider dengan biaya pemakaian, jaringan dan ketergantungan layanan.
Lokal tidak otomatis paling murah; bandingkan biaya total pada volume dan mutu
jawaban yang sama, termasuk pemakaian perangkat ketika idle.

### Bagaimana mengukur apakah produk benar-benar berguna?

Uji pengguna dengan tugas yang sebanding. Catat waktu menemukan sumber, mengikuti
rujukan, menyusun jawaban, memeriksa bukti dan memperbaiki kesalahan. Definisi
“tugas selesai” harus memasukkan kelengkapan dan kebenaran yang ditentukan sebelum
uji; jawaban cepat yang salah bukan keberhasilan bisnis.

Secara konseptual:

$$\Delta T_{task}=T_{manual}-T_{assisted}$$

Waktu assisted mencakup penggunaan sistem dan pemeriksaan/koreksi manusia. Selisih
positif berarti waktu berkurang pada tugas tersebut; angka ini belum tersedia
hanya karena demo berjalan. Urutan tugas/peserta perlu diatur agar efek belajar
tidak disalahartikan sebagai manfaat produk.

Biaya operasi juga perlu dilihat secara menyeluruh:

$$C_{period}=C_{ingestion}+C_{query}+C_{storage}+C_{operations}+C_{review}$$

$$C_{per\ accepted\ task}=C_{period}/N_{accepted\ tasks}$$

Periode dan workload harus sama; masukkan biaya permintaan gagal/retry ke total,
dan laporkan jumlah seluruh tugas, coverage, abstention serta error di samping
tugas yang diterima. Jika tidak ada tugas diterima, rasio tidak terdefinisi dan
tidak dilaporkan sebagai nol. Ini kerangka pengukuran, bukan proyeksi penghematan.

Metrik produk tersebut melengkapi required engineering gates, tidak menggantinya.
Target numerik tetap mengikuti [benchmark policy](../benchmark-policy.md) dan
[benchmark-targets.yaml](../../configs/benchmark-targets.yaml).

### Contoh jawaban interview tentang keputusan bisnis

“Saya memilih struktur pasal dan parent context karena pengguna perlu memeriksa
dasar jawaban, termasuk syarat dan pengecualiannya. Hybrid retrieval melayani
pengguna yang tahu istilah resmi maupun yang bertanya dengan parafrasa. Graph
ditujukan untuk rujukan lintas dokumen, tetapi menambah biaya pemrosesan dan operasi.
Karena itu manfaatnya perlu dibuktikan lewat kelengkapan bukti serta waktu
penyelesaian tugas, bersama latency dan biaya. Saya tidak menganggap arsitektur
yang lebih kompleks otomatis menghasilkan produk yang lebih baik.”
