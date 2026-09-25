# src/server/internal/adapters/inference

Client Go ke runtime embedding, reranker, dan generation lokal atau provider eksternal. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Logika di luar cakupan ini ditempatkan pada komponen pemiliknya. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../../AGENTS.md).

## Peran dan integrasi anak

Tidak menjalankan tensor atau memuat model di dalam request Go. Kontrak model ID, tokenizer, panjang input, deadline, dan token usage mengikuti src/contracts.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

`SemanticService.ProducerManifest()` mengembalikan salinan manifest konfigurasi tanpa provider call. Gateway memakainya untuk ekspor pin operator; perubahan pada hasil ekspor tidak mengubah konfigurasi layanan yang sedang berjalan. Tes memeriksa isolasi salinan tersebut.

Berkas: [cross_encoder.go](cross_encoder.go), [embeddings.go](embeddings.go), [llm.go](llm.go), [semantic.go](semantic.go). Test boundary berada pada file `_test.go` pendamping.

`llm.go` menyediakan adapter HTTP structured output OpenAI-compatible tanpa retry implisit. `semantic.go` mengimplementasikan `Semantic.ExtractBatch`: validasi request/model/schema/ontology, concurrency terbatas, cache operation-key dalam proses, proyeksi ID deterministik, exact UTF-8 span, provenance, support closure, manifest termasuk hash ontology, accounting token, dan error eksplisit per item. Replay durable lintas restart tetap milik coordinator.

## Benchmark dan perhatian performa

Prioritas: p95/p99 latency query, waktu antre dan time-to-first-answer-token; untuk job ukur throughput serta peak RSS. Target numerik wajib ada di [target numerik wajib](../../../../../configs/benchmark-targets.yaml); profil referensi dan status REQUIRED_UNMEASURED berlaku. Pemrosesan berjalan tanpa loading model per request dan tanpa RPC per tahap kecil fusion/filter/context.

Ikuti [kebijakan benchmark](../../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Semantic.ExtractBatch, gate vocabulary ontology, dan adapter provider sudah aktif secara fungsional serta diuji dengan provider deterministic. Embedding/reranking C++, provider/model produksi, benchmark kualitas/latency/biaya, SummarizeBatch dan acceptance produksi belum aktif; ResolveBatch tersedia sebagai proposal kontekstual.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [cross_encoder.go](cross_encoder.go) | Batch query-document pairs with stable IDs and verify model/results correlation, truncation and cancellation. | Test partial scores, timeout, unexpected pairs and length limits; keep original evidence identity through ranking. |
| [embeddings.go](embeddings.go) | Reuse a native client; send purpose/model-bound batches and validate one-to-one results before retrieval/indexing. | Test reordered/duplicate/missing outputs, dimension drift and explicit per-item errors; trace queue vs compute time. |
| [llm.go](llm.go) | Tambahkan provider-specific capability probe dan telemetry biaya sesudah provider produksi dipilih; pertahankan satu request per item serta tanpa retry tersembunyi. | Uji status provider, timeout, refusal, respons oversized/malformed, dan accounting pada sandbox provider. |
| [semantic.go](semantic.go) | Terapkan ontology endpoint/predicate/qualifier allowlist dan cache durable; perluas RPC RESOLVE/SUMMARIZE pada milestone masing-masing. | Uji unknown ontology terms, replay lintas restart, collision operation-key, span multibyte, cancellation, serta evaluasi extraction pada gold split. |

`semantic_resolution.go`, `semantic_resolution_cache.go`, dan `semantic_resolution_client.go` mengimplementasikan gateway/context projection, bounded replay cache, dan client gRPC RESOLVE. Satu gateway memakai task EXTRACT atau RESOLVE terpin; producer/model/schema/context/candidate identity divalidasi. LINK harus merujuk konteks yang mencakup mention dan support kandidat terpilih; metadata label saja tidak cukup. Tes membuktikan projection, malformed input, provenance, cancellation, eviction/coalescing, dan RPC; kualitas model tetap belum diukur. `semantic_provider_integration_test.go` menyediakan smoke endpoint nyata opt-in; konfigurasi serta batas pembuktiannya berada di [panduan resolusi](../../../../../doc/semantic-resolution.md).
