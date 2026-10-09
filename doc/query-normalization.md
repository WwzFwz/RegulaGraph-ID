# Normalisasi pertanyaan dan kontrak integrasinya

Dokumen ini menjelaskan policy query Q01 yang tersedia, cara mengaktifkannya,
input/output, serta batas bukti. Normalisasi merupakan persiapan retrieval dalam
Go; tidak memanggil model dan tidak mengubah corpus atau indeks.

## Policy operator

`REGULAGRAPH_QUERY_NORMALIZATION` berlaku pada CLI `query-evidence` dan API.
Nilai kosong atau `original-v1` mempertahankan pertanyaan persis seperti input.
`mechanical-v1` menggabungkan whitespace Unicode di luar kutipan menjadi satu
spasi, membuang whitespace luar, dan menerapkan NFC kanonis di luar kutipan.
Nilai lain ditolak sebelum membuka backend. Restart API setelah mengubah policy.
Mode versi masuk fingerprint konfigurasi query; perubahan aturan memerlukan
versi policy baru. Ini tidak mengganti analyzer/generation BM25 pada indeks.

```powershell
$env:REGULAGRAPH_QUERY_NORMALIZATION = 'mechanical-v1'
# Jalankan query-evidence/API dengan konfigurasi corpus, snapshot, dan model biasa.
```

Misalnya input `  Pasal<TAB>11 bukan 1; "izin  usaha"  ` menghasilkan bentuk
pencarian `Pasal 11 bukan 1; "izin  usaha"`. `<TAB>` di contoh berarti karakter
tab, bukan teks literal. Spasi di dalam kutipan tetap dua. Tidak ada spelling
correction, lowercase, stemming, terjemahan, compatibility folding, atau inferensi
tanggal. Typo, bahasa informal, dan code-switch tetap menjadi input retrieval.

Kutipan lurus/ganda dan pasangan kutipan melengkung dilindungi byte demi byte;
opener yang tidak ditutup melindungi sisa input. Escape dalam kutipan dipertahankan.
Apostrof di dalam kata bukan opener; huruf, angka, dan combining mark menjadi
batas kata yang konsisten sebelum/sesudah NFC. Ini aturan mekanis konservatif,
bukan parser bahasa alami. Token yang akan mendapat CGJ tambahan dari normalizer
stream-safe dipertahankan utuh. Input dan output maksimal 64 KiB; kosong/blank,
UTF-8 invalid, NUL, atau overflow ditolak, tidak dipotong diam-diam.

## Alur dan ownership

| Tahap | Input dan output | Invariant |
| --- | --- | --- |
| `query.NormalizeQuestion` | Original + policy -> original/search/method/edits | Tidak memutasi input; tidak ada I/O/model |
| `CandidateSearch` | Search -> dense/BM25; original -> graph | Profil, snapshot, dan budget tetap eksplisit |
| Hidrasi | Original request + salinan search result | Report/edits disalin agar callback tidak mengubah hasil caller |
| Reranker dan generator | Original request + evidence terverifikasi | Tidak mengganti pertanyaan pengguna dengan search form |
| CLI | `query_normalization` pada envelope JSON | `original`, `search`, `method`, dan `edits` tersedia untuk audit |
| HTTP | Workflow yang sama dengan policy operator | Schema response tetap; trace belum diekspos di response HTTP |

`edits` berisi interval perubahan byte UTF-8 `[start,end)` pada original dan
search. Gap yang tidak dicatat identik; interval search kosong menyatakan trim.
Satu interval NFC dapat mencakup satu token, bukan pemetaan per karakter. Offset
entity linking graph tetap mengacu pada original, bukan hasil normalisasi.
Trace mengandung teks pertanyaan: jangan menyalinnya otomatis ke log bersama.

## Bukti dan pekerjaan tersisa

[Laporan verifikasi](verification-report-query-normalization.md) mencatat unit,
fuzz, integrasi workflow, wiring operator, dan microbenchmark lokal. Tidak ada
klaim Recall/nDCG meningkat. Bandingkan dua mode pada snapshot/dataset/model dan
budget sama sebelum mengubah default. Ukur slice nomor hukum, negasi, quoted
terms, typo, informal, code-switch, waktu normalisasi serta latency end-to-end.
Target tetap [benchmark-targets.yaml](../configs/benchmark-targets.yaml), status
REQUIRED_UNMEASURED mengikuti [kebijakan benchmark](benchmark-policy.md).
Classifier intent, temporal CURRENT/COMPARE, dan semantic query expansion belum
selesai; normalisasi mekanis tidak menutup keseluruhan Q01.
