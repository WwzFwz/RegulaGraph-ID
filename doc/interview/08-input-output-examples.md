# Input, proses, output, dan contoh setiap tahap

Dokumen ini melengkapi [arsitektur](01-architecture.md) dan [flow](03-flows.md)
dengan perjalanan data konkret pada **rancangan sistem lengkap**. Contoh di bawah
adalah ilustrasi, bukan data hukum, hasil eksekusi, atau kontrak API baru. Nama ID
dipendekkan supaya mudah dibaca; bentuk wire sebenarnya mengikuti
[system-contracts](../system-contracts.md) dan [Protobuf](../../src/contracts/proto/README.md).
Status implementasi tetap berada di [dokumen 7](07-implementation-status.md).

## 1. Satu contoh yang dipakai sepanjang alur

Bayangkan corpus berisi dua **peraturan fiktif** berikut:

| Dokumen | Isi ilustratif | Informasi waktu bersumber |
| --- | --- | --- |
| Peraturan Badan Contoh Nomor 1 Tahun 2024, disingkat A | Pasal 5 ayat (1): “Penyelenggara wajib menyampaikan laporan paling lambat 30 hari setelah kegiatan berakhir.” Ayat (2): “Kewajiban pada ayat (1) tidak berlaku bagi kegiatan internal.” | Ketentuan awal berlaku mulai 1 Januari 2024 |
| Peraturan Badan Contoh Nomor 2 Tahun 2025, disingkat B | Pasal 2 mengganti batas pada A Pasal 5 ayat (1) menjadi 14 hari; ayat (2) tidak diubah | Perubahan berlaku mulai 1 Juli 2025 |

Pertanyaan contoh: **“Per 1 Agustus 2025, berapa batas waktu laporan kegiatan,
dan apakah kegiatan internal juga wajib?”** Jawaban yang diharapkan dari contoh
ini adalah 14 hari setelah kegiatan berakhir, dengan pengecualian kegiatan internal.
Jawaban harus menunjukkan sumber aturan, perubahan, dan pengecualiannya.

Identitas yang mengikuti contoh:

| Label ringkas | Makna |
| --- | --- |
| `src-A`, `src-B` | Sumber dokumen, disertai observation dan blob hash masing-masing |
| `reg-A` | Identitas regulasi A yang ditetapkan registry |
| `p5-1`, `p5-2` | Identitas ketentuan A Pasal 5 ayat (1) dan ayat (2) |
| `v5-1-old`, `v5-1-new`, `v5-2` | Versi lama ayat (1), versi hasil perubahan, dan versi ayat (2) |
| `snap-12` | Snapshot pengetahuan corpus yang sudah dipublikasikan |
| `gen-3` | Generation representasi indeks: model, tokenizer, analyzer, dictionary/statistik |

`snap-12` bukan tanggal hukum. `gen-3` bukan versi peraturan. Satu file PDF juga
dapat memuat banyak pasal, sedangkan satu versi rekonstruksi dapat memakai beberapa
sumber. Karena itu hash file, ID regulasi, ID versi, dan ID chunk tidak disatukan.

## 2. Persiapan corpus: dari portal sampai snapshot siap dicari

### I1. Discovery — Go acquisition

**Input:** daftar portal/listing, konfigurasi connector, batas crawl dan checkpoint.
**Proses:** membaca halaman daftar/detail, mengikuti pagination yang diizinkan,
dan mengenali tautan dokumen. **Output:** kandidat URL detail/PDF dan metadata portal.

Contoh: satu halaman listing menghasilkan kandidat A dan B, masing-masing dengan
judul, penerbit yang dinyatakan portal, dan URL unduh. Kandidat URL belum berarti
PDF berhasil diperoleh atau identitas hukumnya telah diverifikasi.

### I2. Download dan audit — Go acquisition + artifact storage

**Input:** kandidat URL dan policy download. **Proses:** mengunduh dengan batas
ukuran/rate/retry, memeriksa respons/file, menghitung hash, menyimpan receipt.
**Output:** `SourceObservation`, `SourceBlob`, dan referensi artefak immutable.

Contoh: URL A menghasilkan `src-A`, blob dengan hash `sha256:...`, ukuran,
waktu observasi dan metadata redirect. Mirror dengan byte identik dapat berbagi
blob, tetapi observation asalnya tetap disimpan. Respons HTML error dari URL PDF
menjadi kegagalan akuisisi, bukan dokumen PDF sukses.

