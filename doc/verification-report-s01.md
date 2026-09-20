# Verifikasi fondasi storage dan publication S01

Dokumen ini mencatat bukti penyelesaian fondasi control-plane S01 pada 2026-09-20. Perannya mengikat
schema PostgreSQL, adapter artefak lokal, scheduler durable, publication coordinator, hasil pengujian,
dan audit agent independen ke revision yang dapat ditinjau. PASS dalam laporan ini berlaku untuk
correctness fondasi S01 yang diuji; performa produksi, mutation/visibility Qdrant dan Neo4j, recovery
lintas proses, retention/GC, backup/restore, serta ketahanan power-loss tetap **NOT_MEASURED**.

## Identitas revision dan lingkungan

Revision implementasi yang direview adalah `2dd38252f78c10e94a782b224ec1cb5df7c7b2e5`. Pengujian resmi
dijalankan pada Windows amd64 dengan Go 1.26.8 dan PostgreSQL 18.0 x86-64. PostgreSQL memakai database
disposable lokal; integration test tidak memakai mock repository. Raw command log lokal berada di
`artifacts/verification/s01-20260920/` dan diabaikan Git sesuai protokol verifikasi.

Target dan workload dalam `configs/benchmark-targets.yaml` tidak diubah. Status seluruh gate performa
dan kualitas tetap `REQUIRED_UNMEASURED`; laporan ini tidak mengubah angka, denominator, profil hardware,
atau kriteria lulus.

## Cakupan dan hasil

| Pemeriksaan | Hasil | Batas pembuktian |
| --- | --- | --- |
| Migration kosong dan replay checksum | PASS | Schema S01 diterapkan pada PostgreSQL nyata; migration yang sudah diterapkan tidak boleh diedit |
| Job idempotency dan recovery | PASS | Request/checkpoint durable, conflict payload, claim eksklusif, lease renewal/expiry, fence, dan stage regression diuji |
| Cancellation dan terminal transition | PASS | Cancellation menolak commit; transisi langsung RUNNING ke SUCCEEDED ditolak |
| Artifact metadata/dependency | PASS | Metadata immutable, replacement dependency terserialisasi, dan lookup revision memakai CAS |
| Local artifact bytes | PASS | Hash/size, cancellation, immutable concurrent put, traversal, symlink/junction confinement, dan no-replace publish diuji |
| Publication state machine | PASS | Reservation, staging, receipt readiness, parent authoritative, stale fence, CAS, abort, replay, dan operation serialization diuji |
| Historical visibility/read pin | PASS | Snapshot lama dapat direplay dan pin pembaca lama tetap sah saat snapshot baru dipublikasikan |
| Go package tests | PASS | `go test ./src/server/... -count=1` dengan PostgreSQL aktual |
| Static analysis | PASS | `go vet ./src/server/...` |
| Contract descriptor regression | PASS | 157 message, 31 enum, empat service; baseline tidak ditulis ulang |
| Header file lama yang berubah | PASS | Peran, kontrak, benchmark, target numerik, dan status tetap tersedia serta diperbarui sesuai implementasi |
| Whitespace patch | PASS | `git diff --check` tidak menemukan whitespace error |
| Benchmark produksi | NOT_MEASURED | Corpus/gold/model/backend/deployment referensi belum lengkap |

Perintah implementer yang dijalankan:

```powershell
$env:REGULAGRAPH_TEST_POSTGRES_DSN = "postgres://postgres@127.0.0.1:55432/postgres?sslmode=disable"
go test ./src/server/... -count=1
go vet ./src/server/...
python scripts/check_contracts.py
git diff --check
```

## Review independen

Agent `verify_c01` melakukan audit read-only terhadap working tree dan PostgreSQL disposable. Enam kelompok
counterexample independen lulus, mencakup request/checkpoint recovery, parent/fence/lease, historical replay,
read pin, abort-operation race, cancellation saat commit, larangan sukses langsung, dependency replacement,
concurrent immutable write, dan confinement junction. Reviewer menyatakan tidak ada temuan material terbuka
untuk fondasi control-plane S01.

Temuan audit sebelum verdict menghasilkan penguatan berikut:

- transisi job membutuhkan lease hidup dan menolak cancellation atau terminal shortcut;
- checkpoint wajib cocok dengan job, corpus, attempt, stage, owner, dan fence aktif;
- commit publication memuat parent authoritative serta memeriksa state, receipt, generation, fence, dan CAS;
- abort dan operation ledger diserialisasi agar operasi terlambat tidak mengubah publication yang sudah dibatalkan;
- dependency replacement dan immutable artifact write aman terhadap penulis concurrent;
- `os.Root`, penolakan symlink/junction, file fsync, dan atomic no-replace publish mengurung artifact store.

Verdict reviewer adalah **PASS untuk scope fondasi control-plane S01 yang direview**. Verdict tersebut tidak
membuktikan latency, throughput, kualitas retrieval, durability saat kehilangan daya, atau konsistensi backend
yang belum terhubung.

## Keterbatasan dan kelanjutan

Transaksi S01 hanya atomik di PostgreSQL. X01/K01/U01 harus merealisasikan mutasi, visibility filter, dan
compensation nyata untuk Qdrant/Neo4j menggunakan generation/fence yang sudah disediakan. O01 harus menguji
crash lintas proses, recovery, retention/GC, backup/restore, dan batas directory-entry sync pada Windows.
Pengukuran pool saturation, p50/p95/p99, throughput, memori, serta mixed load tetap menunggu corpus, model,
dan deployment referensi. Pekerjaan berikut mengikuti [rencana berbasis dependency](development-plan.md).
