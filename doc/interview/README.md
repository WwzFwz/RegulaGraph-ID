# Panduan interview RegulaGraph-ID

Folder ini menampung bahan untuk menjelaskan masalah, arsitektur, pilihan teknologi,
alur data, algoritma, dan pemetaan implementasi RegulaGraph-ID dalam interview.
**Dokumen 01–06 dan 08 memakai asumsi seluruh komponen arsitektur telah terintegrasi.**
Dengan asumsi ini, penjelasan dibaca sebagai satu sistem utuh, bukan laporan progres.
Keadaan repository dan bukti yang benar-benar tersedia dipisahkan ke
[07-implementation-status.md](07-implementation-status.md).

## Cara membaca

| Urutan | Dokumen | Pertanyaan yang dijawab |
| --- | --- | --- |
| 1 | [Arsitektur](01-architecture.md) | Sistem lengkap ini membangun apa dan bagaimana komponen bekerja bersama? |
| 2 | [Pilihan dan trade-off](02-decisions.md) | Apa alasan teknis/bisnis, biaya alternatif, dan cara membuktikan manfaatnya? |
| 3 | [Alur end-to-end](03-flows.md) | Apa yang terjadi sejak PDF masuk dan sejak pengguna mengirim pertanyaan? |
| 4 | [Algoritma dan matematika](04-math-and-performance.md) | Bagaimana skor dihitung, apa efek parameter, dan bagaimana mengukur keberhasilannya? |
| 5 | [Peta implementasi](05-code-map.md) | Folder/file mana yang memiliki setiap fungsi dalam arsitektur lengkap? |
| 6 | [Latihan menjawab](06-interview-answers.md) | Bagaimana menjelaskan proyek dengan jelas tanpa melebihkan hasil? |
| 7 | [Status aktual](07-implementation-status.md) | Apa yang sudah diimplementasikan, diuji, dan masih terbuka di repository? |
| 8 | [Input/output dan contoh](08-input-output-examples.md) | Bagaimana contoh data berubah pada tiap tahap, mengapa tahap itu dipilih, dan apa manfaat bisnis serta trade-off-nya? |

Jika waktu persiapan hanya 15 menit, baca diagram di dokumen 1, routing adaptif di
dokumen 3, BM25 dan latency di dokumen 4, kemudian latihan di dokumen 6.
Untuk mengikuti satu contoh dari PDF hingga jawaban, baca dokumen 8 setelah diagram
arsitektur; contoh juga mencakup update, recovery, review, dan evaluasi.
Panduan menjalankan aplikasi tetap berada di [interview-demo.md](../interview-demo.md).
Buka Markdown Preview di editor untuk membaca tabel dan persamaan dengan lebih
nyaman; di VS Code biasanya menggunakan `Ctrl+Shift+V`.

## Cara membaca asumsi

Kalimat seperti “classifier memilih kebutuhan retrieval” dalam dokumen utama
menjelaskan perilaku sistem pada asumsi integrasi lengkap. Ia tidak menyatakan
bahwa file classifier saat ini sudah selesai. Dokumen status memakai label DEMO,
KOMPONEN dan RENCANA untuk membedakan kemampuan aktual. Benchmark tetap membutuhkan
hasil run nyata; tidak ada angka keberhasilan yang dibuat dari asumsi.

## Cakupan dan integrasi anak

Dokumen arsitektur memberi istilah dan batas tanggung jawab untuk dokumen lain.
Dokumen keputusan menjelaskan alasan desain; flow menghubungkan komponen;
matematika menjelaskan mekanisme; peta kode menghubungkan penjelasan ke implementasi.
Dokumen input/output menelusuri contoh yang konsisten lintas tahap tanpa membuat
kontrak wire baru; nama record dan invariant tetap mengikuti kontrak sistem.
Contoh JSON bersifat pedagogis, bukan payload API. Alasan bisnis menjelaskan
hipotesis manfaat pengguna, biaya, alternatif dan ukuran pembuktian; bukan klaim
pelanggan, ROI, atau hasil benchmark yang belum tersedia.
Latihan jawaban wajib konsisten dengan semuanya. Tambahan topik interview boleh
masuk di sini selama berupa bahan penjelasan, bukan kode produk atau dataset.

Saat desain berubah, perbarui flow dan peta file; saat implementasi berubah,
perbarui dokumen status dan tautan bukti. Angka
required tetap bersumber tunggal dari [benchmark-targets.yaml](../../configs/benchmark-targets.yaml).
Angka ilustrasi matematika bukan target baru. Hasil smoke demo hanya berlaku untuk
kondisi pada [laporan demo](../verification-report-interview-demo.md).

Rujukan desain utama: [system-design](../system-design.md),
[system-contracts](../system-contracts.md), [storage-consistency](../storage-consistency.md),
dan [development-plan](../development-plan.md). Ringkasan lama dapat tertinggal dari
implementasi; gunakan fungsi aktual dan laporan verifikasi untuk klaim kemampuan.
