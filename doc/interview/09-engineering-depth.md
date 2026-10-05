# Evaluation, Reliability, Observability, Scalability, dan System Design

Dokumen ini menyiapkan pembahasan interview tentang kualitas engineering
RegulaGraph-ID. Seperti [arsitektur](01-architecture.md) dan [contoh pipeline](08-input-output-examples.md),
penjelasannya memakai asumsi sistem lengkap. Ini bukan laporan bahwa seluruh
mekanisme sudah aktif atau teruji; bukti aktual mengacu ke
[status implementasi](07-implementation-status.md) dan laporan verifikasi terkait.

Cara menjelaskan setiap keputusan: **masalah pengguna → invariant/target → desain →
failure case → cara mengukur → trade-off → bukti aktual**. Nama teknologi saja tidak
menunjukkan bahwa keputusan tersebut tepat untuk kebutuhan sistem.

## 1. Evaluation — bagaimana tahu sistem lebih baik?

### Pertanyaan yang perlu dijawab

“Lebih baik” berarti menemukan bukti yang diperlukan, memilih versi yang sesuai,
menghasilkan klaim yang didukung, dan memenuhi biaya/latency pada workload yang
disepakati. Jawaban fasih, banyak edge graph, atau build yang lulus belum menjawab
pertanyaan itu.

| Lapisan | Yang dinilai | Contoh kegagalan | Bukti pengukuran |
| --- | --- | --- | --- |
| Parsing/chunking | Struktur, angka/negasi, locator dan cakupan konteks | Kata “tidak” hilang atau pengecualian terpotong | Gold parsing, source-span checks, retrieval dengan beberapa strategi chunk |
| Extraction/resolution | Predicate/arah/qualifier, candidate recall, false merge/split | Dua penerbit berbeda digabung karena nomor regulasinya sama | Label mention/pasangan/relasi, confusion analysis dan review kasus ambigu |
| Retrieval | Recall@k, nDCG@k, kelengkapan set bukti dan versi | Batas waktu ditemukan, tetapi pengecualian tidak ikut | Ranked output produksi dibanding gold evidence per pertanyaan |
| Answer/citation | Correctness, dukungan klaim, cakupan citation, abstention | ID sitasi valid tetapi klaim membalik makna sumber | Penilaian claim-evidence dan review manusia sesuai policy dataset |
| Runtime | p50/p95/p99, TTFT, antrean, throughput, memori/biaya | Rata-rata cepat tetapi request saat ingestion timeout | Load run dengan arrivals/failures tercatat, resource profile dan manifest |

Contoh A/B pada dokumen 08 memerlukan aturan 14 hari, sumber perubahan, dan
pengecualian kegiatan internal. Menemukan aturan batas waktu saja belum cukup.
Jika gold menetapkan beberapa kombinasi bukti yang sama-sama sah, cukup memenuhi
satu set yang lengkap; tidak perlu memaksa seluruh alternatif muncul bersamaan.

Untuk keluarga set bukti sah G dan hasil retrieval R:

$$complete(q)=\mathbf{1}[\exists S\in G(q):S\subseteq R(q)]$$

Ini berbeda dari rata-rata recall potongan. Pertanyaan tanpa bukti memerlukan
penilaian abstention tersendiri; jangan membagi dengan gold kosong lalu memberi
skor sempurna otomatis. Gold dipakai evaluator, bukan dibocorkan ke query workflow.

### Desain eksperimen yang dapat dipercaya

1. Bekukan corpus snapshot, group split, model/tokenizer/prompt, konfigurasi,
   hardware, workload dan policy cache. Simpan identitasnya dalam run manifest.
2. Pisahkan paraphrase berdasarkan pertanyaan dasar agar tidak bocor antar split.
   Tuning di dev; test tidak menjadi tempat memilih konfigurasi yang menang.
3. Bandingkan Vector RAG, Hybrid RAG, GraphRAG dan Hybrid GraphRAG dengan perbedaan
   branch, reranker, konteks dan compute budget dinyatakan eksplisit. Jangan
   mengaktifkan graph diam-diam pada baseline vector.
4. Lakukan ablation untuk menjawab sebab: efek graph, reranker, parent hydration,
   routing atau retry. Mengganti semuanya sekaligus menyulitkan atribusi manfaat.