### I3. Perencanaan job — Go coordinator

**Input:** source refs, fingerprint source/config/model, checkpoint dan dependency
sebelumnya. **Proses:** menentukan reuse atau reprocess, membuat intent job dan
menetapkan attempt/lease/fence saat pekerjaan diambil. **Output:** job durable
dan rencana tahap dengan referensi input terpin.

Contoh: A baru memerlukan PARSE sampai INDEX. A identik yang dikirim ulang dapat
memakai artefak lama bila seluruh dependency relevan cocok. Mengganti model
embedding menginvalidasi representasi dense meskipun PDF tetap sama.

### I4. Parse dan OCR — Rust document worker

**Input:** referensi PDF terverifikasi dan manifest parser/OCR. **Proses:** membaca
text layer, reading order dan locator; halaman scan melalui OCR. **Output:**
`TextArtifact` dengan teks sumber per halaman, locator dan status halaman.

Contoh: halaman 3 A menghasilkan teks Pasal 5. Halaman scan yang gagal OCR
menghasilkan issue/page failure; sistem tidak mengklaim dokumen lengkap hanya
karena halaman lain berhasil. Tabel mempertahankan hubungan baris/kolom yang relevan.

### I5. Normalisasi dan struktur — Rust document worker

**Input:** teks sumber, page locators dan policy normalisasi. **Proses:** merapikan
artefak layout tanpa membuang angka/negasi, memetakan teks normalisasi ke sumber,
lalu mengenali hierarchy. **Output:** teks normalisasi, offset mapping dan
`StructureNode` untuk bab, pasal, ayat, huruf atau tabel.

Contoh: “Pasal 5”, “(1)” dan “(2)” menjadi parent dan dua child, bukan satu paragraf
tanpa struktur. Kata “tidak” pada ayat (2) tetap ada. Span wire menggunakan byte
UTF-8 start-inclusive/end-exclusive; offset token atau nomor karakter bukan pengganti.

### I6. BIND dan identitas ketentuan — Go registry

**Input:** metadata bersumber, hierarchy dan registry revision. **Proses:**
mencocokkan regulasi dalam scope penerbit/jenis/nomor/tahun, kemudian mengikat
ketentuan dan versi yang dapat ditetapkan. **Output:** canonical bindings,
provision/version refs atau konflik yang perlu diselesaikan.

Contoh: A Pasal 5 ayat (1) terikat ke `reg-A/p5-1/v5-1-old`. Peraturan nomor 1
dari penerbit lain tidak digabung hanya karena nomornya sama. BIND menyediakan
identitas dokumen/ketentuan untuk CHUNK; resolusi semua mention semantik dilakukan
setelah EXTRACT. Registry Go memiliki assignment otoritatif.

### I7. Structural chunking — Rust document worker

**Input:** hierarchy, teks beserta mapping, provision bindings dan tokenizer terpin.
**Proses:** membentuk unit retrieval mengikuti pasal/ayat, memecah unit terlalu
panjang dengan batas token, dan menyimpan parent refs. **Output:** `Chunk` dengan
teks/span, versi ketentuan, node induk dan manifest chunker.

Contoh: `chunk-5-1-old` memuat aturan 30 hari dan `chunk-5-2` memuat pengecualian.
Keduanya menunjuk parent Pasal 5. Hubungan pengecualian yang ditemukan kemudian
dapat memperkaya dependency; kedekatan posisi saja tidak membuktikan makna exception.
Konteks induk dapat dirender untuk embedding atau diambil saat query sesuai policy.

### I8. Ekstraksi fakta dan relasi — semantic gateway + Rust/Go validation

**Input:** chunk berbukti, ontology, schema serta model/prompt terpin. **Proses:**
mengambil rujukan eksplisit dan proposal relasi semantik, kemudian memeriksa bentuk
serta kecocokan evidence terhadap sumber. **Output:** mention, proposal assertion,
support spans dan issue; identitas mention masih dapat provisional.

Contoh konseptual: `penyelenggara -> wajib menyampaikan -> laporan`, dengan
qualifier `batas=30 hari` dan `pemicu=kegiatan berakhir`. Ayat (2) memberi
pengecualian kegiatan internal; B memberi proposal perubahan batas. Nama panah ini
penjelasan makna, bukan tambahan predicate baru di ontology. Output yang mengaku
“7 hari” tanpa span sumber ditolak atau dikarantina, bukan langsung menjadi fakta.

