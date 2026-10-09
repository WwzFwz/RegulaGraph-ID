# Pembacaan graph dari snapshot terbit

Dokumen ini menjelaskan admission PostgreSQL dan pembacaan record Neo4j yang menjadi dasar traversal Q01. Batas ini menjaga scope, snapshot, registry dan provenance; ia belum memilih jalur atau menghasilkan kandidat jawaban.

Pemanggil memperoleh pin snapshot dan scope tepercaya, lalu memanggil `Repository.LoadPinnedGraph`. Metode ini memeriksa manifest indeks yang dipublikasikan, mapping reuse bila ada, scope dari sumber indeks asal, registry revision yang masih disimpan, catalog generation, receipt Neo4j dan applied write intent. Snapshot index-only ditolak. Catalog bukan izin permanen: deadline mengikuti pin, dan pemanggil memeriksa kembali authority PostgreSQL sebelum mengembalikan bukti.

`Store.OpenReader` mencocokkan route, binding, snapshot serta seal inventory tanpa membuat schema. `Reader.ReadRecords` menerima jenis record, ID unik dan anggaran byte. Jenis yang didukung adalah entity, mention, assertion, support dan decision. Hasil mengikuti urutan ID; missing record, hash salah, tipe salah, metadata/projection berbeda atau deadline habis menggagalkan seluruh batch tanpa hasil parsial.

Setiap batch memeriksa seal sebelum dan sesudah pembacaan. Tahap pertama mengambil metadata bertipe dan ukuran; tahap kedua mengulang guard tipe/ukuran sebelum transfer. SHA256, protobuf C01, corpus, visibility sequence dan exact projection diperiksa di Go/database. Maksimum batch 256 ID dan 16 MiB payload protobuf; ini batas implementasi, bukan target benchmark baru.

Neo4j 5.26 menganggap byte array dan integer list sebagai tipe Cypher yang sama. Guard menolak string list sebelum transfer, sedangkan integer list korup tetap dibatasi jumlah elemennya dan ditolak oleh pemeriksaan `[]byte` Go. Transfer integer korup dapat mencapai sembilan byte per elemen ditambah framing; anggaran protobuf bukan batas RSS. Ukur alokasi, round trip dan p95/p99 pada workload nyata. Perbedaan representasi byte array dijelaskan dalam [dokumentasi tipe Neo4j](https://neo4j.com/docs/cypher-manual/current/values-and-types/property-structural-constructed/); tes backend mengunci perilaku versi yang digunakan.

`ReadNeighborhood` kini menambah selection serta pemeriksaan adjacency untuk [discovery traversal](graph-traversal.md). Temporal applicability klaim dan pemetaan support ke teks jawaban tetap memerlukan hidrasi bukti; branch graph ke fusion/reranking/answering belum terhubung. Kualitas model dan required benchmark tetap NOT_MEASURED; lihat [bukti reader](verification-report-graph-read.md).
