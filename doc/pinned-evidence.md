# Pembacaan indeks dan hidrasi bukti terpin

Dokumen ini menjelaskan kontrak library yang menyambungkan indeks terpublikasi
ke evidence dan draft bersitasi. Ia menjadi pedoman integrasi Q01/A01, bukan
klaim endpoint aplikasi, kualitas hukum, atau benchmark produksi sudah selesai.

## Kepemilikan request dan snapshot

`RAGSession.AnswerQuestion` menerima pertanyaan dan `RequestContext` dari pemanggil
yang telah mengautentikasi akses corpus. Jangan menerima konteks tepercaya itu
langsung dari JSON pengguna. Session mem-pin snapshot aktif satu kali, membaca
binding katalog, menjalankan factory/workflow, memeriksa lease lagi sesudah
generation, dan melepas lease dengan konteks cleanup terpisah yang dibatasi.
Kegagalan cleanup dikembalikan sebagai error; hasil tidak diklaim sukses.

Factory menerima `PinnedIndex`. Endpoint, collection dan generation wajib sesuai
`PinnedIndex.Binding`; credential berasal dari konfigurasi tepercaya. Factory
menggunakan ulang client/model yang sudah dimuat. Snapshot dan generation diperiksa sebelum factory dipanggil dan
disalin untuk request. Deadline efektif adalah nilai paling awal dari caller,
batas session, dan expiry lease. Session tidak memperpanjang lease; request panjang
harus gagal sesuai deadline. Permintaan snapshot historis berbeda dari snapshot
aktif ditolak, tanpa pengalihan diam-diam. Library saat ini menerima Vector/Hybrid
RAG non-streaming dengan tanggal AS_OF eksplisit; CURRENT/COMPARE/graph belum aktif.

## Katalog dan asal teks

`LoadPinnedIndex` memeriksa owner, corpus, snapshot, sequence, expiry persis dan
lease hidup di PostgreSQL. Snapshot harus PUBLISHED dengan manifest dan receipt
yang memenuhi gate publication. Binding generation harus cocok dengan backend
Qdrant pada manifest. Pembacaan berikutnya tidak memilih ulang pointer aktif.
Lease bukan pengganti authorization atau mekanisme GC lengkap.

`LoadPinnedIndexRecords` mengambil maksimum 256 record melalui satu query pilihan
ID. SQL membatasi jumlah byte payload sebelum transfer, memeriksa digest/UUID,
corpus/generation/visibility dan filter berpasangan. Duplikat, record hilang,
korupsi atau batas terlampaui menggagalkan seluruh pilihan; urutan caller dipulihkan.

`SourceHydrator` mengikat hit Qdrant ke record katalog lalu mengikuti dependency
plan INDEX, DocumentBatch dan normalized text. Setiap artefak harus sesuai registry,
hash SHA-256 dan ukuran. Plan harus memuat record/chunk tersebut; source view
memeriksa pasangan versi/sumber/filter. Span teks memakai byte UTF-8 dengan akhir
eksklusif, berasal dari artefak terverifikasi. Source URL diambil dari observation
COMPLETE yang terikat blob dan visible, dengan scheme HTTP(S) tanpa userinfo.
Ini mewarisi otoritas acquisition/publication; bukan validasi ulang portal sumber.

Plan terdekode, lookup record, document view dan bytes dipakai ulang selama satu
request. Cache tidak lintas request/pin. Budget agregat artefak maksimum 64 MiB,
per artefak 16 MiB, dan bundle evidence maksimum 4 MiB adalah batas resource
implementasi, **bukan target benchmark yang sudah tercapai**. Pemanggil dapat
memilih batas lebih kecil. Budget bundle mencakup metadata dan dependency, bukan
hanya teks item. URL lookup sesudah hydration memakai data prefetched.

## Tanggal, status dan completeness

Interval memakai `[start,end)`. UNKNOWN/CONFLICT tidak dianggap unbounded dan
tanggal observasi tidak menggantikan tanggal berlaku. REPORT mempertahankan bukti
belum pasti dengan dependency review dan PARTIAL; EXCLUDE mencatat penolakan;
REQUIRE_REVIEW mengembalikan error. REPEALED tanpa batas akhir diketahui dan
NOT_YET_EFFECTIVE tanpa batas awal diketahui tetap memerlukan kebijakan unresolved.
Status berbeda antar pasangan mengikuti kebijakan yang sama; REPORT mempertahankan
CONFLICT. Context menamai status sebagai `status_at_knowledge_snapshot`, sebab
versi yang telah dicabut bisa masih relevan untuk tanggal AS_OF sebelum pencabutan.

Jika satu chunk berisi versi yang harus disertakan sekaligus versi yang harus
dikeluarkan, hydration mengembalikan error eksplisit. Menghapus SourceRef saja
akan meninggalkan teks versi yang tidak berlaku; implementasi proyeksi teks per
versi diperlukan sebelum mendukung kasus ini. Parent dan exception refs dicatat
sebagai dependency hilang/PARTIAL, belum diekspansi menjadi teks. Klaim generator
draft tetap UNREVIEWED; pemeriksaan referensi citation bukan pembuktian entailment.

## Integrasi berikutnya dan pengukuran

Sambungkan coordinator INDEX dengan inventory otoritatif, profil backend wajib,
query factory/cache generation, tokenizer prompt generator sebenarnya, serta
API/CLI terautentikasi. Reader sumber saat ini hanya menerima source snapshot
yang sama dengan target snapshot awal; lineage incremental dan parent context,
reranker penuh, writer Neo4j/graph dan streaming tetap pekerjaan berikutnya.

Ukur p95/p99 lookup lease/katalog, decode/hash/hydration, bytes/RSS, jumlah kandidat
ditolak, coverage citation, versi yang tepat dan latency request lengkap. SQL
lease/katalog dipanggil ulang pada beberapa boundary untuk menjaga admission;
ukur overhead sebelum mengubah pemeriksaan ini. Target tetap
[benchmark-targets.yaml](../configs/benchmark-targets.yaml), REQUIRED_UNMEASURED.
Tes PostgreSQL/Qdrant nyata memakai vector, generator dan token counter sintetis;
hasil terinci ada di [laporan verifikasi](verification-report-pinned-evidence.md).
