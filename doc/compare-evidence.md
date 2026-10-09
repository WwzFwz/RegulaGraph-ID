# Fondasi evidence untuk perbandingan tanggal

Dokumen ini menjelaskan library Q01 untuk menyiapkan bukti bertanggal pada satu
snapshot. Transport evidence tersedia melalui CLI dan HTTP. Generation jawaban
komparatif belum tersedia; permintaan comparative answer ditolak sebelum eksekusi.

## Kontrak dan alur

`RAGSession.CompareEvidence` menerima QuestionRequest C01 dengan scope COMPARE,
2 sampai 8 tanggal eksplisit yang berbeda, profil retrieval, serta RequestContext
yang sudah diotorisasi. Tanggal tunggal `effective_at` tidak boleh hadir bersamaan.
Urutan tanggal mengikuti pengguna, tidak diurutkan otomatis. Unknown/conflict
pada sumber tetap mengikuti `unresolved_policy`, bukan ditimpa dengan asumsi baru.

| Tahap | Input -> output dan invariant |
| --- | --- |
| `query.PlanComparisonScopes` | COMPARE -> salinan scope AS_OF per tanggal; kalender invalid/duplikat/lebih dari batas ditolak, bukan dipangkas |
| `withRAGSnapshot` | Request + context tepercaya -> satu lease, index generation dan workflow; factory tidak dipanggil ulang per tanggal |
| Discovery | Pertanyaan/profil/snapshot yang sama -> satu CandidateSearch; embedding, BM25 dan traversal tidak diulang per tanggal |
| Hidrasi dan projection | Kandidat bersama + scope masing-masing -> evidence dan missing/rejected dependencies bertanggal |
| Reranking | Evidence yang lolos untuk tanggal tersebut -> ranking per tanggal; model failure menggagalkan seluruh comparison |
| Final authority | Graph authorization dan pin masih valid -> RAGComparisonResult; cleanup gagal juga membuang output |

Hasil memiliki snapshot/profil yang sama dan daftar `DatedEvidence`, masing-masing
berisi CalendarDate protobuf serta RAGResult tersendiri. Audit anak adalah AS_OF
yang diturunkan dari tanggal COMPARE, bukan hasil pembacaan clock. Bundle tidak
digabung: evidence ID yang sama pada dua tanggal tetap punya konteks temporal,
ranking, omissions dan projection sendiri. Mutasi satu hasil tidak mengubah anak
lain atau input pemanggil.

Discovery saat ini bergantung pada question/profile/index snapshot dan belum
menerima tanggal hukum sebagai filter. Karena itu reuse hanya dilakukan secara
privat dalam satu callback yang memiliki input/generation/pin identik. Bila
discovery kelak memakai tanggal, cache key dan strategi retrieval harus diperbarui;
jangan mempertahankan reuse tanpa membuktikan kesetaraannya.

Loop pemrosesan tanggal bersifat berurutan, dengan branch discovery paralel seperti
jalur single-date. Ini menghindari perkalian concurrency inference/backend secara
implisit. Ukuran agregat protobuf EvidenceBundle dibatasi 16 MiB dan jumlah tanggal
8; keduanya admission guard, bukan angka benchmark atau jaminan recall. Kandidat
yang tidak ditemukan pada discovery tetap tidak tersedia: comparison tidak
menjamin seluruh amandemen telah tercakup. Duration library mengukur pekerjaan
setelah factory/pin, bukan latency end-to-end transport.

## Verifikasi 2026-10-09

Baseline `97601c4`; raw logs, fingerprint file dan toolchain tersimpan di
`artifacts/verification/20261009-compare-evidence/manifest.json`. Tes Go seluruh
server lulus sesudah refactor shared lease. Tes final query/workflows menguji
semua empat profil, satu pin/factory/discovery, reranker per tanggal, caller/child
ownership, graph applicability yang berbeda antar tanggal, duplicate/invalid
dates, historical snapshot mismatch, cancellation, kegagalan tanggal kedua,
revoked lease, cleanup failure dan budget agregat sesudah delapan tanggal.

Review independen menemukan clone request single-date berpindah sebelum validasi
envelope saat refactor. Validasi request/context dikembalikan sebelum clone dan
calendar planning. Regresi oversized CURRENT membuktikan input ditolak sebelum
clock/factory/pin; tes single-date lama tetap lulus. Reviewer menjalankan ulang regresi dan memberi PASS terbatas pada fondasi library. Assertion budget memeriksa
pesan batas agregat yang tepat, bukan sekadar error sembarang.

```powershell
go test ./src/server/internal/retrieval/query ./src/server/internal/workflows
go test ./src/server/...
go vet ./src/server/internal/retrieval/query ./src/server/internal/workflows
```

Backend, hydrator dan model fixture bersifat sintetis; graph projection dan
orchestration menggunakan kode produksi. Database/native integration yang tidak
dikonfigurasi pada suite tetap skip. Hasil ini tidak membuktikan kebenaran hukum,
kualitas comparative answer, corpus readiness atau required benchmark.
Target tetap [benchmark-targets.yaml](../configs/benchmark-targets.yaml), mengikuti
[benchmark-policy](benchmark-policy.md).

