# Artefak generation lexical

Dokumen ini menjelaskan handoff statistik BM25 dari builder Rust menuju reader
Go. Perannya memastikan writer dan query memakai kebijakan analyzer, dictionary,
populasi, dan formula yang sama. Implementasi ini belum menjadi worker INDEX,
katalog generation otoritatif, atau bukti publication snapshot.

`evidence.proto` mendefinisikan `LexicalAnalyzerArtifact`,
`LexicalDictionaryArtifact`, dan `LexicalStatisticsArtifact`. Field ArtifactRef
pada IndexGeneration menunjuk bytes protobuf pesan tersebut. Media type harus
tepat `application/x-protobuf; message=regulagraph.v1.<NamaPesan>`, schema version
1, dan `artifact_id` sama dengan `meta.record_id`. Content hash mengikat bytes
tersimpan, sedangkan fingerprint mapping/populasi mengikat isi semantik. Keduanya
memiliki peran berbeda; hash bukan bukti bahwa registry atau snapshot mengizinkannya.

Analyzer v1 mem-pin Unicode 15.0.0, NFC stream-safe, pelipatan ASCII, kategori
Letter, serta separator angka/kata sesuai implementasi yang sudah diuji bersama.
Batas tetap: input 2.000.000 byte, term 256 byte, dokumen 100.000 term, query 1.024
term. Nilai lain ditolak. Go dan Rust memeriksa versi tabel saat load, termasuk
build release; perubahan aturan membutuhkan identitas analyzer baru.

Formula `regulagraph-frozen-bm25-v1` memakai bobot dokumen
`tf*(k1+1)/(tf+k1*(1-b+b*length/avg_length))` dan bobot query
`qtf*ln(1+(N-df+0.5)/(df+0.5))`. `k1` positif finite; `b` finite dalam [0,1].
N mencakup chunk dengan token nol; `avg_length=total_tokens/N`.
Populasi tanpa token tidak dapat menjadi generation siap. DF terurut berdasarkan
term ID, positif, dan tidak melebihi jumlah dokumen nonempty; jumlah DF paling
sedikit jumlah dokumen nonempty dan paling banyak total token. Semua ID DF harus
ada pada dictionary dasar statistik, bukan baru muncul pada dictionary descendant.

`Bm25Statistics::freeze_artifact` membentuk statistik dari populasi dokumen yang
benar-benar dimasukkan lewat `upsert`. `build_analyzer` membentuk policy runtime
Rust. Metadata corpus/snapshot wajib cocok dengan dictionary checked. Identitas
dokumen pada populasi harus dipasok dari chunk yang sudah diverifikasi oleh
coordinator; helper ini tidak membuktikan membership atau closure dokumen.

Fingerprint populasi v1 adalah SHA-256 atas domain bytes
`regulagraph-bm25-population-v1` diikuti NUL, analyzer string length-prefixed,
jumlah dokumen, lalu setiap ID dokumen dalam urutan byte UTF-8, jumlah term unik,
dan tiap pasangan term/frequency dalam urutan byte UTF-8. Panjang string dan
jumlah item memakai uint64 big-endian; frequency memakai uint32 big-endian.
String adalah UTF-8 tanpa normalisasi tambahan. Dokumen kosong tetap masuk.
Urutan token tidak masuk fingerprint karena BM25 memakai bag of words; urutan
span sumber tetap milik artefak dokumen. Corpus/snapshot dan mapping dictionary
diikat field terpisah dalam statistik.

`LoadArtifactBM25Encoder` membaca tiga artefak sekali melalui `ReadVerified`,
memeriksa ulang hash/ukuran, decode berbatas, identity/media type, analyzer dan
dictionary/statistics. Checked parent harus cocok dengan parent yang dinyatakan
dictionary; statistics-base lama harus menjadi ancestor yang terbukti dari chain
tersebut. Term baru pada descendant memakai DF=0 dengan N tetap. Authority chain
registry masih wajib dibuktikan coordinator; bukan dipercayai dari nama revision.

`LoadLexicalRetriever` menghubungkan encoder ini ke binding Qdrant. Konstruktor
retriever menolak encoder matematika yang belum mempunyai binding artefak dan
menolak pergantian generation. Query pada snapshot lebih tua dari populasi
ditolak; pada sequence sama seluruh SnapshotRef harus identik. Snapshot lebih
baru boleh memakai statistik frozen jika admission telah membuktikan ancestry,
membership, compatibility dan read lease. Tidak ada I/O dictionary per term/query.

Load vocabulary masih membutuhkan salinan map dan validasi DF; biaya CPU/memori
harus diukur terpisah dari query warm. Population fingerprint juga membaca seluruh
populasi term. Build-plan terpin, worker INDEX, writer/publication, dan katalog
storage tetap tahap berikutnya. Target kualitas/latency tidak berubah; lihat
[benchmark-policy](benchmark-policy.md) dan [bukti verifikasi](verification-report-lexical-generation.md).
