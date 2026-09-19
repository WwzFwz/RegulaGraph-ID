# Kontrak data dan identitas

Dokumen ini menjelaskan rancangan konseptual identitas dan hubungan data lintas komponen. Definisi class, schema database, format ID konkret, dan migrasinya belum diimplementasikan; src/contracts/proto menjadi sumber bentuk wire; domain Go dan Rust merupakan representasi lokal yang mengacu padanya.

Katalog lengkap record dan field semantik ada pada [system-contracts](system-contracts.md). Aturan fingerprint, registry ID, temporal visibility, persistensi, serta recovery ada pada [storage-consistency](storage-consistency.md). Keduanya adalah baseline desain seluruh sistem; dokumen ini mempertahankan ringkasan konsep.

## Dokumen dan ketentuan

Source artifact adalah berkas atau halaman yang benar-benar diambil, dengan locator, content hash, format, dan waktu observasi. Regulation adalah identitas peraturan yang memerlukan tipe, penerbit atau yurisdiksi bila relevan, nomor, dan tahun. Jangan memakai nama tampilan atau filename sebagai satu-satunya kunci identitas.

Provision adalah identitas ketentuan dalam regulation, misalnya pasal/ayat/huruf beserta jalur induknya. Provision version menyimpan teks versi tertentu, bukti perubahan, serta metadata tanggal berlaku yang diketahui. Tanggal terbit, berlaku, observasi, dan pemrosesan dibedakan; unknown merupakan nilai sah, bukan izin untuk menebak.

Chunk menunjuk provision version dan rentang source artifact. Normalisasi teks harus menyediakan mapping ke teks sumber. Parent link memungkinkan context builder memulihkan pasal/ayat yang lengkap dengan versi sama. Chunk ID tidak dipakai sebagai canonical ID entitas.

## Entitas dan relasi

Mention menunjuk kemunculan entitas dalam konteks sumber. Canonical entity menyatukan mention yang benar-benar menunjuk objek sama. Alias membawa tipe serta ruang konteks karena nama identik dapat ambigu. Resolution decision menyimpan alasan/bukti merge agar keputusan dapat dievaluasi dan diperbaiki.

Relasi memiliki source canonical ID, predicate, target canonical ID, arah, kondisi/pengecualian, serta status explicit/inferred bila relevan. Satu relasi dapat memiliki banyak support records yang masing-masing menunjuk source artifact, lokasi teks, versi ketentuan, dan extraction run. Versi teks lama dipertahankan; alias bukan mekanisme menghapus perbedaan versi.

ID relasi dan aturan deduplikasi harus memperhitungkan semantik serta versi yang relevan. Dukungan sumber disimpan terpisah secara konseptual agar penghapusan satu sumber tidak membuang semua dukungan lain. Data yang belum terselesaikan dapat disimpan dengan status jelas dan tidak dianggap tervalidasi.

## Bukti dan jawaban

Evidence memuat identitas bukti, teks, versi, lokasi sumber, asal retriever, ranking/score asal, dan jalur graph jika ada. Fusion mempertahankan provenance ketika deduplikasi. Graph path harus tetap menjelaskan urutan relasi serta pasal yang mendukungnya.

Answer memuat teks jawaban, klaim/citation mapping, status kelengkapan, konflik yang belum terselesaikan, dan metadata run. Citation locator membuktikan referensi tersedia; dukungan semantik terhadap klaim perlu dinilai terpisah.

## Manifest dan snapshot

Manifest menghubungkan sumber dengan artefak turunannya melalui content/dependency fingerprint. Fingerprint menyertakan versi parser, chunker, schema, model, prompt, serta parameter yang memengaruhi hasil. Corpus snapshot menetapkan kumpulan versi yang konsisten untuk retrieval dan evaluasi.

Adapter persisten bertanggung jawab pada constraint lokal. Workflow bertanggung jawab pada urutan penerbitan state lintas backend dan pemulihan failure. Uji identity, source mapping, temporal unknown, idempotensi, dan dependency invalidation sebelum menjadikan kontrak ini implementasi aktif.

## Integrasi lintas runtime

Go server, Rust worker, C++ inference, dan Python evaluator mempertukarkan ID serta versi melalui src/contracts. Source range direncanakan sebagai offset byte UTF-8 start-inclusive/end-exclusive dengan identitas teks asli atau hasil normalisasi; mapping keduanya wajib dipertahankan. Jangan menyamakan byte offset dengan code point atau UTF-16 index.

Rust menghasilkan GraphDelta dan batch record; Go coordinator memegang commit dan publikasi snapshot. Protobuf saat ini hanya mendeklarasikan syntax/package, belum message/service dan belum generated binding. Field semantik seluruh sistem telah dirancang pada system-contracts; realisasi tipe/tag/service, validator, serta codegen dilakukan sebagai paket C01 sebelum konsumen produksi diimplementasikan. Perbedaan yang ditemukan saat pembuktian diperbarui pada spesifikasi dan schema secara bersamaan.
