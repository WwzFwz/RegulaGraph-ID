# Verifikasi kontrak filter legal berpasangan X01

Dokumen ini mencatat pemeriksaan kontrak wire aditif dan gate library Go untuk
filter legal per versi pasal. Dasar kode sebelum perubahan adalah `e0f6570`.
Raw log ada di `artifacts/verification/20260925-x01-paired-filters/` (diabaikan Git).
PASS di sini berlaku hanya untuk kontrak dan validasi lokal, bukan writer,
publication, query backend, akurasi hukum, atau benchmark release.

| Pemeriksaan | Hasil dan cakupan |
| --- | --- |
| `python scripts/generate_contracts.py` | PASS; generated Go binding cocok dengan proto menggunakan protoc terpin 34.1 dan protoc-gen-go 1.36.12. |
| `python scripts/check_contracts.py` | PASS; 164 messages, 32 enums, 4 services, `baseline_written=false`; schema lock tidak diubah. |
| `cmake --build .cache/contracts-build --config Release --parallel 4` | PASS; probe C++ dibangun ulang setelah penambahan message. |
| `python tests/integration/wire_roundtrip.py` | PASS; 54 fixture, termasuk paired filter, UNKNOWN legal interval, field baru, dan unknown-field forwarding lintas Go/Rust/C++/Python. Log `interop.log`. |
| `go test -count=1 ./...` dari `src/server` | PASS; gate generation, coverage pasangan, dan proyeksi fakta sumber diuji bersama suite Go. Log `go-all.log`. |
| `cargo test -p regulagraph-ingestion --offline` | PASS; suite Rust dan integrasi PDF berjalan. Log `rust-full.log`. |
| `git diff --check` | PASS; hanya peringatan konversi line ending Windows. |
| Review agent independen | PASS untuk kontrak dan gate lokal; reviewer menguji kasus visibility, source mutation, compatibility checker, codegen, dan fixture lintas bahasa. |

Toolchain: Go 1.26.8 windows/amd64; rustc/Cargo 1.87.0. Fixture bersifat
sintetis. Perubahan ini menambahkan `IndexFilterFormat.PAIRED_PROVISION_V1`
dan `IndexProvisionFilter`; writer baru harus menolak array legacy yang tidak
mempertahankan pasangan interval/status/versi. `IndexSourceView` memvalidasi
`DocumentBatch` lengkap lalu membandingkan chunk, versi, interval, status,
regulasi, yurisdiksi, dan blob sumber. Visibilitas sumber yang tersedia harus
mencakup visibilitas record indeks.

Kewajiban integrasi masih terbuka: caller membuktikan hash artefak dan
keanggotaan setiap record pada snapshot tepercaya, terutama jika visibility
sumber tidak tersedia. Reader lama dapat mengabaikan field protobuf baru,
sehingga generation paired dilarang dipublikasikan sebelum semua reader yang
dapat menerima traffic mengenali format baru dan menolak format tak dikenal.
Writer Qdrant, pembacaan balik, publication CAS, pencarian, dan required
benchmark tetap **NOT_MEASURED**. Tidak ada angka target yang diubah.
