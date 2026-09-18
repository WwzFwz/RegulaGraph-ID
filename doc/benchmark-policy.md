# Kebijakan benchmark dan gate penerimaan

Dokumen ini mengatur pengukuran serta penerimaan performa dan kualitas lintas runtime. Target numerik wajib ditetapkan atas arahan pengguna, dengan kondisi referensi yang eksplisit dan hasil pengukuran terpisah dari sasaran desain.

Sumber angka: [configs/benchmark-targets.yaml](../configs/benchmark-targets.yaml), suite regulagraph-performance-v1. Status **REQUIRED_UNMEASURED** berarti wajib dicapai tetapi belum diukur. [Panduan target](benchmark-targets.md) menjelaskan asumsi 16 core fisik, RAM 64 GiB, NVMe, GPU 24 GiB untuk embedding/reranker, dan generator endpoint terpisah. Angka ini bukan klaim kemampuan hardware pengguna yang belum ditentukan.

## Cara menerima hasil

Hybrid GraphRAG adalah profil release; seluruh required gate applicable harus terpenuhi. Baseline Vector/Hybrid/GraphRAG tetap dilaporkan sebagai pembanding. Setiap run wajib memuat manifest hardware, model/tokenizer/prompt/precision, corpus/split hash, konfigurasi, serta workload. Kondisi tidak lengkap atau berbeda dari profil tidak diam-diam dinilai comparable.

Status hasil adalah NOT_MEASURED, BLOCKED, FAIL, atau PASS. Sampel kosong, nilai hilang, runner yang belum tersedia, atau model belum dipilih tidak boleh menghasilkan PASS. Runtime masih scaffold; enforcement gate pada runner belum diimplementasikan.

Jalankan tiga run performa valid; semua gate applicable harus lulus pada setiap run, bukan hanya rata-rata. Threshold menggunakan point estimate dan laporan interval ketidakpastian 95%; jangan menganggap point estimate sebagai jaminan populasi. Invariant deterministik berlaku atas seluruh record terbit dan fixtures yang relevan.

## Protokol waktu dan beban

Gunakan open-loop load dengan arrival tetap: 10 request/detik untuk evidence-ready dan 2 request/detik untuk jawaban lengkap. Warm-up 120 detik lalu minimal 20 menit per run, dengan ukuran sampel minimum di YAML. Jangan menurunkan arrival saat server melambat atau menghapus rejected/dropped/timeout dari denominator.

Laporkan nearest-rank p50/p95/p99, waktu antre, TTFT substantif, inter-token latency, waktu jawaban lengkap, throughput, serta RAM/VRAM. Timing dimulai di load generator dan mencakup jaringan/provider. Latency yang tidak tersedia untuk request tak selesai dianggap tak hingga. Pisahkan cold/warm; matikan result cache dan query-embedding cache untuk acceptance utama.

Pakai source/model yang dibekukan. Pertahankan histogram input/output dan jangan memperoleh latency rendah melalui jawaban sangat pendek atau bukti terpotong. Batas per tahap adalah budget tersendiri; percentile tahap tidak dijumlahkan sebagai pengganti pengukuran end-to-end.

## Kualitas dan perbandingan yang adil

Gold set minimal 1000 pertanyaan berlabel manusia, dengan group split berdasarkan base question. Ukur factual, prosedural, multi-hop, temporal, typo, informal, code-switch, dan tanpa bukti. Satu keluarga paraphrase tidak bocor antar split. Label mencakup versi, predicate, syarat/pengecualian, dan bukti minimum yang sah.

Bandingkan empat pendekatan dengan corpus, generator, prompt, serta budget konteks yang sebanding. Pisahkan efek reranker dan seed linking graph. Accuracy, retrieval/citation quality, canonicalization, serta invariant menjadi syarat optimasi performa, bukan metrik yang boleh dikorbankan tanpa persetujuan.

## Ketika target gagal

Jika target gagal, terus perbaiki implementasi, lakukan profiling/optimasi, dan uji ulang sampai target tercapai. Perbaikan dalam scope sudah diotorisasi; hasil FAIL tidak mewajibkan berhenti atau meminta izin, dan tidak otomatis memicu negosiasi benchmark.

Hanya usulan perubahan benchmark yang memerlukan persetujuan pengguna: angka, workload, asumsi penerimaan, atau kriteria lulus. Sertakan target vs aktual, kondisi run, bottleneck terukur, optimasi yang telah dicoba, opsi perbaikan, dan perubahan benchmark beserta dampaknya. Selama belum disetujui, benchmark lama tetap berlaku dan perbaikan yang tidak terblokir tetap dilanjutkan.

