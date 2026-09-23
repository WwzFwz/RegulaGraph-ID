# Verifikasi X01: statistik BM25 lokal

Dokumen ini mencatat uji primitive statistik BM25 dari token hasil analyzer yang identitasnya dipin. Basis sebelum perubahan `da78379`; SHA-256 `src/ingestion/src/indexing/lexical.rs` adalah `532F754D16E96E4176AE74F826ECBFD696596FDFAC56DDC572ADBFCF4E5A6271`. Lingkungan Windows amd64, Rust/Cargo 1.87.0. Raw log disimpan di `artifacts/verification/x01-bm25-stats-20260924/cargo-test.log`; kompilasi awal memerlukan eksekusi di luar sandbox Windows. Log pipeline PowerShell berakhir exit 1 karena penanganan stderr, tetapi memuat hasil `120 passed`; rerun langsung `cargo test -p regulagraph-ingestion --lib --quiet` berakhir **exit 0**, `120 passed, 1 ignored`. Tes interop yang diabaikan mensyaratkan fixture terpisah dan tidak dihitung lulus.

| Pemeriksaan | Status | Expected dan aktual |
| --- | --- | --- |
| Tes statistik BM25 | PASS | Tiga tes mencakup update incremental vs rebuild lokal, idempotensi, token Unicode, analyzer drift, malformed ID/token, parameter nonfinite, dan query berlebih. |
| Seluruh tes library Rust | PASS, exit 0 | 120 lulus, 1 interop fixture diabaikan; tidak ada kegagalan. |
| Review independen | PASS dalam cakupan primitive lokal | Reviewer menemukan ID yang tidak sesuai `ascii_id` wire dan skor nonfinite pada parameter ekstrem; keduanya diperbaiki lalu tes dan kode ditinjau ulang. |
| Analyzer hukum, serialisasi IndexBatch, snapshot generation, writer/query backend, parity corpus, Recall@k, throughput/RSS | NOT_MEASURED | Analyzer/model/backend/gold dan artifact berversi belum tersedia; primitive ini belum menjadi indeks produksi. |

Statistik saat ini mutable dan tidak boleh dipakai oleh query snapshot lama setelah upsert; writer harus membekukan generation serta menyimpan analyzer/dictionary/statistics artifact dan formula skor yang sama untuk query. Uji berikutnya yang bermakna adalah rangkaian perubahan deterministik yang membandingkan skor dengan rebuild, golden analyzer untuk nomor peraturan/Unicode, lalu pengukuran corpus dan target required tanpa mengubah `configs/benchmark-targets.yaml`.
