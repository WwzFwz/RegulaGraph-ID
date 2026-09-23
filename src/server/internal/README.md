# src/server/internal

Komponen internal aplikasi Go, dipisah menurut fungsi domain, API, workflow, retrieval, answering, ingestion, indexing, dan adapter. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Logika di luar cakupan ini ditempatkan pada komponen pemiliknya. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../AGENTS.md).

## Peran dan integrasi anak

Go internal menjaga komponen tidak menjadi dependency langsung evaluator Python; evaluasi memakai endpoint/artefak yang mengikuti src/contracts.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Subfolder: [adapters/](adapters/README.md), [answering/](answering/README.md), [api/](api/README.md), [config/](config/README.md), [domain/](domain/README.md), [indexing/](indexing/README.md), [ingestion/](ingestion/README.md), [retrieval/](retrieval/README.md), [workflows/](workflows/README.md).

Akuisisi dan audit inventory D01 sudah aktif melalui CLI, workflow batch, serta adapter sources. Scheduler durable dan publication coordinator S01 juga aktif. Executor PARSE→STRUCTURE menyerahkan artefak ke worker Rust; executor BIND Go menjalankan exact identity/materialization; executor CHUNK menghasilkan chunk struktural terikat versi; EXTRACT memverifikasi dan meng-commit proposal graph berbukti. Graph/index backend dan query/answer produksi masih scaffold.

## Benchmark dan perhatian performa

Prioritas: p95/p99 latency query, waktu antre dan time-to-first-answer-token; untuk job ukur throughput serta peak RSS. Target numerik wajib ada di [target numerik wajib](../../../configs/benchmark-targets.yaml); profil referensi dan status REQUIRED_UNMEASURED berlaku. Pemrosesan berjalan tanpa loading model per request dan tanpa RPC per tahap kecil fusion/filter/context.

Ikuti [kebijakan benchmark](../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

EXTRACT memakai ontology JSONC bersama: gateway menolak proposal di luar vocabulary dan coordinator memverifikasi versi/hash serta typed graph sebelum commit. Nilai kualitas/performa masih belum diukur.

Collector/audit D01, kontrak/validator C01, evaluator E01, storage/publication S01, durable pipeline sampai EXTRACT, exact identity BIND K01, worker EXTRACT, dan Semantic Gateway sudah tersedia. Graph/index/retrieval, mutasi backend, serta provider/model produksi belum aktif; target kualitas dan latency belum diukur.
