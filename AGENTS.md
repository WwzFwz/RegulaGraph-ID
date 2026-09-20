# Aturan kerja repositori RegulaGraph-ID

File ini menampung aturan penempatan kode dan dokumentasi lintas repositori sesuai arahan pemilik proyek. Perannya adalah menjaga batas fungsional komponen ketika scaffold berkembang menjadi implementasi.

## Baca cakupan sebelum mengubah

Sebelum menambah atau memindahkan komponen, baca README.md pada folder tujuan dan seluruh induknya yang relevan. Setiap README mendefinisikan superset fungsi yang boleh berada di dalam folder serta kontrak integrasi anak-anaknya.

Jika fungsi baru tidak termasuk cakupan tersebut, jangan menaruhnya di sana atau memperluas deskripsi folder secara sepihak. Siapkan penjelasan fungsi, lokasi yang diusulkan, dampak integrasi, dan alasan ketidaksesuaian, lalu tanyakan kepada pengguna apakah perlu membuat folder baru atau memasukkannya ke folder yang ada dengan memperbarui cakupannya. Persetujuan yang sudah diberikan pengguna tetap berlaku; pekerjaan lain yang tidak bergantung pada keputusan itu boleh berlanjut. Penambahan rutin yang sesuai cakupan tidak memerlukan konfirmasi ulang.

## Dokumentasikan setiap folder dan file

Setiap folder sumber, konfigurasi, dokumentasi, evaluasi, dan pengujian yang dikelola proyek wajib memiliki README.md. Penjelasan cakupan, peran arsitektur, dan integrasi harus berupa paragraf yang bermakna. Folder yang hanya dihasilkan otomatis oleh alat, seperti .git, cache, virtual environment, dan direktori build (termasuk .cache dan target), tidak termasuk struktur komponen yang dikelola.

Setiap file kode baru diawali docstring atau komentar yang menjelaskan fungsi, peran dalam komponen, kontrak input/output atau integrasinya, dan perhatian performa yang relevan. File konfigurasi diawali komentar dengan tujuan dan status penggunaannya. File Markdown diawali penjelasan peran dokumen. Berkas biner dan referensi pihak ketiga dijelaskan dalam README pendamping, bukan diubah isinya.

Bila implementasi menggantikan scaffold, perbarui docstring status dan README agar tidak mengklaim fungsi yang belum tersedia atau tetap menyebut fungsi aktif sebagai placeholder. Dokumentasikan anak baru dan perubahan dependency.

## Integrasi dan arsitektur

Pengguna meminta desain seluruh sistem sejak awal, bukan pembatasan ke kontrak minimum. Sebelum implementasi, ikuti doc/system-design.md, doc/system-contracts.md, doc/storage-consistency.md, doc/corpus-plan.md, dan doc/development-plan.md. Keputusan 0005 membedakan baseline desain menyeluruh dari implementasi scaffold. Seluruh kontrak direalisasikan pada paket C01; urutan coding mengikuti dependency, bukan urutan folder. Sumber corpus yang dipilih adalah Database Peraturan BPK, JDIH Kemkomdigi, dan JDIHN Nasional.

Pengelompokan kode produk dalam src dan konfigurasi deployment dalam deployment telah disetujui pengguna pada keputusan 0004. Folder src/server, src/ingestion, src/inference, dan src/contracts menggantikan pembungkus runtime di root. Workspace manifest tetap di root; evaluation, tooling, tests, configs, migrations, scripts, doc, data, dan artifacts tetap terpisah. Perubahan struktur ini tidak memerlukan persetujuan ulang.

Struktur runtime telah disetujui pengguna: src/server adalah Go untuk API/CLI, workflow, retrieval, answering, serta adapter; src/ingestion adalah Rust untuk transformasi dokumen/graph/index batch; src/inference adalah C++ untuk wrapper inference; evaluation dan tooling adalah Python offline. src/contracts menjadi sumber schema wire bersama. Keputusan 0002 menggantikan pemilihan paket produksi Python pada keputusan 0001.

Pertahankan pemisahan fungsi domain, ingestion, knowledge_graph, indexing, retrieval, answering, workflow, dan adapter di runtime pemiliknya. Go memiliki penjadwalan job serta commit/publication storage; Rust menghasilkan batch dan dependency manifest. Route dan CLI memanggil workflow; evaluator menggunakan endpoint atau artefak produksi, bukan salinan algoritmanya. Fusion/filter/context/citation tetap satu proses Go.

