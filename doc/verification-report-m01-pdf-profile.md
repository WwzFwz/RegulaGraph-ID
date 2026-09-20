# Verifikasi profil dan pembanding engine PDF M01

Dokumen ini mencatat bukti eksperimen parser PDF teks pada 2026-09-20. Perannya mengikat inventory D01,
sampling, versi engine, hasil mentah, regression test, dan review agent independen sebelum implementasi I01.
Hasil ini memilih kandidat implementasi; ia belum membuktikan kualitas struktur hukum, OCR, source mapping,
peak RSS, atau gate parsing produksi. Seluruh gate required terkait tetap **REQUIRED_UNMEASURED**.

## Revision dan artefak

Profiler awal berada pada commit `f2cdbb8`, hardening integrity/publication pada `08f2f9f`, pembanding MuPDF
pada `a12558e`, dan pembanding PDFium pada `95a7cdd`. Regression test terkait berada pada commit `78a534c`,
`cf8b180`, `be8adcd`, dan `f1181c2`.

Raw run disimpan lokal dan diabaikan Git:

- `artifacts/m01/pdf-profile-pdfium-120-bound/`;
- `artifacts/m01/pdf-profile-pymupdf-120-bound/`;
- `artifacts/m01/pdf-profile-smoke-pypdf-180/`.

Run final PDFium dan MuPDF mengikat inventory ID
`0dff5a00c9344bbc84d11b58de9c80ba0de3cbe900747ab7ab5dc3a580576b8e`, seed
`m01-pdf-profile-v1`, hash konfigurasi, hash JSONL hasil, versi binding, dan versi core native bila berlaku.
Run pypdf terdahulu mengikat inventory, versi binding, konfigurasi, dan hash hasil sebelum field versi core
ditambahkan; pypdf tidak memiliki core native terpisah. Target pada `configs/benchmark-targets.yaml` tidak diubah.

## Kondisi dan metode

Run berlangsung pada Windows NT 10.0.26200.0, Python 3.12.4, arsitektur AMD64 Family 25 Model 68 dengan
16 logical processor yang terlihat oleh proses. Model CPU lengkap, RAM, kondisi power, cache filesystem,
peak RSS, dan pengulangan statistik belum tersedia, sehingga hasil tidak boleh diperlakukan sebagai benchmark
hardware referensi.

Sampler memilih 120 PDF secara deterministik dari record primary yang integrity-valid dan membagi kandidat
menurut portal serta ukuran. Distribusi sampel adalah 63 BPK dan 57 JDIH Kemkomdigi; 43 file di bawah 1 MiB,
43 file 1--10 MiB, dan 34 file setidaknya 10 MiB. JDIHN belum masuk karena inventory saat ini belum memiliki
PDF sukses dari portal tersebut. Setiap dokumen berjalan dalam subprocess dengan delapan worker dan timeout
120 detik. Hash konten diverifikasi sebelum parser dijalankan; error dan timeout tetap denominator.

## Hasil aktual

| Engine | Versi binding / core | Dokumen sukses | Halaman | Elapsed | Throughput profiler |
| --- | --- | ---: | ---: | ---: | ---: |
| PDFium | pypdfium2 4.30.0 / PDFium 126.0.6462.0 | 120/120 | 27.486 | 63,349 s | 433,880 halaman/s |
| MuPDF | PyMuPDF 1.26.4 / MuPDF 1.26.7 | 120/120 | 27.486 | 70,467 s | 390,056 halaman/s |
| pypdf | pypdf 5.6.0 | 23/24 | 1.540 terhitung | 183,377 s | 8,398 halaman/s |

Run pypdf memakai smoke sample 24 dokumen dan timeout 180 detik. Satu PDF BPK berukuran 42,5 MB tetap
timeout; PDFium dan MuPDF membaca dokumen tersebut sebagai 2.003 halaman. Karena denominator sampel berbeda,
angka pypdf hanya bukti tail failure baseline dan bukan rasio speedup formal terhadap run 120 dokumen.

Distribusi service time pada run 120 dokumen:

