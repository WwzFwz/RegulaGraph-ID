# Verifikasi handoff durable CHUNK I01

Dokumen ini mencatat bukti penyelesaian milestone handoff BIND→CHUNK pada 2026-09-21. Perannya mengikat implementasi chunking registry-bound, coordinator durable, semantic artifact boundary, dan audit independen. Verdict **PASS** hanya berlaku untuk correctness scope tersebut. Parity tokenizer model produksi, kualitas retrieval, latency, throughput, serta RSS tetap **REQUIRED_UNMEASURED**.

## Cakupan yang diverifikasi

Worker Rust menerima tepat satu `DocumentBatch` BIND lengkap, memuat ulang text artifact content-addressed, merekonstruksi struktur, dan mencocokkan setiap node dengan `ProvisionVersion` melalui span eksak serta structural path kanonis. Hierarchy provision harus mengikuti hierarchy structure dan seluruh node pada satu text artifact harus terikat pada regulasi yang sama. Builder menghasilkan chunk source-mapped dengan parent context, batas byte/token, serta ID yang tidak berubah karena sisa execution budget batch.

Tokenizer Hugging Face dimuat sekali dari `tokenizer.json` yang dibatasi 128 MiB dan diverifikasi terhadap SHA-256 deployment. Truncation serta padding dari file dimatikan agar penghitung melihat seluruh input. Hash tokenizer dan konfigurasi chunker masuk manifest/fingerprint.

Coordinator Go merotasi claim PARSE/STRUCTURE/CHUNK, hanya mengirim CHUNK dari checkpoint BIND sukses, membaca ulang output terverifikasi, dan memeriksa corpus, producer manifest, stage shape, completeness, typed reference closure, structure cycle, containment span, serta bukti token sebelum urutan register artifact→dependency manifest→checkpoint. Error storage sementara masuk `RETRY_WAIT`; pelanggaran immutable/semantic menjadi terminal.

## Hasil pemeriksaan

| Pemeriksaan | Hasil | Bukti dan batas |
| --- | --- | --- |
| Rust adversarial binding/token/budget | PASS, 8/8 | Missing/foreign/ambiguous hierarchy, record-order invariance, pin/truncation tokenizer, dan execution budget |
| Go semantic boundary overlay | PASS, 6 top-level/9 leaf | Termasuk dangling ref, span di luar version/structure, transient read, serta empat token-evidence negatives |
| Native lintas bahasa | PASS, 1/1 | PARSE→STRUCTURE Rust, BIND domain Go, lalu CHUNK Rust: 3 structure, 3 version, 2 chunk |
| Rust official suite | PASS | 102 unit aktif dan 1 native PDF boundary; 1 fixture wire interop tetap ignored sesuai desain harness terpisah |
| Rust Clippy | PASS | Seluruh target dengan `-D warnings` |
| Go full suite | PASS | 122 test event lulus, 5 skip environment, lalu `go vet ./...` lulus |
| Format dan diff | PASS | `cargo fmt --check`, `gofmt -l`, dan `git diff --check` tidak menemukan error |
| Benchmark produksi | NOT_MEASURED | Target numerik, workload, dan denominator tidak diubah |

Fingerprint audit untuk 17 file kritis adalah `14ca531daa2423999fc5b7589cef458c41bb9d2a9dd370faa3c5d4f69bc9a0e3`. Log, snapshot, dan counterexample lokal berada di `artifacts/verification/i01-durable-chunk-20260921/` dan diabaikan Git sesuai kebijakan artifact.

## Temuan yang ditutup

- Output read sementara tidak lagi dibungkus sebagai `FailedPrecondition`; retry durable dipertahankan.
- Truncation/padding tokenizer tidak dapat menyamarkan input yang melampaui token cap, dan file harus cocok dengan hash deployment.
- Pembacaan tokenizer tetap bounded bila file tumbuh setelah metadata diperiksa.
- Structure depth dibatasi sebelum recursion berkembang, sedangkan execution chunk budget menghentikan alokasi tanpa mengubah stable ID.
- Binding lookup memakai indeks span+canonical path sehingga kasus exact-span berulang tidak menjadi pencarian kuadratik.
- Hierarchy provision, regulation per dokumen, dan urutan record diverifikasi secara semantic.
- Coordinator menolak closure dangling, span chunk di luar version/structure, serta token count yang hilang, nol, ganda, atau berbeda tokenizer.
- Parent traversal dan parent-child lookup Go tidak memakai pola kuadratik; interval span diindeks sebelum containment lookup.

## Keterbatasan dan pekerjaan berikut

Audit native memakai canonical assignment fixture dari domain Go, bukan allocator PostgreSQL nyata; suite database tidak diulang pada milestone ini. Model tokenizer BGE-M3 produksi belum dibekukan dan parity dengan embedding/reranker belum diukur. Latency p50/p95/p99, throughput, peak RSS, kualitas structure/chunk pada gold corpus, OCR, tabel, exception linking, dan retrieval impact tetap harus dijalankan dengan profile dalam `configs/benchmark-targets.yaml`.

Milestone berikut meneruskan artifact CHUNK ke extraction change-event/EXTRACT. Handoff baru harus mempertahankan source/canonical/provision-version/snapshot identity, dependency manifest, serta pola verifikasi artifact sebelum publikasi.
