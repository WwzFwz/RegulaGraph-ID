# Laporan verifikasi K01: ontology EXTRACT terpin

Dokumen ini mencatat bukti verifikasi untuk vocabulary relasi bersama dan validasi hasil EXTRACT lintas Go Semantic Gateway, Go coordinator, serta Rust ingestion worker. Cakupannya hanya boundary ontology dan persistensi artefak EXTRACT; ini bukan bukti akurasi ekstraksi semantik atau kelulusan seluruh K01.

## Identitas dan cakupan

Pemeriksaan dilakukan pada 2026-09-23 setelah commit `aeafeb5` (dengan perubahan pendahulu `11ba4f9`, `f2ef532`, `9b9e71a`, `b0628ca`, `5c350e0`, dan `a73cbde`). SHA-256 bytes vocabulary `configs/ontology-v1.jsonc` adalah `d4f48615c3d85416a7a343d0d604b237afa66c36725187268eeb17819e7ed0fb`. Raw output tersedia di `artifacts/verification/k01-ontology-20260923/` dan diabaikan Git. Toolchain: Go 1.26.8 windows/amd64, Cargo/rustc 1.87.0, Python 3.12.4. Fixture memakai vocabulary terpin dan proposal graf sintetis valid/invalid, bukan gold label hukum atau provider produksi.

| Pemeriksaan | Expected dan hasil aktual | Status |
| --- | --- | --- |
| Parser ontology Go/Rust | Vocabulary dengan field duplikat, field tidak dikenal, UTF-8 rusak, qualifier/endpoint/predicate tidak sah ditolak; fixture valid diterima | PASS, unit regression |
| Submit dan handoff | Request tanpa hash atau hash salah ditolak sebelum durable enqueue; producer hash dan graph typed diperiksa sebelum artefak worker dipersist; coordinator memeriksa hasil fresh dan recovery | PASS, unit/worker regression |
| `go test ./...` dari `src/server` | Seluruh paket Go lulus; exit 0; `go-test.log` | PASS |
| `go vet ./...` dari `src/server` | Tidak ada temuan; exit 0; `go-vet.log` | PASS |
| `cargo test -p regulagraph-ingestion --lib --locked --offline` dari root | 113 lulus, 0 gagal, 1 ignored (fixture wire lama yang memerlukan generation terpisah); exit 0; `cargo-test.stdout.log` dan `cargo-test.stderr.log` | PASS untuk tes yang dijalankan |
| `cargo check -p regulagraph-ingestion --bins --locked --offline` dari root | Binary worker terkompilasi; exit 0; `cargo-check.stdout.log` dan `cargo-check.stderr.log` | PASS |
| `cargo fmt --all --check`, `python scripts/check_contracts.py`, `git diff --check` | Format, schema lock (159 messages, 31 enums, 4 services; baseline tidak ditulis ulang), dan whitespace valid; exit 0 masing-masing | PASS |
| Review boundary independen | Agent verifier meninjau parser, pin lintas runtime, urutan tolak sebelum persistensi, recovery, dan stabilitas bytes pada checkout; temuan awal diperbaiki dan tes terkait diulang | PASS untuk cakupan review |
| Kualitas dan benchmark release | Belum ada provider/model, gold extraction set, PostgreSQL end-to-end nyata, atau run workload referensi | NOT_MEASURED / BLOCKED sesuai gate |

Review independen tidak menemukan temuan blocking yang masih terbuka pada boundary ini. Ia tidak mengesahkan kebenaran hukum proposal model; graph yang cocok secara bentuk tetap dapat salah secara semantik. Tes worker memakai dependency fixture, sehingga persistensi PostgreSQL aktual dan publikasi snapshot harus diuji pada tahap integrasi terkait. `cargo-test.log` adalah log percobaan awal dengan status wrapper PowerShell yang ambigu; bukti exit 0 memakai `cargo-test.stdout.log` dan `cargo-test.stderr.log` dari pengulangan eksplisit. Tidak ada angka target pada `configs/benchmark-targets.yaml` yang diubah atau dinyatakan tercapai.

## Lanjutan

RESOLVE perlu mengonsumsi proposal yang sudah tervalidasi, memilih canonical identity dengan bukti dan registry revision, serta menguji false merge/split dan candidate coverage pada gold set. ASSEMBLE/INDEX, provider model, quality gate, dan benchmark produksi tetap pekerjaan terpisah.
