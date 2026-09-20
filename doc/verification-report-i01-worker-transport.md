# Verifikasi worker PARSE Go–Rust I01

Dokumen ini mencatat bukti milestone transport worker PARSE pada 2026-09-20. Cakupannya adalah client Go, service Tonic Rust, pemrosesan PDF sampai artefak `DocumentBatch`, fencing/idempotency, deadline/cancellation, integritas input, provenance, dan batas resource. PASS di sini tidak membuktikan scheduler/publication end-to-end, kualitas hukum, atau target performa produksi.

## Identitas dan verdict

Agent verifikasi independen `verify_c01` mereview working tree berbasis revision `99327f2d50087eac6a0bdefbfb4e6c12700b021a`. Fingerprint agregat source yang diuji adalah `60ba7e5d82b20d3656da9a79a3f64f98ccf2c3e0d86d39f3b7370faab02051f8`. Raw command log, fingerprint per file, dan harness reviewer berada di `artifacts/verification/i01-worker-transport-20260920/` dan diabaikan Git.

Verdict reviewer adalah **PASS untuk milestone worker PARSE transport** tanpa temuan material terbuka. Target `configs/benchmark-targets.yaml` tidak diubah dan tetap **REQUIRED_UNMEASURED**.

## Bukti pemeriksaan

| Pemeriksaan | Hasil | Cakupan |
| --- | --- | --- |
| Service adversarial independen | PASS, 11/11 | Payload retry berbeda, terminal failure cache, fence mundur, binding corpus/scope, caller drop, antre-cancel, timeout transport/body, dan validasi response yang melewati deadline |
| Regresi artifact/domain | PASS, 39/39 | Storage/text artifact, overlap artifact-dependency bertipe aman, incremental planner, dan versioning |
| Go–Rust HTTP/2 aktual | PASS, 13 kasus independen + 6 tes resmi | PDF teks/blank, cache, status, mirror, hash/size/MIME/path/junction, malformed/encrypted, batas source, dan batas pesan 16 MiB |
| Rust workspace | PASS, 96 unit + 1 native integration | Satu fixture interop kontrak tetap ignored karena dijalankan lewat harness terpisah |
| Static checks | PASS | Clippy `-D warnings`, Go tests, dan Go vet |
| Kontrak | PASS | 157 messages, 31 enums, 4 services; baseline tidak ditulis ulang |
| Listener | PASS | Bind non-loopback ditolak selama TLS termination belum tersedia |

Positive path membuktikan PDF berteks menghasilkan completion `SUCCEEDED` dan completeness `COMPLETE`. PDF kosong menghasilkan artefak eksplisit dengan completion `FAILED` dan completeness `NONE`. Mirror dengan hash isi sama tetap mempertahankan dua dependency yang sudah diverifikasi, sementara parsing dilakukan sekali. Producer manifest memuat identitas runtime serta hash library PDFium aktual.

Temuan review yang diperbaiki mencakup cache yang sebelumnya hanya terikat tuple job, status lintas corpus, fence yang dapat mundur pada attempt baru, record yang menggantung setelah caller drop atau response invalid, permit concurrency yang lepas terlalu awal, cancellation antre yang tidak segera terminal, deadline yang dapat terlewati saat validasi response besar, descriptor duplikat yang tidak diverifikasi, MIME non-PDF, batas agregat yang terlambat, provenance caller yang tidak tepercaya, completion parsial yang salah menjadi sukses, serta konflik ID artifact dengan dependency manifest.

## Batas pembuktian

Registry worker bersifat ephemeral; Go tetap menjadi sumber job/fence/recovery durable. Cancellation kooperatif diperiksa antar-source dan belum dapat menghentikan satu panggilan PDFium secara aman di tengah file. Artifact root masih diasumsikan dikelola operator. TLS/authentication deployment, scheduler dan publication end-to-end, OCR, stage STRUCTURE–INDEX, crash isolation multi-process, benchmark latency/throughput/RSS, serta akurasi hukum belum diverifikasi.

