# Alur PDF, query, dan jawaban

Dokumen ini menjelaskan input, proses, dan output sistem dengan asumsi seluruh komponen
sudah terintegrasi: ingestion, adaptive retrieval, generation dan feedback loop.
Pemilik file ada di [peta kode](05-code-map.md). Status runnable dan batas demo
berada terpisah pada [status implementasi](07-implementation-status.md).

Pendamping alur ini adalah [input/output per tahap](08-input-output-examples.md):
setiap tahap menjelaskan data masuk, perubahan yang dilakukan, data keluar, dan
contoh berantai, termasuk ketika versi berubah atau bukti belum cukup.

## 1. Ingestion: pekerjaan sebelum pengguna bertanya

| Tahap | Apa yang terjadi | Hasil / hal yang tidak boleh hilang |
| --- | --- | --- |
| 1. Discover dan acquire | Go membaca listing/detail portal, mengunduh PDF dengan batas/rate, mencatat receipt | URL asal, waktu observasi, judul portal, hash dan ukuran; metadata portal belum dianggap kebenaran hukum final |
| 2. Submit job | Go menulis job dan mengikat source/config/model/schema | Idempotency key, source refs, snapshot/lease; job ulang tidak otomatis mengulang mutation |
| 3. Parse | Rust memakai engine PDF untuk teks dan locator halaman | Byte teks, posisi sumber, status halaman; scan yang belum OCR tidak disamarkan sebagai sukses penuh |
| 4. Normalize + structure | Bentuk teks canonical dan hierarchy; pertahankan mapping ke teks sumber | Bab/pasal/ayat/lampiran, source spans, parent refs |
| 5. BIND | Go mencocokkan identitas regulasi/provision dalam scope registry | Canonical bindings; konflik tidak diselesaikan hanya dengan nomor peraturan |
| 6. CHUNK | Rust membentuk chunk berdasarkan struktur dan batas tokenizer | Teks/label induk, provision-version refs, source spans |
| 7. EXTRACT | Model mengusulkan mention/relasi; Rust/Go memvalidasi schema dan evidence | Assertion + support, model/prompt/ontology identity; model tidak boleh memberi sumber fiktif |
| 8. RESOLVE | Candidate blocking, exact rules, model proposal untuk ambiguity, keputusan registry | LINK/DEFER dan jejak keputusan; proposal bukan approval otomatis |
| 9. ASSEMBLE dan INDEX | Siapkan graph delta; render teks, embedding dan sparse BM25 dalam batch | GraphDelta/IndexBatch, dependency manifests, generation yang kompatibel |
| 10. Stage dan publish | Go menulis backend, memeriksa hasil/readiness, kemudian memindahkan pointer snapshot | Reader hanya memakai snapshot committed; error memicu recovery, bukan publication parsial |

## 2. Query: awal sampai akhir

### Routing adaptif dan pengambilan bukti ulang

Alur utama adalah `Question -> Normalize -> Classify -> Retrieve -> Fusion ->
Rerank -> Build context -> Generate -> Validate`. Di antara tahap tersebut terdapat
pemeriksaan evidence dan feedback terarah ketika bukti belum memadai.

```text
Question -> admission / snapshot -> Normalize -> Classify kebutuhan
                                                     |
                      factual: lexical + dense sebagai kandidat awal
                      relational: tambah pencarian graph / path support
                      version-aware: temporal policy pada SEMUA branch
                                                     |
                         retrieval -> filter/fusion -> rerank -> hydrate/context
                                                     |
                                             bukti memadai?
                                      tidak /                 \ ya
                          budget masih ada?                   Generate
                         ya /           \ tidak                  |
             cari dependency/bukti     partial/abstain    validasi klaim + sitasi
                  yang hilang               atau                |
                     |                  klarifikasi        jawab bila layak
                     +---- kembali ke retrieval
```

Factual, relational dan version-aware bukan kelas yang harus saling eksklusif.
Satu pertanyaan dapat memerlukan ketiganya. Classifier menghasilkan kebutuhan dan
ketidakpastian; label bukan alasan otomatis membuang BM25 pada pertanyaan relasional
atau membuang dense pada pertanyaan temporal. Penghematan dengan menutup branch
harus dibuktikan melalui ablation. Temporal filtering juga diperlukan pada hasil
lexical/dense sesuai scope, bukan hanya hasil graph.

Kecukupan sebelum generation meliputi identitas/versi yang cocok, required evidence
set, parent/exception atau path support yang diperlukan. Sesudah generation,
periksa lagi klaim dan sitasi. Jika klaim gagal karena bukti hilang, workflow
dapat kembali mencari bukti; jika masalahnya bentuk output atau salah interpretasi,
retrieval ulang belum tentu membantu. Revisi jawaban atau abstention dapat lebih tepat.
Pengecekan dukungan semantik bukan sesuatu yang otomatis dibuktikan validator ID.

`Retrieve again` harus mengubah pencarian berdasarkan dependency/gap yang ditemukan,
bukan mengulang query identik tanpa kemajuan. Seluruh putaran berbagi deadline,
snapshot, budget kandidat/token/model calls; budget tidak direset. Batas putaran,
kondisi tidak ada bukti baru, serta kondisi stop ditetapkan dalam konfigurasi
request/profile dan dicatat dalam trace. Tidak ada angka target benchmark baru yang ditetapkan di sini.

Profil eksperimen yang dibekukan tetap harus dihormati: jangan mengaktifkan graph
diam-diam dalam run Vector RAG. Routing produk dan perbandingan profil evaluasi
perlu dicatat terpisah. Acuan: [system-design bagian query](../system-design.md).

