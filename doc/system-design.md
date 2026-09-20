# Desain menyeluruh RegulaGraph-ID

Dokumen ini menetapkan rancangan seluruh sistem Hybrid GraphRAG regulasi Indonesia sebelum implementasi komponen dimulai. Perannya menghubungkan kebutuhan pengguna, pemilik fungsi, alur data, keputusan runtime, dan kriteria penerimaan. Cakupan desain lengkap; collector D01 dan kontrak/validator C01 tersedia, sedangkan pipeline runtime masih scaffold. Lihat [implementasi C01](contracts-implementation.md); migrasi, layanan model dan hasil benchmark belum tersedia.

Baseline desain: 2026-09-19. Pengguna memilih perancangan menyeluruh sejak awal, mempertahankan Go/Rust/C++ untuk produksi, Python untuk evaluasi/tooling, serta sumber BPK, JDIH Kemkomdigi, dan JDIHN. Semua angka penerimaan tetap berasal dari [benchmark-targets.yaml](../configs/benchmark-targets.yaml). Keputusan internal di bawah adalah rancangan kerja; pemilihan library/model dan kelayakan performanya dibuktikan sebelum dibekukan dalam manifest release.

## Peta spesifikasi

| Dokumen | Tanggung jawab |
| --- | --- |
| [Kontrak sistem](system-contracts.md) | Katalog record/field, API, RPC, event, validasi, error, evolusi schema |
| [Storage dan konsistensi](storage-consistency.md) | Identitas, versi, persistensi, snapshot, publication, retry, update, recovery |
| [Persiapan corpus](corpus-plan.md) | Akuisisi tiga sumber, provenance, deduplikasi, kurasi, gold set |
| [Rencana pengembangan](development-plan.md) | Dependency pekerjaan, hasil konkret, syarat selesai, skenario integrasi |
| [Kebijakan benchmark](benchmark-policy.md) | Protokol pengukuran, kelulusan, penanganan kegagalan |

## 1. Kebutuhan dan batas produk

Sistem menjawab pertanyaan faktual, prosedural, lintas dokumen, multi-hop, dan temporal dengan bukti setingkat pasal/ayat. Bahasa input mencakup Indonesia formal, informal, typo, dan campuran Indonesia-Inggris. Jawaban mengungkap syarat, pengecualian, konflik, dan ketidakcukupan bukti yang relevan. Rujukan peraturan, FAQ/pedoman, rancangan, dan dokumen penjelas memiliki tipe berbeda; dokumen nonfinal tidak otomatis dianggap aturan berlaku.

Ingestion mencakup PDF teks, halaman HTML resmi, OCR scan, tabel/lampiran, perubahan sebagian ketentuan, dan dokumen yang muncul pada beberapa portal. Operator dapat memeriksa job, sumber bermasalah, keputusan resolution, dan publikasi snapshot melalui API/CLI. Tidak ada UI web atau kebutuhan multi-tenant SaaS yang diasumsikan sudah disetujui. Corpus ID adalah batas data dan izin akses, bukan klaim implementasi tenancy penuh.

| Use case | Hasil yang harus tersedia | Kondisi sulit yang harus ditangani |
| --- | --- | --- |
| UC01 ingest dokumen | Struktur, provenance, versi, chunk, indeks, graph dalam snapshot | PDF rusak, OCR, sumber ganda, identitas belum jelas |
| UC02 pertanyaan faktual | Jawaban dengan citation yang mendukung klaim | Kata sama pada peraturan berbeda, negasi, nomor pasal |
| UC03 prosedur dan multi-hop | Urutan langkah serta seluruh rantai bukti | Pengecualian di dokumen lain, rujukan tidak tersedia |
| UC04 pertanyaan temporal | Ketentuan sesuai tanggal acuan dan snapshot pengetahuan | Perubahan parsial, tanggal unknown, konflik metadata |
| UC05 tanpa bukti | Abstention atau permintaan klarifikasi yang eksplisit | Backend gagal tidak disamarkan sebagai corpus tanpa jawaban |
| UC06 update | Hanya sumber/dependency terdampak dihitung ulang | Merge/split entitas, model baru, sumber hilang sementara |
| UC07 recovery | Replay aman dan snapshot lama tetap dapat dibaca | Crash setelah satu backend berhasil ditulis |
| UC08 evaluasi | Empat profil dan seluruh required gate dapat dinilai | Data bocor antar split, cache, timeout, provider lambat |

