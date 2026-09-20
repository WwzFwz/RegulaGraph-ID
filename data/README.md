# data

Penyimpanan lokal untuk sumber mentah dan hasil antara saat pengembangan. Folder ini menampung data kerja, bukan definisi algoritma. Dokumen ini menjadi kontrak cakupan folder dan panduan penempatan komponennya.

## Batas tanggung jawab

Dataset gold berada di evaluation/datasets; laporan pengukuran berada di artifacts; rahasia tidak disimpan di sini. Komponen baru harus sesuai definisi cakupan di atas. Jika tidak sesuai, jelaskan fungsi serta lokasi yang diusulkan kepada pengguna dan tanyakan apakah perlu folder baru atau perluasan cakupan folder ini sebelum menempatkannya. Ikuti [aturan repositori](../AGENTS.md).

## Peran dan integrasi anak

Anak atau artefak data harus memiliki asal, hash, dan versi schema yang dapat ditelusuri. Path lokal bukan canonical ID; konten besar dan data sensitif tidak masuk version control.

[Rencana corpus](../doc/corpus-plan.md) menetapkan sumber BPK, JDIH Kemkomdigi, dan JDIHN serta inventory, source observations, raw blob, mapping, dan staging. Folder ini tetap menampung data kerja lokal; pertanyaan/jawaban/bukti berlabel untuk penilaian berada di evaluation/datasets. Belum ada unduhan corpus massal atau gold set yang dihasilkan oleh dokumentasi ini.

Anak tidak boleh mengubah kontrak input/output secara tersembunyi. Perubahan bentuk data, identitas, versi, atau penanganan error didokumentasikan bersama konsumennya. File implementasi menjelaskan perhatian spesifiknya pada docstring atau komentar pembuka; aturan induk tetap berlaku.

## Isi saat ini

Collector D01 menghasilkan direktori acquisition untuk PDF content-addressed, HTML sumber, metadata terbaru, serta riwayat observation. Ini data kerja turunan yang diabaikan Git, bukan subkomponen source yang memerlukan README per hash. [Panduan akuisisi](../doc/acquisition.md) menjelaskan format, resume, dan lokasi file. Gold dataset tetap terpisah pada evaluation/datasets.

Artefak aktual dihasilkan collector Go. acquisition/handoff.json merangkum antrean, error, bytes PDF yang tersedia, target 3 GB, dan status unduhan ditunda atas permintaan pengguna; file ini merupakan catatan kelanjutan, bukan scheduler atau konfigurasi batas bytes yang sudah ditegakkan.

Discovery menghasilkan acquisition/discovery.json (checkpoint, asal halaman, judul tautan, error), acquisition/queue.txt (URL detail unik), serta acquisition/listings/ (HTML katalog berhash). PDF yang sudah tersedia tetap di acquisition/blobs. Antrean belum menyatakan setiap URL berhasil diunduh atau peraturan unik secara semantik.

## Benchmark dan perhatian kualitas

**SOURCE.** Ukur keberhasilan akuisisi, bytes/detik, retry, dan waktu p50/p95 per sumber. Gate: konten memiliki hash dan asal yang terlacak; unduhan parsial tidak diterbitkan sebagai sumber lengkap.

Lihat [kebijakan benchmark](../doc/benchmark-policy.md) untuk protokol pengukuran dan penetapan angka target. Target numerik wajib berada di [target numerik wajib](../configs/benchmark-targets.yaml) dengan status **REQUIRED_UNMEASURED**; belum ada hasil yang diklaim tercapai. Gate deterministik berlaku pada data yang diterbitkan dan fixtures yang relevan; hasil semantik tetap memerlukan evaluasi.

## Status implementasi

Collector dan discovery Go sudah menghasilkan artefak lokal. Pipeline parsing/graph/query produksi serta dataset gold belum tersedia; status akuisisi tidak membuktikan kualitas model atau kelengkapan corpus.
