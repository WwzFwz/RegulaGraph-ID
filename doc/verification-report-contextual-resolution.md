# Verifikasi proposal resolusi model kontekstual

Dokumen ini mencatat bukti paket gateway RESOLVE, client gRPC, serta workflow hidrasi/audit/replay pada 2026-09-24. Scope adalah proposal model dari kandidat terverifikasi, bukan penyelesaian seluruh K01, keputusan hukum, atau acceptance performa. Dasar implementasi sebelum perubahan adalah commit `84ca04a`; rangkaian implementasi berakhir pada `1d059ad` (kontrak `b748434`, gateway `6cd224a`, workflow `8be3587`, konfigurasi `1d059ad`); input fixture sintetis, model provider deterministic, dan tidak ada deployment.

## Identitas dan bukti

Raw log berada pada `artifacts/verification/20260924-contextual-resolution/`. Fingerprint workflow `semantic_resolution_model.go`: SHA-256 `384bb0c584189a6d01660bc5c7a9332f823fac84a28a6a1f6153087423a06468`. Fingerprint gateway `semantic_resolution.go`: SHA-256 `848360edc230d9457b727dfe3f924c7e011d21330cadbe584976a62ea0e49ca9`.

Toolchain: Go 1.26.8 windows/amd64, Rust 1.87.0, Python 3.12.4, protoc 34.1, dan MSBuild 16.11.2/Visual Studio 2019. Go cache diarahkan ke workspace. Rust/C++ memerlukan eksekusi di luar sandbox karena compiler semula ditolak oleh lingkungan; rerun menggunakan dependency lokal/offline berhasil.

| Pemeriksaan | Hasil aktual | Status |
| --- | --- | --- |
| `go test -count=1 ./src/server/...` | Seluruh package berhasil, exit 0; test DB opsional tidak menjadi bukti DB nyata | PASS |
| `go vet ./src/server/...` | Exit 0 | PASS |
| `cargo test --workspace --locked --offline` | 129 library tests passed, 1 ignored; 1 integration test passed; exit 0 | PASS untuk test yang dijalankan |
| Build C++ contract probe Release | Library/probe berhasil dibangun, exit 0 | PASS |
| `python scripts/check_contracts.py` | 162 messages, 31 enums, 4 services; baseline lama tidak diubah, exit 0 | PASS |
| `python tests/integration/wire_roundtrip.py` | 47 synthetic fixtures Go/Rust/C++/Python; termasuk context/rationale/supporting IDs; exit 0 | PASS |
| Review independen `verify_model_resolution` | Membaca diff dan menjalankan targeted Go tests; seluruh temuan diperbaiki | PASS dalam scope paket |
| Model lokal nyata, false merge/split, candidate recall, latency/throughput/RSS | Model/backend/hardware/gold belum dipin | NOT_MEASURED |
| Dispatch RESOLVE daemon, automatic candidate planning, bukti kandidat lintas dokumen, auto approval/CREATE/MERGE/SPLIT | Belum diimplementasikan dalam paket ini | Belum selesai |

## Kasus penting dan temuan

Fixture memeriksa input tanpa konteks, corpus/type/revision salah, exact UTF-8 mention bytes, provenance, LINK target buatan, DEFER lengkap/hasil lookup kosong, field model hilang/null/duplikat, serta citation konteks buatan. Cancellation menghentikan kerja dan tidak meracuni retry; bounded admission, concurrent coalescing, eviction, clone isolation, partial retry, dan client/server gRPC diperiksa.

Workflow diuji dengan artefak EXTRACT/document/text dan gateway implementasi sebenarnya, memakai provider deterministic. Request/response tersimpan, retry tidak memanggil provider, dependency yang belum tersimpan akibat crash dipulihkan, serta perubahan registry sebelum/sesudah model ditolak. Respons dengan model/item/source/context palsu tidak diteruskan sebagai proposal siap commit.

Reviewer menemukan ekspansi teks sebelum pembatasan, label versi konteks yang mengikuti mention, budget replay yang berbeda dari run pertama, dan JSON model ambigu. Perbaikan mencadangkan seluruh teks/metadata/candidate sebelum clone, mempertahankan provenance chunk, membatasi audit reads secara terpisah, dan memeriksa key wajib/non-null/unik. Pemeriksaan lanjutan menambahkan regresi canonical besar yang dipakai berulang; dua referensi ke canonical 256 KiB ditolak pada budget 300 KiB sebelum clone kedua. Semua regression tests lulus. Reviewer melakukan review ulang dan tidak menemukan blocker tersisa dalam scope paket.

Rationale bukan bukti legal atau review approval. Proposal masih melewati registry fence/review yang ada. Status dan integrasi berikutnya dijelaskan pada [semantic-resolution.md](semantic-resolution.md); target required tetap mengikuti [benchmark policy](benchmark-policy.md).