## 2. Pemilik komponen dan proses

| Pemilik | Fungsi produksi | Boundary |
| --- | --- | --- |
| Go server | HTTP/streaming, domain, query workflow, fusion/filter/context/citation, sumber, scheduler, publication, adapter database | HTTP untuk klien; gRPC internal untuk batch worker/inference; akses DB langsung |
| Rust ingestion | Parsing/normalisasi/chunking, transformasi versi, extraction postprocessing, resolution, graph assembly, index batch | Menerima job dengan referensi artefak; mengembalikan manifest batch tervalidasi |
| C++ inference | Tokenisasi yang cocok model, session persisten, embedding, cross-encoder, batching | Layanan gRPC pembungkus library, satu permintaan berisi satu batch |
| Go semantic-model gateway | Ekstraksi terstruktur, resolution semantik ambigu, ringkasan profil, adapter generator | Dihost pada server; Rust meminta operasi semantik per batch melalui kontrak inference |
| Python evaluation/tooling | Dataset, metrik, runner, ekspor model, parity | Memakai API/artefak produksi; tidak menyalin retrieval produksi |

Proses target awal adalah satu server Go, worker Rust, service inference C++, PostgreSQL, Qdrant, Neo4j, dan storage artefak. Gateway semantik merupakan fungsi Go, bukan microservice tambahan. Coordinator berada pada server dengan antrean/pool terpisah dari query. Replikasi Go memungkinkan banyak reader, sedangkan publikasi satu corpus diserialkan. Proses terpisah untuk coordinator baru dievaluasi jika profiling membuktikan kebutuhannya; seluruh resource tetap dilaporkan.

Layanan C++ belum ada; scaffold saat ini hanya static library. Rust memiliki executable worker Tonic loopback untuk batch PARSE, dengan Go client dan coordinator durable yang memvalidasi deadline, attempt/fence, output, serta checkpoint; stage lanjutan belum aktif. Tidak ada RPC terpisah untuk fusion, filtering, context builder, atau pemeriksaan citation.

## 3. Alur ingestion lengkap

1. Go melakukan discovery dan akuisisi dari connector yang dipilih. Catat URL detail, URL file, redirect, observation time, metadata portal, checksum, dan provenance. Blob dideduplikasi berdasarkan bytes; identitas regulasi tidak disamakan dengan URL atau hash file.
2. Coordinator menghitung fingerprint input/config dan dependency, menentukan reuse atau reprocess, lalu membuat job persisten. Registry identitas dan dictionary lexical dibaca melalui batch, bukan permintaan per mention/token.
3. Rust memeriksa format dan membuka engine parsing native. PDF teks mengambil reading order, struktur, tabel, dan lokasi; halaman scan melalui OCR. Per halaman dapat berbeda jalur. Kegagalan halaman tidak diabaikan dalam hasil dokumen.
4. Normalisasi menyimpan teks sumber beserta mapping ke teks normalisasi. Artefak footer/header dapat ditandai, tetapi angka pasal, negasi, unit, tabel, dan pengecualian tidak dibuang oleh aturan pembersihan generik.
5. Parser struktur menghasilkan pohon dokumen. Version transform menghubungkan peraturan/perubahan dan ketentuan yang terdampak. Rekonstruksi teks konsolidasi menyimpan setiap sumber penyusunnya; hasil ambigu dikarantina untuk review, bukan dinyatakan berlaku.
6. Chunk mengikuti pasal/ayat/huruf dengan parent dan hubungan pengecualian. Ketentuan panjang dapat menjadi beberapa child chunk dengan pemetaan lengkap. Parent tidak harus dicopy ke semua chunk; context builder mengambilnya secara batch ketika dibutuhkan.
7. Ekstraksi deterministik menangani pola rujukan yang jelas; ekstraksi model menangani relasi semantik terstruktur. Output model harus lolos schema, pemeriksaan endpoint, serta pencocokan bukti ke teks sebelum menjadi assertion.
8. Resolution memisahkan mention dari canonical entity. Exact key dan blocking menghasilkan kandidat; bukti konteks menentukan keputusan merge/keep-separate/unresolved. Kemiripan embedding atau sinonim tidak otomatis membuktikan identitas.
9. Assembly membentuk assertion dan support terpisah, dependency manifest, ringkasan profil yang memiliki referensi evidence, serta batch indeks. Rust tidak memublikasikan atau menulis state otoritatif database sendiri.
10. Go memeriksa manifest, checksum, counts, kemampuan schema, dan dependency snapshot. Go menulis staging lintas backend, memvalidasi kesiapan pencarian, kemudian melakukan publication sesuai [protokol snapshot](storage-consistency.md).