Import/inisialisasi paket dan static initializer tidak boleh membuka koneksi atau memuat model. ID sumber, canonical ID, versi pasal, provenance, dan snapshot corpus harus diteruskan lintas komponen. Jangan menghapus versi lama atau bukti bersama ketika memperbarui satu sumber.

Keputusan yang telah disepakati adalah chunk mengikuti struktur dengan konteks induk, entity resolution/canonical identity, pemilihan versi pasal yang sesuai, dan pemrosesan incremental. Latency dan throughput merupakan prioritas desain. Target numerik benchmark sudah diwajibkan dalam configs/benchmark-targets.yaml pada profil referensi asumsi. Provider/model serta parameter runtime dipilih dan dibekukan sebelum run yang dinilai. Jalur request memakai workflow Go dan tidak bergantung pada LangGraph Python. Jangan memaksakan pruning sebagai optimasi tanpa pengukuran.

## Benchmark

Untuk subkomponen yang memengaruhi akurasi, latency, throughput, biaya, memori, atau konsistensi, sertakan metrik dan cara pengukurannya pada dokumentasi file. Ikuti doc/benchmark-policy.md. Angka target desain yang ambisius telah ditetapkan atas arahan pengguna dalam configs/benchmark-targets.yaml, suite regulagraph-performance-v1. Gunakan asumsi hardware, corpus, dan workload yang dinyatakan. Status REQUIRED_UNMEASURED bukan hasil tercapai atau SLA empiris. Jangan mengarang hasil benchmark; prasyarat tidak lengkap menghasilkan BLOCKED/NOT_MEASURED, bukan PASS.

Perbandingan model/retrieval menyatakan corpus snapshot, split dataset, konfigurasi, versi model/prompt, dan perbedaan anggaran konteks. Mock tidak membuktikan kualitas model. Catat hasil eksperimen dalam artifacts dan keputusan arsitektur dalam doc/decisions.

## Verifikasi

Pengguna mewajibkan proses verifikasi input, proses, output, arsitektur, kualitas, dan performa secara berkelanjutan. Sebelum implementasi dan sebelum menyatakan paket selesai, baca doc/verification.md beserta checklist terkait di doc/verification-contracts.md, doc/verification-pipeline.md, dan doc/verification-quality.md. Panduan pekerjaan berikutnya berada pada doc/implementation-guide.md serta rekomendasi spesifik di header/README komponen. Dokumen tersebut adalah aturan kerja, bukan bukti enforcement otomatis.

Gunakan agent verifikasi terpisah pada milestone dan boundary kritis, bukan untuk setiap perubahan kecil. Cakupan kritis meliputi kontrak lintas bahasa/schema, integritas data dan provenance, storage/publication/recovery, security boundary, versioning/resolution hukum, pemilihan model dan evaluasi kualitas, integrasi retrieval-answer, serta acceptance benchmark/release. Gabungkan perubahan rutin yang saling terkait dan review setelah paket koheren siap agar biaya token tidak mengulang konteks yang sama. Implementer tetap menguji setiap perubahan; perubahan dokumentasi, refactor lokal, dan helper berisiko rendah cukup mendapat pemeriksaan proporsional tanpa agent terpisah. Pengguna menyetujui kebijakan berbasis risiko ini pada 2026-09-20.

Reviewer memeriksa diff/desain/bukti secara independen dan tidak menyatakan lulus dari klaim implementer. Perbaiki temuan dalam scope, tambahkan regression test yang bermakna, dan verifikasi ulang bagian terdampak sebelum klaim milestone selesai. Bila milestone kritis memerlukan reviewer tetapi fasilitas agent tidak tersedia, lakukan review manual terstruktur dan nyatakan review independen belum terverifikasi; jangan mengarang approval.

