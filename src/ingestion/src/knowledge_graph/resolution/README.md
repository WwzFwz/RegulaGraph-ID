# src/ingestion/src/knowledge_graph/resolution

Penyelesaian identitas entitas dan pengelolaan canonical ID serta alias. Folder ini menentukan penyebutan mana yang menunjuk objek yang sama. Transformasi batch berjalan dalam Rust; inference semantik menggunakan adapter model. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Kemiripan makna kata bukan bukti identitas; query linking tidak membuat atau menggabungkan canonical baru. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../../AGENTS.md).

## Peran dan integrasi anak

Anak menerima mention beserta tipe dan konteks sumber. Penggabungan harus terlacak dan dapat dikoreksi; identitas pasal mencakup peraturan induknya, dan versi teks tidak disatukan secara destruktif.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Berkas: [aliases.rs](aliases.rs), [blocking.rs](blocking.rs), [mod.rs](mod.rs), [resolver.rs](resolver.rs).

`blocking.rs` menjaga kandidat dengan tipe dan scope tanpa membuat keputusan identitas. `aliases.rs`
membentuk alias deterministik dari satu mention dalam proposal/keputusan LINK yang sudah diautentikasi
oleh registry Go, untuk seluruh tipe pada ontology v1 yang kini dipetakan ke kode registry Go.
Helper memeriksa ID, revisi, tipe, source/span evidence, batas normalized lookup, dan
mempertahankan mention ID sebagai support. Caller tetap wajib memverifikasi receipt/state registry,
provenance sumber, serta memakai entity dari snapshot yang sama; helper bukan pemberi otoritas LINK.
`resolver.rs` merakit proposal LINK/DEFER per mention dari pilihan eksplisit dan kandidat registry
terpin. Satu kandidat tidak memicu LINK otomatis. Proposal mengikat bukti, revisi, hash artefak
sumber, dan fingerprint batch kandidat; Go tetap harus membuktikan receipt serta keputusan
registry sebelum assignment. Batas item mencakup mention, pilihan, scope, entitas, alias, dan
total kandidat; ukur candidate recall dan biaya proposal pada gold sebelum memilih pemotongan.

## Benchmark dan perhatian performa

**RESOLUTION.** Ukur pairwise precision/recall/F1, false merge, false split, mention yang hilang, waktu per batch, dan biaya. Gate: semua mention tetap terlacak; pasal dari peraturan berbeda tidak digabung hanya karena nama sama. Blocking harus diukur juga terhadap pasangan benar yang terlewat.

Ikuti [kebijakan benchmark](../../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Blocking kandidat, proposal LINK/DEFER, dan materialisasi alias LINK sourced untuk tipe ontology v1 aktif sebagai library.
Gateway/model RESOLVE serta handoff receipt Go tersedia, tetapi dispatch produksi,
merge/split, pengukuran false merge/split, dan benchmark produksi belum tersedia. Status anak dijelaskan pada header masing-masing; tes fixture tidak
membuktikan kualitas resolusi.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [aliases.rs](aliases.rs) | Hubungkan helper alias LINK seluruh tipe ontology v1 ke keputusan registry yang terverifikasi; perubahan tipe berikutnya harus serentak dengan adapter Go/storage. | Uji homonym, singkatan, versi, dan rantai bukti BIND ke alias; ukur false alias merge serta registrasi PostgreSQL nyata. |
| [blocking.rs](blocking.rs) | Retrieve bounded canonical candidates using deterministic legal keys and contextual signals before expensive resolution. | Measure candidate recall and reduction ratio on hard aliases; record empty lookup scopes for later invalidation. |
| [resolver.rs](resolver.rs) | Hubungkan pemilih semantik/peninjau ke builder proposal, validasi artefak kandidat saat dispatch, dan dapatkan keputusan otoritatif Go. | Uji link/review homonym, revisi stale, crash/retry dan gold false merge/split; ukur latency dan review workload. |
