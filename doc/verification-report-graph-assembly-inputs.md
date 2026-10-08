# Verifikasi input ASSEMBLE dan canonical view

Laporan ini mencatat pemeriksaan kontrak, exporter PostgreSQL dan consumer library
Go/Rust pada 2026-10-09. Revision kode `60af82b`, kontrak/generated `9269c01`;
raw logs berada di `artifacts/verification/20261009-graph-registry-view`. Ini bukan
kelulusan seluruh K01, worker ASSEMBLE, kualitas hukum/model atau benchmark release.

## Lingkungan dan hasil

Windows amd64, Go 1.26.8, Rust 1.87.0, Python 3.12.4, CMake 3.30.1.
PostgreSQL 16.8 dan Qdrant 1.18.0 disposable pada port 55448/56348 digunakan
untuk regresi Go; fixture registry menggunakan corpus unik dan profil sintetis.

| Pemeriksaan | Expected dan actual | Bukti / exit |
| --- | --- | --- |
| `python scripts/generate_contracts.py` | Binding empat bahasa dihasilkan dari schema yang sama | `codegen.log`, 0 |
| `python scripts/check_contracts.py` | Compatible additive, baseline tetap; 173 messages, 32 enums, 4 services | `contracts.log`, 0 |
| `cmake --build .cache/contracts-build --config Release --parallel 4` | Wire library/probe C++ terbangun | `cpp-build.log`, 0 |
| `python tests/integration/wire_roundtrip.py` | 77 fixture valid/invalid/unknown terjaga di Go/Rust/C++/Python | `wire.log`, 0 |
| Go targeted exporter/domain | Exact selection dan drift/corruption/overflow ditolak | `export.log`, `independent-go.log`, `independent-postgres.log`, 0 |
| `go test ./... -count=1` dari src/server dengan DSN/URL disposable | Seluruh package PASS, integrasi backend aktif | `go-all-final.log`, 0 |
| `go vet ./...` | Tidak ada temuan | `go-vet.log`, 0 |
| `cargo test -p regulagraph-ingestion --lib --offline` | 176 PASS, 2 ignored opt-in endpoint/wire | `rust-lib-final.log`, 0 |
| Review Rust assembly independen | 17/17 PASS setelah resource fix | `independent-rust-final-elevated.log`, 0 |

Wire fixture menguji field wajib dan forwarding; stage tests terpisah menguji
unknown-field rejection, exact entity ordering/coverage, corpus/revision/schema,
source role, ontology, visibility dan batas ukuran. PostgreSQL test mempertahankan
hasil revision lama setelah profil sintetis berikutnya ditambahkan; profil/hash
rusak, canonical hilang dan fence salah gagal. Ia tidak mengimplementasikan atau
mengesahkan policy perubahan profil canonical produksi.

## Temuan, kegagalan dan penutupan

Reviewer independen `verify_index_jobs` menemukan wrapper membentuk set canonical
sebelum memvalidasi budget ResolutionBatch. Guard kini mendahului alokasi; regression
oversized-resolution membuktikan penolakan admission. Review final tidak menemukan
blocker pada scope library/exporter/typed inputs. Wire logs diperiksa reviewer,
sedangkan Go domain, PostgreSQL dan Rust assembly dijalankan ulang secara independen.

Run Rust awal/independen tertentu gagal meluncurkan compiler karena sandbox Access
denied; log tetap disimpan dan run escalated berhasil. `go-all.log` awal gagal compile
karena file validator Go tidak ada pada saat run; implementasi dipulihkan dari perubahan
tercatat, targeted independen dan full suite diulang hingga PASS. Tidak ada hasil gagal
yang diperlakukan sebagai PASS. `git diff --check` dan tautan lokal dokumen terdampak
juga diperiksa, tanpa temuan.

## Batas penerimaan

RepeatableRead exporter membuktikan binding saat snapshot transaksi. Coordinator
masih wajib mengautentikasi bytes, checkpoint/receipt keputusan dan live fence saat
admission/commit. Persistence plan/view, dispatch ASSEMBLE dan backend graph publication
belum tersedia melalui paket ini. Tidak ada gold/acceptance model atau p95/p99/RSS
produksi yang diukur; target tetap **REQUIRED_UNMEASURED** sesuai YAML dan kebijakan.