### I9. Resolusi entitas dan rujukan — Rust proposals + Go registry

**Input:** mention, kandidat berscope, evidence kedua sisi dan registry revision.
**Proses:** exact matching bila cukup pasti; model membantu ambiguity dengan
konteks, lalu keputusan divalidasi dan dicatat. **Output:** `ResolutionProposal`
diikuti `ResolutionDecision`/canonical assignment atau DEFER/review.

Contoh: frasa “Peraturan Badan Contoh Nomor 1 Tahun 2024” dalam B dihubungkan ke
`reg-A`, dan target perubahan ke `p5-1`. Alias yang menunjuk dua entitas menghasilkan
beberapa kandidat. Model tidak memperoleh izin merge hanya dari kemiripan nama;
proposal terhadap registry revision yang sudah berubah perlu diperiksa ulang.

### I10. Transformasi versi — Rust transform + Go validation

**Input:** teks lama, change-event bersumber, target ketentuan yang sudah terikat
dan tanggal berlaku yang memiliki dukungan. **Proses:** menerapkan perubahan pada
unit yang terkena dan menyimpan lineage rekonstruksi. **Output:** provision version,
interval legal, supporting events dan status konflik/unknown bila diperlukan.

Contoh: `v5-1-old` berlaku pada [2024-01-01, 2025-07-01), sedangkan `v5-1-new`
mulai 2025-07-01. `v5-2` tetap dipertahankan karena B tidak mengubah ayat (2).
Teks rekonstruksi 14 hari menelusuri A dan B; ia tidak diklaim sebagai kutipan
verbatim A lama. Jika target/tanggal ambigu, versi hasil rekonstruksi ditahan untuk
review. Urutan tahap dapat berulang bila penyelesaian versi memerlukan RESOLVE;
diagram linear bukan alasan memaksa dependency yang belum tersedia.

### I11. Assembly graph — Rust, dengan commit oleh Go

**Input:** canonical assignments, assertion/support tervalidasi dan versi ketentuan.
**Proses:** merakit endpoint, relasi bertipe, support serta dependency tanpa
mencampur pohon struktur dengan graph semantik. **Output:** `GraphDelta` dan manifest.

Contoh: jalur yang menghubungkan B, perubahan ayat (1), dan pengecualian ayat (2)
tetap membawa support sumber masing-masing. Menghapus satu observation tidak
menghapus relasi yang masih mempunyai dukungan valid lain. Edge inferred tetap
dibedakan dari pernyataan eksplisit sumber.

### I12. Pembuatan indeks — Rust + native inference C++

**Input:** chunk terpilih, rendered text, model/tokenizer, dictionary/statistik
BM25 dan generation terpin. **Proses:** mengirim batch embedding, membentuk
representasi sparse lexical, dan menyiapkan payload filter. **Output:** `IndexBatch`
dense/sparse, mapping record-evidence-version, serta dependency manifest.

Contoh: teks ayat (1) menghasilkan vector dense 1024 dimensi untuk konfigurasi
BGE-M3, bukan ringkasan kalimat. BM25 memakai term ID dan bobot menurut statistik
generation yang sama. Vector dan skor tidak menentukan kebenaran hukum. Hasil
batch dipasangkan melalui item ID, bukan semata urutan respons. Session model
digunakan ulang dan kapasitas bulk tidak boleh menghabiskan antrean query.

### I13. Staging, readiness, publication — Go + storage backends

**Input:** batch dokumen/graph/index, expected counts/hashes, base snapshot dan
operation identity. **Proses:** validasi, staging backend, readback/search readiness,
kemudian pemindahan pointer committed sesuai protokol. **Output:** receipts,
`PublicationManifest`, katalog dan snapshot aktif yang konsisten.

Contoh: `snap-12/gen-3` baru dibaca query setelah backend wajib profilnya siap.
PostgreSQL menyimpan authority/catalog, Qdrant representasi search, Neo4j graph,
dan blob storage byte sumber/artefak. Bila salah satu backend wajib gagal, pointer
lama bertahan; retry memakai intent/checkpoint. Ini bukan transaksi ACID tunggal
lintas semua backend. Worker Rust tidak mengumumkan snapshot aktif sendiri.

## 3. Query: dari pertanyaan sampai jawaban bersitasi

### Q1. Admission dan snapshot pin — Go API/workflow

