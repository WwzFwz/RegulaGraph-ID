# Verifikasi kontrak kandidat RESOLVE

Dokumen ini mencatat pemeriksaan boundary EXTRACT menuju RESOLVE untuk kontrak kandidat registry yang dibaca sekaligus per batch. Bukti mentah berada di `artifacts/verification/k01-candidate-handoff-20260923/`; laporan ini tidak mengklaim RESOLVE produksi atau kelulusan benchmark.

## Revisi dan cakupan

Pada 2026-09-23, basis commit `5f962ca67bbcce0e50af456373f02e83d4b2c5f9`; fingerprint SHA-256 file utama: `graph.proto` `010320F86720A685749797FE1FBB50F2C594DD7A1454546D0A4CF77C26BAFFFB`, `schema-lock.json` `CADCE36514393FC3F588BE4C44D11CA6525C8E0E3AD990301B0684EB9D6B8CA9`, `candidate_validation.go` `4FBF08E01BCE56B8B1327AE7B2667415E43BC25DE1CFF3536322B3CC3EB59CEA`, dan tesnya `4976E2AC908C40FB0527B11F6F1023AA14E0B895CB82D4A516B69CDC1669AD56`. Kontrak baru bersifat aditif terhadap baseline C01; schema lock diperbarui setelah review kompatibilitas.

## Hasil yang dijalankan

| Pemeriksaan | Perintah | Exit | Hasil |
| --- | --- | ---: | --- |
| Tes Go seluruh server | `go test ./...` dari `src/server` | 0 | PASS; log `go-test.log` |
| Analisis statis Go | `go vet ./...` dari `src/server` | 0 | PASS; log `go-vet.log` |
| Kompatibilitas kontrak | `python scripts/check_contracts.py` | 0 | PASS, 162 message/31 enum/4 service; log `schema-check.log` |
| Binding Rust | `cargo check -p regulagraph-ingestion --bins --locked --offline` | 0 | PASS; log `rust-check.log` |

Lingkungan: Go 1.26.8 windows/amd64, Rust 1.87.0, Python 3.12.4. Tes memakai fixture sintetis domain, tanpa database atau gold set. Pemeriksaan Rust pertama dalam sandbox gagal sebelum `rustc` berjalan karena akses OS ditolak; rerun di lingkungan yang diizinkan lulus dan menjadi hasil yang dilaporkan.

## Review independen dan batas pembuktian

Agent verifier `verify_candidate_contract` memeriksa diff dan menjalankan pemeriksaan kontrak/domain/Rust. Temuan awal: hasil kandidat gabungan tidak dapat membuktikan lookup kosong per scope, daftar dependency/support tidak masuk batas kerja, serta hasil satu scope dapat berbeda antar mention. Ketiganya diperbaiki bersama tes tandingan; benturan key dengan byte NUL juga ditutup memakai key bertipe struct. Review akhir menyatakan **tidak ada blocker dalam cakupan schema aditif dan validator Go**.

Validator menjaga coverage mention, konteks/sumber, candidate type dan canonical scope, konsistensi hasil/revisi lookup per scope, dan batas referensi. Ia **tidak** membuktikan bahwa scope ID/key berasal dari query registry sebenarnya, visibilitas revisi PostgreSQL, atau kebenaran kandidat hukum. Pembaca registry dan konsumen RESOLVE produksi belum memiliki callsite; pengujian database nyata, kualitas candidate recall/false merge, p95/p99, serta gate release pada `configs/benchmark-targets.yaml` tetap **NOT_MEASURED**. Boundary input wajib membatasi bytes/depth/items melalui `DecodeWire` dan producer wajib mencatat revision/snapshot yang dibaca secara tepercaya sebelum artefak dipersist.
