# src/server/internal

Komponen internal aplikasi Go, dipisah menurut fungsi domain, API, workflow, retrieval, answering, ingestion, indexing, dan adapter. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Logika di luar cakupan ini ditempatkan pada komponen pemiliknya. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../AGENTS.md).

## Peran dan integrasi anak

Go internal menjaga komponen tidak menjadi dependency langsung evaluator Python; evaluasi memakai endpoint/artefak yang mengikuti src/contracts.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Subfolder: [adapters/](adapters/README.md), [answering/](answering/README.md), [api/](api/README.md), [config/](config/README.md), [domain/](domain/README.md), [indexing/](indexing/README.md), [ingestion/](ingestion/README.md), [retrieval/](retrieval/README.md), [workflows/](workflows/README.md).

Akuisisi dan audit inventory D01 sudah aktif melalui CLI, workflow batch, serta adapter sources. Scheduler durable dan publication coordinator S01 juga aktif. Executor PARSE→STRUCTURE menyerahkan artefak ke worker Rust; executor BIND Go menjalankan exact identity/materialization; executor CHUNK menghasilkan chunk struktural terikat versi; EXTRACT memverifikasi dan meng-commit proposal graph berbukti. Adapter INDEX/Qdrant, query evidence dan draft answering tersedia sebagai library; daemon INDEX opt-in berakhir pada STAGED. Integrasi graph dan alur corpus-to-answer penuh masih terbuka.

## Benchmark dan perhatian performa

Prioritas: p95/p99 latency query, waktu antre dan time-to-first-answer-token; untuk job ukur throughput serta peak RSS. Target numerik wajib ada di [target numerik wajib](../../../configs/benchmark-targets.yaml); profil referensi dan status REQUIRED_UNMEASURED berlaku. Pemrosesan berjalan tanpa loading model per request dan tanpa RPC per tahap kecil fusion/filter/context.

Ikuti [kebijakan benchmark](../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Loader policy kandidat, scheduler submit yang menuntut hash policy corpus, dan handoff RESOLVE berkonfigurasi tepercaya kini tersedia. CLI menguji submit/replay pada PostgreSQL; dispatch RESOLVE opt-in sampai proposal WAITING_REVIEW tersedia; review/resume dan pilihan nilai scope produksi masih terbuka.

EXTRACT memakai ontology JSONC bersama: gateway menolak proposal di luar vocabulary dan coordinator memverifikasi versi/hash serta typed graph sebelum commit. Nilai kualitas/performa masih belum diukur.

Collector/audit D01, kontrak/validator C01, evaluator E01, storage/publication S01, durable pipeline sampai EXTRACT, exact identity BIND K01, worker EXTRACT, dan Semantic Gateway sudah tersedia. INDEX memiliki inventory/claim/processor dan checkpoint STAGED; Qdrant writer, retrieval lexical/dense, hidrasi dan reranking tersedia sebagai komponen callable. Graph lengkap, pengumpulan/publication corpus otomatis serta answering penuh tetap terbuka; target kualitas dan latency belum diukur.

Adapter inference menyediakan RESOLVE kontekstual; workflow menghydrate input terverifikasi dan menyimpan proposal untuk replay. Dispatch proposal daemon tersedia secara opt-in; review/resume terautentikasi serta acceptance model lokal belum selesai.

Adapter native embedding/reranking kini memiliki implementasi gRPC dan tes C++ nyata; provenance/ranking workflow tetap berada pada domain/retrieval pemiliknya.
Berkas `preview.go` pada storage, retrieval, answering, workflows, dan API
mengimplementasikan baseline demo lokal yang terpisah dari jalur C01 terpublikasi.
Nilai internal/UI berada pada domain/local_preview.go, tanpa kontrak worker baru.
Scope dan batas dijelaskan pada [panduan demo](../../../doc/interview-demo.md).

Jalur INDEX kini menghubungkan source envelope ber-receipt, job durable/recovery, pengumpulan output lengkap dan publication dense/BM25. Workflow/storage tetap pemilik authority; Rust/C++ menghasilkan representasi, dan source/citation ditelusuri kembali ke artefak asli. [Kontrak lanjutan](../../../doc/index-source-publication.md) membatasi status ini dari graph/answering serta acceptance menyeluruh.
