# Latihan menjelaskan sistem lengkap

Dokumen ini menyediakan narasi interview dengan asumsi seluruh arsitektur telah
terintegrasi, sesuai [panduan](README.md). Gunakan untuk menjelaskan desain dan
mekanisme sistem; hasil implementasi dan pengukuran mengacu pada
[status aktual](07-implementation-status.md). Tidak ada hasil benchmark, pengalaman
anotasi atau deployment yang ditambahkan melalui asumsi ini.

## 1. Pembukaan sekitar satu menit

"Dalam arsitektur lengkapnya, RegulaGraph-ID adalah sistem Hybrid GraphRAG untuk
menavigasi regulasi Indonesia yang tersebar, saling merujuk, dan berubah antarversi.
Sistem menyiapkan PDF menjadi chunk mengikuti struktur pasal, membangun identitas
entitas yang konsisten, lalu menyimpan hubungan beserta bukti sumbernya.

Saat pertanyaan masuk, workflow menormalisasi query, mengenali kebutuhan factual,
relational dan temporal, lalu menggabungkan BM25, dense retrieval dan graph sesuai
kebutuhan. Kandidat difusi dan direrank; konteks dilengkapi dengan sumber, versi,
dan pengecualian yang relevan. Jika bukti kurang, sistem mencari tambahan dalam
budget yang sama. LLM menghasilkan jawaban bersitasi yang diperiksa kembali.

Go menangani orchestration dan request, Rust transformasi data batch, C++ inference
embedding dan reranking, sedangkan Python eksperimen serta evaluasi. Prioritasnya
adalah evidence yang dapat ditelusuri dan latency terkendali."

## 2. Pertanyaan teknis dan jawaban inti

**Apa kontribusi engineering selain memanggil LLM?**

Pipeline mempertahankan sumber, hash, offset, canonical ID dan versi sampai jawaban.
Registry mengelola identity, publication menghindari pembacaan backend berbeda
snapshot, dan workflow membatasi retry/deadline. Model adalah salah satu komponen
dalam sistem data yang dapat diperiksa dan direproduksi.

**Mengapa graph kalau sudah ada vector database?**

Vector menemukan teks yang dekat secara semantik. Graph menelusuri hubungan
rujukan, perubahan dan ketergantungan bukti lintas dokumen. Edge salah atau ekspansi
berlebihan dapat menambah noise; manfaatnya dinilai melalui kelengkapan bukti
multi-hop dan ablation terhadap retrieval tanpa graph.

**Mengapa classifier tidak memilih tepat satu jalur?**

Pertanyaan factual dapat sekaligus membutuhkan relasi dan versi pada tanggal
tertentu. Classifier menghasilkan kebutuhan yang dapat tumpang tindih. BM25 dan
dense memberi kandidat awal; graph menambah relasi bila diperlukan; temporal
policy berlaku lintas branch. Menutup branch untuk menghemat latency perlu
pengukuran agar tidak merusak required recall.

**Kapan retrieve again, kapan cukup revisi jawaban?**

Jika bukti belum memuat pasal rujukan atau pengecualian yang diperlukan, pencarian
tambahan menargetkan gap itu. Jika sumber cukup tetapi interpretasi atau format
jawaban salah, revisi lebih tepat. Loop berhenti ketika bukti memadai, tidak ada
kemajuan relevan, batas putaran tercapai, atau budget habis. Seluruh putaran berbagi
snapshot, deadline dan budget total; retrieval ulang tidak memulai budget baru.

**Mengapa empat bahasa? Bukankah terlalu rumit?**

Pembagian mengikuti workload besar: Go untuk request/storage, Rust transformasi
batch, C++ wrapper runtime tensor, Python eksperimen. Biayanya codegen, build dan
debugging lintas runtime. Batch mengurangi overhead boundary. Lebih sedikit bahasa
layak dipilih jika biaya integrasi melebihi manfaat kontrol compute. Bahasa sendiri
bukan bukti percepatan; implementasi Python dengan kernel native juga bisa cepat.

**Apa pembeda BM25, embedding dan reranker?**

BM25 menilai kecocokan term menurut kelangkaan, frekuensi jenuh dan panjang teks.
Embedding mencocokkan representasi semantik yang dapat diindeks sebelumnya.
Cross-encoder menilai query dan passage bersama sehingga lebih mahal per pasangan.
Ia ditempatkan setelah candidate retrieval, dan tidak dapat menemukan bukti yang
sudah hilang dari kandidat awal.

**Mengapa tidak menjumlahkan semua skor?**

