# Verifikasi extraction berbasis kutipan

Dokumen ini mencatat boundary schema/prompt EXTRACT v2 dan pemetaan kutipan ke span
C01, serta kegagalan model nyata yang belum terselesaikan. Mekanisme alignment
dan kualitas ekstraksi dinilai terpisah; required benchmark tidak diubah.

**Cakupan model:** rangkaian diagnostic ini terbatas pada Qwen2.5 kelas 7B lokal
terkuantisasi. Model hosted yang lebih kuat atau model lokal lebih besar belum
dibandingkan. Setup CPU/GPU berbeda antar-run; hasilnya tidak dapat digeneralisasi
sebagai batas kualitas proyek. Lihat [kondisi dan batas kesimpulan](extraction-trial-limitations.md).

## Perilaku dan kompatibilitas

Schema/prompt v1 tetap utuh. Schema `extraction-output-v2.json` meminta quote,
prefix, suffix; gabungannya wajib muncul persis satu kali dalam teks input.
Prefix/suffix adalah konteks langsung, bukan bagian span bukti. Gateway menghitung
offset byte quote, lalu menggunakan projector C01 lama untuk absolute spans,
identitas sumber, manifest, hubungan mention/assertion, support closure, ontology
dan wire validation. Tidak ada fuzzy matching, normalisasi tambahan, memilih
kemunculan pertama dari beberapa kandidat, atau perbaikan offset v1 otomatis.

Selector schema dipakai bersama oleh composition root dan service. Top-level
schema wajib object dengan `$id` unik dan tepat ejaannya. ID null, case-folded,
duplikat, tidak dikenal atau tidak sesuai task ditolak. Schema tanpa ID masih
menggunakan projector legacy untuk kompatibilitas fixture/custom v1. Schema v2
yang dipin memilih projector kutipan; wire schema C01 tidak berubah.

Cache posisi anchor berlaku per item. Pencarian berhenti ketika kecocokan kedua
ditemukan; scan budget 64 MiB per item membatasi biaya terburuk yang dibebankan
pada locator. Prefix/suffix dibatasi 256 karakter dan 1024 byte UTF-8. Budget
yang habis menghasilkan error, bukan penghilangan fakta. Matching persis hanya
membuktikan lokasi teks; entailment relasi hukum tetap perlu evaluasi.

## Bukti verifikasi

Base revision `fccf4b1` ditambah fingerprint kode pada
`artifacts/verification/20261009-quote-extraction/independent-results.json`.
Toolchain Go 1.26.8 windows/amd64. Log pada direktori tersebut:

| Pemeriksaan | Hasil |
| --- | --- |
| `go test ./src/server/...` | Exit 0, `go-final.log`; test opt-in tanpa env SKIP |
| `go vet ./src/server/...` | Exit 0, `go-vet.log` |
| `python scripts/check_contracts.py` | Exit 0, 173 messages / 32 enums / 4 services; baseline tidak ditulis ulang |
| Review independen schema/quote projection | PASS_SCOPED; schema Draft 2020-12 valid, source fingerprints tersimpan |
| UTF-8, ambiguous/overlapping anchor, absent/nonadjacent context, budget/cache, v1/v2 isolation, unknown endpoint, schema identity | PASS mekanisme; fixture bukan bukti model |
| Replay keluaran model nyata melalui projector, ontology, C01 | FAIL, `actual-projection.log`; quote context tidak ditemukan |

Reviewer menemukan selector awal menerima ID schema ambigu dari decoder permisif.
Selector bersama diperketat dan seluruh package inference/gateway diperiksa ulang.
Tes replay model memakai source text aktual tetapi corpus/source IDs dan base
offset fixture; tes tersebut tidak membuktikan durable pipeline atau attestation
bobot model. Tanpa `REGULAGRAPH_TEST_QUOTE_REPLAY_DIR`, tes replay SKIP.

## Model lokal: hasil dan langkah berikutnya

Satu chunk sumber yang sama dari diagnostic sebelumnya dikirim ke endpoint Ollama
lokal dengan prompt/schema v2 dan completion cap 4096. Attempt pertama timeout
150 detik (`actual-diagnostic.json`). Setelah process handle dipastikan terminal,
request identik diulang dengan timeout observasi 300 detik. Artefak attempt kedua
tersimpan di `retry/`; request SHA-256 sama dengan attempt pertama. Kedua attempt
tetap dicatat, bukan membuang run lambat atau mengubah benchmark penerimaan.

Retry selesai sekitar 82,33 detik, 2444 prompt tokens dan 1520 completion tokens,
finish reason `stop`. Model melaporkan `regulagraph-demo:latest`; endpoint/bobot
tidak di-attest ulang dalam diagnostic ini. Keluaran berisi 8 mentions, 2 assertions,
dan 10 locator spans. Empat locator tidak cocok dengan sumber. Salah satunya
menggabungkan nama/nomor regulasi menjadi frasa yang tidak ada persis; tiga lainnya
memakai prefix yang bukan konteks langsung. Validator produksi menolak item penuh.

Jumlah fakta/locator berbeda dari run v1; angka ini bukan perbandingan accuracy,
recall, atau klaim peningkatan kualitas. Waktu laptop ini juga bukan hasil profil
benchmark release. Seluruh pipeline 35 chunk belum lulus.

Pekerjaan berikut adalah memperbaiki kemampuan model menyalin evidence tanpa
mengarang konteks, mempertimbangkan feedback validasi dengan retry yang dibatasi
dan terpin, serta melengkapi admission token prompt dan checkpoint per item.
Setelah itu jalankan corpus penuh dan evaluasi gold/latency bersama. Jangan
mengubah exact-source gate agar model yang salah terlihat berhasil.
