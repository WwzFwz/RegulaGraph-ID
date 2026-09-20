# src/server/internal/ingestion/sources

Adapter pembacaan dokumen asli dari berkas lokal atau sumber resmi beserta metadata pengambilannya. Implementasi runtime berada di Go. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Parsing isi, OCR, serta penentuan tanggal berlaku dikerjakan komponen lain. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../../AGENTS.md).

## Peran dan integrasi anak

Setiap anak mengembalikan konten sumber dan identitas pengambilannya, menangani duplikasi, timeout, dan kegagalan secara eksplisit. Waktu unduh tidak boleh dipakai sebagai tanggal berlaku.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Berkas: [local.go](local.go), [official.go](official.go), [inventory.go](inventory.go), [http.go](http.go), [html.go](html.go), [download.go](download.go), [handoff.go](handoff.go), [handoff_test.go](handoff_test.go), [collector_test.go](collector_test.go), [listing.go](listing.go), [audit.go](audit.go), dan [audit_test.go](audit_test.go).

Collector D01 aktif untuk metadata detail dan PDF sumber: official mengatur satu sumber, http mengatur akses/retry/rate serta discovery terbatas, html membaca metadata/tautan, download menyimpan blob/checksum/receipt, dan inventory mendefinisikan format artefak lokal. `handoff.go` mengubah record complete menjadi locator blob dan `SourceObservation` C01 deterministik yang mengikat metadata portal ke hash PDF tanpa menetapkannya sebagai kebenaran canonical. Workflow Go memanggil collector ini dengan concurrency terbatas. Adapter local.go tetap belum diimplementasikan. Lihat [panduan penggunaan](../../../../../doc/acquisition.md).

listing.go mengambil HTML katalog saja, menyimpan hash/HTML sumber serta judul tautan untuk workflow discover. Tidak mengikuti tautan PDF. Parser mendukung link detail /doc/ JDIHN; ini belum berarti adapter unduhan/metadata JDIHN lengkap.

audit.go mengurung akses melalui `os.Root`, memverifikasi latest record terhadap observation byte-identik, seluruh historical receipt, HTML provenance, ukuran/hash/envelope PDF, queue coverage, dan reference gaps. Output JSONL diurutkan; inventory ID mengikat record, observation, queue, dan blob set. Candidate identity hanya label `unverified`, bukan canonical merge.

## Benchmark dan perhatian performa

**SOURCE.** Ukur keberhasilan akuisisi, bytes/detik, retry, dan waktu p50/p95 per sumber. Gate: konten memiliki hash dan asal yang terlacak; unduhan parsial tidak diterbitkan sebagai sumber lengkap.

Ikuti [kebijakan benchmark](../../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Collector, discovery, audit inventory D01, serta handoff provenance collector→PARSE sudah aktif; audit corpus lokal 3 GB lulus integrity. Connector JDIHN, queue/reference coverage, PDF format strata, dan gold set belum lengkap. Metadata portal tetap assertion unverified sampai registry/review, dan audit hash tidak membuktikan kualitas hukum atau target performa.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [budget.go](budget.go) | Keep total unique-byte accounting atomic at promotion; add cross-process ownership only when a multi-process collector is introduced. | Exercise restart, duplicate hashes and concurrent near-cap writes; distinguish final PDF bytes from temporary/network traffic. |
| [download.go](download.go) | Preserve streamed hash/size/PDF envelope validation and durable receipts; integrate stronger PDF diagnostics through parser output. | Test short body, wrong media, corrupt existing blobs and disk errors; bound temporary bytes and report download throughput. |
| [html.go](html.go) | Maintain deterministic source HTML parsing with canonical URL resolution and explicit unsupported-layout failures. | Test malformed markup, relative URLs, duplicate links and script-driven pages; do not infer PDFs from absent links. |
| [http.go](http.go) | Keep per-host throttling, connection reuse and redirect policy; propagate cancellation and retry budgets. | Test Retry-After, cross-host redirect rejection and transient failures; measure waiting separately from transferred bytes. |
| [inventory.go](inventory.go) dan [handoff.go](handoff.go) | Pertahankan history/checksum serta pemetaan deterministic ke source records; hubungkan handoff ke submit job S01 tanpa menghapus observation lama. | Uji perubahan metadata, blob duplikat, portal/blob forgery, corrupt artifact, dan replay; metadata portal tidak boleh otomatis menjadi canonical truth. |
| [audit.go](audit.go) | Pertahankan manifest deterministik dan confinement; integrasikan inventory yang sudah dibekukan ke pemilihan sample M01/G01. | Uji concurrent collector exclusion/crash, cold-cache throughput/peak RSS, dan perubahan provenance selalu mengubah inventory ID. |
| [listing.go](listing.go) | Extend bounded listing discovery with source-specific pagination and persistent page provenance. | Test next-page cycles, duplicate URLs and incremental resume; distinguish URL count from unique regulations/PDFs. |
| [local.go](local.go) | Import explicitly selected local files through the same hash/receipt contracts as official acquisition. | Test invalid paths, corrupt/duplicate PDFs and interruption; avoid interpreting file timestamps as legal effective dates. |
| [official.go](official.go) | Extend metadata/PDF extraction using captured layouts and preserve observation history; treat dates/status as unverified source assertions. | Test changed layouts, attachment vs regulation links and missing metadata; report per-source extraction coverage. |

## Penambahan C01 dan panduan verifikasi

Berkas terkait: [budget.go](budget.go), [budget_test.go](budget_test.go). Dependency, cara menjalankan dan batas pembuktiannya mengikuti [implementasi C01](../../../../../doc/contracts-implementation.md).