Skala BM25, cosine dan graph berbeda. RRF memakai rank agar raw score tidak perlu
dianggap sebanding, dengan konsekuensi kehilangan informasi selisih skor dalam
branch. Bobot dan konstanta dipilih melalui dev set, bukan test set.

**Canonicalization berarti semua sinonim digabung?**

Alias hanya sinyal kandidat. Identity juga bergantung pada tipe, penerbit,
nomor/tahun, scope dan evidence. Nomor regulasi sama dari penerbit berbeda bukan
otomatis satu entitas. Model mengusulkan resolusi ambigu; invariant, provenance
dan registry revision divalidasi sebelum keputusan diterapkan. Bukti belum cukup
menghasilkan defer/review, bukan merge paksa.

**Bagaimana memilih pasal pada tanggal tertentu?**

Interval keberlakuan hukum berbeda dari snapshot pengetahuan database. Satu request
membaca snapshot/generation yang konsisten dan menerapkan tanggal hukum yang diminta
pada provision version serta support graph. Tanggal unknown tidak dianggap berlaku
selamanya; penanganannya mengikuti policy eksplisit.

**Apakah citation menjamin tidak ada hallucination?**

Tidak. Citation ID valid membuktikan sumber tersedia, bukan dukungan semantik.
Validasi struktural memeriksa ID, versi, span dan bentuk output. Pemeriksaan semantik
menilai hubungan klaim dengan bukti, termasuk pelaku, negasi dan pengecualian.
Pemeriksaan semantik juga dapat salah; kualitas tetap diuji melalui gold dan kasus
adversarial, bukan disimpulkan dari validator ID.

**Bagaimana menjaga latency? Apakah LangGraph mempercepatnya?**

Ukur critical path: preparation, antrean, retrieval, reranking, prefill dan decode.
Jalankan branch independen secara concurrent, pertahankan session model hangat,
batch inference secara terukur, dan hindari extraction corpus saat query.
Framework orchestration tidak otomatis mempercepat kernel model. Hukum Amdahl
menjelaskan mengapa optimasi tahap kecil berdampak kecil ketika generation
mendominasi. Streaming memperbaiki waktu respons pertama, belum tentu waktu total.

**Mengapa tidak selalu menambah top-k, konteks atau hop?**

Kandidat tambahan bisa meningkatkan recall tetapi menambah biaya rerank. Hop lebih
jauh dapat menemukan bukti sekaligus noise. Konteks panjang menambah prefill dan
bisa mengaburkan fokus. Nilai kualitas dan latency bersama; lebih besar tidak
otomatis lebih baik.

**Bagaimana jika backend gagal ketika indeks diperbarui?**

Coordinator melakukan staging, memakai operation identity/checkpoint, dan memeriksa
readiness sebelum pointer aktif berubah. Reader tetap memakai snapshot committed
yang dipin. Retry harus idempotent. Ini protokol publication/recovery, bukan satu
transaksi ACID lintas PostgreSQL, Qdrant, Neo4j dan blob storage.

**Bagaimana kualitas dan performa dinilai?**

Pisahkan parsing, resolution/graph, retrieval, jawaban/citation dan runtime.
Retrieval dinilai dengan Recall/nDCG dan kelengkapan seluruh bukti; jawaban dengan
correctness, faithfulness dan dukungan citation. Runtime mencakup p50/p95/p99,
throughput, antrean, memori serta biaya. Corpus/model/prompt/config/split dibekukan;
paraphrase satu pertanyaan berada pada split sama untuk mencegah leakage.
Bandingkan Vector RAG, Hybrid RAG, GraphRAG dan Hybrid GraphRAG dengan perbedaan
retrieval serta budget dicatat. Target evaluasi bukan hasil pengukuran.

## 3. Urutan menjelaskan sambil membuka kode

Untuk pertanyaan lanjutan tentang **Evaluation, Reliability, Observability,
Scalability, dan System Design**, gunakan [panduan engineering](09-engineering-depth.md).
Siapkan satu failure case dan cara membuktikan penanganannya untuk setiap aspek.

Mulai dari [arsitektur](01-architecture.md), lalu ikuti [flow query](03-flows.md).
Tunjukkan pemilik admission/snapshot, classifier, retrieval/fusion, hydration/context,
dan generation/validation pada [peta file](05-code-map.md). Jelaskan input, output,
invariant dan satu failure case untuk boundary yang ditanyakan.

Untuk matematika, pilih BM25, RRF dan critical path dari
[persamaan](04-math-and-performance.md). Untuk aplikasi yang benar-benar dapat
dijalankan, gunakan [panduan demo](../interview-demo.md) dan cakupan aktual pada
[dokumen 7](07-implementation-status.md). Narasi sistem lengkap tidak mengubah
jalur eksekusi tombol demo.
