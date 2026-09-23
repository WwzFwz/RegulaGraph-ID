# Verifikasi K01: kandidat registry dan proposal sebelum keputusan

Dokumen ini mencatat pemeriksaan boundary Go untuk `RegistryCandidateBatch` dan proposal
LINK/DEFER sebelum panggilan writer registry. Kode yang diuji: commit
`26a8c70e2bea492dbfdf7f0cf15c6a550c51d46b`; SHA-256
`src/server/internal/domain/candidate_validation.go` =
`11b74a1e96c25503f736e4bbb9db773e582ffc05183b5cd5cce904f78b4be4d`,
`src/server/internal/domain/resolution_candidates.go` =
`08e3560c75608c57b5ec10c77c7b139b5d6894f63c9a2b2c423d8792441f5fdf`.

## Hasil dan batas pembuktian

Run 2026-09-24 memakai Go 1.26.8 pada Windows amd64 dan fixture C01 lokal.
Validator kandidat kini menghitung ulang scope ID dari tipe, scope hukum, dan normalized
lookup dengan framing panjang byte yang sama dengan Rust. Setiap kandidat positif harus
memiliki alias bersumber yang cocok pada scope tersebut. Adapter PostgreSQL mendelegasikan
derivasi ID ke domain agar pembaca dan validator tidak bercabang.

Validator proposal menuntut satu LINK/DEFER per mention, revisi registry sama,
evidence span/source version asli, dan target LINK dari lookup mention yang sama. DEFER
menyimpan seluruh kandidat. Identity key CREATE yang diselundupkan, ID yang bertabrakan
dengan EXTRACT, visibility prematur, proposal/artefak terlalu besar, serta aksi lain ditolak.
Pemeriksaan ini berjalan sebelum penulisan keputusan dan tidak memberi otoritas LINK.

| Pemeriksaan | Expected dan aktual | Status |
| --- | --- | --- |
| `go test ./... -count=1` dari `src/server` | Semua paket yang memiliki tes lulus, exit 0; `go-test-all.log`. Paket tanpa tes dinyatakan `[no test files]`. | PASS untuk tes yang dijalankan |
| `go vet ./...` dari `src/server` | Tidak ada temuan, exit 0; `go-vet-all.log`. | PASS |
| `git diff --check` | Tidak ada whitespace error, exit 0; Git hanya memberi peringatan konversi line ending Windows. | PASS |
| Review agent independen, read-only | Menemukan dan memeriksa perbaikan identity key terselubung, collision ID, visibility, alias positif tanpa bukti, dan scope ID palsu. Reviewer menjalankan ulang tes domain/adapter; tidak ada blocker lokal tersisa. | PASS untuk boundary lokal |
| Receipt PostgreSQL, CAS revision, dan stage RESOLVE | `REGULAGRAPH_TEST_POSTGRES_DSN` tidak tersedia; writer dan workflow RESOLVE belum tersambung. | BLOCKED |
| Candidate recall, false merge/split, latency/throughput required | Gold resolution, model terpin, dan workload referensi belum tersedia. | NOT_MEASURED |

Raw log berada di `artifacts/verification/k01-go-resolution-candidates-20260924/`
dan diabaikan Git. Fixture menguji hasil lookup negatif/ambigu, alias salah, scope ID
palsu, target LINK asing, DEFER yang menyembunyikan kandidat, revisi stale, evidence
palsu, ID bertabrakan, serta exact hash vector. Go tetap harus membaca artefak
content-addressed dan mengautentikasi receipt registry pada tahap workflow berikutnya.
Target `configs/benchmark-targets.yaml` tidak diubah; statusnya tetap
**REQUIRED_UNMEASURED**.