| Engine | Page p50 | Page p95 | Page p99 | Document p50 | Document p95 | Document p99 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| PDFium | 3,236 ms | 17,341 ms | 54,865 ms | 141,073 ms | 4.469,115 ms | 7.600,526 ms |
| MuPDF | 6,368 ms | 33,845 ms | 65,852 ms | 346,797 ms | 11.575,871 ms | 17.411,210 ms |

Throughput profiler mencakup verifikasi hash, startup subprocess, dan ekstraksi teks/image indicator. Definisi
`PARSING.TEXT_THROUGHPUT` juga mewajibkan normalisasi, chunking, serta source mapping pada workload referensi.
Karena tahapan itu belum aktif dan peak RSS belum diukur, angka di atas tidak dinyatakan sebagai PASS target.

## Pemeriksaan fidelity awal

PDFium dan MuPDF memilih 120 SHA-256 yang sama. Keduanya melaporkan page count identik pada 120/120 dokumen,
kelas heuristic identik pada 120/120, serta karakter teks identik pada 109/120. PDFium menghasilkan total 872
karakter lebih banyak dari sekitar 29,14 juta karakter, dengan delta per dokumen -81 sampai +984. Sampel
terbagi menjadi 93 text candidate, 19 scan candidate, dan 8 mixed candidate.

Kesamaan count bukan bukti reading order, ketepatan pasal/ayat, tabel, negasi, nomor, atau offset sumber.
Perbedaan 11 dokumen harus masuk anotasi G01 dan perbandingan struktur sebelum parser diterbitkan.

## Lisensi dan keputusan kandidat

PDFium dipilih sebagai kandidat utama untuk prototype I01 karena pada run ini lebih cepat, tidak timeout,
memiliki hasil count yang nyaris sama dengan MuPDF, dan jalur lisensinya lebih cocok untuk distribusi produk.
Dokumentasi resmi pypdfium2 menyatakan binding tersedia dengan Apache-2.0/BSD-3-Clause dan PDFium memakai
lisensi bergaya BSD, dengan kewajiban membawa notice dependency pada distribusi binary
([pypdfium2 licensing](https://github.com/pypdfium2-team/pypdfium2/blob/main/README.md#licensing)). Dokumentasi
PyMuPDF menyatakan pilihan GNU AGPL atau lisensi komersial
([PyMuPDF FAQ](https://pymupdf.readthedocs.io/en/latest/faq/index.html)). Ini bukan nasihat hukum; build native
tetap harus menghasilkan software bill of materials dan third-party notices yang ditinjau sebelum release.

MuPDF dipertahankan sebagai pembanding diagnosis untuk gold set, bukan dependency runtime yang otomatis ikut
didistribusikan. pypdf tidak dilanjutkan sebagai parser produksi karena timeout nyata dan tail latency buruk.
Keputusan PDFium tetap bersyarat pada gold parsing, source-coordinate mapping, peak RSS, malformed corpus,
dan benchmark pipeline Rust lengkap.

## Review independen

Agent `verify_c01` memberi PASS pada working tree final. Test resmi berjumlah 11/11 PASS dan overlay PDFium
19/19 PASS. Review mencakup PDF text/image/blank/malformed/encrypted, timeout subprocess, forwarding pilihan
engine, identity/config/result binding, cleanup resource pada success/error, probe 100 pembacaan, path/hash
confinement, serta atomic publication. Review sebelumnya juga menutup stale inventory ID, boolean integrity
palsu, timeout non-finite, overwrite, dan concurrent publication race.

Batas review adalah fixture sintetis dan fault injection tidak membuktikan kualitas regulasi atau bebas memory
leak native. Uji kehilangan daya dan perubahan input secara bersamaan juga belum dilakukan.

## Pekerjaan berikutnya

I01 harus mengikat PDFium C API ke Rust dengan lifetime dan ownership eksplisit, menghasilkan teks/block/
coordinate per halaman, mempertahankan mapping byte UTF-8 raw-normalized, dan mengarantina kegagalan parsial.
G01 harus melabeli contoh text/scan/mixed, multi-column, tabel, pasal/ayat, nomor, negasi, serta 11 dokumen yang
berbeda count karakter. OCR dipilih melalui eksperimen terpisah; scan candidate tidak boleh diterbitkan sebagai
teks kosong yang seolah lengkap.