## Integrasi berikutnya sampai COMPARE penuh

1. Transport evidence selesai: snapshot dan daftar tanggal/bundle C01 divalidasi
   bersama sebelum output; satu tanggal gagal tidak menjadi partial success.
2. Bentuk context komparatif yang tetap menyatakan applicability/unknown/omissions
   per tanggal dan menangani evidence ID yang muncul di beberapa bucket. Pakai
   tokenizer eksak; jangan flatten tanggal sehingga model melihat fakta seolah
   semuanya berlaku serentak.
3. Sambungkan generation komparatif dan validasi citation/claim terhadap evidence
   bertanggal di bawah lease yang sama. Widening Answer.EffectiveDates tidak boleh
   menjadi satu-satunya perubahan; rendering validator dan prompt harus ikut
   mendukungnya dan direview. Kegagalan generator bukan evidence-only fallback.
4. Uji corpus nyata yang berubah antar tanggal, versi unknown/conflict, penerbit
   berbeda, dan required retrieval/answer/performance gates. Fixture bukan gold.

## Menggunakan transport evidence

Gunakan konfigurasi/backend/published corpus yang sama dengan
[evidence API](evidence-api.md) dan CLI query biasa. Dari root PowerShell:

```powershell
go run ./src/server/cmd/cli query-evidence -question 'Apa ketentuan izin?' -profile hybrid -compare-dates '2026-01-01,2025-01-01' -timeout 2m
```

`-compare-dates` menerima 2..8 tanggal unik, mempertahankan urutan pengguna dan
bersifat eksklusif dengan `-as-of`, `-current`, serta `-answer`. Zona waktu tidak
diperlukan untuk tanggal eksplisit. Dependency dan model tetap mengikuti profil;
perintah ini tidak menyiapkan corpus atau menyalakan backend secara otomatis.

Pada `POST /v1/evidence`, gunakan autentikasi/profil operator yang sama dan scope:

```json
{
  "corpus_id": "corpus:example",
  "question": "Apa ketentuan izin?",
  "requested_profile": "RETRIEVAL_PROFILE_HYBRID_RAG",
  "response_mode": "RESPONSE_MODE_COMPLETE",
  "temporal_scope": {
    "mode": "TEMPORAL_MODE_COMPARE",
    "compare_dates": [{"year": 2026, "month": 1, "day": 1}, {"year": 2025, "month": 1, "day": 1}],
    "unresolved_policy": "UNRESOLVED_POLICY_REPORT"
  }
}
```

Output JSON memakai `mode: evidence_comparison`, `profile`, `snapshot`, dan
`dates[]`. Setiap bucket berisi `effective_date`, `evidence` (C01 ProtoJSON),
`rejected`, serta `reranking` bila diminta melalui konfigurasi CLI. HTTP saat ini
tidak mengaktifkan reranker. `X-Snapshot-ID` menyatakan snapshot bersama;
`X-Effective-Date` tunggal tidak ditulis. Output AS_OF/CURRENT tidak berubah.
`POST /v1/questions` COMPARE ditolak 400; belum ada sintesis komparatif otomatis.

`api/schemas/comparison.go` memeriksa urutan/date audit, original question,
profil, corpus, full snapshot, bundle ID unik, status sukses, dan diagnostic
budget. Agregat JSON dibatasi 16 MiB selain cap protobuf library. Akumulasi
reranking memperhitungkan manifest yang berulang sebelum menyimpan scores;
cap output bukan jaminan peak RSS 16 MiB. Respons divalidasi seluruhnya sebelum
ditulis; kegagalan jaringan saat menulis tetap dapat menghasilkan transfer putus.

## Checkpoint transport dan pekerjaan tersisa

Baseline implementasi `f89cf02`; raw logs/fingerprint tersedia pada
`artifacts/verification/20261009-compare-transport/manifest.json`.
Review independen menemukan mixed comparison/single-date answer yang perlu ditolak
serta akumulasi diagnostics reranking sebelum cap. Keduanya diperbaiki dan dites
ulang. Kasus regresi meliputi tanggal invalid/duplikat/urutan, scope/snapshot/audit
mismatch, hasil parsial, cancellation, error redaction, kegagalan penulisan CLI,
budget berulang dan JSON escaping. Go 1.26.8 windows/amd64: `go test ./src/server/...`,
tes transport final, `go vet` komponen terdampak, dan CLI `query-evidence -help`
seluruhnya exit 0. Review independen scoped PASS; pemeriksaan diff dan tautan lokal
lulus. Percobaan tes awal terhalang akses sandbox ke Go cache, lalu run ulang
berhasil. Fixture transport sintetis bukan hasil corpus
nyata atau pengukuran kualitas/performa. Required benchmark tetap NOT_MEASURED.

Checkpoint berhenti atas permintaan penghematan usage pengguna. Langkah berikut:
context komparatif dengan applicability per tanggal, generation/citation bertanggal,
kemudian corpus nyata dan evaluasi. Jalur single-date RAG/draft dan demo tetap
tersedia dengan prasyarat pada README. Jangan menandai seluruh proyek selesai.
