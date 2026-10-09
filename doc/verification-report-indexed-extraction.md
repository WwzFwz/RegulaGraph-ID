# Verifikasi locator EXTRACT berbasis unit sumber

Dokumen ini mencatat boundary opsional EXTRACT v3 pada 2026-10-09: representasi
sumber bernomor, proyeksi rentang ke byte C01, serta satu eksperimen lokal yang
timeout. Baseline sebelum perubahan `539e68a`; fingerprint implementasi final
dan perintah berada di `artifacts/verification/20261009-indexed-extraction/`.
PASS boundary tidak berarti model atau seluruh graph pipeline sudah berhasil.

**Cakupan model:** rangkaian diagnostic ini terbatas pada Qwen2.5 kelas 7B lokal
terkuantisasi. Model hosted yang lebih kuat atau model lokal lebih besar belum
dibandingkan. Setup CPU/GPU berbeda antar-run; hasilnya tidak dapat digeneralisasi
sebagai batas kualitas proyek. Lihat [kondisi dan batas kesimpulan](extraction-trial-limitations.md).

## Masalah dan desain

Eksperimen v2 sebelumnya sering gagal menyalin newline, kapitalisasi atau kutipan
persis. Format v3 mengizinkan model memilih range sumber; surface/quote/offset
dibentuk deterministik dari source asli. Run L/M/N Unicode menjadi unit, rune
non-space lain menjadi unit tersendiri, whitespace tetap utuh. Model tetap
memilih entitas dan menalar hubungan, bukan parser hardcode relasi hukum.

Schema/prompt v3 opsional dan memiliki pin baru. Renderer/Unicode version masuk
producer identity; completion replay mengikat seluruh request/source dan exact
encoded envelope. Versi lama tidak menerima v3 diam-diam, default tidak berubah,
dan protobuf C01 tidak diubah. [Kontrak profil](semantic-model-profiles.md)
menjelaskan batas representasi, resource, dan cara mengganti model.

## Bukti implementasi

Toolchain Go 1.26.8 Windows/amd64. Cache Windows default sempat menolak akses;
run berikutnya dengan akses cache yang sesuai menghasilkan hasil berikut:

| Pemeriksaan | Hasil |
| --- | --- |
| `go test ./src/server/internal/adapters/inference -run 'TestIndexed' -count=1` | PASS, exit 0, `unit.log` |
| `go test ./src/server/internal/adapters/inference ./src/server/internal/config ./src/server/internal/workflows ./src/server/internal/answering ./src/server/cmd/semantic-gateway ./src/server/cmd/cli -count=1` | PASS, exit 0, `regressions.log` |
| Final `go test ./src/server/internal/adapters/inference ./src/server/cmd/semantic-gateway -count=1` sesudah admission/schema correction | PASS, exit 0, `final-tests.log` |
| `go vet` kedua package final tersebut | PASS, exit 0, `vet.log` |
| `python scripts/check_contracts.py` | PASS, exit 0; 173 messages, 32 enums, 4 services; baseline tidak ditulis ulang |
| Reviewer independen `/root/verify_index_abort` | Scoped code/test review PASS; metadata schema yang masih menyebut quote diperbaiki |
| Saved model v3 projection | NOT_MEASURED: tidak ada completion lengkap; tes opt-in SKIP pada suite biasa |
| Model quality, full PDF/graph, required benchmark | NOT_MEASURED |

Regresi membuktikan layout CRLF/tab/Unicode dan byte multibyte tetap tepat,
occurrence berulang tidak digabung, punctuation-adjacent mention dapat dipilih,
invalid/overflow/reversed/null ranges dan duplicate keys ditolak, output legacy
tidak diterima sebagai v3, ontology/endpoint gate tetap aktif, source overflow
ditolak sebelum provider, serta expanded surface/quote memakai aggregate budget.
Restart replay memakai completion yang sama, tetapi teks berubah dengan panjang
dan posisi token sama tidak boleh mengambil completion sumber lama.

Independent focused tests mencakup `TestIndexed`, `TestQuotedExtraction`,
`TestQuotedSpan`, `TestSemanticSchema`, dan `TestSemanticExtract` dengan exit 0.
Review menemukan description schema masih menjelaskan quote alignment; description
dan nama definition diperbaiki. Original request eksperimen disimpan apa adanya,
sehingga hasilnya tidak diklaim memakai metadata schema final tersebut.

## Eksperimen model aktual: FAIL timeout

Input adalah **seluruh chunk pembuka yang sama** dari
`20261009-quote-extraction/retry/actual-request.json` (PDF PP 12/2006), bukan
pilihan fakta yang dipangkas. Source disimpan pada `source.json`, unit map pada
`source-units.json`; 354 unit. Runtime model yang sudah resident di port 55110
memakai GGUF Qwen Q4_K_M yang sama dengan [generation](verification-report-real-answer.md),
llama.cpp `b11515-3d65c90d0`, CPU empat thread, window 8192, no context shift.
Temperature 0 dan output cap 4096 dipertahankan.

Skrip diagnostic `run.py` memperoleh full input count 4016, sehingga count + cap
8112 masih muat. Ini HTTP diagnostic, bukan bukti startup/admission gateway C01.
Deadline diagnostic 600 detik berbeda dari run v2 300 detik; angka ini bukan
perubahan benchmark dan tidak boleh digunakan sebagai perbandingan latency adil.

Request timeout pada **600,031 detik**, exit 1. Server mengonfirmasi pembatalan
task 364 dan pelepasan slot pada `n_tokens=5998`, `truncated=0`. Tidak ada
`actual-response.json` lengkap; tidak ada klaim validitas projection, jumlah fakta,
recall atau kualitas model. Raw request, props, count, result dan potongan server
log dipertahankan. Tidak ada retry otomatis atau sampling tambahan setelah run ini.

`TestIndexedExtractionSavedModelProjection` disiapkan untuk memeriksa kesamaan
rendering diagnostic dengan produksi serta menjalankan projector/ontology/C01
atas completion tersimpan kelak. Ia memakai identitas corpus/source fixture,
dan tidak membuktikan semua prompt/schema/ontology envelope sama dengan admission
produksi. Jangan menyebut tes tersebut end-to-end ingestion atau attestation model.

## Kelanjutan tanpa ketergantungan pada Qwen

Pengguna mengizinkan penggantian ke model yang lebih baik nanti. Tidak ada model
baru yang dipilih pada paket ini. Gunakan adapter/profil yang sama, freeze pin baru,
dan mulai dari input gagal yang disimpan; tidak perlu menulis ulang arsitektur.
Format v3 belum dipromosikan sebagai profil operasional. Full-PDF EXTRACT,
RESOLVE/review, graph publication, serta kualitas/latency tetap terbuka. Jalur
hybrid answer yang telah berhasil tetap memakai konfigurasi sebelumnya.
