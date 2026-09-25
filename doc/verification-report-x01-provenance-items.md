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

## Gate halaman sumber pada preflight Go

Commit `36ce974` membuat `NewIndexSourceView` memeriksa locator node pemilik untuk setiap
chunk yang akan diindeks: source blob harus milik TextArtifact chunk,
nomor halaman harus ada dan sukses, serta rentang teks halaman nyata harus
overlap rentang node pemilik. Generic `ValidateDocumentBatchClosure` sudah
menolak `version.text_ref` yang bukan teks normalisasi dan versi lintas
artefak; audit membuktikan validasi Go itu tidak perlu diduplikasi.

Kasus nomor halaman 9999 dan halaman dengan dua span yang memiliki gap
ditambahkan sebagai regresi. Rentang node serta halaman diurutkan dan
digabung sekali per identitasnya; pasangan rentang dicek dengan binary
search sambil mengiterasi slice yang lebih pendek. Ini menghindari scan
halaman berulang per chunk atau per node, tetapi biaya p95/peak RSS tetap
harus diukur pada PDF besar. Log `go test -count=1 ./...` dan
`go vet ./internal/domain` berada di
`artifacts/verification/20260926-x01-go-page-locators/`, keduanya exit 0.
Review independen berstatus PASS tanpa blocker: 13 probe boundary,
512 kasus oracle interval termasuk simetri, 166 leaf tests domain, dan
`go vet` lulus. Raw output, fingerprint, perintah, serta exit code ada di
`artifacts/verification/20260926-x01-go-page-locators/reviewer.log`.
Probe mencakup locator multi-halaman, halaman tidak ada/gagal,
source asing, span bergap/kosong, serta batas end-exclusive. Ini masih
preflight library; snapshot membership, publication Qdrant, kualitas
retrieval, dan benchmark required **NOT_MEASURED**.
