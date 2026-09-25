# Verifikasi provenance chunk untuk input native INDEX

Dokumen ini merekam pemeriksaan bridge X01 dari `DocumentBatch` CHUNK ke
`TextItem` embedding dan penyamaan proyeksi bukti dengan EXTRACT pada
2026-09-26. Basis source sebelum perubahan adalah commit
`b127455f1f5552c0dfdcb69c7ad84fd883617d58`; log mentah berada di
`artifacts/verification/20260926-x01-provenance-items/`. Lingkungan Windows
amd64, Rust/Cargo 1.87.0. Fixture utama adalah batch CHUNK sintetis yang
dibangun melalui worker, bukan gold corpus regulasi atau index backend hidup.
Implementasi bridge tercatat pada commit `57ed48d`; perubahan sesudah review
independen pada file kode hanya pembaruan komentar status/header.

| Pemeriksaan | Expected dan aktual | Status |
| --- | --- | --- |
| `cargo test -p regulagraph-ingestion --offline` | EXTRACT/INDEX berbagi source blob, provision version, regulation, span, dan halaman; 156 unit + 1 integrasi PDF lulus, 2 opt-in ignored. `cargo-test.log`, exit 0. | PASS |
| Mutasi referensi dan cakupan | Raw text ref yang menggantikan normalized ref, span versi dari artefak lain, versi tak mencakup chunk, node pemilik asing, serta halaman palsu ditolak pada fixture CHUNK. | PASS |
| Review independen bridge | Reviewer read-only mengulang empat probe adversarial serta chunk lintas halaman/batas end-exclusive. Semua diterima atau ditolak sesuai invariant; `reviewer-probes.log` mencatat perintah, fingerprint, output, exit 0, dan batas pembuktian. Tidak ada blocker tersisa dalam scope bridge. | PASS |
| `rustfmt --edition 2021 --check` pada empat file Rust terdampak; `git diff --check` | Keduanya exit 0. `cargo fmt --all -- --check` gagal karena format lama pada `src/inference/tokenizer/lib.rs`, di luar perubahan ini; file tersebut tidak diubah. | PASS untuk file terdampak |
| Pipeline INDEX, registry generation, Qdrant dan target required | Belum dipanggil oleh worker INDEX atau diterbitkan ke backend. Model/corpus gold dan load run tidak termasuk fixture. | NOT_MEASURED |

`ChunkProvenanceIndex` memvalidasi objek batch lengkap satu kali, membuat
lookup ID, lalu memproyeksikan versi dan halaman per chunk. Versi harus
menunjuk `normalized_text_ref` artefak primer dan semua span versinya harus
berasal dari artefak yang sama. Node pemilik harus mencakup span chunk,
rantai parent harus cocok, serta nomor halaman harus ada pada artefak
sumber. Locator keluaran dibatasi pada halaman sukses yang overlap chunk;
halaman ditemukan dengan pencarian batas pada `PageResult` berurutan.
Jika node tidak menyimpan locator, hanya locator tingkat halaman tanpa
bounding box yang disintesis dari `PageResult`.

`VerifiedIndexInputs::prepare_selected` menghasilkan item native berikut
hash render dan key reuse generation-bound secara all-or-nothing. Pemanggil
masih wajib mengautentikasi byte batch, mem-pin corpus/job dan membership
snapshot. Keberhasilan suite ini tidak membuktikan kualitas hukum, akurasi
embedding, Recall@k, latency p95/p99, atau kesiapan publikasi. Target numerik
tetap **REQUIRED_UNMEASURED** menurut `configs/benchmark-targets.yaml`.

## Preflight generation sebelum pembacaan artefak

Perubahan susulan pada commit `d2d0c4d` memisahkan validasi corpus, policy input, dan seluruh field
model pada `reuse.rs` agar `prepare_selected` dapat menolaknya sebelum
`ReadVerified`/normalisasi TextArtifact. Cancellation yang sudah aktif tetap
memiliki prioritas pertama. Key reuse dari byte/model yang valid tidak berubah;
validasi hash byte render tetap berjalan per item setelah I/O. Fixture
menyuntik normalizer yang salah untuk membuktikan generation/policy dan model
yang salah gagal lebih dulu, serta menguji cancellation. Log implementer ada
di `artifacts/verification/20260926-x01-generation-preflight/cargo-test.log`:
`cargo test -p regulagraph-ingestion --offline`, exit 0, 156 unit dan 1
integrasi PDF lulus, 2 opt-in ignored. `rustfmt --edition 2021 --check`
pada file terdampak dan `git diff --check` exit 0. Review independen menguji
key before/after pada 32 input valid serta enam lokasi unknown field;
empat tes reuse dan fixture cancellation/urutan error juga lulus. Lognya ada
di `artifacts/verification/20260926-x01-provenance-items/reviewer-preflight.log`
dengan fingerprint stabil dan exit 0. Hasil fixture tidak mengukur
penghematan I/O atau latency corpus nyata.
