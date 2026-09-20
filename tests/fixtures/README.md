# tests/fixtures

Sampel kecil dan deterministik untuk kasus parsing, versi, canonicalization, dan sumber citation. Dokumen ini menjadi kontrak cakupan folder dan panduan penempatan komponennya.

## Batas tanggung jawab

Tidak menyimpan data pengguna, rahasia, atau salinan corpus besar. Komponen baru harus sesuai definisi cakupan di atas. Jika tidak sesuai, jelaskan fungsi serta lokasi yang diusulkan kepada pengguna dan tanyakan apakah perlu folder baru atau perluasan cakupan folder ini sebelum menempatkannya. Ikuti [aturan repositori](../../AGENTS.md).

## Peran dan integrasi anak

Anak mencatat asal atau status sintetis, expected structure, serta hak penggunaan jika relevan. Contoh regulasi sintetis harus dinyatakan fiktif dan tidak dipresentasikan sebagai aturan nyata.

Anak tidak boleh mengubah kontrak input/output secara tersembunyi. Perubahan bentuk data, identitas, versi, atau penanganan error didokumentasikan bersama konsumennya. File implementasi menjelaskan perhatian spesifiknya pada docstring atau komentar pembuka; aturan induk tetap berlaku.

## Isi saat ini

File C01 di bawah sudah dapat dijalankan dengan prasyarat toolchain pada dokumentasi. Pipeline produksi dan benchmark model belum aktif.

## Benchmark dan perhatian kualitas

**TEST.** Pengujian harus memverifikasi perilaku dan kegagalan yang bermakna. Unit test tidak membutuhkan jaringan; integration/end-to-end menyatakan layanan yang diperlukan. Mock hanya memvalidasi alur, bukan akurasi model. Tidak ada klaim coverage atau test lulus sebelum dijalankan.

Lihat [kebijakan benchmark](../../doc/benchmark-policy.md) untuk protokol pengukuran dan penetapan angka target. Target numerik wajib berada di [target numerik wajib](../../configs/benchmark-targets.yaml) dengan status **REQUIRED_UNMEASURED**; belum ada hasil yang diklaim tercapai. Gate deterministik berlaku pada data yang diterbitkan dan fixtures yang relevan; hasil semantik tetap memerlukan evaluasi.

## Status implementasi

Status lintas repositori: collector PDF dan kontrak/validator C01 sudah tersedia; lihat [cakupan implementasi C01](../../doc/contracts-implementation.md). Pipeline parsing/graph/retrieval, adapter storage, layanan model dan evaluator benchmark masih belum aktif. Status anak dijelaskan pada header masing-masing; build dan fixture tidak membuktikan target kualitas atau latency.

## Penambahan C01 dan panduan verifikasi

Berkas terkait: [wire-cases.json](wire-cases.json). Dependency, cara menjalankan dan batas pembuktiannya mengikuti [implementasi C01](../../doc/contracts-implementation.md).
