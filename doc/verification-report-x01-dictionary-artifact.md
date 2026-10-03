# Verifikasi handoff dictionary typed X01

Dokumen ini mencatat kontrak snapshot dictionary antara allocator PostgreSQL/Go
dan reader Rust, beserta batas pembuktiannya. Basis `003b0be`, implementasi
`8254726`, `afe4f39`, dan `656b589`; diuji 2026-10-03. Raw log, hash file dan
toolchain berada di `artifacts/verification/20261003-x01-dictionary-artifact/`
(diabaikan Git). Ini menyelesaikan subkomponen handoff dictionary, bukan X01 penuh.

Lingkungan: Windows amd64, Go 1.26.8, Rust/Cargo 1.87.0, Python 3.12.4,
CMake 3.30.1, PostgreSQL 18.0 disposable pada port 55439. Fixture biner sintetis
`tests/fixtures/lexical-dictionary-v1.pb` memiliki SHA-256
`51cd7b9c7cc76c7c395abcd45d4cf54b08f56424a44f100563777a1e0c518898`.
Tidak memakai model atau corpus produksi.

| Pemeriksaan/perintah | Hasil aktual dan bukti |
| --- | --- |
| `python scripts/generate_contracts.py` | PASS, exit 0; delapan schema, binding Go/Python/C++ dan descriptor (`generate.log`). |
| `python scripts/check_contracts.py` sebelum/sesudah lock | PASS, exit 0; 166 message, 32 enum, empat service. Dua message baru aditif; baseline ditulis sesudah review kompatibilitas (`compatibility-*.log`). |
| `cmake --build .cache/contracts-build --config Release` | PASS, exit 0; probe C++ dibangun dengan schema baru (`cpp-build.log`). |
| `python tests/integration/wire_roundtrip.py` | PASS, exit 0; 59 fixture round trip Go/Rust/C++/Python, termasuk root/parent wire, term ID nol dan hash wajib hilang (`wire-roundtrip.log`, `wire-result.json`). |
| `go test ./src/server/...` | PASS, exit 0; seluruh paket Go sebelum perbaikan preflight budget (`go-all.log`). Tes database bersyarat tanpa DSN tidak dihitung sebagai integrasi nyata. |
| `cargo test --workspace --locked --lib` | PASS, exit 0; 159 tes ingestion + dua tokenizer, dua tes ingestion ignored sesuai prasyarat (`rust-library.log`). |
| `go test ./src/server/internal/domain ./src/server/internal/adapters/postgres -run LexicalDictionary -count=1 -v` dengan DSN disposable | PASS, exit 0; pin revisi historis, append/replay/concurrency, ekspor root dan penolakan truncation, parent closure serta budget (`budget-go-postgres.log`). |
| `go test ./src/server/internal/domain ./src/server/internal/adapters/postgres -count=1` setelah fix | PASS, exit 0; termasuk regresi budget gagal sebelum akses pool (`go-final.log`). |
| `cargo test --workspace --locked dictionary_artifact` setelah fix | PASS, exit 0; dua tes reader, bytes fixture bersama dan batas item (`budget-rust.log`). |
| `go vet ./src/server/internal/domain ./src/server/internal/adapters/postgres` | PASS, exit 0 (`go-vet-final.log`). |
| Tautan lokal Markdown terdampak | PASS (`documentation-links.json`). |
| Required kualitas, latency, throughput, RSS | NOT_MEASURED; tidak ada workload acceptance pada run ini. |

Expected/actual: builder Go menghasilkan bytes identik dengan fixture yang
dibaca Rust. Kedua reader menolak corpus berbeda, fingerprint salah, term tidak
terurut, parent yang tidak cocok/tidak disediakan, dan reassignment walau hash
mapping dihitung ulang dengan benar. Checked object memiliki data sendiri.
Exporter SQL membaca revisi yang dipin, tidak memotong hasil, dan menghasilkan
root yang tidak mengklaim ancestry historis. Fingerprint mapping berbeda dari
hash bytes artefak; `registry_revision` di sini adalah revisi lexical dictionary,
bukan canonical entity registry K01.

Reviewer independen `/root/verify_candidate_contract` membaca diff dan
menjalankan ulang pemeriksaan kompatibilitas serta tes Go/Rust terarah. Temuan
budget diperbaiki: generic wire menghitung `2*n+2` item untuk root dan `2*n+3`
untuk child, sehingga preflight sekarang menolak kapasitas sebelum SQL/sorting.
Regresi menguji batas tepat dan satu di bawahnya, serta pool nil untuk memastikan
kegagalan sebelum I/O. Reviewer menerima perbaikan dan tidak menemukan blocker
isi/schema pada cakupan ini. Reviewer tidak menjalankan PostgreSQL sendiri;
bukti integrasi database berasal dari run implementer di atas.

Pekerjaan berikut tetap terbuka: autentikasi bytes melalui storage, binding
otoritatif revisi PostgreSQL ke generation, katalog lineage, artefak
statistik/analyzer/build-plan, dispatch INDEX, publication/recovery dan pengujian
backend end-to-end. Batas byte wire diperiksa setelah mapping SQL dimuat;
batas term bukan jaminan peak RSS. Default wire budget memuat maksimal 49999 term
root; vocabulary lebih besar memerlukan konfigurasi eksplisit dan profiling.
Tidak ada target pada `configs/benchmark-targets.yaml` yang diubah atau diklaim
tercapai oleh fixture ini.
