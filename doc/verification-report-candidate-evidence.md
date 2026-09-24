# Verifikasi bukti kandidat lintas dokumen

Dokumen ini mencatat pemeriksaan paket katalog EXTRACT dan bukti kandidat RESOLVE pada 2026-09-24. Scope meliputi transaksi checkpoint, hidrasi konteks dua sisi, validasi citation LINK, serta replay/dependency. Ini bukan kelulusan K01, dispatch daemon, kualitas hukum, atau acceptance benchmark. Baseline sebelum perubahan adalah `99aa2a7`; tidak ada deployment.

Revisi implementasi: kontrak `3117dca`, katalog/checkpoint `546edf2`, konteks kandidat `8d7fca3`, dan smoke provider `48ede95`. Commit dokumentasi berikutnya hanya merangkum status dan bukti paket ini.

## Identitas run

Raw logs dan manifest berada pada `artifacts/verification/20260924-candidate-evidence/`. `code-manifest.json` mencatat SHA-256 per file untuk 26 file kode/kontrak/prompt/migration/fixture; fingerprint gabungan `ef4f643ac86ff5f9615f14897108f4efd97c946f2fee4f686617c12c85356e3c`. Fixture `tests/fixtures/wire-cases.json` mempunyai SHA-256 `c366772782cfad6657c807e92a6089e8b2e4d4d2af565155cf44f0f6a4ecb6ad`. Seluruh contoh mention dan canonical untuk paket ini sintetis, bukan gold legal.

Toolchain: Go 1.26.8 windows/amd64, Rust 1.87.0, Python 3.12.4, protoc 34.1, dan MSBuild 16.11.2/Visual Studio 2019. Go memakai cache workspace dan `GOPROXY=off`; Rust memakai `.cache/rust-target` serta dependency offline. PostgreSQL nyata dijalankan sebagai cluster disposable tersendiri pada loopback port 55433, database `candidate_evidence_test`; bukan database corpus pengguna.

## Hasil aktual

| Pemeriksaan/perintah | Expected dan actual | Status |
| --- | --- | --- |
| `python scripts/generate_contracts.py` | Bindings dibuat ulang dari schema; exit 0 | PASS |
| `go test -count=1 ./src/server/...` | Seluruh package berhasil; exit 0, `go-tests.log`; integration opsional tanpa env tidak dihitung sebagai DB/model PASS | PASS |
| `go vet ./src/server/...` | Exit 0, `go-vet.log`; ada pesan telemetry sandbox ditolak tanpa kegagalan vet | PASS |
| `go test -count=1 -v ./src/server/internal/adapters/postgres` dengan `REGULAGRAPH_TEST_POSTGRES_DSN` khusus | Seluruh suite PostgreSQL nyata lulus, termasuk migration/catalog/fence/replay, exit 0; `postgres-tests.log` | PASS |
| `cargo test --workspace --locked --offline` | Percobaan sandbox gagal mengeksekusi build script; rerun terotorisasi berhasil, 129 unit tests passed, 1 ignored, exit 0; `rust-tests-elevated.log` | PASS untuk unit tests yang dijalankan |
| Integration PDFium | Test bersyarat kembali tanpa parsing karena environment PDFium tidak disetel; tidak diklaim sebagai eksekusi native PDFium baru | NOT_MEASURED pada run ini |
| `cmake --build .cache/contracts-build --config Release` | Library/probe kontrak berhasil dibangun, exit 0; `cpp-build.log` | PASS |
| `python scripts/check_contracts.py` | 163 messages, 31 enums, 4 services; baseline tidak ditulis ulang, exit 0; `contracts-check.json` | PASS |
| `python tests/integration/wire_roundtrip.py` | 50 fixture wire memberi hasil yang sama di Go/Rust/C++/Python, exit 0; `wire-roundtrip.log` | PASS |
| `go test -count=1 -v ./src/server/internal/adapters/inference -run TestStructuredProviderEndpointIntegration` | Ollama `http://127.0.0.1:11434`, `qwen2.5:7b`, tanpa key; structured output serta kedua citation valid, exit 0; `local-model-smoke.log` | PASS untuk kompatibilitas protokol |
| Review independen `verify_candidate_evidence` | Membaca diff/kontrak/migration dan menjalankan targeted Go tests ulang; tiga temuan ditutup, tidak ada blocker tersisa dalam scope | PASS dalam scope review |
| Gold same/different, false merge/split, candidate recall, latency/throughput/RSS pada profil required | Dataset/model/hardware penerimaan belum dibekukan dan run belum dilakukan | NOT_MEASURED |

Smoke model nyata memakai prompt/schema produksi dengan dua excerpt fiktif dari fixture. Satu panggilan mengembalikan LINK, mencatat 1.923 input tokens, 110 output tokens, dan durasi 15,7217044 detik. Angka tunggal ini tidak memiliki warm/cold protocol atau ukuran sampel untuk menyatakan p95; model tidak ditetapkan sebagai pilihan produksi. Target tetap `configs/benchmark-targets.yaml` dengan status REQUIRED_UNMEASURED.

## Coverage dan review

Tes storage memeriksa commit checkpoint/catalog atomik, recovery/replay tanpa duplikasi, rollback bila metadata tidak cocok, cancellation/stale fence, partial/failed EXTRACT tanpa katalog, isolasi corpus/auth/snapshot, support hilang, overflow, dan append-only. Katalog hanya locator; workflow tetap menolak byte/hash/source/ontology/alias yang tidak cocok sebelum model dipanggil.

Workflow diuji dengan dua dokumen berbeda, kandidat dari dokumen yang sama, audit request-response, seluruh dependency sumber, replay tanpa sampling, budget agregat, missing support, serta respons yang tidak mengutip kandidat terpilih. Gateway memeriksa exact UTF-8 span, pemilik kandidat, duplicate/conflicting context, source correlation, dan citation mention/candidate. Wire fixture menambahkan candidate context serta kasus support/excerpt wajib yang hilang.

Reviewer menemukan tiga masalah: DocumentBatch belum diikat ke auth/snapshot EXTRACT, dependency sumber yang sama dapat tercatat dua kali, dan beberapa konsumen RESOLVE menolak media type keluaran EXTRACT worker. Semuanya diperbaiki dengan gate closure, deduplikasi metadata-identik, dukungan typed media, serta regression tests. Reviewer menjalankan ulang package domain/workflows/inference/postgres dengan exit 0, memeriksa raw DB/model logs, dan tidak menemukan blocker baru. Reviewer tidak menjalankan ulang layanan DB/model; hasil nyata tersebut dijalankan implementer. Fingerprint 14 file implementasi/kontrak yang dicatat reviewer: `ce65f113a1713e6207d461a47e1f7d73faa6c5a84434ea84253f9b07e5ad2f72`.

## Batas paket

EXTRACT lama tidak otomatis masuk katalog; perlu revalidasi. Reuse bukti lintas snapshot belum tersedia tanpa membership proof. Gateway/workflow serta hash prompt perlu diperbarui bersama meskipun wire field bersifat aditif. Scope planning, dispatch otomatis RESOLVE, keputusan canonical baru/MERGE/SPLIT, review terautentikasi, graph/index/retrieval/answer end-to-end, dan acceptance masih harus dilanjutkan sesuai [development-plan](development-plan.md).
