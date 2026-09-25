# Verifikasi policy kandidat dan submit job

Dokumen ini mencatat verifikasi boundary konfigurasi policy RESOLVE dan CLI submit pada 2026-09-25. Paket ini mengikat policy kandidat per corpus ke request ingest durable, memeriksa ulang pin tersebut saat handoff RESOLVE, dan menyediakan submit job PostgreSQL untuk referensi blob. Ini bukan kelulusan K01 atau acceptance benchmark.

Baseline sebelum perubahan: `141cb5d`; implementasi policy/handoff `4a642f8` dan CLI submit `ed837d6`. Hash SHA-256 dari 12 file kode dan tes ada di `artifacts/verification/20260925-policy-submit/code-manifest.json`; hash manifest adalah `65e99e0365f4c5d2c587e6d29a89a80aead780077ab825bcde5fcffed08e3f1b`. Raw output tes ada di direktori yang sama. Toolchain: Go 1.26.8 windows/amd64 dan PostgreSQL 18 pada cluster disposable loopback. Fixture corpus, policy, source blob, dan request bersifat sintetis.

## Kontrak dan hasil

Input: file ontology dan policy JSON yang byte-nya cocok dengan pin SHA-256, request ProtoJSON berukuran paling banyak 16 MiB, serta corpus dengan policy valid. Loader menolak field/duplikat/case variant tak dikenal, budget tak terbatas atau mustahil, dan corpus yang tidak terkonfigurasi. Scheduler menuntut hash ontology dan policy pada manifest request. CLI menambahkan dua hash itu tanpa duplikasi sebelum submit idempotent ke PostgreSQL. URL ditolak selama dispatcher ACQUIRE belum tersedia.

Handoff RESOLVE membandingkan policy yang dipakai dengan konfigurasi corpus tepercaya, producer, dan request durable. Pembacaan request dibatasi lease. Budget lookup scope unik dan referensi dasar diverifikasi sebelum membaca source; policy berubah atau config EXTRACT tidak cocok menyebabkan kegagalan eksplisit. Jalur rencana scope eksplisit untuk caller tepercaya tetap terpisah.

| Pemeriksaan | Expected dan actual | Status |
| --- | --- | --- |
| `go test -count=1 ./src/server/...` dengan `REGULAGRAPH_TEST_POSTGRES_DSN` | Seluruh package Go lulus, termasuk adapter PostgreSQL nyata; exit 0, `go-tests.log`. Tes submit DB memakai env terpisah dan tidak berjalan dalam perintah ini. | PASS untuk cakupan tes |
| `go test -count=1 -v ./src/server/cmd/cli -run TestSubmitCommandPersistsPinnedRequestAndReplays` dengan `REGULAGRAPH_TEST_SUBMIT_POSTGRES_DSN` | Submit, durable request hash, dan idempotent replay lulus terhadap PostgreSQL nyata; exit 0, `cli-postgres.log`. | PASS |
| `go vet ./src/server/...` | Tidak ada temuan; exit 0, `go-vet.log`. | PASS |
| `git diff --check` | Tidak ada whitespace error; exit 0. Git hanya memperingatkan konversi LF/CRLF. | PASS |
| Review independen `verify_policy_submit` | Reviewer memeriksa boundary dan tes, menemukan lalu meminta perbaikan pada deadline lease, parsing JSON ambigu, budget, dan preflight referensi; regresi ditambahkan. Review ulang tidak menemukan blocker dalam cakupan. | PASS dalam cakupan review |
| Gold candidate coverage, false merge/split, p95/p99 latency, throughput, RSS, dan target required | Belum ada policy legal produksi, corpus gold, atau run penerimaan dengan model/workload terpin. | NOT_MEASURED |

Percobaan awal yang menjalankan tes CLI dan adapter pada database yang sama secara serentak terganggu oleh cleanup fixture lintas package; tes kemudian memakai environment DB terpisah dan dijalankan berurutan. Percobaan setelah perubahan pesan error sempat gagal karena cache Go di luar workspace tidak dapat diakses, lalu lulus dengan `GOCACHE` dan `GOMODCACHE` di `.cache/` serta `GOPROXY=off`. Kegagalan lingkungan ini tidak dihitung sebagai hasil implementasi.

CLI hanya memeriksa bentuk referensi blob, bukan keberadaan byte di storage saat submit; downstream tetap harus membuktikan integritasnya. Job queued belum membuktikan PARSE sampai RESOLVE, keputusan legal, publikasi, ataupun jawaban. Nilai scope corpus produksi, dispatch RESOLVE, review kanonik baru/MERGE/SPLIT, dan acceptance model masih terbuka. Target numerik tetap [benchmark-targets.yaml](../configs/benchmark-targets.yaml) dengan status **REQUIRED_UNMEASURED**.