Contoh pertanyaan konseptual: “Apa kewajiban X, pengecualiannya, dan aturan yang
mengubahnya pada tanggal T?” Contoh ini menjelaskan kebutuhan data, bukan jawaban hukum.

### Langkah 1–3: menerima dan menetapkan konteks

API memvalidasi ukuran pertanyaan dan mode; identitas pengguna/corpus berasal dari
otoritas server, bukan dipercaya dari JSON pengguna. Workflow menetapkan deadline,
lalu mem-pin satu snapshot committed dan representation generation sepanjang request.

Query preparation mempertahankan nomor, negasi dan nama; menyiapkan token lexical,
kandidat entity/alias dan kebutuhan temporal. Teks asli tetap disimpan. Normalisasi
tidak boleh mengubah “tidak wajib” menjadi “wajib”. Query spelling/fuzzy expansion
perlu diukur karena bisa merusak identifier hukum.

### Langkah 4: mencari kandidat dengan dependency yang tepat

```text
Query lexical -----------------------> BM25 ----------+
Query ----------------> embedding ---> dense ---------+--> kandidat
Query -> entity seeds ---------------> graph awal ----+
                               dense hits -> graph tambahan (bila perlu)
```

BM25 mencari kecocokan istilah. Dense mengubah query menjadi vector dengan model
yang sama dengan generation indeks. Graph memakai seed yang telah diikat ke
identitas, lalu menelusuri edge/support yang relevan.

BM25 dan dense dapat berjalan concurrent. Graph yang memerlukan dense hits harus
menunggu dense; tidak semua kotak di diagram dapat diparalelkan. Graph extraction
corpus tidak diulang saat query.

### Langkah 5–6: filter, deduplikasi, dan fusion

Filter corpus/snapshot yang dapat diekspresikan backend dipasang saat pencarian.
Pembaca memverifikasi lagi metadata otoritatif ketika menghidrasi hasil. Tanggal
berlaku unknown tidak dianggap otomatis eligible; policy report/exclude/review
menentukan penanganannya.

RRF menggabungkan ranked lists menggunakan peringkat, bukan penjumlahan raw score.
Bukti yang sama pada versi sama digabung sambil mempertahankan provenance branch.
Versi berbeda tidak boleh melebur hanya karena teksnya serupa.

### Langkah 7: rerank kandidat yang layak

Cross-encoder menilai pasangan query–passage secara bersama. ID pasangan dijaga
ketika batching supaya skor tidak tertukar jika backend mengembalikan urutan lain.
Skor reranker adalah skor ranking, bukan probabilitas kebenaran hukum.

Teks kandidat yang diperlukan reranker sudah diambil dan diverifikasi sebelum
inference. Hydration setelah reranking melengkapi konteks terpilih dengan parent,
exception dan path support; bukan pertama kalinya teks passage dibaca.

### Langkah 8–9: hydrate bukti dan bangun konteks

Hydration mengambil byte teks terverifikasi dari artifact storage berdasarkan
katalog, bukan mempercayai teks bebas dari payload search backend. Source span,
halaman, source/provision-version refs dan snapshot harus cocok.

Desain lengkap mengambil parent, pengecualian, definisi dan seluruh path support
yang dibutuhkan. Context builder menyusun bukti dalam budget tokenizer generator.
Bukti wajib yang hilang harus menurunkan completeness, bukan dipotong diam-diam.

### Langkah 10–12: generation, pemeriksaan, dan respons

Generator menerima pertanyaan dan konteks terpilih. Model diminta menghasilkan
klaim bersumber atau abstain. Go memeriksa bentuk output, ID referensi, mapping
kutipan, sumber/versi dan batas output. Pemeriksaan deterministik ini tidak
membuktikan entailment semantik; kualitas jawaban tetap dievaluasi terpisah.

Reader memeriksa lease masih sah sampai akhir dan melepasnya sesudah selesai atau
cancel. Desain streaming harus membedakan token sementara, final answer, timeout,
dan terminal error. Pada mode streaming, final answer hanya diterbitkan setelah pemeriksaan terminal;
putus koneksi membatalkan pekerjaan dan melepaskan lease.

## 3. Failure path dan kondisi berhenti

| Kondisi | Tindakan sistem lengkap |
| --- | --- |
| Interpretasi temporal/identitas material ambigu | Meminta klarifikasi atau melaporkan ketidakpastian sesuai policy |
| Branch wajib gagal | Menandai kegagalan; fallback hanya jika diizinkan dan effective profile dicatat |
| Bukti primer/path/exception belum lengkap | Retrieval tambahan ditargetkan pada dependency yang hilang |
| Retrieval ulang tidak memberi bukti baru | Hentikan loop; partial/abstain atau klarifikasi, bukan reset budget |
| Klaim tidak didukung bukti yang ada | Revisi/buang klaim atau cari bukti tambahan bila gap jelas dan budget tersedia |
| Deadline atau budget habis | Keluaran terminal eksplisit; jangan melaporkan pencarian lengkap |
| Snapshot lease kedaluwarsa | Jangan menyajikan hasil seolah snapshot tetap terpin; lakukan cleanup |
| User membatalkan request | Batalkan turunan, hentikan antrean/model bila dapat dibatalkan, lepas resource |

Pemeriksaan kecukupan deterministik mencakup identitas, source/version, required
sets dan missing dependencies. Pemeriksaan semantik memeriksa hubungan makna
klaim dan bukti; bila memakai model tambahan, biayanya masuk deadline/model budget.
Keduanya tidak digabung menjadi klaim "LLM pasti tahu kapan bukti cukup".

Jika pertanyaan sejak awal dapat dijawab dengan evidence yang memadai, jalur selesai
pada satu putaran retrieval dan generation. Sistem adaptif tidak mengharuskan semua
pertanyaan menjalani loop atau memakai seluruh branch.
