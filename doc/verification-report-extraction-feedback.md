# Verifikasi diagnostik locator dan eksperimen koreksi model

Dokumen ini mencatat diagnostik EXTRACT yang dapat dipakai untuk koreksi terarah,
serta dua eksperimen feedback pada model lokal. Eksperimen belum menunjukkan
perbaikan validitas sumber, sehingga retry otomatis tidak ditambahkan ke runtime.

**Cakupan model:** rangkaian diagnostic ini terbatas pada Qwen2.5 kelas 7B lokal
terkuantisasi. Model hosted yang lebih kuat atau model lokal lebih besar belum
dibandingkan. Setup CPU/GPU berbeda antar-run; hasilnya tidak dapat digeneralisasi
sebagai batas kualitas proyek. Lihat [kondisi dan batas kesimpulan](extraction-trial-limitations.md).

## Implementasi yang diverifikasi

Gateway mengumpulkan lokasi kesalahan kutipan v2 seperti `mentions[1].span` dan
`supports[0].spans[0]`. Alasan berasal dari validator statis; diagnostic ini tidak
mengandung kutipan, surface form, atau local ID dari model. Maksimum 32 detail;
jika batas terlampaui, detail terakhir menyebut ada error tambahan yang tidak
ditampilkan. Batas ini hanya berlaku pada laporan error: item tetap ditolak penuh
sebelum projector fakta bersama, bukan menerima atau menghilangkan sebagian fakta.

Budget scan/caching locator tetap berlaku. Guarantee teks sumber tidak muncul di
detail ini hanya mencakup diagnostic locator baru, bukan audit seluruh pesan
error decoder/ontology yang sudah ada. Format C01 tidak diubah.

## Bukti

Base revision `7d929f8` ditambah fingerprint dalam
`artifacts/verification/20261009-extraction-feedback/independent-results.json`.
Toolchain Go 1.26.8 windows/amd64. Semua log berikut berada pada root run tersebut.

| Pemeriksaan | Hasil |
| --- | --- |
| `go test ./src/server/internal/adapters/inference ./src/server/internal/workflows` | Exit 0, `regressions.log`; opt-in tanpa env SKIP |
| `go vet` kedua package tersebut | Exit 0, `go-vet.log` |
| Review independen diagnostic/source boundary | PASS_SCOPED, `independent.log` dan fingerprint |
| Replay koreksi pertama melalui validator produksi | FAIL, `actual-projection.log` |
| Replay format source-last melalui validator produksi | FAIL, `source-last/actual-projection.log` |

Saved replay memakai teks chunk aktual, tetapi identitas corpus/source dan base
offset fixture; tidak membuktikan durable ingestion atau attestation model.
Validator production yang digunakan tetap mencakup projection, ontology dan C01;
kedua keluaran gagal sebelum dapat diterima.

## Hasil eksperimen model lokal

Kedua eksperimen memakai endpoint Ollama yang sudah berjalan dan
`regulagraph-demo:latest` (Qwen2.5 7B). Request, response, hash dan waktu disimpan.
API key cloud tidak digunakan. Bobot tidak di-attest ulang dalam diagnostic ini.

1. Proposal v2 sebelumnya diberikan sebagai pesan assistant, diikuti feedback
   empat locator gagal. Completion cap 4096, 4160 prompt tokens dan 1512 completion
   tokens, sekitar 80,17 detik, finish reason stop. Delapan mentions, dua assertions,
   sepuluh locator; empat locator yang sama masih gagal.
2. Sumber asli dan feedback ditempatkan bersama pada pesan user terakhir, dengan
   completion cap 2048. Sebanyak 4156 prompt tokens dan 1419 completion tokens,
   sekitar 81,98 detik, finish reason stop. Tujuh mentions, dua assertions, sembilan
   locator; empat locator tetap gagal. Satu mention berkurang, sehingga ini bukan
   bukti kelengkapan atau peningkatan recall.

Profil request eksperimen kedua berbeda; tidak dinilai sebagai perbandingan
benchmark yang terkontrol. Angka cap produksi/required benchmark tidak diubah.
Semua attempt gagal tetap disimpan. Tidak ada klaim bahwa feedback selalu buruk;
dua observasi ini hanya belum membenarkan tambahan retry pada model/profil ini.

Berikutnya: bandingkan model/profil extraction pada sumber dan gate yang sama,
lengkapi full-prompt admission dan checkpoint per item. Preferensi lokal tetap
berlaku; opsi provider lain memerlukan pemilihan pengguna dan credential melalui
environment. Gold quality dan acceptance corpus/performa tetap belum lulus.