5. Laporkan per jenis pertanyaan: factual, prosedural, multi-hop, temporal,
   typo, informal, code-switch dan tanpa bukti; sertakan ukuran sampel serta
   ketidakpastian. Rata-rata baik dapat menyembunyikan kegagalan satu kategori.
6. Catat semua arrivals, reject, timeout dan failure. Acceptance mengikuti profil
   beban pada policy; hasil laptop/smoke tidak disamakan dengan profil referensi.

LLM judge dapat membantu penyaringan analisis, tetapi perlu diperiksa bias serta
kesepakatannya dengan penilaian manusia. Parity native terhadap model reference
membuktikan kesesuaian implementasi pada kasus uji, bukan kualitas model untuk
regulasi Indonesia.

**Alasan bisnis:** evaluasi membantu memilih peningkatan yang memang mengurangi
waktu riset/koreksi pengguna. Biayanya berupa anotasi, review dan compute eksperimen.
Tanpa evaluasi, optimasi dapat sekadar memindahkan biaya dari server ke manusia
yang harus memperbaiki jawaban.

**Jawaban interview:** “Saya memisahkan masalah retrieval dari generation. Jika
pengecualian tidak masuk konteks, memperbaiki prompt saja belum menyelesaikan
penyebabnya. Saya membandingkan perubahan pada snapshot dan dataset yang sama,
lalu melihat evidence completeness, correctness, serta p95/p99 bersama.”

Lokasi: [evaluation](../../evaluation/README.md),
[runner](../../evaluation/runner.py), [metrics](../../evaluation/metrics/README.md),
[datasets](../../evaluation/datasets/README.md). Ambang dan protokol tetap mengikuti
[benchmark policy](../benchmark-policy.md) dan
[benchmark-targets.yaml](../../configs/benchmark-targets.yaml).

## 2. Reliability — apa yang terjadi ketika sesuatu gagal?

Keandalan mencakup respons layanan dan integritas datanya. Respons sukses yang
mencampur versi hukum atau mengutip sumber korup bukan hasil yang andal. Pisahkan
semantic status, seperti partial/abstain, dari completion status, seperti
failed/cancelled.

### Invariant utama

- Satu request menggunakan snapshot dan generation yang konsisten sampai selesai.
- Sumber, span, provision version dan support dapat ditelusuri; hash/ID tidak ditebak.
- Retry tidak menggandakan mutasi dan tidak memperoleh deadline/budget baru.
- Pointer aktif berubah setelah backend wajib siap dibaca, bukan sekadar menerima write.
- Versi lama dan support independen tidak hilang saat satu dokumen diperbarui.
- Output attempt yang kehilangan kewenangan tidak boleh di-commit sebagai hasil baru.

### Kasus kegagalan yang layak dibahas

| Gangguan | Perilaku yang dirancang | Verifikasi yang diperlukan |
| --- | --- | --- |
| PDF terpotong atau hash tidak cocok | Artefak tidak dipromosikan sebagai sumber siap | Corrupt/truncated input dan pemeriksaan status/receipt |
| Worker mati setelah menulis artefak | Resume memakai intent/checkpoint; artefak saja bukan bukti job sukses | Crash sebelum/sesudah checkpoint dan replay idempotent |
| Qdrant berhasil, Neo4j wajib gagal | Snapshot baru belum aktif; recovery membaca ledger | Fault injection di antara backend writes dan sebelum publication |
| Respons write hilang | Tentukan hasil lewat operation identity/readback sebelum retry aman | Lost-ack test; tidak ada duplicate mutation |
| Worker/publisher lama mengirim hasil terlambat | Validasi attempt/fence/revision dan tahan unsafe takeover | Delayed response serta in-flight write pada pergantian publisher |
| Snapshot baru publish saat query | Query lama tetap membaca snapshot yang dipin | Concurrent publication/read, termasuk parent dan path support |
| Model timeout atau output tidak valid | Failure/revision/abstain sesuai sebab; tidak menjadi jawaban lengkap | Deadline, malformed output, unsupported/unknown citation |
| Graph branch gagal | Gagal eksplisit atau fallback yang diizinkan dengan effective profile tercatat | Partial backend failure; tidak mengaku GraphRAG lengkap setelah melewati graph |
| User disconnect | Batalkan turunan dan lepas queue slot/read lease | Cancellation selama antrean, inference dan streaming |