**Input:** pertanyaan contoh, permintaan tanggal/mode, identitas pengguna yang
divalidasi server, dan konfigurasi profil. **Proses:** validasi ukuran/scope,
deadline/budget, lalu pilih snapshot committed dan tahan read lease.
**Output:** request context dengan `snap-12`, `gen-3`, tanggal 2025-08-01 dan budget.

Contoh: user meminta kondisi pada 1 Agustus 2025 tetapi pengetahuan dibaca dari
`snap-12`. Request tidak boleh memaksakan corpus di luar hak akses melalui field
JSON. Semua putaran berikutnya menggunakan snapshot yang sama.

### Q2. Normalisasi — Go query preparation

**Input:** pertanyaan asli. **Proses:** normalisasi bentuk teks sesuai policy,
pertahankan identifier, tanggal dan negasi. **Output:** query asli + normalisasi,
token lexical dan catatan transformasi.

Contoh: variasi spasi dibersihkan, tetapi “apakah kegiatan internal juga wajib”
tidak diubah menjadi klaim bahwa kegiatan internal wajib. Ekspansi typo/sinonim
dicatat dan diuji; tidak otomatis mengganti nomor regulasi.

### Q3. Classify, entity linking dan retrieval planning — Go

**Input:** query, scope temporal, registry alias/candidate lookup dan profil.
**Proses:** mengenali kebutuhan bukti, ambiguity dan seed entitas; tetapkan branch
beserta dependency/budget. **Output:** `RetrievalPlan`, linked candidates dan
missing/ambiguous scope yang perlu klarifikasi.

Contoh: kebutuhan `factual + relational + temporal`, dengan tanggal 2025-08-01.
Jika corpus contoh membuat rujukan A jelas, seed menuju `reg-A`; jika ada beberapa
aturan laporan yang sama-sama mungkin, sistem meminta klarifikasi atau mencari
kandidat tanpa mengarang satu canonical ID. Classifier bukan pemilih kelas tunggal.

### Q4. BM25 — Go lexical encoder + Qdrant

**Input:** token query, analyzer/dictionary/statistik `gen-3` dan filter scope.
**Proses:** bentuk sparse query yang kompatibel, cari kecocokan lexical.
**Output:** ranked candidates berisi evidence key, versi, rank dan skor branch.

Contoh: kata “laporan”, “kegiatan”, “internal” menemukan `chunk-5-2` dan kandidat
aturan batas waktu. Output ini daftar calon bukti, belum jawaban. Nomor/istilah
eksplisit terbantu oleh lexical; skor BM25 bukan confidence kebenaran.

### Q5. Query embedding dan dense search — C++ + Go/Qdrant

**Input:** query text, model manifest yang kompatibel dengan `gen-3`, filter scope.
**Proses:** encode query sekali lalu ANN search atas vector dokumen yang sudah
dibangun. **Output:** query vector dan ranked dense candidates dengan provenance.

Contoh: “batas waktu laporan” dapat menemukan teks “paling lambat 14 hari”. Query
vector berupa array angka; tidak berisi ID pasal secara langsung. BM25 dapat
berjalan saat embedding/dense sedang dikerjakan. Extraction seluruh PDF tidak diulang.

### Q6. Graph retrieval — Go traversal + Neo4j

**Input:** entity/provision seeds yang sah, predicate/path policy, snapshot, tanggal
dan budget hop/kandidat. **Proses:** telusuri relasi/support yang memenuhi scope.
**Output:** kandidat evidence tambahan dan path beserta dukungannya.

Contoh: dari aturan laporan A, traversal menemukan perubahan oleh B serta ayat
pengecualian. Seed dari alias dapat dipakai lebih awal; seed yang berasal dari
dense hits menunggu dense selesai. Jumlah edge/hop bukan ukuran kecukupan jawaban.

### Q7. Filter, deduplikasi dan fusion — Go retrieval

**Input:** ranked lists lexical/dense/graph. **Proses:** pastikan eligibility
snapshot/temporal, deduplikasi evidence pada versi sama, gabungkan rank dengan RRF.
**Output:** satu ranked candidate set, tetap menyimpan rank/provenance tiap branch.

Contoh: bukti batas 14 hari yang ditemukan dense dan graph menjadi satu kandidat
dengan dua asal. Versi 30 hari tidak eligible sebagai aturan berlaku untuk tanggal
contoh; ia dapat muncul sebagai konteks historis hanya jika diberi peran yang jelas.
Filter yang didukung backend dipasang sejak pencarian, bukan baru sesudah fusion.

