# Algoritma, persamaan, dan trade-off performa

Dokumen ini membantu menjelaskan mekanisme dan arah perubahan parameter saat
interview. Persamaan menyatakan model matematis atau penyederhanaan dengan asumsi
yang disebutkan. Tidak ada persamaan yang membuktikan kualitas corpus tanpa
pengujian; angka ilustratif bukan perubahan benchmark required.

## 1. Chunking: ukuran dan overlap

Untuk teks panjang D unit, window L unit, dan overlap O dengan 0 ≤ O < L:

$$N \approx 1 + \left\lceil\frac{\max(0,D-L)}{L-O}\right\rceil$$

N adalah jumlah window untuk dokumen tidak kosong. Unit harus sama: token dengan
token, atau rune dengan rune. Rumus ini untuk sliding window tanpa penyesuaian
boundary; structural chunking mempunyai panjang yang mengikuti dokumen.

- L lebih kecil: potongan lebih spesifik, tetapi syarat/pengecualian lebih mudah
  terpisah; jumlah record dan overhead naik.
- O lebih besar: mengurangi kehilangan konteks di batas, tetapi menambah duplikasi,
  biaya embedding, memori dan token ketika hasil tidak dideduplikasi.
- L lebih besar: konteks lokal lebih utuh, tetapi satu vector merangkum lebih
  banyak topik dan prompt bisa membesar. Tidak ada jaminan recall naik.

Demo menggunakan rune untuk window dan byte UTF-8 untuk locator. Produksi memakai
struktur serta tokenizer yang dipin. “1.600 rune” tidak berarti “1.600 token”.

## 2. BM25: istilah langka dan frekuensi yang jenuh

Untuk query q dan passage d:

$$\operatorname{BM25}(q,d)=\sum_{t\in q}
\operatorname{IDF}(t)\frac{f(t,d)(k_1+1)}
{f(t,d)+k_1(1-b+b|d|/\overline{L})}$$

$$\operatorname{IDF}(t)=\ln\left(1+\frac{N-df(t)+0.5}{df(t)+0.5}\right)$$

| Simbol | Makna |
| --- | --- |
| f(t,d) | Jumlah kemunculan term t pada passage d |
| N | Jumlah passage dalam populasi statistik, bukan jumlah PDF |
| df(t) | Banyak passage yang memuat term t |
| Panjang d dan rata-rata L | Panjang passage dan rata-rata panjang dalam token analyzer |
| k1 | Mengatur kejenuhan kontribusi frekuensi term |
| b | Kekuatan normalisasi panjang, lazim dibatasi 0 sampai 1 |

Jika N tetap, df makin kecil memberi IDF lebih tinggi: istilah yang lebih jarang
lebih membedakan dokumen. f naik meningkatkan kontribusi dengan tambahan yang
makin kecil, bukan garis lurus tak terbatas. b naik memperbesar penalti relatif
untuk dokumen di atas panjang rata-rata; bukan berarti kualitas selalu lebih baik.

Contoh ilustratif: N=100 dan df=10 memberi IDF sekitar 2,26; df=90 memberi sekitar
0,11. Itu bobot retrieval, bukan confidence bahwa suatu pasal benar.

**Implementasi:** demo memakai k1=1,2, b=0,75, term query unik, serta token
`judul + teks` untuk setiap passage. Jadi panjang/df demo termasuk judul yang
berulang per passage. Produksi membekukan analyzer, dictionary dan statistik per
generation; query encoder juga dapat membawa frekuensi term query. Jangan
mencampur statistik demo dan produksi saat membandingkan skor.

Kode: [preview.go](../../src/server/internal/retrieval/preview.go),
[sparse.go](../../src/server/internal/retrieval/query/sparse.go),
[lexical.rs](../../src/ingestion/src/indexing/lexical.rs),
[statistics.rs](../../src/ingestion/src/indexing/statistics.rs).

## 3. Dense retrieval: kesamaan representasi

$$v_q=f_\theta(q),\quad v_d=f_\theta(d),\qquad
\cos(v_q,v_d)=\frac{v_q^\top v_d}{\|v_q\|_2\|v_d\|_2}$$

f adalah encoder dan v vector embedding. Untuk vector unit hasil normalisasi L2,
cosine sama dengan dot product. Kesamaan lebih tinggi berarti kedekatan menurut
representasi model, bukan bukti entailment atau keberlakuan hukum. Dua teks yang
berlawanan karena kata “tidak” masih dapat dekat secara semantik.

Model, tokenizer, pooling dan normalisasi harus kompatibel antara query dan
indeks. Mengganti encoder tanpa membangun ulang representasi tidak aman hanya
karena dimensinya sama.

Untuk N vector berdimensi m dengan s byte per komponen:

$$M_{\text{raw-vector}}\approx Nms$$

Ini belum menghitung payload, graph ANN, allocator atau replica. Sebagai ilustrasi,
1 juta vector × 1024 × 4 byte sekitar 4,096 GB desimal. m yang lebih besar menaikkan
storage/compute linear pada operasi dot product, tetapi kualitas tidak otomatis naik.

