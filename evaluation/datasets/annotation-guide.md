# Pedoman anotasi G01

Dokumen ini adalah instruksi kerja untuk membuat gold parsing, graph, dan pertanyaan dari corpus
regulasi yang dibekukan. Ia menghubungkan antrean PDF kandidat D01 dengan `GoldQuestion` dan
`DatasetManifest` C01 yang dinilai E01. Antrean dengan `review_state=UNREVIEWED` bukan label;
tidak ada contoh atau tebakan model yang otomatis menjadi gold. Angka minimum dan definisi
workload tetap bersumber dari [benchmark-targets.yaml](../../configs/benchmark-targets.yaml).

## Prasyarat sebelum reviewer mulai

Bekukan `SnapshotRef` corpus, hash manifest, daftar source blob dan versi peraturan/pasal,
serta versi pedoman ini. Audit ulang byte PDF yang dipilih, izin akses, identitas sumber, dan
status parse halaman. Bila halaman, regulasi rujukan, atau versi yang diperlukan belum ada,
catat gap dan antrekan perbaikan corpus; jangan mengisi fakta dari ingatan atau internet
seolah tersedia dalam snapshot. Reviewer melihat PDF asli, hasil parse dan locator secara
berdampingan agar kesalahan OCR/reading order tidak diwariskan ke gold.

Pilih sampel berdasarkan portal, tahun, jenis dokumen, perubahan/pencabutan, text/scan/mixed,
halaman tabel, multikolom, dan rantai rujukan. Strata metadata dari
[antrean kandidat](../../tooling/corpus/README.md) hanya petunjuk awal. Review format halaman
secara manual dan laporkan jumlah tersedia, dipilih, gagal, serta gap tiap strata. JDIHN dan
sumber rujukan yang belum tersedia tetap tercatat sebagai gap, bukan diam-diam dikeluarkan
dari denominator yang disepakati.

## Label dokumen, struktur, dan graph

Untuk setiap halaman yang dipilih, tandai PDF SHA-256, source record, nomor halaman asli,
status text/scan, reading order, blok/tabel/kolom, hierarki peraturan–bab–bagian–pasal–ayat,
dan rentang byte UTF-8 pada teks original serta normalized. Rentang memakai start-inclusive
dan end-exclusive; simpan identitas kedua artefak serta pemetaannya. Tandai token kritis
nomor, angka, satuan, negasi, pengecualian, dan tanggal. Ketika karakter tidak terbaca,
gunakan status unknown/uncertain dengan lokasi sumber; jangan menebak teks agar CER tampak baik.

Untuk graph, catat setiap mention dengan surface, tipe, scope, source/version ref, dan span.
Label pasangan `same_entity` dan `different_entity` dengan alasan dan bukti. Nama atau nomor
pasal yang sama pada peraturan berbeda tetap pasangan berbeda kecuali ada identitas hukum
yang mendukung penyamaan. Catat relasi dengan predicate, arah, kondisi/pengecualian, waktu,
support span, serta versi ontology. Singkatan, ejaan alternatif, dan pergantian nama
memerlukan bukti alias bersumber. Catat kandidat yang tidak ditemukan oleh blocking agar
candidate recall tidak hanya dihitung dari pasangan yang mudah.

## Label pertanyaan dan bukti jawaban

Tulis pertanyaan berdasarkan kebutuhan pengguna dan bukti yang benar-benar ada dalam snapshot.
Tentukan tanggal berlaku yang dimaksud serta `answerability` terhadap snapshot tersebut:
`ANSWERABLE`, `UNANSWERABLE`, `AMBIGUOUS`, atau `CONFLICTING`. Dua status terakhir tidak boleh
diubah menjadi jawaban pasti hanya agar metrik lebih sederhana. Untuk `ANSWERABLE`, tulis
claim yang diharapkan, syarat dan pengecualian, semua `provision_version_id` relevan, dan
satu atau beberapa *minimal acceptable evidence sets*. Set yang alternatif mewakili jalur
bukti yang sama-sama cukup; jangan memasukkan pasal yang tidak perlu agar recall mudah lulus.
Pertanyaan multi-hop juga mencatat jalur relasi berurutan dan support tiap edge. Untuk
`UNANSWERABLE`, beri alasan gap/ketiadaan dukungan dalam snapshot, tanpa evidence set palsu.

Tandai slice `factual`, `procedural`, `multi_hop`, `temporal`, `typo`, `informal`,
`code_switched`, dan `unanswerable` sesuai isi, boleh bertumpang tindih. Variasi typo,
parafrasa, ragam informal, atau campuran bahasa dari satu pertanyaan dasar memakai satu
`base_question_group`. Jangan membuat variasi dengan sekadar mengganti kata jika bukti,
versi, atau kondisi hukumnya ikut berubah; perlakukan itu sebagai kasus baru yang direview.

## Review, adjudikasi, dan freeze

Anotator menyimpan ID, waktu, versi pedoman, dokumen/span yang diperiksa, ketidakpastian,
dan usulan label. Reviewer manusia kedua memeriksa kasus temporal, exception, multi-hop,
same/different yang membingungkan, serta sampel dari kasus biasa tanpa melihat output
sistem yang akan diuji. Ketidaksepakatan disimpan beserta alasan dan bukti; adjudikator
memilih label final atau status ambigu. Jangan menghapus kasus karena sistem salah.

Setelah adjudikasi, petakan label ke `GoldQuestion` dan `ReviewProvenance` pada
[evaluation.proto](evaluation.proto), validasi dengan [loader](loader.py), lalu bekukan
`DatasetManifest` dan hash JSONL. `train`, `development`, dan `test` dipisah berdasarkan
`base_question_group`; audit duplikasi dekat serta overlap keluarga regulasi. Label test
tidak dipakai untuk tuning parser, retrieval, resolver, model, atau prompt. Koreksi label
sesudah freeze menghasilkan versi dataset dan catatan dampak, tidak menimpa run lama.

Sebelum E01 menyatakan run eligible, periksa jumlah dan cakupan aktual terhadap target YAML,
snapshot/versi model yang sama, review provenance lengkap, evidence set dan path yang
merujuk record produksi, serta tidak ada split leakage. `CANDIDATES_ONLY`, fixture sintetis,
dan validasi schema yang lulus tetap **NOT_MEASURED** untuk kualitas hukum dan benchmark.
