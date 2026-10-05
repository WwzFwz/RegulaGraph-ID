# Latihan menjelaskan proyek

Dokumen ini menyediakan contoh jawaban yang dapat disesuaikan dengan pemahaman dan
kontribusi nyata pengguna. Ia tidak menambahkan klaim pengalaman, hasil benchmark,
gold annotation atau deployment yang belum dilakukan.

## 1. Pembukaan sekitar satu menit

“RegulaGraph-ID adalah proyek knowledge intelligence untuk regulasi Indonesia.
Masalahnya, jawaban bisa tersebar di beberapa pasal dan dokumen, termasuk perubahan
serta pengecualian. Saya merancang Hybrid GraphRAG untuk menggabungkan pencarian
istilah, embedding, dan hubungan antardokumen, sambil mempertahankan sumber serta versi.

Arsitekturnya memisahkan orchestration Go, transformasi batch Rust, native inference
C++, dan evaluasi Python offline. Saat ini saya bisa mendemokan baseline BM25 dengan
model lokal dari PDF nyata, lengkap dengan kutipan dan halaman. Komponen native
embedding, storage dan hybrid retrieval juga tersedia, tetapi integrasi GraphRAG
produksi serta evaluasi gold end-to-end belum selesai.”

## 2. Pertanyaan teknis yang mungkin muncul

**Apa kontribusi engineering selain memanggil LLM?**

Jelaskan acquisition receipts, hash/offset sumber, pemisahan identity dari alias,
version/snapshot admission, checkpoint/retry, batch inference, rank fusion dan
validasi citation. Pilih dua bagian yang benar-benar dipahami lalu buka file terkait.
Jangan mengaku semua keputusan/model sudah terbukti optimal.

**Mengapa perlu graph kalau sudah ada vector database?**

Vector membantu kemiripan bahasa. Graph ditujukan untuk relasi eksplisit seperti
rujukan dan hubungan bukti lintas dokumen. Graph tidak selalu menambah kualitas;
edge salah atau ekspansi terlalu luas dapat merugikan. Keuntungannya harus diukur
dengan kelengkapan bukti multi-hop dan ablation melawan baseline tanpa graph.
Sampaikan bahwa graph belum dipakai demo ini.

**Kenapa empat bahasa? Bukankah terlalu rumit?**

Pembagiannya mengikuti workload dan boundary besar, bukan satu bahasa untuk tiap
fungsi kecil. Go mengatur request/storage, Rust transformasi batch, C++ wrapper
inference, Python eksperimen. Biayanya adalah codegen, build, debugging lintas
runtime, dan koordinasi kontrak. Untuk tim/produk lebih sederhana, lebih sedikit
bahasa bisa lebih tepat. Belum ada benchmark yang membuktikan pembagian ini terbaik.

**Apa pembeda BM25 dengan embedding?**

BM25 memberi bobot kecocokan term menurut kelangkaan, frekuensi jenuh dan panjang
passage. Embedding mencocokkan representasi semantik. Istilah/nomor hukum membuat
lexical penting; parafrasa memberi alasan untuk dense. Hybrid mencoba mengambil
manfaat keduanya, dengan quality/cost yang harus dibandingkan.

**Apa bedanya embedding dan reranker?**

Embedding dokumen dapat dihitung sebelumnya dan dicari lewat index. Cross-encoder
menilai query bersama passage, sehingga lebih mahal jika diterapkan ke seluruh
corpus. Karena itu ia ditempatkan setelah candidate retrieval. Tidak ada jaminan
reranker memperbaiki query jika bukti yang tepat sudah tidak masuk kandidat.

**Mengapa tidak semua skor dijumlahkan?**

Skor BM25, cosine dan graph bisa mempunyai skala berbeda. RRF menggunakan rank
sehingga tidak memerlukan penyamaan raw score, dengan harga kehilangan informasi
jarak antar-skor. Bobot/fusion tetap perlu tuning pada dev set, bukan test.

**Canonicalization berarti sinonim digabung?**

Tidak otomatis. Alias membentuk kemungkinan identitas; registry menetapkan ID
dengan scope dan evidence. Nomor regulasi sama dari penerbit berbeda bisa berbeda.
Model dapat mengusulkan resolusi ambigu, tetapi invariants dan commit tetap
divalidasi secara deterministik, dengan defer/review bila bukti belum cukup.

**Bagaimana menjaga jawaban tidak memakai pasal yang sudah berubah?**

