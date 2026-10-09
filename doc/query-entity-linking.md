# Query entity linking

Dokumen ini menjelaskan seed discovery read-only dari pertanyaan. Implementasi
berada di `retrieval/query/entity_linker.go`, validasi observasi di
`entity_linker_validation.go`, dan wiring di `workflows/query_graph_seeds.go`.
Ini bukan keputusan merge/LINK ingestion atau pembuktian kebenaran hukum.

Input adalah pertanyaan asli, live snapshot pin, registry revision published
graph, serta policy namespace/type dan budget tepercaya. Constructor menyalin
policy; fingerprint `exact-alias-phrases-v1` harus dicatat konfigurasi serving.
Namespace tidak disimpulkan dari portal atau nomor pasal.

Planner membuat frasa contiguous dalam jendela token terkonfigurasi dan menyimpan
offset byte UTF-8 asli. Fungsi normalisasi registry mempertahankan angka/negasi
dan tanda baca internal; whitespace diringkas dan Unicode di-lowercase.
Varian outer punctuation mencakup intermediate trims: `Pasal 1(2)?` mencoba
`pasal 1(2)`, dan `PT.?` mencoba `pt.`. Semua varian dihitung dalam budget.

Key unik `(type, scope, normalized phrase)` dibaca dalam satu batch
`LookupPinnedCanonicalAliases`. PostgreSQL memeriksa lease dan revision milik
publication, bukan latest registry. Linker memvalidasi key/revision, corpus,
profil, alias/support reference closure, duplikasi dan bytes. Support reference
belum membuktikan teks; graph/source hydration tetap wajib. Error reader tidak
diubah menjadi hasil kosong.

Output `EntityLinks` memuat method/policy hash, snapshot/revision, match dengan
offset/alias/canonical IDs, observasi positif/negatif, seluruh seed dan uncertainty.
Alias bersama tetap membawa semua kandidat serta `query_alias_ambiguous`, tanpa
memilih yang pertama atau mengarang confidence. Profil unreviewed/alias temporal
menyatakan unresolved; tidak ada seed menjadi `query_seed_unresolved`. Kata biasa
yang tidak cocok tidak otomatis dianggap mention yang hilang. Graph branch
meneruskan uncertainty ke frontier traversal dan setiap path; evidence mapping
mempertahankannya sebagai missing dependency. Graph-only tanpa seed dapat abstain
tanpa provider; hybrid tetap mempunyai dense/BM25 dengan missing graph terlihat.

Batas keras: query 64 KiB, frasa 16 token, 2.048 frasa, 4.096 key, 128 alias/key,
64 seed, registry bytes 16 MiB dan laporan 4 MiB termasuk repeated IDs/string
overhead. Aggregate lookup mengikuti batas registry. Overflow gagal eksplisit.
Deadline dicek sebelum return; callback reports diperiksa sebelum owned clone.
Ini batas admission, bukan target benchmark atau jaminan peak RSS.

Baseline hanya exact alias dalam scope/jendela terkonfigurasi; typo, parafrasa,
issuer implisit dan model disambiguation belum selesai. Unique match bukan bukti
relevansi. Alias registry yang belum materialized di graph bisa menghasilkan read
error eksplisit. API/CLI belum mengonfigurasi backend/policy graph. DTO lookup
dibagi domain/adapter tanpa perubahan protobuf atau migration.

Ukur candidate recall, false exclusions, ambiguity, scope coverage, key/alias
counts, database wait serta p50/p95/p99 pada corpus/gold terpin. Target tetap
[benchmark-targets.yaml](../configs/benchmark-targets.yaml), REQUIRED_UNMEASURED.
Native test dengan alias/model sintetis membuktikan integrasi yang diuji, bukan
kualitas hukum/model atau kelulusan release.