## 4. RRF: menyatukan ranking

$$\operatorname{RRF}(d)=\sum_{j:d\in L_j}\frac{w_j}{c+r_j(d)}$$

Lj adalah daftar hasil branch j, r rank mulai 1, w bobot branch, dan c konstanta
positif. Dokumen yang tidak muncul dalam branch tidak mendapat kontribusi branch itu.
Skor tidak dibaca sebagai probabilitas.

c lebih besar mengecilkan perbedaan kontribusi antar-rank. w lebih besar membuat
branch lebih dominan. RRF membuang informasi jarak skor dalam satu branch; kalibrasi
score sum atau learned ranking bisa menjadi alternatif setelah data evaluasi ada.
Kode: [fusion.go](../../src/server/internal/retrieval/fusion.go).

## 5. Cross-encoder dan candidate budget

$$s(q,d)=g_\phi([q;d])$$

Query dan passage diproses sebagai pasangan, sehingga interaksi keduanya dapat
dinilai sebelum ranking akhir. Output implementasi native berupa raw logit.
Menjalankan sigmoid atas logit tidak otomatis membuatnya terkalibrasi.

Dengan k pasangan dan batch efektif B, penyederhanaannya:

$$T_{\text{rerank}}\approx T_{\text{queue}}+
\lceil k/B\rceil T_{\text{batch}}(B,L)$$

L adalah panjang input; waktu batch sendiri berubah bersama B dan L. k naik
memberi peluang mengevaluasi kandidat tambahan, tetapi meningkatkan biaya.
Reranker tidak dapat menemukan bukti yang sudah hilang dari kandidat awal.

## 6. Graph expansion dan kualitas multi-hop

Dengan branching factor rata-rata b dan batas hop h, jumlah kunjungan naif dapat
bertumbuh seperti:

$$1+b+b^2+\cdots+b^h$$

Ini ilustrasi ekspansi tree tanpa deduplikasi, bukan kompleksitas pasti query Neo4j.
Graph nyata memiliki cycle, pruning, index dan filter. Menaikkan h dapat menemukan
rujukan jauh sekaligus memperbanyak noise dan latency. Edge tambahan juga dapat
merusak jawaban jika relasi/predicate/sumbernya salah.

Jika query memerlukan bukti A dan B:

$$P(A\cap B)=P(A)P(B\mid A)$$

Menemukan satu bukti sering tidak cukup. Angka recall tinggi untuk potongan
individual tidak otomatis berarti seluruh rantai ditemukan. Perkalian memakai
probabilitas kondisional; jangan mengasumsikan semua retrieval event independen.

## 7. Snapshot dan tanggal hukum

Untuk sequence snapshot S, record terlihat bila:

$$\operatorname{visible}(x,S)=
[x.from\_seq\le S]\land[x.to\_seq=\varnothing\ \lor\ S<x.to\_seq]$$

Untuk interval legal yang diketahui [a,b), tanggal T eligible bila a ≤ T < b.
Unknown tidak disamakan dengan unbounded. Kedua filter menjawab pertanyaan berbeda:
“versi data mana yang diketahui sistem?” dan “interval hukum mana yang dimaksud?”.
Corpus, generation, node/edge/support dalam path juga harus konsisten.

## 8. Context budget dan generation

$$B_{\text{system}}+B_{\text{question}}+B_{\text{evidence}}+
B_{\text{template}}+B_{\text{output-reserve}}\le B_{\text{model}}$$

Semua B dihitung menggunakan tokenizer/template generator yang sesuai. Menambah
konteks dapat membantu menemukan syarat tetapi juga menambah noise, prefill dan
memori. Menghapus evidence wajib demi muat harus menghasilkan status partial,
bukan klaim jawaban lengkap. Preview belum memiliki exact token admission produksi.

Secara konseptual, generation mengikuti:

$$P(y\mid q,E)=\prod_i P(y_i\mid y_{<i},q,E)$$

E adalah evidence, tetapi conditioning pada E tidak menjamin semua klaim benar.
Pemeriksaan citation ID hanya membuktikan referensi tersedia; semantic support
perlu evaluasi terpisah.

## 9. Latency, throughput, dan mengapa bahasa bukan jawaban tunggal

Untuk branch independen, perkiraan critical path target:

$$T\approx T_{\text{admission}}+
\max(T_{\text{BM25}},T_{\text{embed}}+T_{\text{dense}},T_{\text{graph}})
+T_{\text{hydrate/rerank/context}}+T_{\text{generate}}$$

Jika graph bergantung pada dense, dependency itu harus ditambahkan berurutan.
Formula berlaku untuk waktu sebuah request; p95 total tidak dihitung dengan
menjumlahkan p95 tahap. Ukur distribusi total langsung.

Untuk generation autoregresif, perkiraan praktis:

