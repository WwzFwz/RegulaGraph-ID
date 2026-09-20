# Verifikasi durable PARSE dispatch I01

Dokumen ini mencatat bukti milestone coordinator PARSE pada 2026-09-20. Cakupannya adalah pemilihan stage ACQUIRE/PARSE, claim PostgreSQL khusus PARSE, deadline/fence, dispatch Go–Rust, binding artifact/checkpoint, klasifikasi kegagalan, cancellation durable, dan handoff terminal. PASS ini tidak membuktikan stage STRUCTURE–INDEX, publication menyeluruh, kualitas hukum, atau target performa produksi.

## Identitas dan verdict

Agent verifikasi independen `verify_c01` mereview working tree berbasis revision `81b29d352e02a1ef6af2e67b4f60eb838c26a90a`. Fingerprint agregat 36 file yang diuji adalah `79eb2fd47ad4e889c8df232d3dec91fb4dc6987da148623fbf794746417e3b8a`. Raw log, manifest command/exit, harness, fixture hash, archived overlay, diff, dan fingerprint berada di `artifacts/verification/i01-durable-dispatch-20260920/` dan diabaikan Git.

Verdict reviewer adalah **PASS untuk scope durable PARSE dispatch** tanpa temuan material terbuka. Target `configs/benchmark-targets.yaml` tidak diubah dan tetap **REQUIRED_UNMEASURED**.

## Bukti pemeriksaan

| Pemeriksaan | Hasil | Cakupan |
| --- | --- | --- |
| Workflow adversarial independen | PASS, 8 kasus | Shutdown menjadi retry, race monitor, late cancellation, forged/mixed output, ID maksimum, deadline storage, dan klasifikasi error |
| PostgreSQL–Go–Rust aktual | PASS, 7 kasus | Text ke `STAGED`, blank ke `WAITING_REVIEW`, checkpoint/hash/provenance, claim PARSE saja, cancellation atomic, recovery `STAGED`, payload korup, dan batas 257 source |
| Lifecycle coordinator | PASS, 1 kasus | Claim, dispatch, persistence, completion, serta shutdown dependency pada composition root |
| Go | PASS, 58 top-level test dan vet | Seluruh package server termasuk adapter, workflow, command, dan domain boundary |
| Rust | PASS, 97 unit dan 1 native integration | Worker/service/parser/artifact regression; satu fixture wire tetap ignored karena memiliki harness terpisah |
| Static dan kontrak | PASS | Clippy `-D warnings`; 157 messages, 31 enums, 4 services tanpa penulisan ulang baseline |

Jalur sukses membuktikan `DocumentBatch` nyata didaftarkan sebelum checkpoint dan completion. Checkpoint menyebut artifact ID/hash yang sama, memakai producer manifest runtime, dan dapat dibaca kembali identik. Respons parsial tetap masuk `WAITING_REVIEW`. Cancellation yang tiba setelah checkpoint mengalahkan hasil secara atomik, sedangkan shutdown coordinator mengembalikan job ke `RETRY_WAIT`. Job URL tetap di `ACQUIRE`; daemon PARSE hanya mengklaim request yang seluruh source-nya sudah berupa blob.

Temuan review yang diperbaiki mencakup claim PARSE yang semula dapat mencuri stage ACQUIRE/later, shutdown yang semula menjadi cancellation terminal, race cancellation setelah monitor, output tambahan yang tidak didaftarkan, forged checkpoint yang semula di-retry, correlation ID melebihi batas wire, operasi storage tanpa deadline lease, lease `STAGED` yang sempat dilepas sehingga tidak recoverable, response invalid lintas adapter tanpa tipe permanen, payload durable korup yang dapat retry terus, serta batas batch deterministik yang semula dilaporkan sebagai overload sementara.

## Batas pembuktian

Shutdown diuji melalui context dan belum melalui injeksi signal OS. Cancellation PDFium tetap kooperatif antar-source. API untuk meminta cancellation, durable retry availability/backoff/max-attempt, lease heartbeat untuk batch panjang, TLS/auth deployment, stage STRUCTURE–INDEX, publication seluruh pipeline, load test, serta evaluasi kualitas hukum belum selesai. Windows leaf-symlink case memerlukan privilege dan fixture interop Go dijalankan melalui harness terpisah. Semua benchmark numerik tetap **REQUIRED_UNMEASURED**.
