# Query CURRENT dengan tanggal yang dibekukan

Dokumen ini menjelaskan perencanaan tanggal Q01, konfigurasi operator, output
audit dan batas verifikasinya. CURRENT berarti tanggal kalender pada zona waktu
yang dipilih operator saat request dimulai; bukan klaim bahwa semua peraturan
terbaru sudah masuk corpus. Snapshot pengetahuan dan tanggal berlaku hukum tetap
dua hal terpisah.

## Cara menjalankan

Tetap siapkan corpus terpublikasi, backend dan model seperti
[query CLI](query-evidence.md) atau [API](evidence-api.md). Tidak perlu rebuild
corpus. CURRENT harus dipilih eksplisit; aplikasi tidak menebak tanggal dari teks.

```powershell
$env:REGULAGRAPH_QUERY_TIME_ZONE = 'Asia/Jakarta'
go run ./src/server/cmd/cli query-evidence -question 'Apa ketentuan izin saat ini?' -current -profile hybrid
# Tambahkan -answer dengan konfigurasi generator biasa untuk draft bersitasi.
```

`-current` dan `-as-of YYYY-MM-DD` saling eksklusif. Tanpa zona terkonfigurasi,
CURRENT ditolak. Nilai `Local` dan zona tidak dikenal ditolak; `UTC`,
`Asia/Jakarta`, `Asia/Makassar`, atau `Asia/Jayapura` adalah contoh zona bernama.
Pemilihan zona adalah kebijakan kalender operator, bukan inferensi yurisdiksi.
AS_OF mempertahankan tanggal eksplisit dan tidak membaca clock.

Restart API setelah mengubah konfigurasi. Body request `/v1/evidence` atau
`/v1/questions` tetap memakai C01 QuestionRequest; untuk CURRENT gunakan:

```json
{
  "corpus_id": "corpus:example",
  "question": "Apa ketentuan izin saat ini?",
  "response_mode": "RESPONSE_MODE_COMPLETE",
  "requested_profile": "RETRIEVAL_PROFILE_HYBRID_RAG",
  "temporal_scope": {
    "mode": "TEMPORAL_MODE_CURRENT",
    "unresolved_policy": "UNRESOLVED_POLICY_REPORT"
  }
}
```

Jangan sertakan `effective_at` atau `compare_dates` pada CURRENT. Tanggal ambigu
dalam pertanyaan tidak ditafsirkan otomatis; pilih AS_OF untuk kebutuhan historis.
Mode COMPARE dan streaming tetap belum aktif.

## Alur, output, dan integritas

| Tahap | Perilaku |
| --- | --- |
| Admission | CLI/API memvalidasi mode, konfigurasi zona, corpus, profil dan deadline |
| `query.ResolveTemporalScope` | Membaca clock sekali untuk CURRENT; menghasilkan salinan scope AS_OF dan audit `explicit-calendar-v1` |
| `RAGSession` | Membekukan tanggal sebelum pin; mempertahankan snapshot request/policy unresolved dan pertanyaan asli |
| Retrieval sampai generation | Semua tahap memakai salinan scope bertanggal yang sama; tidak membaca ulang clock untuk tanggal hukum |
| Output CLI | `temporal_resolution` berisi policy, requested_mode, effective_date, time_zone dan resolved_at (UTC) untuk CURRENT |
| Output HTTP | Payload C01 evidence tetap; header `X-Effective-Date` dan, untuk CURRENT, `X-Query-Time-Zone`; Answer memuat tanggal dalam `effective_dates` |
| Validasi output | Audit harus cocok dengan mode, kalender dan zona operator; tanggal Answer harus sama pada CURRENT maupun AS_OF |

Contoh: `2025-12-31T17:00:00Z` di Asia/Jakarta menjadi `2026-01-01`.
Request yang berlanjut melewati tengah malam tetap menggunakan tanggal awal.
Interval sumber yang unknown/conflict tetap mengikuti policy unresolved, bukan
dianggap berlaku hanya karena tanggal query sudah diketahui.

Zona dan versi policy masuk fingerprint konfigurasi CLI/API. `time/tzdata`
menyediakan fallback portabel, tetapi `time.LoadLocation` dapat memakai data host
atau `ZONEINFO`. Isi tzdb belum dipin hash; reproduksibilitas aturan zona lintas
deployment memerlukan pencatatan/pinning tzdb. Audit menyimpan instant dan tanggal
hasil run, tanpa mengklaim data zona identik di semua lingkungan.

## Verifikasi 2026-10-09

Baseline `6094ff4`; fingerprint file final, toolchain dan raw logs ada di
`artifacts/verification/20261009-current-query/manifest.json`. `go test
./src/server/...` lulus setelah perubahan interface internal EvidenceService
menjadi RAGResult; kontrak wire HTTP tidak diganti. Tes tambahan final CLI,
routes, workflows, query serta `go vet` lulus. Integration suite eksternal tanpa
backend terkonfigurasi tetap skip, bukan klaim pengujian corpus nyata.

Fixture mencakup batas tengah malam/tahun baru, leap day, UTC/Jakarta/Jayapura,
clock hanya sekali, salinan scope/audit independen, tanggal caller pada CURRENT
ditolak, zona invalid, mode dinonaktifkan, serta audit/tanggal/zona keluaran yang
rusak. Review independen menemukan CLI AS_OF belum membandingkan tanggal Answer;
temuan diperbaiki untuk kedua mode dengan regresi AS_OF dengan/tanpa audit dan
CURRENT draft invalid. Reviewer memverifikasi ulang perbaikan dan memberi PASS terbatas pada milestone ini. Log `temporal-final.log` mencatat tes perbaikan.

Tidak ada perubahan schema C01 atau benchmark. Kualitas legal, freshness corpus,
Recall/nDCG dan required latency tetap NOT_MEASURED. Ikuti
[benchmark-policy](benchmark-policy.md). Intent classifier, routing otomatis,
multi-date COMPARE dan semantic query expansion tetap pekerjaan Q01 berikutnya.
