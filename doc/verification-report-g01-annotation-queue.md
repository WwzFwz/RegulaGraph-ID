# Verifikasi G01: antrean kandidat anotasi

Dokumen ini merekam hasil persiapan antrean PDF untuk anotasi manusia. Cakupannya adalah integritas
inventory-ke-antrean dan determinisme pemilihan, bukan penilaian legal, gold dataset, atau acceptance
benchmark. Kode diperiksa pada parent revision `7d1f932` dengan SHA-256
`tooling/corpus/prepare_gold_queue.py` = `0c66a670c819549c07ba29e96189a94d0e5a9627834b54d9942eb7c3ff7a47b8`
dan `tests/unit/test_g01_annotation_queue.py` =
`c6292c83702197e041971deaa5c87a1c230b8abc0fe3de71dc1edd6b1eb7fb13`.

## Input, output, dan hasil

Run 2026-09-24 memakai Python 3.12.4, pytest 9.0.1, inventory ID
`0dff5a00c9344bbc84d11b58de9c80ba0de3cbe900747ab7ab5dc3a580576b8e`, dan
`inventory.records.jsonl` SHA-256
`47d6f1d9c604783961ca27db8afb8707c33ce1800bd108508109bfe6bfb91de1`.
Perintah `python -m tooling.corpus.prepare_gold_queue --inventory data/acquisition/inventory.json
--output artifacts/g01-annotation-queue-20260924` exit 0. Antrean berisi 650 PDF primary unik:
568 BPK dan 82 Kemkomdigi; JDIHN belum memiliki PDF valid dalam inventory lokal. Hash `queue.jsonl`
adalah `821ae690ed8e6fea2351f9599891ea584d1b0ff41d4d7b26493a6743ea354a9d`, sama
dengan manifest. Output lokal diabaikan Git dan berstatus `CANDIDATES_ONLY`, `UNREVIEWED`.

| Pemeriksaan | Expected dan aktual | Status |
| --- | --- | --- |
| `python -m pytest tests/unit/test_g01_annotation_queue.py -q --basetemp=.cache/pytest-g01-final -p no:cacheprovider` | Dua tes determinisme, penolakan overwrite, dan perubahan records lulus; exit 0; raw `pytest.log` | PASS |
| Audit inventaris independen | Reviewer membandingkan 650 SHA unik dengan semua PDF primary eligible, memeriksa hash queue dan hitungan portal; semuanya cocok | PASS untuk antrean lokal |
| `git diff --check` | Tidak ada kesalahan whitespace; exit 0, dengan peringatan line ending Windows | PASS |
| Byte PDF, snapshot, label, split, dan review hukum | Tool tidak membaca ulang blob, membekukan snapshot, atau membuat GoldQuestion; audit D01 adalah sumber integritas blob terdahulu | NOT_MEASURED |
| Target kualitas/latency required | Belum ada label manusia dan workload produksi lengkap | NOT_MEASURED |

Raw log tes dan manifest pemeriksaan berada di `artifacts/verification/g01-annotation-queue-20260924/`
serta `artifacts/g01-annotation-queue-20260924/`. Reviewer independen melaporkan tidak ada temuan
blocking untuk antrean kandidat; keterbatasan audit ulang blob dan label tetap terbuka sebagai tahap
G01 selanjutnya. Antrean tidak boleh dipakai sebagai gold test atau dihitung sebagai G01 selesai.
