# src/server/internal/retrieval

Normalisasi query mekanis opsional berada di `query/normalizer.go`. Dense/BM25 menerima search form dari workflow; graph tetap menerima original untuk offset linking. [Policy](../../../../doc/query-normalization.md).

Discovery [graph/traversal.go](graph/traversal.go) kini menelusuri batch neighborhood
terpin dengan jalur alternatif dan batas resource eksplisit. Branch graph kini
membawa kandidat source beserta discovery ke fusion, shared hydration dan draft.
Lihat [traversal](../../../../doc/graph-traversal.md) dan
[integrasi fusion](../../../../doc/graph-fusion.md). Baseline
[exact-alias linking](../../../../doc/query-entity-linking.md) aktif; konfigurasi
graph entrypoint serta disambiguasi semantik tetap pekerjaan berikutnya.

Pengubahan pertanyaan menjadi kumpulan kandidat bukti melalui pencarian lexical, dense, graph, fusion, filtering, dan reranking. Implementasi runtime berada di Go. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Tidak menyusun jawaban akhir atau membangun ulang graph pada jalur pertanyaan. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../AGENTS.md).

## Peran dan integrasi anak

Anak mengembalikan domain Evidence dengan sumber, versi, ranking asal, dan jalur graph bila ada. Skor berbagai retriever tidak dianggap sebanding; filter temporal diterapkan konsisten sebelum bukti dipakai.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Hidrasi normalized text mengikuti referensi dalam DocumentBatch terautentikasi,
sesuai keluaran worker yang tidak mendaftarkan blob teks sebagai root terpisah.
Hash, ukuran, budget dan descriptor cache tetap diverifikasi; plan/DocumentBatch
tetap wajib sesuai registry meskipun bytes pernah masuk cache sebagai nested text.
Regresi boundary berada di `hydration_artifacts_test.go`.

Hydration memeriksa plan/dokumen terhadap `PinnedIndex.EvidenceSnapshot()` asal
dan tetap mengeluarkan evidence pada snapshot query. Ini memungkinkan reuse indeks
yang di-admit PostgreSQL tanpa mengubah bytes/source provenance; lihat
[kontrak](../../../../doc/index-reuse.md).

Bundle yang dihasilkan `hydration.go` memiliki ID terikat lease, corpus/snapshot
dan pertanyaan agar hasil concurrent dapat dilacak berbeda. Item evidence tetap
memakai identitas record index/sumber yang stabil. Hash identitas ini tidak
menggantikan admission bytes, snapshot atau dukungan citation.

Subfolder: [graph/](graph/README.md), [query/](query/README.md).

Berkas: [dense.go](dense.go), [filters.go](filters.go), [fusion.go](fusion.go), [lexical.go](lexical.go), [reranking.go](reranking.go).

## Benchmark dan perhatian performa

**RETRIEVAL.** Ukur Recall@k, nDCG@k, kelengkapan semua bukti multi-hop, serta latency p50/p95 per jenis pertanyaan. Catat jumlah kandidat, hop, snapshot, dan kondisi cold/warm. Ambang wajib mengikuti [target numerik wajib](../../../../configs/benchmark-targets.yaml); jangan menganggap batas hop/kandidat atau graph selalu meningkatkan kualitas.

**RERANK.** Bandingkan nDCG@k dan kelengkapan bukti sebelum/sesudah reranking; ukur p50/p95, batch size, panjang pasangan, dan truncation. Kandidat yang hilang sebelum reranking tidak dapat dipulihkan. Target mutu dan budget latency wajib mengikuti [target numerik wajib](../../../../configs/benchmark-targets.yaml); perubahan memerlukan persetujuan pengguna.

**VERSIONING.** Ukur ketepatan pemilihan versi pada pertanyaan bertanggal, kebocoran versi yang tidak berlaku, dan penanganan status tidak diketahui. Gate: tanggal observasi tidak menggantikan tanggal berlaku; fixtures perubahan/pencabutan/unknown menghasilkan status yang ditentukan label.