Desain memisahkan versi pasal, interval legal dan snapshot pengetahuan sistem.
Retrieval harus membaca satu snapshot/generation yang konsisten dan menerapkan
tanggal yang diminta. Unknown tidak diasumsikan berlaku selamanya. Jelaskan bahwa
demo halaman belum menjalankan version selection produksi.

**Apakah citation menjamin tidak ada hallucination?**

Tidak. Citation ID valid hanya menunjukkan sumber itu tersedia. Model masih dapat
membalik pelaku, menghilangkan pengecualian, atau mengutip bagian yang tidak
mendukung klaim. Proyek memisahkan structural validation dari semantic evaluation;
draft demo ditandai belum diverifikasi. Satu smoke umum memang pernah menunjukkan
masalah pelaku; prompt diperketat dan kasusnya dicatat, bukan disebut quality PASS.

**Mengapa demo membutuhkan beberapa detik?**

Pada smoke yang dicatat, BM25 sekitar pecahan milidetik sampai sekitar satu
milidetik; generation model lokal membutuhkan detik. Cold load dan CPU/GPU
offload juga berpengaruh. Framework workflow tidak otomatis mempercepat kernel
model. Jelaskan dengan critical path dan hukum Amdahl, bukan menyalahkan LangGraph.
Angka itu observasi sample, bukan p95 atau SLA.

**Kenapa tidak langsung menambah top-k, konteks, atau hop?**

Lebih banyak kandidat bisa memperbaiki evidence recall tetapi menambah rerank dan
prompt cost. Hop yang lebih jauh bisa menambah bukti sekaligus relasi tidak relevan.
Konteks panjang bisa memuat pengecualian sekaligus mengaburkan fokus. Pilih dengan
ablation kualitas/latency, bukan asumsi “lebih besar selalu lebih bagus”.

**Bagaimana menghadapi backend gagal separuh jalan?**

Target publication memakai staging, operation identity, checkpoint dan readiness
receipts sebelum pointer aktif berubah. Reader tetap memegang snapshot committed.
Tidak ada klaim satu transaksi ACID lintas semua backend. Fondasi storage ada,
tetapi recovery/compensation penuh setiap backend tetap perlu pembuktian integrasi.

**Bagaimana Anda mengukur kualitas?**

Pisahkan retrieval Recall/nDCG dan all-required-evidence; graph/resolution false
merge/support; parsing struktur/angka; jawaban correctness/faithfulness/citation;
serta latency p50/p95/p99, throughput dan memori. Bekukan corpus/model/prompt/config
dan split; kelompokkan paraphrase agar tidak leakage. Harness ada, gold manusia
lengkap dan acceptance run belum selesai.

## 3. Kalimat yang perlu diperbaiki jika muncul di CV

| Hindari bila belum dibuktikan | Penjelasan yang sesuai kondisi sekarang |
| --- | --- |
| “Benchmarked four RAG architectures and achieved …” | Harness dan profil evaluasi tersedia; hasil benchmark lengkap belum ada |
| “Hand-labeled a complete gold evaluation set” | Persiapan antrean/review dataset tersedia; hanya sebut anotasi yang benar-benar dilakukan |
| “Production Hybrid GraphRAG deployed” | Arsitektur dan komponen dalam pengembangan; demo baseline lokal berjalan |
| “All answers verified with article-level legal citations” | Demo memberi kutipan halaman; structural citation checks bukan verifikasi hukum |
| “Rust/C++ made the entire system faster” | Pembagian runtime dipilih untuk kontrol throughput/latency; gain komparatif belum diukur |
| “Built with FastAPI and LangGraph” | Serving/workflow proyek sekarang Go; Python untuk tooling/evaluation offline |

## 4. Walkthrough kode singkat saat screen sharing

Mulai dari UI dan satu pertanyaan yang sudah dicoba. Buka PDF rujukan, lalu
`workflows/preview.go` untuk menunjukkan urutan. Buka `retrieval/preview.go` untuk
BM25 dan `answering/preview.go` untuk batas prompt/citation. Setelah itu tunjukkan
`workflows/rag_session.go` sebagai contoh fondasi produksi yang lebih kuat:
snapshot pin, deadline dan release.

Jelaskan batas setiap langkah. Untuk source map lengkap gunakan
[peta file](05-code-map.md); untuk command startup/stop gunakan
[panduan demo](../interview-demo.md). Jangan mengedit kode atau mengganti konfigurasi
model saat presentasi kecuali memang bagian demonstrasi yang sudah diuji.
