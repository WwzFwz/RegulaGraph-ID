# Verifikasi persiapan vocabulary dan statistik corpus

Dokumen ini mencatat pemeriksaan populasi BM25 dari chunk terverifikasi, alokasi
dictionary PostgreSQL, dan handoff Rust–Go–Rust. Basis kode `2372417`; fingerprint
final, toolchain dan raw logs tersimpan di `artifacts/verification/20261009-lexical-population`.
Hasil fixture tidak membuktikan relevansi gold, throughput corpus pengguna atau
kelulusan benchmark required.

| Pemeriksaan | Expected dan hasil aktual | Bukti |
| --- | --- | --- |
| Rust package | 161 library tests PASS, 2 ignored; 1 CLI argument test PASS; pdf_parser test PASS sesuai konfigurasi fixture | `cargo test -p regulagraph-ingestion --locked --offline`, exit 0, `rust-final.log` |
| Population/INDEX fixture final | Rendering/DF sama dengan reference; perubahan page size tidak mengubah artifact; duplicate chunk lintas sumber, scope/snapshot, cancellation serta byte/token budget ditolak | Focused CHUNK integration, exit 0, `fixture-export.log` |
| Go indexing/CLI | Validasi vocabulary/hash dan build komponen PASS; tes opt-in tanpa env tidak dinilai sebagai integrasi | `go-unit.log`, exit 0 |
| PostgreSQL nyata | 5.001 term dibagi dua transaksi; interupsi registrasi dipulihkan; replay setelah alokasi lain menghasilkan artifact historis sama | `postgres.log`, exit 0, PostgreSQL 16.8-alpine disposable |
| Tiga executable invocation | Rust vocabulary → Go dictionary PostgreSQL/FileStore → Rust frozen statistics; 6 chunk, 54 tokens dari fixture yang sama | `cli-vocabulary.log`, `cli-dictionary.log`, `cli-statistics.log`, exit 0 masing-masing |
| Konsumen Go | Menerima artifact statistik Rust dengan dictionary dari Go; snapshot, population count, input policy dan validator BM25 cocok; INDEX output Rust tetap diterima | `interop.log`, exit 0 |
| Go gabungan final | Seluruh paket lulus dengan PostgreSQL dan fixture interop; pemeriksaan terdampak diulang sesudah helper retry diekstrak | `go-all-final.log`, `go-final-targeted.log`, exit 0 |
| Static analysis | `go vet` indexing dan CLI | `go-vet.log`, exit 0 |
| Review independen | `verify_lexical_population` membaca Rust/CLI dan Go replay/identity, menjalankan worker 7 tests, CLI Rust 1 test dan vocabulary Go test | PASS pada pemeriksaan tersebut; database/CLI process smoke dijalankan implementer |
| Kualitas dan required performance | Belum dijalankan | NOT_MEASURED |

Review mengidentifikasi bahwa ukuran DocumentBatch tidak membatasi referenced
text/mapping I/O. Perbaikan menambahkan batas per artefak serta biaya pembacaan
ulang per halaman sebelum I/O. Regression test membuktikan budget tersebut dan
duplicate chunk pada dua sumber berbeda. Tidak ada temuan substantif terbuka.

Percobaan awal build di sandbox gagal mengeksekusi Cargo build script (Windows
access denied); tes diulang dengan izin eksekusi dan lulus. Ini bukan kegagalan
algoritma yang disembunyikan sebagai PASS. Build offline memakai Cargo.lock yang
sama; schema/generated bindings dan benchmark target tidak diubah.

Command ini belum membuktikan source membership/checkpoint Go, pengikatan
statistik ke inventory durable, scheduler INDEX, registration dependencies
statistik, atau publication. Batas tersebut dijelaskan dalam
[panduan penggunaan](lexical-population.md). Model dense pada fixture tetap
sintetis; corpus PDF pengguna belum menjadi acceptance dataset pada paket ini.

Run Go gabungan pertama gagal SQLSTATE40001 ketika transaksi registry berjalan
bersamaan (`go-all.log`). Allocator kini membatasi retry rollback40001/40P01
menjadi empat attempts dan menghormati cancellation; commit yang tidak pasti
tidak diulang. Regression test membedakan rollback, constraint/unknown commit,
budget dan cancellation. Satu percobaan build perbaikan gagal karena import
time belum ditambahkan (`go-all-retry.log`); final build/test lulus setelah
koreksi. Reviewer memeriksa kebijakan retry tersebut tanpa temuan penghalang.
