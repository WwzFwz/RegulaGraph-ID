# src/ingestion/src/indexing

Persiapan representasi dense/lexical dan record indeks di worker Rust. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Logika di luar cakupan ini ditempatkan pada komponen pemiliknya. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../AGENTS.md).

## Peran dan integrasi anak

Hasil akhirnya batch untuk Go publisher. `inputs.rs` merender teks primer chunk dengan label induk secara berbatas, berdasarkan span UTF-8 dan node yang sudah diverifikasi pemanggil; policy rendering harus dipin pada generation. `analyzer.rs` membuat istilah dokumen menurut aturan lexical v1: NFC Unicode 15, lipat huruf ASCII saja, pertahankan nomor dengan garis miring dan kata majemuk berhubung. `lexical.rs` menghitung statistik BM25 incremental; `dictionary.rs` membaca term-ID immutable yang nantinya dialokasikan Go serta memeriksa descendant terikat digest; `statistics.rs` membekukan DF dan menghasilkan bobot sparse dokumen/query. Term baru pada descendant memakai frozen DF=0. Dot product diuji terhadap skor BM25 lokal. Learned sparse BGE-M3 tetap representasi berbeda. Serialisasi artefak generation, allocator Go, writer backend, dan publication belum tersambung; folder ini tidak menulis publication marker.

NFC menggunakan kebijakan stream-safe x/text Go yang direplikasi di Rust dengan tabel
properti Unicode 15 terpin. Tabel kategori Letter dan properti NFC dibangkitkan
offline; Jamo terurai maupun Hangul tersusun mengikuti aturan yang sama. Perubahan
versi Unicode atau properti memerlukan generation analyzer baru. Input gagal tidak
menghasilkan indeks lexical parsial.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Berkas: [analyzer.rs](analyzer.rs), [dense.rs](dense.rs), [dictionary.rs](dictionary.rs), [inputs.rs](inputs.rs), [letter_ranges.rs](letter_ranges.rs), [nfc_properties.rs](nfc_properties.rs), [lexical.rs](lexical.rs), [statistics.rs](statistics.rs), [mod.rs](mod.rs). Dua tabel Unicode dihasilkan oleh [generator Unicode](../../../../scripts/generate_lexical_letters.go); jangan mengeditnya secara manual.

## Benchmark dan perhatian performa

Prioritas: p95/p99 latency query, waktu antre dan time-to-first-answer-token; untuk job ukur throughput serta peak RSS. Target numerik wajib ada di [target numerik wajib](../../../../configs/benchmark-targets.yaml); profil referensi dan status REQUIRED_UNMEASURED berlaku. Pemrosesan berjalan tanpa loading model per request dan tanpa RPC per tahap kecil fusion/filter/context.

Ikuti [kebijakan benchmark](../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Analyzer, dictionary reader dengan pemeriksaan lineage lokal, statistik BM25 incremental/frozen, encoder sparse, renderer parent-label per batch, dan pembentuk satu batch dense melalui native inference tersedia sebagai library. Renderer membangun lookup node sekali agar biaya banyak chunk tidak mengulang scan semua struktur; hash output hanya mengikat byte teks hasil render dan belum merupakan reuse key model. Dense batch memerlukan chunk text dengan source/span provenance, batas 128 item dan 2 MiB teks; respons parsial, drift model, truncation, serta vektor tidak valid ditolak tanpa output parsial. Bukti lineage tepercaya dari registry Go, artefak typed, dan pengujian backend masih diperlukan sebelum update dapat dipublikasikan. Fixture bersama Rust–Go membuktikan kasus tokenisasi terpilih; parity skor pada corpus kecil diuji. IndexBatch, allocator, writer terkoordinasi, dan acceptance kualitas/latency belum tersedia. Fixture tidak membuktikan Recall@k atau target required.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [dense.rs](dense.rs) | Hubungkan hasil satu batch dense yang telah divalidasi ke record IndexBatch dengan ID/reuse key deterministik dan generation terpin. | Uji input tanpa provenance, drift model, partial embeddings dan retry idempotency; ukur throughput/RSS batch dan downstream recall. |
| [inputs.rs](inputs.rs) | Hubungkan renderer dengan loader `DocumentBatch`/TextArtifact `ReadVerified`, version joins, dan rendering policy terpin di plan. | Uji byte hash sumber, ancestor berbeda, label Unicode, batas parent/context, dan efek policy terhadap Recall@k serta token cost. |
| [analyzer.rs](analyzer.rs) | Analyzer v1 aktif; lanjutkan corpus-scale profiling dan pin artifact identity pada IndexGeneration. | Go/Rust memakai fixture sama; versi Unicode diuji, namun seluruh vocabulary corpus belum diaudit. |
| [dictionary.rs](dictionary.rs) | Reader term-ID dan cek descendant lokal aktif; sambungkan allocator/binding revision PostgreSQL, bukti lineage tepercaya dan artefak typed. | Tolak duplicate ID/term, reassignment, future/sibling dan digest revision yang bertentangan sebelum reuse backend. |
| [lexical.rs](lexical.rs) dan [statistics.rs](statistics.rs) | Statistik incremental, frozen DF dan bobot sparse termasuk term append aktif sebagai library; tambah serialisasi dan build batch dua pass. | Dot product parity vs skor referensi lulus pada fixture; full corpus, throughput/RSS, dan Recall@k tetap NOT_MEASURED. |
