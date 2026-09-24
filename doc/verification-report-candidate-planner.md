# Verifikasi planner kandidat resolusi

Dokumen ini mencatat pemeriksaan paket planner kandidat RESOLVE pada 2026-09-24. Cakupannya adalah policy scope terpin, normalisasi alias Go/Rust, tipe kanonik ontology v1, handoff artefak, dan isolasi alias pasal di PostgreSQL. Ini bukan kelulusan K01 atau benchmark rilis. Revisi dasar sebelum perubahan adalah `badd1dc358d9d9d28e1f68dbf4012b62620f29d9`; artefak log dan hash berada di `artifacts/verification/20260924-candidate-planner/`.

## Input, proses, output yang diperiksa

Input: `ExtractionBatch` lengkap dari checkpoint EXTRACT sukses, request ingest durable yang diverifikasi checksum storage, `CandidatePlanningPolicy`, producer kandidat, dan revision registry. Policy harus ada pada manifest request yang tersimpan serta producer; corpus dan fingerprint konfigurasi EXTRACT harus cocok dengan request. Proses: planner mempertahankan seluruh scope dan mention tanpa truncation, menormalisasi lookup key konsisten dengan Rust, lalu lookup batch di bawah fence dan revision. Output: `RegistryCandidateBatch` beserta artefak content-addressed dan dependency manifest yang mempertahankan producer dan EXTRACT. LINK atau identitas baru tidak diputuskan oleh planner.

`code-manifest.json` menyimpan SHA-256 untuk 11 file implementasi/test/manifest Go-Rust yang berubah; hash berkas manifest adalah `3e6d70484a5e871ad147f56c28c6e7683927502bab134aa203ab8c0480725b5d`. Fixture Go dan SQL menggunakan identitas sintetis. Toolchain: Go 1.26.8 windows/amd64, Cargo/Rust 1.87.0, PostgreSQL 18 pada cluster disposable loopback port 55433. Go dan Cargo berjalan offline dengan cache workspace.

| Pemeriksaan | Ekspektasi dan hasil aktual | Status |
| --- | --- | --- |
| `go test -count=1 ./src/server/...` dengan `REGULAGRAPH_TEST_POSTGRES_DSN` | Seluruh package lulus, termasuk tes PostgreSQL nyata untuk alias `provision` dan scope regulation; exit 0, `go-tests.log`. | PASS untuk scope tes |
| `go vet ./src/server/...` | Tidak ada temuan vet; exit 0, `go-vet.log`. | PASS |
| `cargo test --workspace --locked --offline` | 131 unit test lulus, 1 ignored, exit 0, `rust-tests.log`; tes key Unicode dan alias provision ikut berjalan. Tes PDFium bersyarat kembali sukses tanpa parsing jika environment native belum disetel. | PASS untuk unit; PDFium native NOT_MEASURED |
| `git diff --check` | Tidak ada whitespace error; exit 0. Peringatan konversi LF/CRLF bukan kegagalan diff. | PASS |
| Review independen `verify_candidate_planner` | Reviewer memeriksa diff dan kontrak, mengulang tes Go terfokus serta 15 tes Rust resolution. Temuan casing sigma, hash policy yang hanya self-attested, dan raw surface >512 byte diperbaiki serta mendapat regresi. Tidak ada blocker tersisa dalam scope. Reviewer tidak mengulang tes DB nyata. | PASS dalam scope review |
| Gold candidate recall/false exclusion, false merge/split, latency p95/p99, throughput, memori, dan biaya | Corpus gold, konfigurasi model, serta workload penerimaan belum dibekukan; tidak ada hasil yang sah untuk dibandingkan dengan target. | NOT_MEASURED |

Tes scope PostgreSQL membuktikan alias pasal tidak bocor dari satu regulation scope ke scope lain. Tes itu tidak membuktikan rantai provenance BIND ke alias atau kebenaran penyamaan pasal pada dokumen hukum nyata. Fixture producer dan scope `national`/`regional` menguji mekanisme; konfigurasi scope produksi belum ditentukan. Job lama yang tidak mem-pin policy pada request ingest tidak memenuhi jalur planner otomatis dan harus disubmit sebagai job baru. Jalur caller-supplied plan tetap membutuhkan pemanggil tepercaya.

K01 masih memerlukan pengisian policy pada submit produksi, dispatch RESOLVE, review terautentikasi/keputusan kanonik baru dan merge/split, evaluasi gold, serta benchmark required pada [target numerik](../configs/benchmark-targets.yaml). Status target tetap **REQUIRED_UNMEASURED**.