Ikuti [kebijakan benchmark](../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Status lintas repositori: collector/audit D01, kontrak/validator C01, evaluator E01, serta fondasi storage/publication S01 sudah tersedia. Native inference, dense/BM25, publication dan hidrasi tersedia pada komponen pemiliknya; graph, gold dataset dan acceptance produksi belum lengkap. Status anak dijelaskan pada header masing-masing; audit integrity, build, dan fixture tidak membuktikan target kualitas atau latency.

Fusion RRF deterministik pada [fusion.go](fusion.go) aktif sebagai fungsi lokal: input cabang berbatas, rank dan keputusan filter divalidasi, hasil ambigu antar cabang dideduplikasi berdasarkan evidence key, dan seluruh provenance dipertahankan. [reranking.go](reranking.go) kini mengorelasikan seluruh hasil batch model ke kandidat, menolak hasil hilang/gagal/salah model, menyalin provenance, serta mempertahankan urutan fusion saat skor seri. Pemanggil tetap wajib membentuk key dari bukti/versi tepercaya, menerapkan filter corpus/snapshot/versi, dan mengikat pair ke teks bukti yang benar. Branch dense dan BM25 kini callable dengan client native/Qdrant; hidrasi storage dan admission snapshot tersambung; graph belum tersedia; tes lokal tidak membuktikan Recall@k atau p95/p99.

## Hidrasi sumber terpublikasi

[hydration.go](hydration.go) membaca katalog di bawah lease, memverifikasi plan,
source dan normalized text, memetakan span UTF-8 serta URL, dan menerapkan
kebijakan AS_OF. Cache bytes/plan/source view berlaku per request dengan budget
agregat. Parent/exception belum diekspansi; versi campuran yang memerlukan
proyeksi teks ditolak. Lihat [kontrak hidrasi](../../../../doc/pinned-evidence.md).
[hydration_test.go](hydration_test.go) memeriksa interval dan unresolved policy;
integrasi PostgreSQL/Qdrant sampai draft berada pada tes indexing.

Source `DocumentBatch` dapat disimpan worker dengan alamat content-addressed yang
berbeda dari `meta.record_id`. Hydrator mengautentikasi sumber melalui ref/hash
dalam plan immutable, registry, corpus, snapshot dan source view; tidak menuntut
kesamaan ID fisik/logis. Plan typed tetap exact-ID. Uji kedua bentuk alamat ada
di `initial_writer_test.go` pada komponen indexing.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [dense.go](dense.go) | Encode query with the index-compatible model and retrieve bounded snapshot/temporal candidates via Qdrant adapter. | Measure Recall@k and p95/p99 including embedding queue; test missing generation and representation mismatch. |
| [filters.go](filters.go) | Apply consistent corpus/snapshot/effective-date/visibility policy before evidence acceptance; retain explicit unknown/conflict dates. | Test boundary dates, repeals, historical snapshots and unknown-date policy; measure filter selectivity and false exclusion. |
| [fusion.go](fusion.go) | Fuse ranked lists deterministically, preserve source ranks and deduplicate by evidence/version; expose configurable RRF baseline. | Test ties, empty branches and duplicate evidence with different supports; compare recall/nDCG and candidate cost on fixed corpus. |
| [lexical.go](lexical.go) | Implement BM25 query path using the pinned analyzer/statistics generation; keep learned sparse as a distinct representation. | Test exact legal identifiers, typo/code-switch strata and empty queries; evaluate recall and latency without merging score scales. |
| [reranking.go](reranking.go) | Korelasi dan transport berbatas aktif lewat evidence_reranking.go; ukur efek urutan baru terhadap kualitas dan kelengkapan konteks. | Tes hasil reordered/missing/duplicate, salah model, token/truncation, alias provenance, dan ties lulus; nDCG, coverage, queue latency tetap NOT_MEASURED. |

`RetrieveDense` mempertahankan query asli, memakai purpose QUERY pada model milik
generation, menolak truncation/vektor invalid, dan meneruskan deadline ke backend.
`LexicalRetriever` memakai encoder frozen tanpa mengalokasikan term; semua-OOV
menghasilkan branch kosong dengan hitungan OOV, sedangkan kegagalan tetap error.
`BranchOutput` hanya kandidat dengan ID/versi/rank, belum bukti hukum terhidrasi.
Factory tepercaya wajib membuktikan artefak pembentuk encoder sesuai generation;
`LoadLexicalRetriever` kini memeriksa hash/media/corpus/statistik dan mengikat
encoder ke generation. Constructor menolak encoder tanpa binding; publication
dan authority registry tetap harus dibuktikan caller. Pemanggil
memegang read lease dan menerapkan kebijakan tanggal pada hidrasi berikutnya.
[search_test.go](search_test.go) menguji boundary dan cancellation dengan doubles.

[search_integration_test.go](search_integration_test.go) dapat dijalankan dengan
`REGULAGRAPH_TEST_QDRANT_ENDPOINT=http://127.0.0.1:56333` dan
`go test ./src/server/internal/retrieval -run TestRetrieveBranchesAgainstQdrant -count=1 -v`
dari root pada Qdrant disposable. Tes membuat collection unik lalu menghapusnya;
create/upsert/readback, branch dense/BM25 dan isolasi sequence telah lulus pada
Qdrant 1.18.0. Vektor/model dan corpus masih sintetis; artefak lexical sudah dibaca
melalui factory dengan pemeriksaan hash. Snapshot lebih tua ditolak sebelum BM25
memakai statistik masa depan. Tes tidak membuktikan kualitas atau publication corpus.
`preview.go` menjalankan BM25 in-memory untuk demo PDF lokal memakai analyzer query
bersama, offset UTF-8 halaman, overlap window dan ranking deterministik. Ini tidak
menggunakan dense/graph atau filter temporal produksi. `preview_test.go` menguji
ranking, OOV, pembatalan dan exact source offsets; kualitas gold belum diukur.

[evidence_reranking.go](evidence_reranking.go) menilai semua item terhidrasi dengan
model RERANK terpin. Request dibagi menurut jumlah pasangan dan ukuran protobuf
sebelum RPC pertama; skor hilang, salah model, atau terpotong menggagalkan tahap.
Ties mempertahankan urutan fusion lintas batch, seluruh sumber/dependency/path dan
completeness tetap utuh. Tidak ada pruning atau fallback tersembunyi. Model dan
client dipakai ulang; hasil menyertakan skor terurut, jumlah batch dan durasi.
[evidence_reranking_test.go](evidence_reranking_test.go) menguji budget, urutan,
ownership, cancellation dan failure; model fixture tidak membuktikan kualitas.

[evidence_reranking_integration_test.go](evidence_reranking_integration_test.go)
memakai endpoint/model native opt-in untuk memeriksa batching dan urutan skor
nyata. Teksnya fixture; laporan terpisah tidak mengklaim kualitas corpus/gold.
