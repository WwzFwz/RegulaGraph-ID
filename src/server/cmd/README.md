# src/server/cmd

Entry point executable Go yang menyusun konfigurasi dan dependency aplikasi. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Logika di luar cakupan ini ditempatkan pada komponen pemiliknya. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../AGENTS.md).

## Peran dan integrasi anak

CLI collect memanggil internal/workflows untuk acquisition PDF/metadata; CLI submit memanggil scheduler durable dengan ontology dan candidate policy terpin. `ingestion-worker` menyusun coordinator durable; `semantic-gateway` menyusun boundary model EXTRACT yang dibatasi loopback. API tetap entry point scaffold yang keluar dengan kode 2. Entry point tidak menggandakan algoritma domain, parser, atau proyeksi output model.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Subfolder: [api/](api/README.md), [cli/](cli/README.md), [ingestion-worker/](ingestion-worker/README.md), [semantic-gateway/](semantic-gateway/README.md).

Akuisisi sumber D01 sudah aktif melalui CLI collect, workflow batch, dan adapter sources. CLI submit membuat job dengan manifest policy terverifikasi untuk source blob yang telah terdaftar. Daemon ingestion-worker menjalankan PARSE -> STRUCTURE -> BIND -> CHUNK -> EXTRACT dan worker Rust memanggil Semantic Gateway untuk EXTRACT. Gateway memverifikasi model/prompt/schema/ontology yang dipin, membatasi concurrency/byte, serta memproyeksikan output menjadi kontrak C01. Coordinator membaca ulang source/evidence, memvalidasi closure, dan meng-commit output EXTRACT secara durable; stage setelahnya belum aktif.

## Benchmark dan perhatian performa

Prioritas: p95/p99 latency query, waktu antre dan time-to-first-answer-token; untuk job ukur throughput serta peak RSS. Target numerik wajib ada di [target numerik wajib](../../../configs/benchmark-targets.yaml); profil referensi dan status REQUIRED_UNMEASURED berlaku. Pemrosesan berjalan tanpa loading model per request dan tanpa RPC per tahap kecil fusion/filter/context.

Ikuti [kebijakan benchmark](../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Status lintas repositori: collector/audit D01, kontrak/validator C01, evaluator E01, fondasi storage/publication S01, coordinator hingga EXTRACT, worker EXTRACT, serta Semantic.ExtractBatch Gateway sudah tersedia. Provider/model produksi belum dipilih; hasil kualitas, latency, biaya, stage RESOLVE-INDEX, query, gold dataset, dan acceptance produksi belum aktif. Test deterministic tidak membuktikan target model.

`semantic-gateway` dapat dijalankan dalam mode EXTRACT atau RESOLVE pada proses terpisah. RESOLVE menyediakan RPC proposal kontekstual; daemon ingestion-worker masih memerlukan wiring dispatch.