Simpan raw log/hasil di artifacts/verification/<run-id> dan ringkasan status yang perlu dilacak di dokumentasi. Laporan menyebut revision atau fingerprint kode, perintah/exit code, toolchain, fixture/dataset, expected vs actual, cakupan, dan temuan terbuka. PASS hanya untuk pemeriksaan yang dijalankan; prasyarat belum lengkap menjadi BLOCKED/NOT_MEASURED. Build/fixture PASS tidak membuktikan kualitas model, kebenaran hukum, atau required benchmark release. Perubahan kode setelah review memerlukan verifikasi ulang bagian terdampak.

Lakukan pemeriksaan yang sesuai perubahan. Untuk scaffold, periksa build Go, cargo check, CMake C++, syntax Python, dokumentasi folder/file, tautan lokal, serta validitas metadata/config; tidak perlu menulis unit test yang hanya mencerminkan daftar file. Untuk perilaku yang diimplementasikan kemudian, pilih pengujian bermakna dan laporkan keterbatasan verifikasinya.

## Granularitas commit

Pisahkan commit berdasarkan fitur, komponen, atau subkomponen yang dapat ditinjau dan diuji secara mandiri. Pesan commit menjelaskan perilaku konkret, misalnya `feat(acquisition): verify corpus artifacts and provenance`; ID paket seperti D01/S01 boleh menjadi konteks laporan tetapi tidak boleh menjadi satu-satunya penjelasan perubahan. Pisahkan implementasi library, wiring CLI/API, dokumentasi/status, generated output, dan perbaikan temuan verifikasi ketika pemisahan tersebut menghasilkan riwayat yang lebih jelas. Jangan memecah perubahan yang harus atomik sampai membuat commit per file tanpa makna fungsional.

## Kontrak lintas bahasa

Setiap boundary batch atau inference menggunakan schema version serta source/canonical/provision-version/snapshot ID yang konsisten. src/contracts/proto adalah sumber wire schema; baseline C01 tercatat pada src/contracts/schema-lock.json dan diperiksa scripts/check_contracts.py. Jangan menulis ulang baseline untuk meluluskan perubahan tanpa review kompatibilitas. Source offset yang dipertukarkan direncanakan sebagai byte UTF-8, start-inclusive/end-exclusive, disertai identitas teks asli/normalisasi; implementasi harus mempertahankan pemetaan keduanya. Jangan mendefinisikan kontrak paralel yang tidak sinkron di Go/Rust/Python.

Gunakan batch untuk pekerjaan besar dan hindari RPC per langkah kecil retrieval. Jangan memuat model per request, mengulang graph extraction saat query, atau membiarkan job bulk menghabiskan kapasitas query. Ukur p95/p99 dan waktu antre selain throughput rata-rata. Generated binding/artefak compiler adalah keluaran alat; jangan menganggapnya komponen baru yang membutuhkan README per folder cache.

## Target required dan negosiasi

Setiap required gate applicable adalah syarat kelulusan profil release Hybrid GraphRAG. Latency, throughput, kualitas, dan invariant dinilai bersama. Agent tidak boleh menurunkan angka, mengurangi beban, mengganti denominator, membuang kasus sulit, atau melaporkan run gagal sebagai lulus agar target terlihat tercapai.

Jika run valid gagal, terus perbaiki implementasi, lakukan profiling/optimasi, dan uji ulang sampai target tercapai. Kegagalan benchmark bukan alasan berhenti atau meminta izin untuk memperbaiki kode; pekerjaan tersebut sudah diotorisasi dalam scope. Jangan otomatis mengalihkan kegagalan menjadi negosiasi standar.

Persetujuan pengguna diperlukan ketika mengusulkan perubahan benchmark: angka target, workload, asumsi penerimaan, atau kriteria lulus. Usulan harus memuat target vs hasil aktual, kondisi run, bottleneck, optimasi yang telah dicoba, opsi perbaikan, dan perubahan benchmark beserta dampaknya. Selama perubahan belum disetujui, benchmark lama tetap berlaku dan perbaikan yang tidak terblokir tetap dilanjutkan. Simpan raw results dan versi suite sebelumnya. Ini mengikuti klarifikasi pengguna pada 2026-09-19.

Angka disimpan satu kali di YAML; dokumentasi dan header subkomponen merujuk sumber yang sama. Runner E01
offline sudah mengimplementasikan evaluasi gate untuk bundle yang memenuhi eligibility, tetapi belum ada
acceptance run produksi. Jangan mengklaim target tercapai dari konfigurasi atau fixture sintetis.