Dilarang menurunkan target, mengganti workload, menghapus query sulit, mengganti label/denominator, atau mengubah precision/model tanpa evaluasi ulang demi memperoleh PASS. Persetujuan pengguna diperlukan untuk melonggarkan target atau asumsi penerimaan. Perubahan dicatat dengan suite version baru dan hasil lama tetap disimpan. Tuning dilakukan pada dev, bukan test.

## Katalog kontrak benchmark

### DOC

Dokumentasi harus konsisten dengan status scaffold/implementasi dan tidak menyatakan hasil eksperimen yang belum dijalankan. Tautan lokal dan rujukan kontrak diperiksa.

### CONFIG

Konfigurasi yang tidak valid harus ditolak secara eksplisit saat loader diimplementasikan. Import modul tidak boleh memicu I/O atau inference. Ukur startup dan waktu validasi bila menjadi bottleneck; ambang wajib mengikuti configs/benchmark-targets.yaml; hasil belum diukur.

### DOMAIN

Gate deterministik: ID dan referensi sumber/versi tidak ambigu; invalid record ditolak atau dikarantina secara eksplisit. Kompatibilitas schema diperiksa dengan fixtures; belum ada hasil pengukuran.

### SOURCE

Ukur keberhasilan akuisisi, bytes/detik, retry, dan waktu p50/p95/p99 per sumber. Gate: konten memiliki hash dan asal yang terlacak; unduhan parsial tidak diterbitkan sebagai sumber lengkap.

### PARSING

Ukur ketepatan batas pasal/ayat, ketepatan urutan baca, CER/WER OCR pada sampel berlabel, dan detik/halaman per format. Gate: sumber dan lokasi teks tetap terlacak; nomor, negasi, dan pengecualian pada fixtures dipertahankan. Target statistik wajib mengikuti configs/benchmark-targets.yaml; ukur dengan gold set yang memenuhi profil.

### CHUNKING

Ukur cakupan pemetaan chunk ke sumber/induk, proporsi ketentuan-pengecualian yang terpisah, Recall@k bukti, jumlah token, serta waktu ingestion. Gate: tiap chunk terbit punya parent/source/version yang valid. Bandingkan struktur+konteks induk dengan baseline pada corpus yang sama; ambang kualitas/latency wajib mengikuti configs/benchmark-targets.yaml.

### VERSIONING

Ukur ketepatan pemilihan versi pada pertanyaan bertanggal, kebocoran versi yang tidak berlaku, dan penanganan status tidak diketahui. Gate: tanggal observasi tidak menggantikan tanggal berlaku; fixtures perubahan/pencabutan/unknown menghasilkan status yang ditentukan label.

### EXTRACTION

Ukur precision/recall/F1 entitas dan relasi dengan predicate, arah, kondisi, serta bukti sumber; catat kegagalan schema, token, biaya/dokumen, dan waktu p50/p95/p99. Target wajib mengikuti configs/benchmark-targets.yaml dan memerlukan gold set valid; schema valid tidak dianggap fakta benar.

### RESOLUTION

Ukur pairwise precision/recall/F1, false merge, false split, mention yang hilang, waktu per batch, dan biaya. Gate: semua mention tetap terlacak; pasal dari peraturan berbeda tidak digabung hanya karena nama sama. Blocking harus diukur juga terhadap pasangan benar yang terlewat.

### GRAPH

Ukur validitas endpoint dan provenance, ketepatan predicate/arah, kelengkapan jalur bukti, waktu assembly/traversal, dan penggunaan memori. Gate: graph yang dipublikasikan tidak memiliki endpoint/bukti wajib yang hilang. Connectivity adalah diagnosis, bukan target memaksa satu komponen.

### INDEX

Ukur konsistensi ID/versi antarindeks, freshness lag, throughput indexing, ukuran indeks, dan retrieval Recall@k. Gate: penghapusan/upsert idempotent dan model/dimensi representasi cocok. Target kapasitas wajib mengikuti profil corpus/hardware dalam configs/benchmark-targets.yaml.

### RETRIEVAL

Ukur Recall@k, nDCG@k, kelengkapan semua bukti multi-hop, serta latency p50/p95/p99 per jenis pertanyaan. Catat jumlah kandidat, hop, snapshot, dan kondisi cold/warm. Ambang wajib mengikuti configs/benchmark-targets.yaml; jangan menganggap batas hop/kandidat atau graph selalu meningkatkan kualitas.

### RERANK

