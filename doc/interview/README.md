# Panduan interview RegulaGraph-ID

Folder ini menampung bahan untuk menjelaskan masalah, arsitektur, pilihan teknologi,
alur data, algoritma, dan implementasi RegulaGraph-ID dalam interview. Ini adalah
penjelasan dari kode dan rancangan proyek, bukan spesifikasi baru atau bukti bahwa
seluruh sistem telah selesai. Status diperiksa pada 5 Oktober 2026, setelah demo lokal.

## Cara membaca

| Urutan | Dokumen | Pertanyaan yang dijawab |
| --- | --- | --- |
| 1 | [Arsitektur](01-architecture.md) | Sistem ini membangun apa, komponennya apa, dan mana yang sudah berjalan? |
| 2 | [Pilihan dan trade-off](02-decisions.md) | Mengapa memilih bagian itu, mengapa bukan alternatifnya, kapan keputusan berubah? |
| 3 | [Alur end-to-end](03-flows.md) | Apa yang terjadi sejak PDF masuk dan sejak pengguna mengirim pertanyaan? |
| 4 | [Algoritma dan matematika](04-math-and-performance.md) | Bagaimana skor dihitung, apa efek parameter, dan bagaimana mengukur keberhasilannya? |
| 5 | [Peta implementasi](05-code-map.md) | Di folder/file mana fungsi itu berada, dan apa yang masih perlu dibangun? |
| 6 | [Latihan menjawab](06-interview-answers.md) | Bagaimana menjelaskan proyek dengan jelas tanpa melebihkan hasil? |

Jika waktu persiapan hanya 15 menit, baca bagian status di dokumen 1, alur demo di
dokumen 3, BM25 dan latency di dokumen 4, kemudian latihan di dokumen 6.
Panduan menjalankan aplikasi tetap berada di [interview-demo.md](../interview-demo.md).
Buka Markdown Preview di editor untuk membaca tabel dan persamaan dengan lebih
nyaman; di VS Code biasanya menggunakan `Ctrl+Shift+V`.

## Label status yang digunakan

| Label | Makna |
| --- | --- |
| **DEMO** | Dipakai pada jalur demo lokal yang sudah dicoba dengan PDF dan model nyata. |
| **KOMPONEN** | Ada implementasi/library atau integrasi terbatas dengan pengujian; belum berarti seluruh pipeline produk aktif. |
| **RENCANA** | Bagian arsitektur target yang belum lengkap/tersambung, termasuk scaffold. |

Satu komponen bisa mempunyai bagian KOMPONEN dan bagian RENCANA. Contohnya native
embedding sudah tersedia, tetapi demo BM25 tidak memanggilnya. Keberadaan file,
test yang lulus, dan kualitas sistem pada gold dataset adalah tiga bukti berbeda.

## Cakupan dan integrasi anak

Dokumen arsitektur memberi istilah dan batas status yang dipakai dokumen lain.
Dokumen keputusan menjelaskan alasan desain; flow menghubungkan komponen;
matematika menjelaskan mekanisme; peta kode menghubungkan penjelasan ke implementasi.
Latihan jawaban wajib konsisten dengan semuanya. Tambahan topik interview boleh
masuk di sini selama berupa bahan penjelasan, bukan kode produk atau dataset.

Saat implementasi berubah, perbarui status, flow, dan peta file bersama. Angka
required tetap bersumber tunggal dari [benchmark-targets.yaml](../../configs/benchmark-targets.yaml).
Angka ilustrasi matematika bukan target baru. Hasil smoke demo hanya berlaku untuk
kondisi pada [laporan demo](../verification-report-interview-demo.md).

Rujukan desain utama: [system-design](../system-design.md),
[system-contracts](../system-contracts.md), [storage-consistency](../storage-consistency.md),
dan [development-plan](../development-plan.md). Ringkasan lama dapat tertinggal dari
implementasi; gunakan fungsi aktual dan laporan verifikasi untuk klaim kemampuan.