$$T_{\text{generate}}\approx T_{\text{load-if-cold}}+T_{\text{queue}}+
T_{\text{prefill}}+n_{\text{output}}/r_{\text{decode}}$$

r adalah token/detik efektif. Output lebih panjang umumnya menambah waktu;
GPU offload, prompt length dan concurrent jobs mengubah r. Streaming dapat
mempercepat jawaban pertama terlihat, tanpa harus mengurangi waktu selesai total.

Hukum Amdahl, bila fraksi waktu p dipercepat s kali:

$$\operatorname{Speedup}=\frac{1}{(1-p)+p/s}$$

Ilustrasi: hanya 1% waktu berada di orchestration; mempercepatnya 10 kali memberi
sekitar 1,009× total. Ini menjelaskan mengapa menambah framework/bahasa tidak
menyelesaikan bottleneck generation. Angka 1% ini ilustrasi, bukan hasil profiling repo.

Untuk sistem antrean yang stabil, Little's law: **L = λW**. L adalah rata-rata
request dalam sistem, λ throughput masuk yang stabil, W rata-rata waktu tinggal.
Concurrency tanpa batas tidak menciptakan kapasitas compute; antrean dapat
memperburuk tail latency. Batching menambah efisiensi tetapi menunggu batch juga
menambah latency. Ukur throughput bersama p95/p99 dan error/timeout.

## 10. Entity resolution: kemiripan bukan keputusan identitas

Misalkan m adalah mention dan e kandidat entitas. Secara konseptual model mencoba
menilai p = P(entitas sama | mention, kandidat, konteks dan bukti). Nilai confidence
yang ditulis LLM tidak otomatis merupakan p yang terkalibrasi. Proyek tidak
menganggap model sebagai otoritas penulisan registry.

Untuk ilustrasi keputusan biner dengan biaya false merge CFM dan false split CFS:

$$R(\text{merge})=(1-p)C_{FM},\qquad R(\text{separate})=pC_{FS}$$

Dalam model biaya sederhana ini, merge lebih murah hanya bila
**p > CFM / (CFM + CFS)**. Jika false merge lebih merusak, ambangnya makin tinggi.
Ini penjelasan trade-off, bukan threshold yang sudah dipakai kode: p, biaya dan
calibration belum dibuktikan. Pilihan defer/review juga menambah alternatif nyata.
Exact identity rules, scope, evidence dan registry revision tetap perlu diperiksa.

## 11. Incremental: hemat compute dengan dependency yang benar

Jika biaya membangun ulang artefak i adalah ci, A seluruh artefak dan D closure
artefak yang benar-benar terdampak perubahan, perkiraan biaya adalah:

$$C_{\text{full}}=\sum_{i\in A}c_i,\qquad
C_{\text{incremental}}=C_{\text{detect}}+\sum_{i\in D}c_i+C_{\text{validate}}$$

Semakin kecil D, potensi penghematan lebih besar, tetapi overhead deteksi/validasi
tetap ada. Jika model, ontology atau statistik indeks berubah luas, D dapat mendekati A.
Reuse hanya aman bila source dan dependency fingerprint yang relevan cocok.
Jangan mengecilkan D dengan mengabaikan rujukan lintas dokumen hanya untuk terlihat cepat.

## 12. Metrik: apa yang dianggap lebih baik?

$$\operatorname{Recall@k}=|R_k\cap G|/|G|$$

Rk adalah hasil teratas dan G gold relevant evidence yang didefinisikan. Untuk
ranking tetap, menambah k tidak menurunkan recall set biasa, tetapi biaya naik dan
precision dapat turun. Jika ANN/search budget ikut berubah, monotonicity ini tidak
otomatis berlaku. Pertanyaan tanpa jawaban memerlukan policy evaluasi tersendiri.

$$DCG@k=\sum_{i=1}^{k}\frac{2^{rel_i}-1}{\log_2(i+1)},\quad
nDCG@k=DCG@k/IDCG@k$$

rel adalah label relevansi; IDCG ranking ideal pada definisi evaluasi yang sama.
Nilai ideal nol perlu ditangani eksplisit, bukan membagi dengan nol.

Untuk beberapa set bukti alternatif Gj, kelengkapan satu pertanyaan dapat dinilai:

$$\operatorname{complete}(q)=\mathbf{1}[\exists j:G_j\subseteq R_k]$$

Citation precision semantik menghitung klaim–kutipan yang benar-benar didukung
dibagi pasangan yang dinilai. Ini berbeda dari persentase ID citation yang valid.
Ukur pula hallucination/abstention, correctness, graph false merge, parsing error,
serta latency menurut jenis pertanyaan. Jangan tuning pada test split; paraphrase
dari pertanyaan dasar yang sama harus berada pada split yang sama.

Kode metrik: [retrieval.py](../../evaluation/metrics/retrieval.py),
[citations.py](../../evaluation/metrics/citations.py),
[runtime.py](../../evaluation/metrics/runtime.py). Required gates tetap mengikuti
[benchmark policy](../benchmark-policy.md); gold/acceptance produksi belum selesai.