Lease PostgreSQL saja tidak menghentikan write backend lama yang sudah dikirim.
Takeover publication perlu memastikan writer lama tidak lagi dapat mengubah state,
dan kompensasi dilakukan sesuai ledger. Menambah retry tanpa protokol ini justru
dapat memperburuk inkonsistensi. Restore juga harus memulihkan satu manifest yang
konsisten lintas backend, bukan hanya membuktikan setiap database dapat menyala.

**Alasan bisnis:** mengurangi hasil salah akibat gangguan, pekerjaan perbaikan
manual dan kehilangan kepercayaan. Harganya adalah state machine, storage tambahan,
uji failure, serta kemungkinan menahan publication demi menjaga konsistensi.
Query terhadap snapshot committed dapat tetap tersedia jika dependency bacanya sehat.

**Jawaban interview:** “Saya tidak mengklaim transaksi ACID lintas semua storage.
Go memiliki intent dan publication; worker menghasilkan batch. Ketika satu write
berhasil tetapi backend lain gagal, snapshot lama tetap menjadi acuan. Recovery
membaca ledger dan memeriksa hasil operasi sebelum melanjutkan.”

Lokasi: [workflows](../../src/server/internal/workflows/README.md),
[indexing](../../src/server/internal/indexing/README.md),
[PostgreSQL adapter](../../src/server/internal/adapters/postgres/README.md),
[storage adapter](../../src/server/internal/adapters/storage/README.md).
Rujukan invariant: [storage consistency](../storage-consistency.md).

## 3. Observability — bagaimana mengetahui penyebab masalah?

Log error saja tidak cukup untuk menjelaskan jawaban lambat atau salah. Observability
menghubungkan **apa yang dialami pengguna, tahap penyebabnya, dan versi data/model
yang digunakan**. Telemetry tidak otomatis membuktikan kebenaran semantik.

| Sinyal | Contoh isi | Pertanyaan yang dijawab |
| --- | --- | --- |
| Metrics | Request/error/reject rate, latency histogram, queue depth, tokens, RSS/VRAM, freshness lag | Seberapa luas dampaknya dan kapan mulai terjadi? |
| Traces | Span admission, embed, BM25, dense, graph, hydration, rerank, generation, validation/retry | Tahap atau dependency mana yang memperpanjang critical path? |
| Structured logs | Error code, stage, attempt, operation/request ID, safe reason | Peristiwa apa yang terjadi pada operasi tertentu? |
| Manifests/audit | Snapshot, generation, model/prompt/config fingerprints, decision dan support refs | Dengan keadaan apa hasil dibuat, dan apakah bisa direproduksi? |

Contoh struktur trace, **tanpa angka hasil pengukuran**:

```text
request req-1 [snapshot=snap-12, generation=gen-3, profile=hybrid-graphrag]
  admission / queue
  normalize / classify / entity candidates
  retrieval
    BM25 -------------------------------+
    query embedding -> dense -----------+ parallel sesuai dependency
    graph dari alias / dari dense seeds +
  hydrate candidate text -> rerank
  context -> evidence gap: exception missing
  targeted retrieval [iteration=2, snapshot tetap]
  generation [queue, prefill/TTFT, decode, output tokens]
  claim/citation validation
  response terminal / lease release
```

Request ID digunakan untuk korelasi trace/log, bukan label metrics yang menghasilkan
seri baru untuk setiap request. Dimensi metrics dipilih berbatas, misalnya stage,
profile dan outcome. Sampling trace harus dicatat; trace terpilih bukan denominator
untuk menghitung seluruh failure rate. Log default tidak menyimpan API key atau
seluruh query/dokumen; input reproduksi disimpan lewat artefak terkontrol.

### Contoh diagnosis

| Gejala | Pemeriksaan pertama | Hipotesis dan tindakan berikutnya |
| --- | --- | --- |
| End-to-end lambat, backend search tetap cepat | Queue, model TTFT/decode dan jumlah token | Profil model/antrean; optimasi BM25 tidak menyelesaikan bottleneck generation |
| Latency naik ketika ingestion berjalan | Queue per workload, GPU/CPU/connection pool | Perebutan resource; periksa fairness, admission dan ukuran batch |
| Jawaban menghilangkan pengecualian | Evidence IDs sebelum/sesudah fusion, rerank dan context | Bedakan retrieval miss, pruning, context truncation, dan salah interpretasi model |
| Jawaban memakai versi lama | Tanggal, snapshot/generation, filter keputusan dan cache binding | Bedakan snapshot lama yang sah dari cache/filter yang salah |
| Regresi setelah ganti prompt/model | Producer fingerprints dan hasil per slice | Reproduksi run yang sebanding sebelum menyimpulkan penyebab |