### Q8. Hidrasi teks kandidat dan reranking — Go + C++

**Input:** kandidat, katalog record/artifact, query dan manifest cross-encoder.
**Proses:** ambil teks terverifikasi, cek hash/span/version, lalu nilai pasangan
query-passage dalam batch. **Output:** kandidat dengan skor/urutan reranker dan teks
yang terikat pada evidence ID.

Contoh: pasangan pertanyaan dengan ayat pengecualian dinilai bersama, bukan hanya
membandingkan dua vector. Pair ID mencegah skor tertukar. Raw logit bukan probabilitas
kebenaran. Reranker tidak bisa mengembalikan bukti yang hilang dari candidate set.

### Q9. Pelengkapan bukti dan context packing — Go answering/workflow

**Input:** hasil rerank, parent/exception/path refs dan budget tokenizer generator.
**Proses:** ambil dependency relevan, pertahankan provenance, susun konteks dengan
ruang untuk system prompt, query, template dan output. **Output:** `EvidenceBundle`
beserta completeness/missing dependencies dan konteks generator.

Contoh: konteks berisi `E1=aturan 14 hari + sumber perubahan` dan
`E2=pengecualian kegiatan internal`, berikut label pasal/ayat dan locator. Parent
Pasal 5 membantu interpretasi. Jika E2 wajib tetapi tidak muat, status partial
dicatat; ayat itu tidak dipotong diam-diam sambil mengklaim jawaban lengkap.

### Q10. Pemeriksaan kecukupan dan retrieve again — Go workflow

**Input:** evidence bundle, kebutuhan query, missing dependencies dan sisa budget.
**Proses:** tentukan apakah bukti cukup, gap dapat dicari, atau perlu berhenti.
**Output:** lanjut generation, retrieval plan tambahan, klarifikasi atau abstention.

Contoh: putaran awal hanya mendapat batas waktu. Workflow menargetkan ayat (2)
atau hubungan pengecualian, bukan mengulang query identik. Tidak ada tambahan bukti
relevan atau budget habis memicu berhenti. Required evidence saat runtime berasal
dari kebutuhan/dependency yang dapat dikenali, **bukan membaca jawaban gold**;
ketidakpastian semantic completeness tetap dinyatakan.

### Q11. Generation — Go provider adapter + LLM

**Input:** query, konteks terpilih, citation IDs yang tersedia, model/prompt terpin
dan batas output. **Proses:** model menyusun klaim dengan rujukan atau abstain.
**Output:** draft terstruktur berisi klaim, citation refs dan informasi ketidakpastian.

Contoh bentuk penjelasan, bukan payload API:

```text
Klaim C1: Pada tanggal yang ditanyakan, batasnya 14 hari setelah kegiatan berakhir.
Rujukan: E1 (ketentuan dan perubahan yang mendasarinya).
Klaim C2: Kewajiban tersebut tidak berlaku bagi kegiatan internal.
Rujukan: E2 (Pasal 5 ayat (2)).
```

Model tidak boleh menciptakan `E99` atau menjawab 30 hari dari pengetahuan internal
ketika evidence yang eligible menunjukkan 14 hari. Prompt membantu mengarahkan
perilaku, tetapi tidak membuktikan model selalu mematuhinya.

### Q12. Validasi klaim dan sitasi — Go + pemeriksaan semantik sesuai policy

**Input:** draft, evidence bundle dan request context. **Proses:** periksa schema,
ID/span/source/version, dukungan klaim, negasi dan pengecualian. **Output:** jawaban
yang lolos pemeriksaan, issue/revision request, retrieval tambahan atau abstention.

Contoh: “kegiatan internal juga wajib” mempunyai ID citation valid tetapi
bertentangan dengan E2. Pemeriksaan struktural saja tidak mendeteksinya. Revisi
interpretasi lebih tepat jika bukti cukup; mencari ulang diperlukan bila dukungan
memang hilang. Bila pemeriksaan memakai model tambahan, biaya masuk budget request.

### Q13. Respons dan cleanup — Go API/workflow

**Input:** hasil validasi, semantic/completion status, citation locators dan trace.
**Proses:** render jawaban/sumber, terbitkan event terminal, lepaskan lease/resource.
**Output:** jawaban atau status partial/abstain/clarification/error yang eksplisit.