Review manusia dipakai pada konflik material sumber, identitas, teks rekonstruksi, dan keberlakuan yang belum dapat dibuktikan. Keputusan review memiliki reviewer/reason/evidence; parser dan model tidak diberi wewenang menebak tanggal berlaku. Data karantina tetap dapat diaudit namun tidak dicampur sebagai evidence terverifikasi.

## 4. Graph engineering

Pohon struktur dokumen, graph relasi semantik, dan graph dependency pemrosesan memiliki makna berbeda. Hubungan parent menyusun dokumen; assertion menyatakan fakta bersumber; dependency menjelaskan artefak yang harus diinvalidasi. Menyatukannya sebagai edge tanpa tipe akan merusak query maupun update.

Ontology awal meliputi regulation, provision, organization, role, activity, obligation, requirement, exception, dan defined term. Predicate mencakup references, amends, repeals, implements, defines, requires, permits, prohibits, applies_to, serta exception_to. Vocabulary berversi dan setiap predicate menentukan endpoint type, arah, qualifiers, bukti yang dibutuhkan, dan aturan temporal. Vocabulary dapat diperluas berdasarkan data; unknown predicate ditolak atau direview.

Entity canonical memakai identitas objek; definisi istilah dapat scoped ke peraturan atau versi. Pergantian nama lembaga tidak otomatis berarti entitas baru atau sama: identitas dan suksesi adalah keputusan bersumber. Merge/split dapat dibatalkan melalui decision history dan revision registry tanpa memusnahkan mention lama.

Assertion menyimpan kondisi, exception refs, explicit/inferred status, support, dan temporal applicability. Satu dokumen mirror tidak dihitung sebagai banyak bukti independen. Ringkasan profil hanya membantu penemuan kandidat; klaim akhir harus menunjuk evidence primer. Traversal mempertahankan seluruh support path yang dipakai menjawab, menghindari cycle, dan melaporkan frontier/budget exhaustion. Batas hop pada workload benchmark bukan batas universal kemampuan produk.

## 5. Alur query lengkap

