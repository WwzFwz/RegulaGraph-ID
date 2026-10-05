# Verifikasi demo interview lokal

Laporan ini mencatat cakupan demo BM25 → model lokal → kutipan PDF pada
2026-10-05. Ia bukan kelulusan release, legal quality, atau target performa produksi.
Baseline kode sebelum perubahan: `32de78d2eb497355168156b446ebd79de28c8735`.
Raw log dan fingerprint perubahan berada di `artifacts/verification/20261005-interview-demo`.

## Hasil

| Pemeriksaan | Hasil dan batas |
| --- | --- |
| `go test ./src/server/...` | PASS, exit 0; tes yang membutuhkan backend opt-in tetap dapat skip. Bukan run seluruh backend produksi. |
| Tes retrieval, storage, generator, API preview | PASS: exact UTF-8 spans, ranking/OOV, cancellation, hash corruption, duplicate/path manifest, citation asing/kosong/duplikat, abstention, Host/Origin, JSON bounds. |
| `python -m unittest tests.unit.test_demo_corpus -v` | PASS, 2 tes, exit 0 setelah retry elevated untuk ACL TemporaryDirectory Windows; export mengikat semua file, menolak overwrite dan PDF korup. |
| Persiapan PDF nyata | 24 PDF, 1.061 halaman, 2.071 passage Go. 11 kandidat dilewati dan tercatat; bukan klaim seluruh corpus berhasil diproses. |
| Model lokal nyata | `regulagraph-demo:latest`, Qwen2.5 7B bobot lokal; profil konteks 8192, output 768. Dua smoke awal menghasilkan draft bersitasi. |
| Pertanyaan tenggat pemberitahuan kegagalan data | Draft menunjuk S1/Pasal 46/halaman 19. Retrieval 0,505 ms, generation 15.204,433 ms pada run ini. |
| Pertanyaan hak subjek data | Draft dengan 4 klaim/ID sumber valid; retrieval 0,504 ms, generation 18.924,782 ms. Bukan verifikasi semantik semua klaim/kelengkapan hak. |
| Smoke pertanyaan umum tentang kewajiban penyelenggara | Prompt awal mengubah tindakan pemeriksaan/pengawasan menjadi kewajiban penyelenggara. Hasil tetap disimpan dalam live-system.json; ini temuan kualitas, bukan PASS semantik. Prompt diperketat agar tidak membalik aktor/penerima dan abstain bila potongan tidak jelas. |
| Smoke setelah restart dengan prompt akhir | final-breach: generation 5.695 ms; final-notice: 5.629 ms; final-general: 8.869 ms. Semuanya draft dengan ID sumber valid. Kesalahan aktor spesifik tidak muncul dalam hasil ulang, tetapi kualitas semantik menyeluruh tetap NOT_MEASURED; jawaban umum masih terbatas konteks pelindungan anak. |
| PDF endpoint nyata | PASS: PDF sumber S1 dapat diambil, memiliki header PDF, dan SHA-256 sama dengan receipt. |
| Browser Edge headless | PASS desktop 1440×1100 dan mobile 390×844; nol JS exception, lima kutipan tampil, mobile tanpa overflow horizontal. Browser menjalankan pencarian nyata; screenshot jawaban memakai response model nyata yang disimpan, bukan generation tambahan. |
| Reviewer independen | `verify_candidate_contract`: membaca diff, menjalankan ulang tes preview dengan cache terpisah; tidak ada blocker terkonfirmasi dalam scope preview. |
| Kualitas/gold, latency p95/p99, benchmark required | NOT_MEASURED. Smoke satu laptop tidak memenuhi eligibility benchmark produksi. |

Hardware run: NVIDIA RTX 3060 Laptop 6 GiB; Ollama melaporkan model 5,4 GB,
21%/79% CPU/GPU saat smoke. PyMuPDF 1.26.4. SHA256SUMS sample:
`b550ae479bd0983223ea88a40f04808138b01c51b041b9cd49b49cd3fd4fc8b3`.
Versi toolchain dan diff fingerprint dicatat dalam artefak run.
Toolchain: Go 1.26.8 windows/amd64, Python 3.12.4, Ollama 0.32.1.

Reviewer menekankan bahwa SHA256SUMS adalah integrity pin lokal, bukan signed
provenance attestation; teks tidak diekstrak ulang dari PDF saat startup. Endpoint
CLI dapat dikonfigurasi ke remote HTTPS, sedangkan script/uji ini memakai loopback.
Tidak ada benchmark yang diturunkan. Deployment tidak dilakukan.

Reviewer follow-up menemukan bahwa script reuse tidak memuat ulang kode dan semula
dapat mengabaikan permintaan SearchOnly. Script sekarang menolak mismatch mode
serta menjelaskan tidak ada reload; daemon dihentikan dan dibangun/dijalankan ulang
sebelum final-smoke-summary.json. Perubahan kode berikutnya tetap memerlukan restart.

## Pekerjaan produksi yang ditunda

Pengguna mengalihkan prioritas ke demo runnable sebelum interview. Coordinator
INDEX, publikasi corpus penuh, graph/dense integration, dan acceptance tetap
mengikuti development-plan. Investigasi awal juga menemukan pemeriksaan ID fisik
artefak versus ID logis batch pada `indexing/initial_prepare.go` dan
`retrieval/hydration.go` yang perlu diperbaiki sebelum alur hasil Rust asli dipakai.
Perbaikan tersebut belum masuk perubahan demo; fixture Go produksi sebelumnya
menyamakan kedua ID sehingga belum membuktikan jalur itu.
