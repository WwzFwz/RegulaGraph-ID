# Verifikasi durable PARSE retry schedule I01

Dokumen ini mencatat bukti milestone retry coordinator PARSE pada 2026-09-20. Cakupannya adalah migration eligibility/max-attempt, isolasi claim stage, exponential backoff, cancellation precedence, cleanup tanpa lock convoy, dan recovery lease. PASS ini tidak membuktikan contention atau latency produksi.

## Identitas dan verdict

Agent verifikasi independen `verify_c01` mereview delta berbasis revision `100e9b294615f8b817ad94fef9137999fb34007e`. Fingerprint agregat 12 file adalah `ddde3d6a2eb31448b52297a3f75a009756d61db805414d2a94ba77f6efd5e153`. Raw log, manifest, harness, dan fingerprint berada di `artifacts/verification/i01-retry-schedule-20260920/` dan diabaikan Git.

Verdict reviewer adalah **PASS untuk durable PARSE retry schedule** tanpa temuan correctness terbuka. Target benchmark tidak diubah dan tetap **REQUIRED_UNMEASURED**.

## Bukti pemeriksaan

| Pemeriksaan | Hasil | Cakupan |
| --- | --- | --- |
| PostgreSQL adversarial | PASS, 10/10 | Belum due, sudah due, attempt terakhir, expired exhausted, cancellation precedence, lease/recovery `STAGED`, isolasi claim generik, durasi 1 ns, lock contention, checksum migration |
| Arithmetic/config | PASS, 2/2 | Pertumbuhan eksponensial, cap, dan attempt besar tanpa overflow |
| Migration | PASS | Database kosong dan replay checksum revision 0001–0002 |
| Go | PASS, 58 top-level test dan vet | Workflow, adapter PostgreSQL, command config, serta regression server |

Retry transient disimpan sebagai `next_attempt_at`; claim melewati row yang belum due. Delay bertumbuh eksponensial dari konfigurasi coordinator dan berhenti pada cap. Attempt kedelapan menjadi `FAILED` secara atomik kecuali cancellation sudah diminta, yang menghasilkan `CANCELLED`. Cleanup exhausted memakai batch maksimal 64 row dengan `FOR UPDATE SKIP LOCKED`, sehingga row yang sedang terkunci tidak menahan claim lain. Claim generik tidak mengambil PARSE pada state `QUEUED`, `RETRY_WAIT`, atau `RUNNING`, tetapi tetap dapat meneruskan handoff `STAGED` ke coordinator berikutnya.

Temuan review yang diperbaiki mencakup bypass jadwal melalui claim generik, cleanup yang menimpa cancellation, interval Go nanosecond yang tidak dapat diparse PostgreSQL, dan cleanup tanpa `SKIP LOCKED` yang dapat menahan daemon serial.

## Batas pembuktian

Native worker interoperability tidak diulang karena delta hanya menyentuh scheduling Go/PostgreSQL. Fixture interop dan leaf-symlink Windows tetap mengikuti harness/privilege terpisah. Jitter retry, konfigurasi budget per workload, contention skala produksi, latency, throughput, dan benchmark kualitas belum diukur.