Gunakan durasi monotonic untuk tahap lokal dan pengukuran end-to-end dari client/load
generator. Jangan menjumlahkan p95 tiap span untuk mengklaim p95 request. Alert
dikaitkan ke dampak yang terukur—failure, latency, backlog/freshness atau invariant—
beserta owner dan langkah diagnosis; jumlah log yang banyak bukan keberhasilan.

**Alasan bisnis:** memperpendek diagnosis dan mengarahkan biaya optimasi ke bottleneck
yang benar. Harganya overhead instrumentation, penyimpanan dan pengelolaan akses.
Pilih telemetry yang menjawab keputusan operasi; ukur overheadnya.

**Jawaban interview:** “Kalau jawaban salah, saya ingin tahu apakah evidence tidak
ditemukan, dibuang saat reranking, terpotong ketika context packing, atau sudah
tersedia tetapi salah ditafsirkan model. Trace dan evidence provenance membantu
memisahkan empat penyebab itu.”

Pemilik: [Go server](../../src/server/README.md),
[workflows](../../src/server/internal/workflows/README.md),
[native inference](../../src/inference/README.md), dan
[runtime metrics](../../evaluation/metrics/runtime.py).
Kontrak sinyal mengikuti [system design](../system-design.md); dokumen ini tidak
menetapkan vendor telemetry atau mengklaim dashboard sudah tersedia.

## 4. Scalability — apa yang terjadi ketika beban bertambah?

Pisahkan pertumbuhan **jumlah dokumen, frekuensi update, query bersamaan, panjang
konteks, dan kompleksitas graph**. Masing-masing menekan komponen berbeda.
Menambah replica API tidak otomatis menambah kapasitas GPU atau database.

| Pertumbuhan | Bottleneck yang mungkin muncul | Arah desain dan trade-off |
| --- | --- | --- |
| Corpus membesar | Parse/OCR, embedding, RAM/storage/index | Batch dan incremental; ANN/partition dievaluasi terhadap recall, bukan ukuran saja |
| Update lebih sering | Dependency closure, publication dan freshness lag | Komputasi batch paralel, publication serial per corpus; writer concurrency tidak boleh merusak snapshot |
| Query bersamaan naik | Model queue, DB pool, bandwidth, CPU | Admission berbatas, warm sessions, replica sesuai bottleneck; antrean tak terbatas bukan kapasitas |
| Dokumen/query panjang | Tokenizer, prefill, batch memory | Token/shape-aware batching dan context budget; truncation tercatat dan completeness diperiksa |
| Graph semakin terhubung | Fan-out, path explosion, hydration | Budget traversal dan deduplikasi terukur; pruning bisa menghilangkan evidence penting |
| Ingestion dan query bersamaan | GPU/CPU/pool dipakai bersama | Kapasitas dan antrean dibatasi per workload; prioritaskan query tanpa membuat ingestion tidak pernah mendapat layanan |

Untuk antrean stabil, Little's law menghubungkan rata-rata concurrency L, throughput
lambda dan waktu tinggal W:

$$L=\lambda W$$

Ini hubungan rata-rata, bukan rumus langsung untuk menentukan worker count atau
p95. Jika arrival terus melebihi kapasitas layanan, antrean tumbuh; menambah queue
memindahkan masalah menjadi waktu tunggu dan penggunaan memori.

Batch yang lebih besar dapat meningkatkan item/detik sekaligus menambah waktu
menunggu batch dan kebutuhan VRAM. Ukur throughput **bersama** p95/p99, error,
panjang input, dan campuran ingestion/query. Deadline serta cancellation diteruskan
lintas proses; pekerjaan setelah pengguna berhenti juga merupakan biaya kapasitas.

### Scale-out dan cache harus menjaga correctness

