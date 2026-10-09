# Fondasi evidence untuk perbandingan tanggal

Dokumen ini menjelaskan library Q01 untuk menyiapkan bukti bertanggal pada satu
snapshot. Ini tahap dependency bagi transport COMPARE dan generation komparatif;
CLI/API publik masih menolak COMPARE. Jangan menyebut endpoint atau sintesis
perbandingan sudah tersedia dari library ini.

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

1. Tambahkan envelope transport berisi snapshot dan daftar tanggal/bundle C01 pada
   CLI dan endpoint evidence. Validasi urutan, tanggal, profil, ukuran total dan
   snapshot sebelum output; satu tanggal gagal tidak menjadi partial success.
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
