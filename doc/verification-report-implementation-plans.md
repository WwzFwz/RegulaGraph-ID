# Verifikasi rencana implementasi X01, K01, Q01, dan A01

Dokumen ini mencatat audit desain empat paket sebelum coding dilanjutkan. Ia bukan
laporan implementasi atau hasil acceptance kualitas/performa. Raw fingerprint dan
pemeriksaan tautan berada di `artifacts/verification/20260925-implementation-plans/`.

Basis audit adalah revision `17bba83ac56e30cde2c7e3e157b359c1c770e954` dan
working tree rencana yang ditulis sesudahnya. Pemeriksa independen membaca rencana
X01 serta K01/Q01/A01 secara read-only terhadap kontrak dan kode yang tersedia.

| Pemeriksaan | Expected | Actual | Status |
| --- | --- | --- | --- |
| Tautan lokal di empat rencana, roadmap, panduan, dan README doc | Seluruh target file ada | 160 tautan diperiksa, 0 hilang; fingerprint tercatat pada raw JSON | PASS |
| Batas X01 | Readiness mencakup semua route/replica pembaca, dictionary reuse hanya directed ancestor dengan ID term stabil dan cakupan cukup | Dua gap desain ditemukan, direvisi, dan dicek ulang oleh reviewer | PASS untuk review rencana |
| Batas K01–Q01–A01 | Registry historis terikat snapshot, satu owner read lease, seluruh teks faktual jawaban divalidasi | Tiga gap desain ditemukan, direvisi, dan dicek ulang oleh reviewer | PASS untuk review rencana |
| Kode produksi X01/K01/Q01/A01 | Implementasi + integrasi nyata sesuai rencana | Review ini tidak menguji kode baru | NOT_MEASURED |
| Gold/benchmark required | Dataset dan workload eligible dengan hasil sesuai YAML | Prasyarat belum terpenuhi; tidak ada run acceptance | BLOCKED/NOT_MEASURED |

Reviewer tidak mengedit kode/rencana. Koreksi X01 menambahkan proof readiness untuk
setiap route pembaca dan kompatibilitas dictionary berdasarkan ancestor terverifikasi.
Koreksi K01/Q01 menambahkan binding immutable snapshot ke registry revision dan
admission snapshot historis. Koreksi A01 mengikat read lease sampai validasi terminal
serta mewajibkan coverage semua teks faktual, termasuk teks di luar daftar Claim.

Rencana ini menjadi arahan coding. Setiap perubahan kontrak C01, storage/publication,
retrieval-answer, atau model harus diuji dan di-review kembali pada implementasinya.
Status numerik required tetap [benchmark-targets.yaml](../configs/benchmark-targets.yaml).
