# Alur PDF, query, dan jawaban

Dokumen ini menjelaskan urutan input–proses–output, pemilik tahap dan perilaku gagal.
Alur demo diberi label DEMO; alur produksi adalah target dengan komponen yang baru
sebagian tersambung. Semua path file dirinci pada [peta kode](05-code-map.md).

## 1. Ingestion target: pekerjaan sebelum pengguna bertanya

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

Status: durable document pipeline sampai EXTRACT dan proposal RESOLVE opt-in
tersedia. Review/resume, graph penuh, coordinator INDEX corpus nyata, dan
acceptance lintas tahap masih harus diselesaikan. Library worker INDEX dan writer
awal tidak berarti langkah 1–10 sudah tersedia sebagai satu tombol produksi.

## 2. Query target: awal sampai akhir

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

**KOMPONEN:** RAGSession, catalog admission, lexical analyzer dan helper query
tersedia. **RENCANA:** seluruh preparation/entity-linking/profile graph terhubung
pada endpoint produksi terautentikasi.

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

**KOMPONEN:** CandidateSearch sekarang menerima Vector RAG dan Hybrid RAG
(dense + BM25). Permintaan profil graph ditolak; belum ada graph branch aktif
dalam workflow ini. Kegagalan salah satu branch hybrid membatalkan sibling dan
tidak diam-diam diubah menjadi vector-only yang dilaporkan sebagai hybrid.

### Langkah 5–6: filter, deduplikasi, dan fusion

Filter corpus/snapshot yang dapat diekspresikan backend dipasang saat pencarian.
Pembaca memverifikasi lagi metadata otoritatif ketika menghidrasi hasil. Tanggal
berlaku unknown tidak dianggap otomatis eligible; policy report/exclude/review
menentukan penanganannya.

RRF menggabungkan ranked lists menggunakan peringkat, bukan penjumlahan raw score.
Bukti yang sama pada versi sama digabung sambil mempertahankan provenance branch.
Versi berbeda tidak boleh melebur hanya karena teksnya serupa.

**KOMPONEN:** filter, RRF, dan hydration terpin tersedia pada integrasi terbatas.
Selecting effective date tidak sama dengan membuktikan semua temporal assertions
dalam corpus sudah diisi dengan benar.

### Langkah 7: rerank kandidat yang layak

Cross-encoder menilai pasangan query–passage secara bersama. ID pasangan dijaga
ketika batching supaya skor tidak tertukar jika backend mengembalikan urutan lain.
Skor reranker adalah skor ranking, bukan probabilitas kebenaran hukum.

**KOMPONEN:** native reranker dan helper correlation tersedia; tidak digunakan
oleh demo. Memakai helper tidak membuktikan seluruh jalur rerank produksi telah
terhubung atau menghasilkan gain kualitas.

### Langkah 8–9: hydrate bukti dan bangun konteks

Hydration mengambil byte teks terverifikasi dari artifact storage berdasarkan
katalog, bukan mempercayai teks bebas dari payload search backend. Source span,
halaman, source/provision-version refs dan snapshot harus cocok.

Desain lengkap mengambil parent, pengecualian, definisi dan seluruh path support
yang dibutuhkan. Context builder menyusun bukti dalam budget tokenizer generator.
Bukti wajib yang hilang harus menurunkan completeness, bukan dipotong diam-diam.

**KOMPONEN:** direct evidence hydration/packing tersedia. **RENCANA:** hidrasi
parent/exception/path lengkap; implementasi saat ini menandai dependency tersebut
sebagai missing/partial. Prompt tokenizer produksi juga harus benar-benar sesuai
dengan generator, bukan estimasi jumlah kata.

### Langkah 10–12: generation, pemeriksaan, dan respons

Generator menerima pertanyaan dan konteks terpilih. Model diminta menghasilkan
klaim bersumber atau abstain. Go memeriksa bentuk output, ID referensi, mapping
kutipan, sumber/versi dan batas output. Pemeriksaan deterministik ini tidak
membuktikan entailment semantik; kualitas jawaban tetap dievaluasi terpisah.

Reader memeriksa lease masih sah sampai akhir dan melepasnya sesudah selesai atau
cancel. Desain streaming harus membedakan token sementara, final answer, timeout,
dan terminal error. Saat ini library draft non-streaming sudah ada; jangan
mengklaim demo telah melakukan streaming token.

## 3. Alur demo yang bisa ditunjukkan sekarang

1. **Startup:** `cli/demo.go` memuat export offline dan memanggil `LoadPreviewCorpus`.
   Hash, path, batas byte dan record PDF diperiksa. `NewPreviewIndex` membuat
   passage window maksimal 1.600 rune dengan overlap sampai 160 rune dan BM25
   in-memory. Batas ini kebijakan demo, bukan chunk policy produksi.
2. **Browser:** UI mengirim `POST /api/ask` dengan `generate=false`. HTTP handler
   membatasi input dan memanggil workflow `Preview.Ask`.
3. **Retrieval:** tokenizer lexical dipakai; demo membuang daftar stopword ringan
   dan mendeduplikasi term query menjadi satu kemunculan. BM25 mengurutkan passage;
   dipilih lima hasil dari halaman berbeda. ID S1–S5 adalah label per respons.
4. **Tampilan awal:** browser menampilkan kutipan dan PDF link. Jika checkbox model
   aktif, browser mengirim POST kedua dengan `generate=true`. Pencarian saat ini
   diulang secara murah; bukan reuse hasil POST pertama yang dipercaya dari browser.
5. **Generation:** satu slot model diperbolehkan; Ollama dipanggil melalui adapter
   structured output. Tidak ada embedding, graph atau cross-encoder pada langkah ini.
6. **Validasi dan tampilkan:** setiap klaim wajib punya source ID yang tersedia;
   hasil tetap `unreviewed_draft`. Browser memakai `textContent`, bukan HTML model.
7. **Buka PDF:** endpoint hanya menerima hash dari receipt yang dimuat. Byte PDF
   diperiksa kembali sebelum disajikan; `#page=N` mengarahkan viewer ke halaman.

| Kondisi demo | Hasil |
| --- | --- |
| Tidak ada hasil lexical | `no_evidence`; tidak memanggil model |
| Model dimatikan | `evidence_only` |
| Bukti menurut model tidak cukup | `abstain` |
| Provider gagal atau klaim tidak valid | `generation_failed`, evidence tetap ditampilkan |
| Model sedang dipakai request lain | HTTP 429; tidak membuat antrean tanpa batas |

Pengujian nyata dan batas demo tercatat di
[verification-report-interview-demo](../verification-report-interview-demo.md).