Go reader dapat direplikasi dengan state otoritatif tetap di storage bersama.
Worker memproses batch yang lease/attempt-nya sah. Snapshot publication tetap
memiliki satu publisher efektif per corpus; throughput batch dan throughput commit
adalah dua hal berbeda. Backend replica yang menerima query harus memenuhi
readiness/watermark snapshot, bukan sekadar merespons health check.

Cache perlu terikat pada input semantik: corpus/scope, snapshot, tanggal/policy,
model/generation dan konfigurasi yang relevan. Result cache tanpa binding itu dapat
cepat tetapi salah versi. Cache key embedding query dapat berbeda dari cache hasil;
tidak semua cache perlu menyimpan seluruh snapshot, tetapi harus mempertahankan
dependency yang menentukan outputnya. Acceptance utama mengikuti policy cache yang
ditetapkan, sehingga warm cache tidak dipakai untuk menyamarkan compute sebenarnya.

Capacity test memakai beberapa tingkat beban untuk menemukan titik saturasi;
hasil diagnostik itu tidak mengganti workload acceptance required. Laporkan
rejected/timeout dan kualitas pada beban yang sama, serta cold/warm secara terpisah.

**Alasan bisnis:** menjaga waktu respons dan freshness ketika penggunaan meningkat
dengan biaya yang bisa diperkirakan. Autoscaling, replication dan cache menambah
biaya serta kompleksitas; scale komponen yang terbukti membatasi layanan.

**Jawaban interview:** “Saya memisahkan scaling ingestion dari query. Worker dapat
bertambah untuk komputasi batch, tetapi publication satu corpus tetap diserialkan
demi konsistensi. Untuk query, saya mengukur antrean model dan database sebelum
menambah replica API. Throughput tinggi tidak cukup kalau p99 dan timeout memburuk.”

Lokasi: [batching.cpp](../../src/inference/src/batching.cpp),
[Rust worker](../../src/ingestion/src/worker/README.md),
[retrieval workflow](../../src/server/internal/workflows/retrieval.go),
[indexing](../../src/server/internal/indexing/README.md).
Ini pembahasan desain, bukan klaim load test atau deployment multi-replica selesai.

## 5. System Design — mengapa batas komponen seperti ini?

Mulai dari kebutuhan: pertanyaan factual/prosedural/lintas dokumen/temporal,
kutipan yang dapat dibuka, pembaruan incremental, latency rendah, dan versi yang
konsisten. Baru pilih pembagian proses dan storage yang mendukungnya.

| Keputusan | Alasan desain | Alternatif dan konsekuensi |
| --- | --- | --- |
| Offline ingestion terpisah dari query | Parse/extract/index tidak diulang setiap pertanyaan | Query-time processing sederhana untuk satu file, tetapi mahal jika corpus terus dipakai |
| Go memegang workflow/state; Rust batch; C++ inference; Python offline | Ownership dan boundary workload eksplisit | Lebih sedikit bahasa mengurangi build/debugging; manfaat pembagian perlu pengukuran |
| Fusion/filter/context/citation dalam Go | Menghindari RPC untuk tiap fungsi kecil | Service terpisah memberi isolation tetapi menambah serialisasi, failure dan operasi |
| Protobuf dan manifest pada boundary besar | Schema, ID, versi dan korelasi hasil konsisten lintas runtime | JSON sederhana untuk inspeksi; kontrak kompatibilitas tetap diperlukan |
| PostgreSQL sebagai authority; search/graph sebagai representasi terikat manifest | State durable dan retrieval punya kebutuhan berbeda | Konsolidasi storage dapat lebih sederhana, tetapi perlu membuktikan kemampuan workload/filter |
| Structural chunks + parent context | Unit retrieval spesifik dengan konteks yang tetap dapat dilacak | Window/semantic-only lebih fleksibel tetapi perlu kontrol batas/source/version |
| Canonical registry terpisah dari model proposal | Reasoning model tidak langsung mengubah identity global | Semua rule terbatas pada variasi; semua model menambah ketidakpastian mutation |
| Hybrid retrieval + graph bersumber | Exact terms, parafrasa dan hubungan memiliki kebutuhan berbeda | Satu retriever lebih murah; nilai tambah tiap branch perlu ablation |
| Adaptive retrieval berbatas | Cari dependency hilang sambil menjaga deadline/cost | Satu putaran lebih cepat; loop tak terbatas sulit diprediksi dan belum tentu benar |

### Boundary yang perlu bisa dijelaskan saat ditanya

