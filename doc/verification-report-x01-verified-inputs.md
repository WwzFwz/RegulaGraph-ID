# Verifikasi input indeks terikat sumber dan key reuse embedding X01

Dokumen ini merekam boundary library X01 pada 2026-09-26, bukan kelulusan
milestone X01. Raw log berada di
`artifacts/verification/20260926-x01-verified-inputs/cargo-test.log`
(diabaikan Git). Lingkungan Windows amd64, Rust `rustc 1.87.0`.
Fingerprint SHA-256 yang direview independen: `inputs.rs`
`77E7EE85BBEAE57BC8F26DEFC05188190EDF9AD3DBC6BC8C83396784638B63BA`,
`loading.rs`
`492DE98A9B2A90EC27F1C8187BB1DA48B2940EE6F2B26099993EA6C79C62E2F2`,
dan `reuse.rs`
`FF68C77478A734CACEA997FD5B0AE382B9CB518151DF857102A92355FF03DE6E`.

| Pemeriksaan | Expected dan aktual | Status |
| --- | --- | --- |
| `cargo test -p regulagraph-ingestion --offline` | 155 unit dan 1 PDF integration lulus; 2 tes opt-in diabaikan. Exit 0. | PASS |
| Review independen renderer dan loader | 5 tes renderer, 1 integrasi CHUNK→loader, dan probe artefak nyata lulus. Duplicate, corpus asing, batch incomplete, batas item/byte, serta normalized bytes rusak ditolak. | PASS pada boundary library |
| Review independen key reuse | 3 tes dan 3 probe tambahan lulus. Seluruh field manifest v1 terikat, unknown field nested ditolak, encoding field tidak ambigu, dan cache hanya mengulang vektor. | PASS pada boundary key |
| Worker INDEX→native→IndexBatch→Qdrant, benchmark required dan gold retrieval | Belum dijalankan pada alur produksi dan workload referensi. | NOT_MEASURED |

Policy render berubah menjadi `structure-labels-v1`: teks primer chunk tetap
eksak, sedangkan konteks menyertakan label root-to-parent dan node pemilik
non-DOCUMENT. Dengan ini body Pasal yang terpisah dari judulnya tetap membawa
nomor Pasal ke model. Perubahan policy wajib memutus reuse embedding generasi
sebelumnya. Key reuse juga mengikat corpus, byte hasil render, dan seluruh
identitas model v1; source refs, status hukum, dan visibility harus diproyeksikan
ulang dari batch baru.

`VerifiedIndexInputs::new` memvalidasi objek `DocumentBatch`, tetapi pemanggil
tetap harus membaca batch melalui `load_document_batch`/`ReadVerified` dan
mem-pin corpus/job. `load_selected` memverifikasi TextArtifact dan mapping
normalisasi, menjaga urutan seleksi, serta membatasi output 128 item/2 MiB.
Satu pembacaan dan normalisasi artefak yang sedang berjalan belum dapat
diinterupsi; batas output bukan batas I/O maupun peak RSS. Target dalam
`configs/benchmark-targets.yaml` tetap **REQUIRED_UNMEASURED**.
