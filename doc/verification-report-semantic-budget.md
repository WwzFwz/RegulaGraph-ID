# Verifikasi completion budget semantic

Dokumen ini mencatat perbaikan penyebab output model terpotong pada EXTRACT dan
batas bukti yang diperoleh. Cap eksplisit bukan pengganti validasi fakta, span,
atau pengukuran full-prompt/context admission. Angka benchmark tidak diubah.

## Perubahan dan kompatibilitas

Gateway mengirim completion cap positif pada setiap EXTRACT/RESOLVE. Environment
`REGULAGRAPH_SEMANTIC_MAX_OUTPUT_TOKENS` default 4096, harus lebih kecil dari
context limit model. Config fingerprint dan producer input hash berubah ketika
cap berubah. Library SemanticConfig juga mewajibkan cap; caller lama harus
mengisinya. Ekspor ulang producer dan buat request baru bila konfigurasi berubah.

Adapter HTTP menolak reported completion usage di atas cap. Coordinator EXTRACT
membandingkan ConfigHash producer aktual dengan request tersimpan, serta mengecek
seluruh input pin aktual terhadap set yang diotorisasi request. Ini menutup
kasus gateway baru mengembalikan context request lama tanpa mencerminkan perubahan
konfigurasi pada context tersebut. Pemeriksaan juga dipakai saat recovery;
manifest historis tidak ditulis ulang. Pemeriksaan set memiliki biaya linear
terhadap jumlah hash, bukan perkalian dua daftar.

## Bukti

Base revision `057f236` ditambah fingerprint final dalam
`artifacts/verification/20261009-semantic-budget/independent-results.json`.
Toolchain Go 1.26.8 windows/amd64. Run ini tidak menggunakan cloud API key.

| Pemeriksaan | Hasil |
| --- | --- |
| `go test ./src/server/...` | Exit 0, `go-final.log`; integrasi opt-in yang tidak dijalankan tetap SKIP |
| `go vet ./src/server/...` | Exit 0, `go-vet.log` |
| Review independen gateway/provider/EXTRACT/RESOLVE | PASS_SCOPED; input cap invalid, pin drift, actual producer drift, provider overrun, dan regression replay |
| Diagnostic satu chunk lokal | JSON lengkap, finish reason stop; bukan acceptance pipeline |

Seluruh log berada di root run di atas. Run suite awal gagal karena shared fixture
RESOLVE belum memasok cap wajib; fixture kemudian memakai cap eksplisit serta
ProducerManifest gateway aktual, dan suite final lulus. Review menemukan admission
ConfigHash aktual belum dibandingkan; temuan diperbaiki dan diperiksa ulang.

## Diagnostic model nyata

Request pertama yang sebelumnya terpotong direplay ke Ollama lokal dengan isi
request sama dan `max_tokens=4096`. Model melaporkan `regulagraph-demo:latest`;
hasil tersimpan dalam `actual-request.json`, `actual-response.json`, serta hash dan
waktu pada `actual-diagnostic.json`. Diagnostic ini memakai endpoint model yang
sudah berjalan; tidak melakukan ulang attestation bobot atau menjalankan seluruh
pipeline 35 chunk.

Hasil: 2292 prompt tokens, 1064 completion tokens, finish reason `stop`, sekitar
65,30 detik. Ini menunjukkan request tidak lagi berhenti pada limit demo 768 token
untuk kasus tersebut. JSON memuat 7 mentions dan 2 assertions tanpa endpoint ID
yang tidak dideklarasikan, tetapi **9 dari 9 span gagal exact-source matching**.
Pemeriksaan terbatas ini dicatat di `actual-content-diagnostic.json`; bukan
pengganti validator produksi, gold evaluation, atau benchmark latency.

Berikutnya: representasi kutipan yang dapat diikat secara deterministik ke sumber
tanpa meminta model menghitung byte UTF-8, dengan penolakan ambiguitas/kutipan yang
tidak ditemukan. Tetap diperlukan full-prompt token admission, checkpoint/resume
per item, run seluruh corpus, dan quality/performance acceptance. Status required
benchmark tetap REQUIRED_UNMEASURED.