**Authority:** siapa boleh membuat canonical ID, siapa hanya mengusulkan, dan siapa
mengubah active snapshot? Registry/coordinator Go menjadi pemilik keputusan durable.
Worker menghasilkan batch dan model menghasilkan proposal; keduanya tidak membuat
sumber kebenaran global yang berbeda.

**Data contract:** apa arti source ID, canonical ID, provision-version, snapshot dan
generation? Bagaimana hasil batch dikorelasikan jika urutan berubah? Input invalid,
schema incompatible atau artifact hash salah harus menghasilkan error yang jelas.
Generated binding tidak diedit manual untuk menghindari kontrak menyimpang.

**Security:** auth/scope berasal dari server, bukan field yang dipercaya begitu saja
dari user. Source/model output diperlakukan sebagai data tak tepercaya; teks PDF
tidak berwenang mengubah instruksi sistem. Path/URL/storage access dan ukuran payload
dibatasi di boundary yang sesuai. Log tidak menjadi jalur kebocoran credential.

**Consistency:** apa yang dilihat reader ketika corpus berubah? Pin snapshot dan
legal date menjawab dua pertanyaan berbeda; publication readiness menghubungkan
semua representasi. Kegagalan satu dependency tidak boleh diam-diam mengubah makna
hasil menjadi “tidak ada aturan”.

**Evolution:** mengganti model/tokenizer/analyzer dapat mengubah representasi indeks.
Generation baru dibangun dan divalidasi sebelum publish; ID versi dan provenance
lama tetap dapat dirujuk sesuai retention. Kemampuan decode schema bukan satu-satunya
ukuran kompatibilitas semantik.

**Alasan bisnis:** batas ownership dan kontrak mengurangi biaya perubahan serta
diagnosis. Kompleksitas arsitektur harus dibenarkan oleh kebutuhan; jangan memecah
service atau menambah storage hanya untuk memperbanyak nama teknologi di CV.

**Jawaban interview:** “Saya mulai dari invariant sumber, versi dan snapshot, lalu
menentukan pemilik state. Pembagian bahasa mengikuti workload besar, sedangkan
fungsi retrieval kecil tetap satu proses. Trade-off utamanya adalah kontrol
performa versus biaya integrasi dan operasi lintas runtime/backend.”

Rujukan: [system contracts](../system-contracts.md),
[storage consistency](../storage-consistency.md),
[decisions](02-decisions.md), dan [code map](05-code-map.md).

## 6. Menghubungkan kelima aspek dalam satu cerita interview

Gunakan kasus dari dokumen 08: jawaban menyebut 30 hari padahal versi untuk tanggal
query adalah 14 hari. Ini **skenario diagnosis ilustratif**, bukan insiden yang
diklaim sudah terjadi di produksi.

1. **Evaluation:** kasus temporal mendeteksi jawaban/versi salah; tentukan expected
   evidence dan outcome, bukan hanya ketidakcocokan kata pada jawaban.
2. **Observability:** telusuri snapshot, effective date, generation, filter,
   candidate version dan isi konteks untuk menemukan titik penyimpangan.
3. **Reliability:** periksa apakah cache, publication atau hydration mencampur
   state; pertahankan snapshot yang konsisten dan jangan meluluskan output salah.
4. **System Design:** perbaiki pada pemilik invariant—misalnya binding cache atau
   filter versi—bukan menambahkan prompt untuk menyembunyikan masalah storage.
5. **Scalability:** uji ulang ketika update berjalan bersama query dan ketika beban
   meningkat; correctness yang hanya berlaku saat single request belum cukup.

Saat menjelaskan pekerjaan aktual, tutup cerita dengan bukti yang memang ada:
diff, regression test, manifest/log run, hasil sebelum/sesudah dan keterbatasannya.
Jika hanya desainnya tersedia, sebut desain; jika hanya fixture lulus, sebut fixture.
Tidak ada angka peningkatan, load capacity atau quality PASS yang disimpulkan dari
diagram. Target required tetap tidak berubah.

Checklist persiapan interview: bisa menjelaskan satu keputusan, satu alternatif,
satu failure case, satu metrik dan satu lokasi kode untuk setiap aspek. Bukti hasil
diambil dari [status aktual](07-implementation-status.md), bukan dari asumsi sistem
lengkap dalam bahan belajar ini.
