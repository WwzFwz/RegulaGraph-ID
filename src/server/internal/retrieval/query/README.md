# src/server/internal/retrieval/query

`entity_linker.go` dan `entity_linker_validation.go` kini menyediakan exact-alias
phrase discovery, snapshot-pinned lookup dan validasi key/revision/alias closure.
Semua alternatif ambigu dipertahankan. Fuzzy/model disambiguation dan quality
acceptance tetap terbuka; lihat [kontrak](../../../../../doc/query-entity-linking.md).

Persiapan pertanyaan melalui normalisasi, klasifikasi, dan pengaitan penyebutan ke entitas graph yang sudah ada. Implementasi runtime berada di Go. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Tidak mengubah canonical registry atau menebak fakta yang tidak ada dalam pertanyaan. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../../AGENTS.md).

## Peran dan integrasi anak

Loader generation BM25 memeriksa `LexicalStatisticsArtifact.input_policy` terhadap
policy generation. Statistik token-only lama tetap dapat diperiksa offline, tetapi
tidak memenuhi admission serving sampai policy populasi dibuktikan dan dipin.

Anak mempertahankan pertanyaan asli, nomor, tahun, negasi, dan filter waktu. Ketidakpastian linking harus diteruskan; kegagalan satu jalur tidak otomatis menutup jalur retrieval lain.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

[artifacts.go](artifacts.go) memuat encoder dari bytes analyzer/dictionary/statistik
yang hash, tipe, corpus, ID dan ancestry lokalnya cocok dengan IndexGeneration.
Pemuatan dilakukan sekali; Encode tidak membaca storage. Constructor matematika
tetap tersedia untuk proyeksi/tes, tetapi tidak dapat langsung masuk retriever.
Publication, membership snapshot dan authority registry tetap milik admission.

Berkas: [classifier.go](classifier.go), [entity_linker.go](entity_linker.go), [lexical.go](lexical.go), [normalizer.go](normalizer.go), [sparse.go](sparse.go), dan tesnya.

`lexical.go` menyediakan analyzer query BM25 v1 dengan aturan yang sama seperti Rust
indexing. `sparse.go` membangun encoder query dari proyeksi dictionary/statistik BM25
yang telah diverifikasi, memeriksa digest/lineage lokal saat load, lalu menghasilkan
vektor query tanpa mutasi vocabulary. Pembuktian hash artefak dan ancestry mapping
lokal tersedia pada loader; authority registry PostgreSQL dan admission snapshot
tetap perlu integrasi. Backend sparse tersedia pada retriever induk.
Fingerprint dictionary kini memakai encoder bersama di `internal/domain`, bukan
salinan formula di query. Pemuatan dictionary tetap menyalin mapping agar aman
untuk pembacaan konkuren; ukur peak RSS pada vocabulary corpus acuan.
Fixture [lexical-analyzer-v1.json](../../../../../tests/fixtures/lexical-analyzer-v1.json)
memeriksa nomor hukum, negasi, Unicode NFC, dan kesesuaian keluaran kedua bahasa.
Kategori huruf dan properti stream-safe NFC Rust memakai tabel Unicode 15 yang
dihasilkan dari `unicode.IsLetter` dan x/text NFC Go oleh
[generator](../../../../../scripts/generate_lexical_letters.go). Query dibatasi
1.024 term; Jamo terurai dan Hangul tersusun mengikuti aturan normalisasi yang
sama. Perubahan versi tabel/normalisasi harus membentuk analyzer generation baru.

## Benchmark dan perhatian performa

**RETRIEVAL.** Ukur Recall@k, nDCG@k, kelengkapan semua bukti multi-hop, serta latency p50/p95 per jenis pertanyaan. Catat jumlah kandidat, hop, snapshot, dan kondisi cold/warm. Ambang wajib mengikuti [target numerik wajib](../../../../../configs/benchmark-targets.yaml); jangan menganggap batas hop/kandidat atau graph selalu meningkatkan kualitas.

**RESOLUTION.** Ukur pairwise precision/recall/F1, false merge, false split, mention yang hilang, waktu per batch, dan biaya. Gate: semua mention tetap terlacak; pasal dari peraturan berbeda tidak digabung hanya karena nama sama. Blocking harus diukur juga terhadap pasangan benar yang terlewat.

Ikuti [kebijakan benchmark](../../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Analyzer lexical query dan encoder sparse frozen BM25 aktif sebagai library dengan
batas input/term/term-count. Loader artefak verified, pembuktian dictionary ancestor
tepercaya, query planner menyeluruh, gold dataset, serta acceptance belum lengkap.
Exact-alias linking terpin aktif sebagai library; fixture parity tidak membuktikan
kualitas pencarian atau kelengkapan linking.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [classifier.go](classifier.go) | Resolve query intent/temporal mode into an auditable RetrievalPlan with an uncertainty path. | Evaluate per-intent confusion and routing cost; ambiguity must not silently pick a legal date or skip relevant retrieval branches. |
| [entity_linker.go](entity_linker.go) | Integrasikan policy serving dan ukur exact-alias coverage sebelum fuzzy/model disambiguation. | Homonym, scopes, offset, corrupt reads dan budget diuji; candidate recall/false exclusions memerlukan gold. |
| [normalizer.go](normalizer.go) | Preserve original question and normalize mechanical variants while keeping negation, article numbers, years and quoted terms. | Test informal/typo/Indonesian-English cases and destructive normalization counterexamples; record original-to-normalized trace. |
