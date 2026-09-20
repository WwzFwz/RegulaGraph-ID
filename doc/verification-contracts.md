# Verifikasi kontrak dan boundary

Dokumen ini menjadi checklist agent verifikasi untuk C01 dan setiap perubahan schema/konsumen sesudahnya. Ia menguji kesesuaian data lintas Go, Rust, C++, dan Python terhadap [system-contracts](system-contracts.md). Gunakan bersama [protokol verifikasi](verification.md); tidak semua invariant memerlukan database, tetapi invariant yang bergantung state harus dibuktikan oleh paket storage terkait.

## Sebelum mengubah schema

Petakan field ke kebutuhan semantik, pemilik, presence, satuan, versi, dan failure behavior. Wire ID berada pada field bernama atau RecordMeta.record_id dengan tipe record yang jelas; hash blob bukan canonical ID. Jangan menambahkan salinan kontrak di dataclass/domain struct yang berbeda semantik. Generated bindings dibuat ulang dari proto, tidak diedit tangan. Evaluasi memiliki schema pada evaluation/datasets/evaluation.proto dan memakai referensi produksi yang sama.

Tag field yang sudah menjadi baseline tidak boleh dipakai ulang. Penghapusan memerlukan reservation nama/nomor; perubahan tipe, cardinality, oneof, required semantics, enum atau service harus diperiksa terhadap pembaca/penulis lama. Evolusi JSON berbeda dari binary: ProtoJSON tidak melestarikan unknown fields. Tentukan reject atau transformasi eksplisit; jangan mengklaim JSON round trip lossless jika ada informasi dibuang.

## Matriks pemeriksaan

| Kelompok | Kasus valid dan kasus tandingan wajib | Pemilik pembuktian |
| --- | --- | --- |
| Primitive | ID kosong/non-ASCII, hash salah, enum unspecified/unknown, NaN/infinity, schema tak didukung | Validator setiap boundary |
| Presence | Field absent vs explicit zero/empty, oneof kosong, unknown field bertahan di binary | Codegen + uji empat bahasa |
| Offset | Byte UTF-8, start-inclusive/end-exclusive, multibyte, span melampaui teks, normalisasi many-to-many | C01 helper + I01 artifact resolver |
| Temporal | Leap year, interval terbalik, unknown/conflict vs unbounded, dua time axes | C01 field validator + I01/S01 versi |
| Dokumen | Provenance lengkap, page gagal, child/parent/order, chunk menyebut seluruh versi, source hash cocok | I01 batch guard + S01 referensi |
| Canonical | Alias ambigu, issuer/type berbeda, proposal revision stale, merge/split reversible | Registry/K01 + S01 |
| Graph | Arah edge, qualifier, exception, semua path edge memiliki support yang terlihat | K01/Q01 + S01 |
| Indeks | Dense dimensi/model sesuai, sparse terurut unik, stats generation sama, filter versi | C01 + X01/Q01 |
| Batch | Duplikat/missing/extra item ID, urutan respons berubah, item error eksplisit, model mismatch | C01 guard + N01/RPC adapter |
| Worker | Job/attempt/fence salah, lease kadaluarsa, checkpoint corpus berbeda | C01 guard + S01 scheduler |
| Publication | Receipt hilang/duplikat, hash/count/generation mismatch, durable ack tanpa search-ready | C01 guard + S01 backend nyata |
| Citation | Claim/evidence tidak ada, versi salah, locator tidak tercakup, URL bukan sumber terverifikasi | C01/answering + source registry |
| Stream | Sequence duplikat/gap, request tercampur, dua terminal, ERROR setelah teks, EOF tanpa terminal | C01 state validator + A01 transport |
| Payload | Byte/depth/item limit sebelum alokasi tak terbatas; nested unknown field; invalid encoding | Decoder setiap runtime |
| Evaluasi | Group bocor lintas split, corpus/model berbeda, denominator nol, hasil gagal menjadi PASS | Dataset validator + E01 |

Periksa kedua sisi boundary. Validator Go tidak melindungi worker yang menerima input langsung tanpa memanggil validator Rust, dan helper C++ tidak otomatis aktif hanya karena berhasil dikompilasi. Uji adapter benar-benar memanggil decode/validate sebelum kerja mahal atau side effect ketika adapter tersebut diimplementasikan.

## Bukti yang disimpan

Simpan hasil compiler, dependency lock, descriptor, synthetic fixture manifest, dan hasil round trip setiap bahasa. Uji matriks old-reader/new-writer dan new-reader/old-writer ketika ada versi released berbeda. Untuk baseline pertama, unknown-field fixture hanya membuktikan forward-preservation mekanisme; tidak menggantikan seluruh matriks versi masa depan.

Gunakan pemeriksaan domain terpisah untuk state yang tidak tersedia di pesan: keberadaan artefak, pemetaan teks, registry identity, snapshot visibility, izin corpus, lease aktif, URL sumber resmi, dan kesetaraan full rebuild. Jangan mengisi fake yang dianggap cukup untuk release. Tautkan setiap pemeriksaan state ke paket dan integration test yang akan membuktikannya.
