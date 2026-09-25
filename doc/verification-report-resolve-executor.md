# Verifikasi executor proposal RESOLVE

Dokumen ini mencatat verifikasi dispatch RESOLVE opt-in, pin producer resolver, dan antrean proposal durable pada 2026-09-25. Cakupannya berakhir pada WAITING_REVIEW atau penyelesaian EXTRACT tanpa mention; laporan ini bukan kelulusan K01, kualitas model, atau acceptance benchmark.

Baseline sebelum perubahan adalah `78618a93ce84d035912bfb8fb147ac897a7bc1b8`. Fingerprint file kode, tes, dan migration tersimpan di `artifacts/verification/20260925-resolve-executor/code-manifest.json`, SHA-256 `d047a8ff24c1b79ca13b80716aea3fbcceadf272dd7d61beb1128cdfe04cd53b`. Raw log berada pada direktori yang sama. Toolchain: Go 1.26.8 windows/amd64 dan PostgreSQL 18.0 pada cluster disposable loopback. Fixture corpus, EXTRACT, policy, dan provider bersifat sintetis; pengujian database menggunakan PostgreSQL nyata.

## Kontrak input, proses, dan output

Implementasi tersimpan pada commit `0950e62` (antrean storage), `79ab778` (executor dan pin), serta `1771df9` (wiring daemon/CLI/gateway). Pemeriksaan `git diff --check` dan resolusi tautan lokal Markdown terdampak lulus setelah pembaruan dokumentasi.

CLI submit menyimpan hash ontology, policy kandidat, dan manifest producer resolver pada request durable. Loader membuktikan hash tepat file producer/schema, sedangkan executor membekukan manifest, policy, dan budget sebelum claim. Handoff memeriksa scope akses, snapshot/config, ontology, source checkpoint, serta producer pada jalur baru maupun recovery sebelum inference atau commit ulang. Budget agregat alias mengikuti kapasitas lookup registry; input berlebih ditolak tanpa truncation.

Executor memakai deadline attempt di dalam lease, memantau cancellation durable, dan membatasi retry dengan backoff. Artefak model yang telah terdaftar dapat direplay setelah restart tanpa panggilan provider ulang. PostgreSQL mengunci job dan memeriksa fence, lalu menyimpan locator proposal dan memindahkan job ke WAITING_REVIEW secara atomik. Cancellation yang telah tercatat mendapat prioritas. Proposal tidak mengesahkan LINK. EXTRACT kosong dapat diselesaikan tanpa inference; recovery checkpoint tanpa intent tetap membuktikan sumber kosong dan scope yang sesuai.

## Bukti pemeriksaan

| Pemeriksaan | Expected dan actual | Status |
| --- | --- | --- |
| `go test -count=1 ./src/server/...` dengan `REGULAGRAPH_TEST_POSTGRES_DSN` | Seluruh package Go lulus, termasuk adapter PostgreSQL nyata; exit 0, `go-tests.log`. Tes submit DB memiliki environment terpisah. | PASS dalam cakupan tes |
| `go test -count=1 -v ./src/server/internal/adapters/postgres -run TestSemanticProposalQueueAgainstPostgres` | Enam kasus park/reload, cancellation, stale fence, foreign artifact, metadata drift, dan rollback transaksi lulus; exit 0, `postgres-queue.log`. | PASS |
| `go test -count=1 -v ./src/server/cmd/cli -run TestSubmitCommandPersistsPinnedRequestAndReplays` dengan `REGULAGRAPH_TEST_SUBMIT_POSTGRES_DSN` | Tiga pin request tersimpan dan replay idempotent lulus pada PostgreSQL; exit 0, `cli-postgres.log`. | PASS |
| `go vet ./src/server/...` | Tidak ada temuan; exit 0, `go-vet.log`. | PASS |
| Review independen `verify_resolve_executor` | Review ulang tidak menemukan blocker tersisa dalam boundary yang diperiksa. Reviewer menjalankan ulang tes Go terpilih dan memeriksa kode/log tes DB; tidak menjalankan ulang PostgreSQL sendiri. | PASS dalam cakupan review |
| Kualitas resolusi, false merge/split, latency p95/p99, throughput, RSS, dan required gates | Belum ada run penerimaan memakai model/corpus/workload produksi terpin untuk paket ini. | NOT_MEASURED |

Reviewer menemukan recovery yang belum memeriksa ulang auth/config/ontology, batas scope-alias yang dapat melewati kapasitas registry, serta perlunya pembuktian sumber kosong pada recovery tanpa intent. Ketiganya diperbaiki dan mendapat regression test. Tes workflow juga memeriksa cancellation selama panggilan model, retry, drift pin sebelum inference, dan replay artefak persisten tanpa model ulang. Kesalahan fixture awal berupa perbedaan nil-versus-empty pada clone policy dan key fixture tidak valid diperbaiki sebelum run final; hasil awal tersebut tidak dihitung sebagai PASS.

## Batas hasil dan tindak lanjut

Tidak ada perubahan wire schema, Rust, atau C++; suite runtime tersebut tidak dijalankan ulang. Tidak ada model sungguhan maupun run PDF end-to-end pada paket ini. Dispatch nonaktif secara default dan memerlukan konfigurasi di [semantic-resolution.md](semantic-resolution.md). API/CLI review terautentikasi, resume proposal, canonical CREATE/MERGE/SPLIT, policy hukum corpus produksi, lease heartbeat, dan partitioning batch masih terbuka. Crash sebelum metadata response terdaftar masih dapat memerlukan panggilan model ulang. Satu coordinator menjalankan satu attempt pada satu waktu; fairness rotasi tidak membuktikan throughput paralel.

Target tetap [benchmark-targets.yaml](../configs/benchmark-targets.yaml), status **REQUIRED_UNMEASURED**. Fixture correctness dan review independen tidak membuktikan kebenaran hukum atau kemampuan model. Tidak ada target, workload, atau denominator acceptance yang diubah.