1. Validasi/authenticate request, corpus scope, ukuran input, dan deadline. Resolve tanggal acuan serta snapshot aktif satu kali. Bila user menentukan tanggal, gunakan tanggal tersebut; bila tidak, gunakan tanggal kalender zona corpus Asia/Jakarta dan tampilkan pada metadata jawaban. Pengetahuan yang tersedia tetap dibatasi snapshot.
2. Simpan query asli. Normalisasi ringan mempertahankan nomor, negasi, nama, dan istilah penting. Classifier memilih kebutuhan temporal/prosedural/multi-hop; classifier bukan alasan menghapus jalur retrieval tanpa bukti ablation.
3. Alias/entity lookup dan lexical search dapat mulai lebih awal. Embedding query berjalan melalui C++; dense search menunggu embedding. Graph traversal dari seed alias dapat paralel; ekspansi dengan seed hasil dense menunggu kandidat tersebut.
4. Semua adapter memakai corpus, snapshot, tipe dokumen, dan temporal policy yang sama. Bukti dengan tanggal unknown ditandai; bukti itu tidak dinyatakan berlaku pada tanggal tertentu. Permintaan klarifikasi digunakan ketika interpretasi query material ambigu.
5. Kandidat dideduplikasi berdasarkan identitas evidence/versi, mempertahankan skor dan asal setiap jalur. Rancangan fusion memakai reciprocal-rank fusion berversi dengan bobot konfigurasi; bobot/k dipilih pada dev. Skor BM25, similarity, dan graph tidak dijumlah mentah seolah setara.
6. Graph expansion tambahan dipakai ketika belum ada rantai bukti lengkap. Deadline dan budget ekspansi mengikat; bila perlu berhenti, completeness menjadi PARTIAL, bukan diam-diam COMPLETE. Seed linking untuk eksperimen GraphRAG dilaporkan terpisah.
7. Reranker menilai pasangan query-evidence melalui C++; Go mengurutkan hasil dan mempertahankan syarat/path yang diperlukan. Potongan panjang memiliki kebijakan window dan truncation yang terukur. Angka 50 pasangan pada benchmark bukan alasan membuang exception yang diperlukan untuk jawaban.
8. Context builder mengambil parent, kondisi, exception, serta sumber secara batch dari snapshot yang sama. Urutan konteks dan token accounting reproducible. Jika evidence wajib tidak muat, pilih subpertanyaan/abstention/partial sesuai kontrak; jangan membuat jawaban penuh dari konteks terpotong.
9. Generator menerima evidence ID dan teks sebagai data. Isi PDF tidak boleh mengganti instruksi sistem atau memerintahkan tool. Tidak ada pencarian web spontan pada jalur query; pengayaan corpus dilakukan melalui ingestion yang terlacak.
10. Go memetakan klaim dan citation, memvalidasi keberadaan evidence/versi/locator, lalu menghasilkan terminal result. Validitas locator berbeda dari dukungan semantik klaim; penilaian semantik diukur pada evaluator dan jika ada online verifier, waktunya termasuk latency end-to-end.

Streaming mengirim metadata, potongan teks berstatus provisional, kemudian answer final atau error terminal. Token substantif pertama menjadi TTFT; heartbeat tidak dihitung. Citation boleh ditambahkan setelah teks, tetapi terminal answer wajib memiliki claim mapping dan status. Token yang telah tampil tidak dapat ditarik kembali; klien harus menandai jawaban belum selesai sampai terminal event. Mode nonstreaming menunggu validasi final dengan biaya latency yang dilaporkan.

## 6. Index, model, dan inference

Qdrant menyimpan representasi dense dan sparse lexical terpisah; Neo4j memiliki graph; PostgreSQL menjadi sumber metadata, status dan manifest. Query tidak memuat seluruh graph ke heap Go. Detail versi indeks dan isolasi snapshot dijelaskan di [storage-consistency.md](storage-consistency.md).

BGE-M3 adalah kandidat embedding dari PLAN. Keluaran dense, sparse model, dan BM25 adalah representasi berbeda; memakai BGE-M3 tidak otomatis memenuhi jalur BM25. Reranker, generator, engine PDF/OCR, backend C++, tokenizer, precision, lisensi distribusi, dan driver harus dipilih melalui matriks kompatibilitas + quality/performance trial. ONNX Runtime merupakan kandidat, bukan dependency yang dianggap sudah cocok tanpa ekspor/parity test.

ModelManifest mengikat model/tokenizer checksum, pooling, normalisasi, dimensi, token limits, precision, backend, dan prompt template bila berlaku. Runtime warm memiliki bounded queue, microbatch berdasarkan token/shape, prioritas query, dan slot ingestion yang tetap mendapat layanan. Model dengan manifest berbeda tidak masuk batch yang sama. Oversize input ditolak atau di-window sesuai kontrak; truncation selalu tercatat. Output per-item menjaga korelasi walaupun sebagian item gagal.