Contoh: pengguna melihat dua klaim di atas beserta dokumen, pasal/ayat, halaman,
snapshot dan tanggal acuannya. Mode stream mengirim token sebagai provisional;
FINAL atau ERROR menjadi terminal. Putus koneksi membatalkan pekerjaan turunan.
Model timeout adalah kegagalan proses, bukan bukti bahwa corpus tidak memiliki jawaban.

## 4. Alur pendamping yang tetap bagian dari arsitektur

| Tahap/pemilik | Input | Proses dan contoh | Output |
| --- | --- | --- | --- |
| Review resolusi/versi — Go + reviewer | Proposal ambigu, kandidat, evidence, expected revision | Reviewer memeriksa dua kemungkinan penerbit; keputusan dan alasan direkam, revision stale ditolak | Keputusan audited atau tetap DEFER; job dapat dilanjutkan sesuai kontrak |
| Incremental update — Go plan + Rust batch | Observation baru, dependency fingerprints dan base snapshot | Dokumen C mengubah ayat (2); hitung closure dependency, recompute yang terdampak, pertahankan versi lama dan support lain | UpdatePlan, batch baru dan snapshot baru setelah publication |
| Recovery — Go coordinator | Intent, checkpoint, receipts dan state publication | Qdrant sudah menulis tetapi respons hilang; readback/retry idempotent menentukan langkah berikutnya | Job pulih atau gagal eksplisit; tidak ada duplicate mutation atau pointer setengah siap |
| Model preparation — Python tooling + C++ admission | Model/tokenizer, export config dan reference outputs | Ekspor bundle, verifikasi hash, uji parity token/vector/logit sebelum pemakaian | ModelManifest/bundle dan laporan parity; bukan otomatis PASS kualitas retrieval |
| Gold preparation — Python tooling + annotator | PDF/snapshot, pertanyaan dan kandidat evidence | Label jawaban, bukti/alternatif valid, kasus tanpa jawaban; kelompokkan paraphrase satu split | Dataset terpisah train/dev/test dengan provenance/review |
| Evaluation — Python runner | Output API/artefak produksi, gold, RunManifest dan benchmark suite | Ukur retrieval, graph, jawaban, citation dan runtime; bandingkan profil dengan budget tercatat | Metric results, eligibility/gate status dan raw artifacts; prerequisite kurang menjadi NOT_MEASURED/BLOCKED |
| Telemetry — seluruh runtime, agregasi Go/evaluator | Trace/span, tokens, queue time, error dan model/config identity | Pisahkan waktu query embedding, retrieval, rerank, generation serta tiap retry | Diagnosis bottleneck dan distribusi p50/p95/p99, bukan hanya satu angka rata-rata |

Contoh incremental: request yang sudah mem-pin `snap-12` tetap membaca snapshot
itu meskipun `snap-13` dipublikasikan di tengah generation. Retention/read lease
menjaga sumber yang masih dipakai; versi lama tidak dihapus demi pembaruan satu sumber.

Evaluasi menguji apakah sistem menemukan bukti yang benar; gold tidak dimasukkan
ke prompt produksi agar hasil tampak bagus. Fixture contoh A/B di dokumen ini
tidak menggantikan gold regulasi nyata atau workload required.

## 5. Ringkasan perubahan bentuk data dan integrasi

```text
URL -> PDF + receipt -> text + locator -> hierarchy + source mapping
    -> registry bindings -> chunks -> mentions/assertions + support
    -> resolution + legal versions -> graph delta + dense/sparse index batch
    -> readiness receipts -> committed snapshot

question + scope -> pinned request -> normalized query + retrieval plan
    -> ranked candidates + graph paths -> eligible fused candidates
    -> verified text + rerank -> evidence bundle + context
    -> draft claims + citation IDs -> validated result/status -> response
```

Setiap boundary mempertahankan ID, schema/producer manifest, scope dan failure
status yang relevan. File besar dipindahkan lewat referensi artefak beserta hash;
operasi komputasi menggunakan batch. Tidak semua kotak menjadi RPC atau service:
fusion, filter, context, citation dan workflow tetap berada dalam proses Go.

Untuk lokasi implementasi tiap pemilik gunakan [peta kode](05-code-map.md).
Untuk arah pengaruh parameter terhadap latency/akurasi gunakan
[matematika dan performa](04-math-and-performance.md). Target numerik tetap di
[benchmark-targets.yaml](../../configs/benchmark-targets.yaml); contoh ini tidak
menambah target, mengubah schema, atau menyatakan fitur sudah aktif.
