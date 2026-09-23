# Verifikasi paket triase PDF G01

Dokumen ini mencatat pemeriksaan alat offline yang menyiapkan sampel PDF untuk review manusia. Cakupannya hanya integritas sumber, pemilihan deterministik, keamanan daftar baca, dan batas status; ini bukan bukti gold dataset atau kualitas hukum/model.

## Input, hasil, dan reproduksi

- Tanggal: 2026-09-24. Base revision: `72c1d4c0fc50ccb7963e9397f513260299cb5ed3`; fingerprint final file kode dan tes tersimpan di `artifacts/verification/g01-review-packet-20260924/file-hashes.txt`.
- Toolchain: Python 3.12.4. Input nyata: `data/acquisition/inventory.json` (`inventory_id=0dff5a00c9344bbc84d11b58de9c80ba0de3cbe900747ab7ab5dc3a580576b8e`) dan `artifacts/g01-annotation-queue-20260924/queue.jsonl` (SHA-256 `821ae690ed8e6fea2351f9599891ea584d1b0ff41d4d7b26493a6743ea354a9d`).
- `python -m pytest tests/unit/test_g01_review_packet.py -q -p no:cacheprovider --basetemp=.cache/pytest-g01-review-packet`: exit 0, **4 passed**. Raw log: `artifacts/verification/g01-review-packet-20260924/pytest.txt`. Kasus mencakup determinisme, overwrite, perubahan byte PDF, metadata/path palsu, judul berisi HTML/Markdown, dan penghilangan baris antrean.
- `python -m tooling.corpus.prepare_review_packet --queue artifacts/g01-annotation-queue-20260924 --corpus-root data/acquisition --output artifacts/g01-review-packet-20260924-v3`: exit 0. Raw log: `artifacts/verification/g01-review-packet-20260924/real-corpus.txt`. Hasil 6 PDF, satu per portal/ukuran yang tersedia; SHA-256 `packet.jsonl` adalah `733d3f9b4a291c1d353d47c09cfdf3964726bc5717ddd443f92bca66e4ea1b2b`.
- `git diff --check`: exit 0. Output artefak berada di `artifacts/` yang diabaikan Git; PDF tidak disalin ke laporan.

## Review independen dan batas klaim

Agent verifikasi read-only menghitung ulang hash antrean/paket dan hash serta ukuran keenam PDF. Ia menemukan dua celah: antrean self-consistent belum diikat ulang ke inventory dan judul scrape dapat menyisipkan Markdown/HTML. Setelah perbaikan, ia menemukan kemungkinan antrean menghilangkan baris. Pemeriksaan ulang final menyatakan perbandingan himpunan antrean dengan kandidat inventory dan empat tes regresi menutup temuan tersebut; tidak ada blocker tersisa dalam cakupan alat triase.

Status output adalah `SOURCE_TRIAGE_ONLY`. Metadata tahun/judul tetap berasal dari sumber dan belum disahkan sebagai fakta hukum. JDIHN belum ada dalam inventory lokal; review halaman, anotasi struktur/versi/relasi, second review, adjudikasi, frozen snapshot/split, kualitas model, serta target benchmark tetap **BLOCKED/NOT_MEASURED**. Paket ini tidak boleh dipakai sebagai gold atau acceptance run B01.