Semantic gateway Go melayani Extract/Resolve/Summarize untuk worker; generation jawaban memakai adapter Go secara langsung. Timeout/retry provider dibatasi deadline dan operation key. Cache hasil semantik memakai fingerprint input/model/prompt/schema; respons model yang telah diterima disimpan untuk replay, karena sampling ulang tidak dijamin identik.

## 7. API, error, dan degradasi

API query menerima pertanyaan serta pilihan tanggal/snapshot dan menghasilkan jawaban/evidence. API operasional mengelola ingestion/update, status/cancel job, review item, serta inspeksi sumber/versi/snapshot. Endpoint kesehatan membedakan proses hidup dan kesiapan melayani workload. Daftar operasi dan event ada di [system-contracts.md](system-contracts.md).

| Kondisi | Perilaku yang dirancang |
| --- | --- |
| Tidak ada bukti yang memadai | ABSTAIN dengan alasan, bukan jawaban dari ingatan model |
| Pertanyaan ambigu material | NEEDS_CLARIFICATION dan penjelasan ambiguitas |
| Bukti saling bertentangan | CONFLICT atau PARTIAL dengan kedua sumber yang relevan |
| Backend wajib gagal | UNAVAILABLE/DEADLINE_EXCEEDED; jangan dihitung sebagai abstention yang benar |
| Jalur opsional sengaja tidak dipakai | Manifest profil mencatatnya; tidak mengklaim profil Hybrid GraphRAG lengkap |
| Generation gagal setelah token keluar | Error terminal; partial text bukan jawaban final sukses |
| Deadline/kapasitas habis | Penolakan terstruktur; masuk denominator benchmark |
| Artefak/offset/schema tidak valid | Karantina batch/item dan job gagal terlapor sebelum publikasi |

Default penerimaan Hybrid GraphRAG tidak meluluskan fallback yang diam-diam menghilangkan retriever. Eksplorasi fallback boleh dilaporkan sebagai profil berbeda. Status jawaban semantik dipisah dari completion status transport.

## 8. Observabilitas dan pengendalian latency

Satu trace menghubungkan request/job, snapshot, manifest model, dan stage timing. Span mencakup admission/queue, normalisasi, linking, embedding, tiap backend, fusion, rerank, parent fetch, context, provider TTFT, stream, validation, serta publication. Gunakan durasi monotonic lokal; timestamp lintas mesin bukan pengganti pengukuran end-to-end oleh load generator.

Metrik proses memuat CPU/RSS/VRAM, ukuran batch/token, bytes RPC, antrean, connection pool, cache hit, retry, truncation, coverage, serta counts error. Request/record ID berada di trace/log, bukan label metrik cardinality tak terbatas. Log default tidak menyimpan secret atau seluruh isi query/dokumen; artefak eksperimen terkontrol menyimpan input yang diperlukan untuk reproduksi.

