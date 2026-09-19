# Rencana corpus dan dataset evaluasi

Dokumen ini merancang akuisisi, provenance, kurasi, dan evaluasi data dari sumber yang dipilih pengguna: Database Peraturan BPK, JDIH Kemkomdigi, dan JDIHN Nasional. Perannya memastikan desain pipeline dan kontrak diuji terhadap bentuk dokumen nyata. Belum ada crawling massal, dataset berlabel, atau klaim kelengkapan corpus yang dihasilkan oleh dokumen ini.

## 1. Sumber pilihan dan perannya

| Source ID desain | Portal | Peran dalam corpus |
| --- | --- | --- |
| bpk | [Database Peraturan BPK](https://peraturan.bpk.go.id/) | Discovery lintas jenis/tahun/instansi, metadata dan dokumen rujukan |
| komdigi | [JDIH Kemkomdigi](https://jdih.komdigi.go.id/) | Sumber kementerian untuk dokumen bidang komunikasi/digital dan relasinya |
| jdihn | [JDIHN Nasional](https://www.jdihn.go.id/) | Discovery dokumen anggota dan pelacakan ke instansi asal |

Pemeriksaan sumber pada 2026-09-19: halaman BPK menyediakan klasifikasi peraturan dan menyatakan dokumen berasal dari instansi pemerintahan terkait. Jika isi berbeda, portal mengarahkan pengecekan ke instansi tersebut. Desain karena itu mempertahankan jalur asal dan konflik, tidak memakai urutan nama portal sebagai kebenaran mutlak. [Keterangan sumber BPK](https://peraturan.bpk.go.id/)

Kemkomdigi menampilkan kategori produk hukum, putusan, artikel, monografi, dan dokumen pembentukan peraturan. Connector wajib mempertahankan document_kind agar rancangan atau penjelasan tidak otomatis dianggap peraturan final. [Kategori pada JDIH Kemkomdigi](https://jdih.komdigi.go.id/)

JDIHN menyatakan dokumen terintegrasi berasal dari laman anggota dan merujuk konfirmasi ke anggota terkait. Pada pemeriksaan ini halaman apex gagal dibaca langsung oleh alat web, sementara halaman www/statistik tersedia melalui hasil pencarian; kondisi ini dicatat sebagai keterbatasan pemeriksaan, bukan bukti portal tidak bisa diakses aplikasi. [JDIHN](https://www.jdihn.go.id/), [statistik dan keterangan sumber](https://jdihn.go.id/statistik)

Ketiga pilihan adalah portal sumber, bukan satu rumpun hukum. Rancangan mendukung lintas rumpun. Inventory akan menentukan proporsi tema yang memiliki rantai rujukan lengkap; tidak mengasumsikan tiga portal mencakup seluruh peraturan Indonesia atau seluruh dokumen tersedia dalam format yang sama.

## 2. Proses discovery dan akuisisi

Connector Go di src/server/internal/ingestion/sources mempunyai operasi Discover, FetchMetadata, FetchArtifact, dan CheckChanged. Setiap connector mengembalikan format SourceObservation yang sama sambil menyimpan metadata asli portal. Adapter sumber berdiri terpisah dari parser Rust; perubahan HTML portal tidak mengharuskan perubahan algoritma chunking.

Sebelum implementasi pengambilan skala besar, periksa mekanisme akses yang tersedia, pagination, sitemap/export/API bila ada, robots/ketentuan akses yang berlaku, redirect file, dan batas layanan. Jangan menganggap portal mempunyai API publik hanya karena UI pencarian tersedia. Kode memakai bounded concurrency per host, timeout, retry dengan backoff, conditional request bila didukung, dan checkpoint discovery agar tidak mengulang seluruh penelusuran.

Setiap download diverifikasi status, content type, byte size, serta checksum. Respons halaman error dengan ekstensi PDF bukan PDF sukses. Sumber lokal juga didukung agar pipeline dan evaluator tidak bergantung pada availability portal saat benchmark. Kegagalan akses dicatat; tidak menebak konten atau menandai sumber dicabut hanya karena tidak ditemukan saat satu crawl.

| Metadata inventory | Kegunaan |
| --- | --- |
| portal_id, source_record_key, detail_url, artifact_url, final_url | Pelacakan asal dan redirect |
| issuer, jurisdiction, type, number, year, title | Kandidat identitas regulasi, belum otomatis canonical |
| publication/enactment/effective date assertions | Temporal processing dengan sumber setiap nilai |
| portal_status, related/amending/repealing refs | Kandidat relasi yang harus diverifikasi terhadap dokumen |
| fetched_at, etag, last_modified, raw_sha256, mime, size | Change detection dan reproduksi |
| language, text/scan/mixed, pages, table/multicolumn flags | Stratifikasi parsing dan kapasitas |
| access/result status, conflict flags, missing_reference refs | Antrian perbaikan data dan review |

## 3. Deduplikasi dan otoritas

Dedup tahap pertama adalah byte-identical blob; provenance observation tetap disimpan. Tahap kedua membandingkan identitas regulasi, edition, normalized text, lampiran, dan signature metadata untuk mendeteksi mirror atau versi berbeda. Teks hampir sama tidak cukup untuk membuang halaman yang mungkin mengandung perubahan angka, pengecualian, atau tanggal.

Metadata portal merupakan assertion bersumber. Dokumen resmi penerbit beserta bukti perubahan menjadi bahan verifikasi konflik; tidak ada voting tiga portal untuk menentukan kebenaran hukum. Konflik tipe/nomor/tahun/tanggal/isi disimpan sebagai review item dengan semua evidence. Normalisasi nama kementerian dan pergantian nama historis memakai registry bersumber, bukan penggantian string global.

Rantai rujukan dibangun dari peraturan induk, pelaksana, perubahan, pencabutan, definisi, serta lampiran yang relevan. Missing reference menjadi gap corpus yang dapat ditindaklanjuti. Dokumen ganda yang tersebar di tiga portal tidak dihitung sebagai tiga regulasi unik atau tiga dukungan independen.

## 4. Data kerja, gold set, dan artefak

| Lokasi | Fungsi |
| --- | --- |
| data/ | Blob sumber, inventory kerja, teks ekstraksi/mapping, staging lokal; diabaikan Git kecuali README |
| evaluation/datasets/ | Schema/manifest, pedoman label, dan gold set kecil yang ditinjau; file besar memakai lokasi eksternal + hash |
| tests/fixtures/ | Kasus deterministik yang kecil untuk offset, kontrak, versi, kegagalan, serta replay |
| artifacts/ | Output run, trace/samples, prediksi, laporan gate dan reproduksi; diabaikan Git kecuali README |

Lokasi fisik file besar dapat berpindah ke storage eksternal tanpa mengubah ArtifactRef semantik. Fixture tidak menggunakan credential atau dokumen privat. Benchmark tidak mengambil dokumen live dari portal pada setiap run; gunakan corpus snapshot immutable sehingga perubahan portal tidak mengubah hasil diam-diam.

## 5. Cakupan data yang harus disiapkan

Target jumlah corpus, halaman, pertanyaan, mention, relasi, dan pasangan resolution mengikuti [benchmark-targets.yaml](../configs/benchmark-targets.yaml). Inventory eksplorasi boleh dimulai sebelum jumlah akhir tercapai, tetapi tidak menurunkan syarat acceptance atau mengubah status menjadi PASS.

Sampling harus mencakup regulasi pusat dan kementerian sesuai scope inventory, variasi tahun, perubahan sebagian pasal/ayat, definisi scoped, tabel/lampiran, PDF teks/scan, rujukan lintas dokumen, kondisi/pengecualian, serta konflik/unknown temporal. Sampling berdasarkan portal saja tidak membuktikan variasi masalah. Setiap strata menyimpan jumlah tersedia, jumlah dipilih, alasan, serta coverage gap.

## 6. Prosedur penyusunan gold

1. Bekukan corpus snapshot dan daftar dokumen/versi yang dapat dipakai. Tetapkan label guideline serta contoh ambigu sebelum menilai output sistem.
2. Tulis pertanyaan dan intended date/interpretation. Tentukan answerable/unanswerable terhadap snapshot tersebut, bukan seluruh pengetahuan dunia.
3. Label expected claims, syarat/pengecualian, provision-version IDs, source spans, dan satu atau beberapa acceptable minimal evidence sets. Multi-hop menyertakan ordered relations/support yang cukup menjelaskan jawaban.
4. Tandai tipe factual/procedural/multi_hop/temporal/typo/informal/code_switched/unanswerable; satu pertanyaan boleh mempunyai beberapa slice. Variasi bahasa berasal dari base_question_id yang sama.
5. Pisahkan train/dev/test berdasarkan base group, lalu audit kedekatan paraphrase/template dan keluarga kasus. Dokumen test boleh berada di corpus retrieval; yang tidak boleh bocor ke tuning adalah label/jawaban test dan varian dekatnya. Laporkan overlap dokumen dan lakukan holdout keluarga regulasi tambahan bila ingin mengukur generalisasi lintas keluarga.
6. Review manusia memeriksa label, evidence alternatif, dan disagreement. Simpan reviewer, versi guideline, uncertainty, serta adjudication. Model dapat mengusulkan pertanyaan, tetapi gold tidak berasal dari penilaian model yang sama tanpa review.
7. Bekukan test manifest/hash; gunakan dev untuk pemilihan model, fusion, chunking, dan prompt. Koreksi label test harus bersumber, berversi, dan dilaporkan; jangan menghapus kasus karena model gagal.

Gold parsing memberi struktur pasal/ayat, reading order, spans, CER reference, serta token kritis angka/negasi/pengecualian. Gold graph memberi mention, canonical same/different, predicate/arah/conditions, supports, dan blocking candidate coverage. Label sumber/teks tidak boleh diasumsikan benar hanya karena extractor menghasilkan JSON valid.

## 7. Hasil persiapan data yang harus tersedia

Tahap data selesai bila ada inventory/provenance manifest, corpus snapshot dengan hash, audit duplikasi/konflik, daftar missing references, stratifikasi format/tema/versi, pedoman anotasi, split manifest, serta gold berlabel sesuai workload required. Jumlah dokumen yang banyak tanpa rantai bukti dan label tidak cukup. Coverage serta status pengumpulan dilaporkan terpisah dari kualitas/latency sistem.
