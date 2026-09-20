# src/ingestion/src/knowledge_graph/resolution

Penyelesaian identitas entitas dan pengelolaan canonical ID serta alias. Folder ini menentukan penyebutan mana yang menunjuk objek yang sama. Transformasi batch berjalan dalam Rust; inference semantik menggunakan adapter model. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Kemiripan makna kata bukan bukti identitas; query linking tidak membuat atau menggabungkan canonical baru. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../../AGENTS.md).

## Peran dan integrasi anak

Anak menerima mention beserta tipe dan konteks sumber. Penggabungan harus terlacak dan dapat dikoreksi; identitas pasal mencakup peraturan induknya, dan versi teks tidak disatukan secara destruktif.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Berkas: [aliases.rs](aliases.rs), [blocking.rs](blocking.rs), [mod.rs](mod.rs), [resolver.rs](resolver.rs).

## Benchmark dan perhatian performa

**RESOLUTION.** Ukur pairwise precision/recall/F1, false merge, false split, mention yang hilang, waktu per batch, dan biaya. Gate: semua mention tetap terlacak; pasal dari peraturan berbeda tidak digabung hanya karena nama sama. Blocking harus diukur juga terhadap pasangan benar yang terlewat.

Ikuti [kebijakan benchmark](../../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Status lintas repositori: collector/audit D01, kontrak/validator C01, evaluator E01, serta fondasi storage/publication S01 sudah tersedia. Pipeline parsing/graph/retrieval, mutasi backend, layanan model, gold dataset, dan acceptance produksi belum aktif. Status anak dijelaskan pada header masing-masing; audit integrity, build, dan fixture tidak membuktikan target kualitas atau latency.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [aliases.rs](aliases.rs) | Generate scoped alias proposals from mentions without equating synonyms to identity automatically. | Test homonyms, acronyms and same article numbers across laws; preserve provenance and measure false alias merges. |
| [blocking.rs](blocking.rs) | Retrieve bounded canonical candidates using deterministic legal keys and contextual signals before expensive resolution. | Measure candidate recall and reduction ratio on hard aliases; record empty lookup scopes for later invalidation. |
| [resolver.rs](resolver.rs) | Resolve or explicitly abstain, emitting revision-aware proposals for authoritative Go registry decisions. | Test ambiguous merges, splits and concurrent registry revisions; measure precision/recall and review workload. |
