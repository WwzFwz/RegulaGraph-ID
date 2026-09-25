# Verifikasi boundary dense Rust dan indeks payload Qdrant X01

Dokumen ini merekam bukti untuk dua subkomponen X01 dari basis `366c314`
sampai `b8cc75a`, bukan menyatakan X01 atau profil release selesai. Raw log
implementer berada di `artifacts/verification/20260925-x01-dense-payload/`
(diabaikan Git). Lingkungan: Windows amd64, Rust `rustc 1.87.0`, Go
`go1.26.8`; fixture Rust dan HTTP lokal, tanpa server Qdrant hidup.

| Pemeriksaan | Expected dan actual | Status |
| --- | --- | --- |
| `cargo test -p regulagraph-ingestion --offline` | Dense batch menolak respons partial/model drift/truncation, mempertahankan urutan, cancellation, dan klasifikasi error; 147 unit + 1 PDF integration lulus, 2 ignored. Log `cargo-test.log`. | PASS, exit 0 |
| `go test -count=1 ./...` dari `src/server` | Semua paket Go lulus, termasuk create/readback index payload, schema salah, range dimatikan, collection lama tidak lengkap, dan respons tanpa index. Log `go-test.log`. | PASS, exit 0 |
| `go vet ./internal/adapters/qdrant`, `rustfmt --edition 2021 --check src/ingestion/src/indexing/dense.rs`, `git diff --check` | Pemeriksaan statis/format terhadap perubahan lulus. | PASS, exit 0 |
| Review independen read-only | Verifier membuat probe dense untuk cancellation/error classification dan probe Qdrant untuk `range=false` serta count kosong. Temuan awal direproduksi, diperbaiki, dan probe ulang lulus. | PASS untuk boundary yang diperiksa |
| Qdrant hidup, native RPC lewat worker INDEX, publication, replica/read-route, kualitas retrieval, target numerik | Belum dijalankan pada alur produksi dan workload referensi. | NOT_MEASURED |

Dense builder hanya mengolah satu batch maksimum 128 item/2 MiB teks yang
sudah diverifikasi oleh pemanggil; keberadaan source/span pada request belum
membuktikan autentikasi byte maupun membership snapshot. Seluruh item harus
berhasil sebelum vektor dikembalikan. Cancellation menjatuhkan future RPC,
namun tesnya memakai client pending sintetis, bukan layanan native hidup.

`EnsureCollection` sekarang mensyaratkan indeks payload untuk corpus,
generation, batas sequence dan versi pasal sebelum adapter siap. Integer
index dengan `range=false` ditolak. Indeks yang kurang hanya dapat dipasang
pada collection yang baru dibuat oleh pemanggilan tersebut; Qdrant melaporkan
`points_count` perkiraan sehingga metadata nol tidak dipakai untuk mengizinkan
perbaikan collection lama. Pemanggil harus menguasai bootstrap secara
eksklusif lintas proses; mutex adapter sendiri tidak cukup. Bootstrap parsial
memerlukan recovery/rebuild eksplisit.

Review ini tidak membuktikan ID/reuse key, artefak build terpin,
`IndexBatch` dari worker, allocator, mutation ledger, operasi closure,
readiness seluruh point/route, atau publication. Target required dalam
`configs/benchmark-targets.yaml` tetap **REQUIRED_UNMEASURED**.