Bandingkan nDCG@k dan kelengkapan bukti sebelum/sesudah reranking; ukur p50/p95/p99, batch size, panjang pasangan, dan truncation. Kandidat yang hilang sebelum reranking tidak dapat dipulihkan. Target mutu dan budget latency wajib mengikuti configs/benchmark-targets.yaml; perubahan memerlukan persetujuan pengguna.

### CONTEXT

Ukur cakupan gold evidence, kelengkapan jalur graph, duplikasi, token count, dan waktu membangun konteks. Gate: tiap item konteks dapat dipetakan ke sumber dan versi; pemotongan/ketidakcukupan bukti dilaporkan. Context budget tidak boleh diam-diam menghapus syarat penting.

### ANSWER

Ukur ketepatan jawaban, citation precision/coverage, ketepatan versi, abstention pada pertanyaan tanpa bukti, faithfulness, latency p50/p95/p99, dan token/biaya. Gate deterministik hanya validitas referensi; dukungan semantik harus dievaluasi tersendiri dengan review manusia terkalibrasi.

### UPDATE

Gate: sumber tak berubah tidak diekstraksi ulang; update identik idempotent; dependensi terdampak diinvalidasi; bukti yang masih didukung sumber lain tetap tersedia. Uji pemulihan kegagalan parsial dan snapshot konsisten. Ukur waktu/biaya update terhadap full rebuild serta freshness lag.

### STORAGE

Ukur latency p50/p95/p99, throughput batch, pool saturation, retry, dan error rate pada concurrency serta volume data yang disebutkan. Gate: timeout terlapor, resource dilepas, dan operasi tulis idempotent sesuai kontrak; tidak ada asumsi transaksi atomik lintas layanan.

### MODEL

Ukur cold load terpisah dari inference warm, throughput token/embedding/pasangan, p50/p95/p99, RAM/VRAM, truncation, dan biaya. Catat model/version, precision, hardware, dan batch size. Cache harus memasukkan versi model serta input yang relevan; target numerik wajib mengikuti configs/benchmark-targets.yaml.

### API

Gate: input/response mengikuti schema, error tidak disamarkan menjadi jawaban sukses, dan readiness sesuai dependency yang diperlukan. Ukur latency end-to-end p50/p95/p99, error rate, concurrency, dan timeout pada workload yang dinyatakan; target benchmark wajib ada di configs/benchmark-targets.yaml; hasil belum diukur.

### EVAL

Gate: setiap run merekam dataset/split, corpus snapshot, model/prompt/config version, seed bila relevan, serta biaya. Laporkan ukuran sampel dan ketidakpastian, hindari tuning pada test set, dan pisahkan kualitas retrieval, graph, jawaban, serta waktu/biaya.

### TEST

Pengujian harus memverifikasi perilaku dan kegagalan yang bermakna. Unit test tidak membutuhkan jaringan; integration/end-to-end menyatakan layanan yang diperlukan. Mock hanya memvalidasi alur, bukan akurasi model. Tidak ada klaim coverage atau test lulus sebelum dijalankan.

## Integrasi hasil

Docstring file mencantumkan ID benchmark yang relevan. Runner evaluasi menulis hasil dengan run ID ke artifacts; tests menjaga invariant dan regresi perilaku. RAGAS atau judge LLM merupakan sinyal tambahan yang harus dikalibrasi terhadap review manusia. Mock tidak menjadi bukti accuracy. Tidak ada pengujian atau hasil model yang telah dijalankan hanya karena scaffold ini tersedia.

## Prioritas runtime lintas bahasa

Go serving, Rust batch worker, C++ inference, dan Python evaluator mengukur tahap dengan trace/run ID bersama. Prioritaskan p95/p99 query, waktu antre, time-to-first-answer-token (bukan pesan status), inter-token latency, dan waktu jawaban lengkap. Ukur ingestion halaman/detik serta edge/detik dan peak RSS pada saat query juga berjalan.

Uji kontrak lintas bahasa menggunakan source/canonical/version/snapshot ID yang sama serta offset byte UTF-8 end-exclusive. Bandingkan skor/embedding native dengan referensi model dan metrik retrieval; codegen atau compile yang sukses tidak membuktikan parity semantik. Sasaran desain sejak awal adalah session/pool dipakai ulang, tidak ada N+1 query sumber, tidak ada inference load per request, serta tidak ada RPC untuk tiap fungsi kecil fusion/filter/context.

Build Go, cargo check, dan CMake scaffold hanya memverifikasi struktur kode. Tidak ada hasil latency/throughput atau model quality yang dapat disimpulkan dari keberhasilan build tersebut.
