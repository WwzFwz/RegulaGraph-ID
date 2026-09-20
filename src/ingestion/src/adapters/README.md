# src/ingestion/src/adapters

Adapter Rust untuk pembacaan sumber dan panggilan engine native atau inference. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Logika di luar cakupan ini ditempatkan pada komponen pemiliknya. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../AGENTS.md).

## Peran dan integrasi anak

Boundary native menjelaskan kepemilikan buffer dan lifetime; boundary inference memakai batch serta model/snapshot identity. Adapter storage menyediakan penyimpanan lokal immutable dan content-addressed bagi artefak worker, sedangkan commit dan publication tetap dimiliki coordinator Go.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Berkas: [document_batches.rs](document_batches.rs), [inference.rs](inference.rs), [mod.rs](mod.rs), [pdf_engine.rs](pdf_engine.rs), [storage.rs](storage.rs), dan [text_artifacts.rs](text_artifacts.rs).

## Benchmark dan perhatian performa

Prioritas: p95/p99 latency query, waktu antre dan time-to-first-answer-token; untuk job ukur throughput serta peak RSS. Target numerik wajib ada di [target numerik wajib](../../../../configs/benchmark-targets.yaml); profil referensi dan status REQUIRED_UNMEASURED berlaku. Pemrosesan berjalan tanpa loading model per request dan tanpa RPC per tahap kecil fusion/filter/context.

Ikuti [kebijakan benchmark](../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Status lintas repositori: collector/audit D01, kontrak/validator C01, evaluator E01, serta fondasi storage/publication S01 sudah tersedia. `storage.rs` menulis dan membaca object lokal immutable dengan key SHA-256; `text_artifacts.rs` menyimpan raw/normalized/mapping; `document_batches.rs` menyimpan dan memuat batch protobuf terverifikasi. Object storage jarak jauh, lifecycle orphan, worker RPC, pipeline graph/retrieval, layanan model, gold dataset, dan acceptance produksi belum aktif. Status anak dijelaskan pada header masing-masing; audit integrity, build, dan fixture tidak membuktikan durability atau target latency produksi.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [document_batches.rs](document_batches.rs) | Integrasikan dengan response worker dan pembacaan coordinator Go tanpa mengirim blob besar melalui RPC. | Uji retry/fence/cancellation, corrupted remote object, cross-language decode, payload besar, serta p95/p99 dan peak RSS. |
| [inference.rs](inference.rs) | Send typed semantic/embedding batches to pinned services; retain item IDs, producer manifests and bounded retry. | Test missing/duplicate/reordered items and partial errors; verify no model initialization per chunk. |
| [pdf_engine.rs](pdf_engine.rs) | Bind the parser selected by M01 with explicit buffer ownership, safe page lifetimes and bounded worker concurrency. | Test malformed/encrypted/large PDFs and native error propagation; measure pages/s and RSS without copying entire corpus. |
| [storage.rs](storage.rs) | Tambahkan backend object storage dengan semantik descriptor yang sama, lifecycle temporary-object, dan integrasi descriptor ke `ArtifactRef`; pertahankan publication sebagai tanggung jawab Go. | Jalankan fault injection untuk crash sebelum/sesudah rename, filesystem penuh, permission error, retry cleanup, dan durability; ukur throughput, p95/p99, fsync cost, serta peak RSS pada workload resmi. |
| [text_artifacts.rs](text_artifacts.rs) | Integrasikan lifecycle orphan dan input worker; pertahankan hash binding raw/normalized/mapping serta status page failure. | Uji crash antar-object, retry konkuren, cleanup aman, corpus PDF nyata, throughput, p95/p99, dan peak RSS. |