Target tahap tidak dijumlahkan sebagai p95 end-to-end. Analisis critical path membedakan bagian paralel dan serial. Query punya deadline menyeluruh; retry tidak memperoleh budget baru. Propagasi deadline gRPC adalah mekanisme yang tersedia, tetapi kode harus menghentikan pekerjaan turunannya setelah pembatalan. [Dokumentasi deadline gRPC](https://grpc.io/docs/guides/deadlines/)

Cache metadata/parent menggunakan corpus, revision/snapshot, dan versi schema. Cache model memasukkan identitas model/tokenizer dan input lengkap. Query-result serta query-embedding cache dimatikan pada acceptance utama sesuai YAML. Cache produksi hanya digunakan bila invalidasinya diuji dan kebijakannya dicatat.

## 9. Deployment dan operasi

Deployment target Linux x86-64; Windows tetap lingkungan development. Image server, worker, dan inference dibangun dengan dependency minimum masing-masing, model di storage terpisah dengan checksum, serta versi artefak terkunci. Evaluation/tooling dijalankan sebagai job khusus. Database dan internal RPC tidak diekspos sebagai API publik; autentikasi service dan izin corpus diperiksa pada server.

Role query boleh membaca corpus yang diizinkan; operator boleh mengelola sumber/job/review; publisher memiliki credential tulis database. Worker hanya memperoleh akses artefak yang dibutuhkan dan gateway semantik, tanpa credential publisher. Fetcher memvalidasi URL/redirect tujuan agar request ingestion tidak mengakses jaringan internal lewat URL arbitrer. Parser dokumen dijalankan dengan batas CPU/memori/waktu; output model dan dokumen diperlakukan sebagai input tidak tepercaya.

Startup memvalidasi konfigurasi, schema capabilities, dependency model, storage, dan manifest sebelum readiness. Shutdown menghentikan admission baru, membatalkan atau menyelesaikan pekerjaan sesuai deadline, menyimpan checkpoint, lalu melepas lease. Publication yang sedang berjalan dipulihkan dari ledger. Backup/restore harus mengembalikan satu manifest snapshot lintas backend; restore tiga dump yang tidak serasi tidak dianggap recovery berhasil.

Deployment pertama memakai resource reference benchmark yang telah ditetapkan sebagai asumsi, bukan klaim hardware milik pengguna. GPU embedding/reranking berbeda dari endpoint generator dalam profil itu. Menjalankan generator lokal pada GPU yang sama merupakan eksperimen deployment berbeda dan harus dinilai ulang.

## 10. Evaluasi dan definisi selesai

Empat profil Vector RAG, Hybrid RAG, GraphRAG, dan Hybrid GraphRAG memakai corpus/generator/prompt/context budget yang sebanding. Reranker, routing, dan seed linking menjadi faktor ablation eksplisit. Gold evidence adalah set bukti yang cukup, dapat memiliki alternatif yang sah, dan menyatakan versi serta kondisi. LLM judge/RAGAS dapat membantu, tetapi bukan pengganti label manusia untuk gate yang mensyaratkannya.

Seluruh 59 gate required pada YAML dipetakan ke workload, instrumentation, dataset, raw output, serta owner tahap di [development-plan.md](development-plan.md). Fixture sintetis membuktikan invariant dan failure handling; fixture itu tidak membuktikan accuracy model pada corpus regulasi. Performance trial kecil tidak mengganti acceptance workload yang telah disetujui.

Desain lengkap berarti setiap use case memiliki owner, data, alur sukses/gagal, persistensi, serta cara verifikasi. Release selesai hanya setelah implementasi nyata dan seluruh required gate applicable lulus. Gagal benchmark berarti memperbaiki implementasi dan menguji ulang; hanya perubahan benchmark membutuhkan persetujuan pengguna.

## 11. Keputusan yang perlu pembuktian

| Keputusan | Sudah ditetapkan | Yang masih harus dibuktikan |
| --- | --- | --- |
| Corpus | BPK, JDIH Kemkomdigi, JDIHN; lintas bidang | Inventory, cara akses connector, keragaman, kelengkapan rantai rujukan |
| Runtime | Go/Rust/C++, Python offline; batch gRPC sebagai rancangan transport | Library binding, overhead serialisasi, cancellation, deployment executable |
| Temporal | Pisahkan tanggal hukum, observation time, dan snapshot knowledge | Parser metadata/perubahan dan label kasus ambigu |
| Publication | Satu coordinator, staged generation, pinned snapshot | Adapter visibility, restart/compensation, indeks siap dicari |
| Lexical | BM25 sparse dengan statistics generation eksplisit | Tokenizer/dictionary/scoring, drift quality, biaya rebuild statistik |
| Model/engine | Manifest dan parity wajib; kandidat mengikuti PLAN | Kecocokan model/backend, akurasi, latency, memori, distribusi artefak |
| Kapasitas | Profil reference serta semua target required tetap | Hardware aktual dan hasil load test nyata |

Bagian yang menunggu pembuktian tidak dihilangkan dari desain atau dianggap sudah berjalan. Temuan eksperimen memperbarui keputusan teknis dan kontrak dengan versi/impact review; perubahan standar benchmark tetap mengikuti otorisasi pengguna.
