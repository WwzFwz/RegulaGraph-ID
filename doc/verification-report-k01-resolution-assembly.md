# Verifikasi assembly artefak RESOLVE K01

Dokumen ini mencatat pemeriksaan builder artefak RESOLVE pada 2026-09-24. Perannya
membedakan assembly setelah receipt terverifikasi dari workflow RESOLVE dan transaksi
registry PostgreSQL yang belum aktif. Builder menerima EXTRACT/kandidat terpin, request,
receipt, identitas model/producer, dan token usage; outputnya adalah `ResolutionBatch`
immutable yang membawa semua keputusan dan dependency lookup positif/negatif.

Base revision: `3c259eb`. Fingerprint `resolution_batch_builder.go` adalah
`31987257842ef1a0f86b60ec1a1d63fbf0f5f22c`; fingerprint tesnya
`bb8f9030b40652a39e8c486bea2e8c723b81d798` (`git hash-object`).
Raw log dan versi toolchain ada di `artifacts/verification/k01-resolution-assembly-20260924/`.

| Pemeriksaan | Status | Bukti dan batas |
| --- | --- | --- |
| Assembly receipt LINK/DEFER | PASS | Tes valid mengikat EXTRACT dan kandidat dengan dua artifact hash, revision lookup, canonical ID, serta request ID RESOLVE yang berbeda dari EXTRACT. |
| Kegagalan dan immutability | PASS | Receipt parsial, ID bentrok, artifact ID dipakai ulang, producer melebihi batas byte ditolak; mutasi input setelah build tidak mengubah proposal/decision hasil. |
| Go test dan vet penuh | PASS | `go -C src/server test ./...` dan `go -C src/server vet ./...`, masing-masing exit 0; raw `go-test-final.log`, `go-vet-final.log`. |
| Review independen | PASS untuk scope helper | Agent `verify_k01_receipts` menemukan context EXTRACT yang tersalin dan preflight ukuran yang terlambat; keduanya diperbaiki, lalu tes terarah dijalankan independen. Regresi distinct request ID dijalankan ulang setelah sarannya. |
| Autentikasi byte/ref kandidat serta receipt PostgreSQL | NOT_MEASURED | Workflow harus membaca artefak content-addressed dan membuktikan ref cocok dengan byte kandidat yang divalidasi. Builder murni tidak dapat mengesahkannya. |
| Kualitas hukum, latency, throughput, acceptance | NOT_MEASURED | Model/gold dan runtime stage belum tersedia; target required tetap seperti `configs/benchmark-targets.yaml`. |

Tes memakai `GOCACHE` di workspace karena cache default Windows tidak dapat dibaca di
sandbox. Stage RESOLVE durable, CAS registry, merge/split, serta graph assembly masih
pekerjaan berikutnya. Tidak ada angka benchmark yang diubah atau dinyatakan tercapai.
Tes akhir setelah fixture request ID dibedakan berada di `go-test-context-final.log` (exit 0).
