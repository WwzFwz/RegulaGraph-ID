# Protokol verifikasi implementasi

Dokumen ini menetapkan pekerjaan agent verifikasi untuk memastikan implementasi sesuai desain RegulaGraph-ID. Perannya memeriksa input, proses, output, integrasi, kualitas, dan performa berdasarkan bukti. Ia berlaku untuk model/agent implementasi mana pun, termasuk kelanjutan dengan Sol 5.6. Keberadaan dokumen ini tidak menjalankan tes atau menjamin kualitas secara otomatis.

## Acuan dan cakupan

Mulai dari [AGENTS.md](../AGENTS.md), README folder terdampak, [rencana dependency](development-plan.md), [desain sistem](system-design.md), [kontrak](system-contracts.md), dan [konsistensi storage](storage-consistency.md). Instruksi terbaru pengguna mengungguli rekomendasi implementasi. Jangan menyamakan pembagian folder dengan urutan coding. Jangan memperluas cakupan komponen tanpa mengikuti aturan repositori.

Gunakan [verifikasi kontrak](verification-contracts.md) untuk boundary data, [verifikasi pipeline](verification-pipeline.md) untuk alur input–proses–output, dan [verifikasi kualitas/performa](verification-quality.md) untuk pengukuran. [Panduan implementasi](implementation-guide.md) menjelaskan cara melanjutkan komponen; arahan file yang lebih spesifik ada pada header file dan README pemiliknya.

## Kapan agent verifikasi bekerja

Agent terpisah bekerja pada milestone atau boundary kritis setelah satu paket perubahan koheren selesai diuji implementer. Boundary kritis mencakup schema/kontrak lintas bahasa, integritas data dan provenance, storage/publication/recovery, security, versioning atau entity resolution hukum, pemilihan model dan evaluasi kualitas, integrasi retrieval-answer, serta acceptance benchmark/release. Perubahan rutin berisiko rendah—helper lokal, refactor tanpa perubahan kontrak, dokumentasi status, dan koreksi kecil—cukup diperiksa implementer secara proporsional dan digabung ke audit milestone berikutnya. Kebijakan berbasis risiko ini mencegah reviewer membaca ulang konteks yang sama pada setiap commit tanpa mengurangi gate pada bagian yang dapat merusak correctness, akurasi, atau performa sistem.

Implementer tetap bertanggung jawab menjalankan tes dan memperbaiki temuan. Agent verifikasi membaca kode/desain/bukti secara independen; ia tidak menerima klaim implementer sebagai bukti. Mulai dalam mode read-only agar temuan dan perbaikannya dapat ditelusuri. Reviewer bukan pengganti penilaian manusia untuk gold label/keberlakuan hukum. Jika fasilitas agent terpisah tidak tersedia, lakukan review manual terstruktur, nyatakan keterbatasan independensinya, dan tandai review independen sebagai pekerjaan belum terverifikasi; jangan mengarang approval.

## Urutan pelaksanaan

1. Tetapkan paket, use case, file, kontrak, dependensi, dan expected behavior sebelum coding. Tuliskan input valid/tidak valid, output yang diharapkan, efek samping, serta cara membuktikan kegagalan ditangani.
2. Implementer menyelesaikan perubahan dan menjalankan pemeriksaan yang sesuai. Simpan raw output, versi lingkungan, dan hasil aktual; jangan hanya menyimpan kalimat “tes lulus”.
3. Pada boundary/milestone kritis, agent verifikasi meninjau diff final gabungan, spesifikasi, serta bukti; jalankan ulang pemeriksaan berisiko tinggi atau susun kasus tandingan bila perlu. Pada perubahan rutin, implementer mencatat pemeriksaan untuk dibawa ke audit milestone berikutnya. Bedakan bug paket sekarang dari pekerjaan paket mendatang.
4. Implementer memperbaiki bug, menambahkan regression test yang bermakna, dan menguji ulang bagian terdampak. Agent memeriksa penyelesaian temuan; jangan mengubah status temuan tanpa bukti.
5. Simpan laporan akhir yang merujuk revision/fingerprint kode dan perintah aktual. Perubahan setelah laporan memerlukan verifikasi ulang pada bagian yang berubah.

## Pemeriksaan wajib reviewer

| Dimensi | Pertanyaan yang harus dijawab dengan bukti |
| --- | --- |
| Input | Apakah tipe, presence, ukuran, encoding, referensi, versi, deadline, dan izin divalidasi pada boundary pemiliknya? |
| Proses | Apakah algoritma mengikuti desain, urutan dependency benar, efek samping idempotent, dan resource dibatasi? |
| Output | Apakah seluruh item terhitung, ID/provenance tetap terikat, partial/error eksplisit, dan klaim selesai sesuai hasil? |
| Integrasi | Apakah perubahan kompatibel lintas runtime, snapshot/model sama, serta status siap dibedakan dari enqueue? |
| Kegagalan | Apakah timeout, cancellation, retry, crash, stale fence, data corrupt dan backend parsial diuji sesuai cakupan? |
| Kualitas | Apakah gold/denominator/split dan dukungan citation sah? Fixture sintetis tidak membuktikan kualitas model. |
| Performa | Apakah latency/throughput/memori diukur pada workload yang berlaku tanpa menyembunyikan antrean atau error? |
| Dokumentasi | Apakah header/README/status akurat, dependency baru tercatat, dan langkah reproduksi dapat dijalankan? |

## Format temuan dan laporan

Temuan memuat severity, file/baris, pemicu konkret, expected vs actual, dampak, dan cara memverifikasi perbaikan. Prioritaskan data salah/hilang, kebocoran corpus, invalid output yang diterima, deadlock/crash, serta klaim benchmark palsu. Jangan menyajikan preferensi gaya sebagai bug arsitektur.

Laporan turunan berada di `artifacts/verification/<run-id>/` dan diabaikan Git; direktori tersebut keluaran run, bukan komponen baru. Ringkasan milestone yang perlu dilacak berada dalam dokumentasi proyek. Catat commit atau fingerprint working tree, tanggal, verifier, perintah, exit code, toolchain, input fixture/dataset hash, expected/actual, cakupan, temuan terbuka, dan raw log. Data sumber tetap di data; jangan menyalin PDF ke laporan.

Gunakan status PASS, FAIL, BLOCKED, NOT_MEASURED, atau NOT_APPLICABLE **per pemeriksaan**. PASS hanya untuk pemeriksaan yang benar-benar dijalankan dan memenuhi expected behavior. Build PASS tidak menjadi quality PASS. Jika satu gate applicable gagal, paket/release terkait belum lulus meskipun pemeriksaan lain lulus. NOT_APPLICABLE memerlukan alasan berdasarkan cakupan; bukan sarana meniadakan kasus sulit. Catatan “reviewed” tanpa hasil pengujian bukan PASS.

## Aturan penyelesaian

Jangan menyatakan seluruh proyek selesai hanya karena satu paket selesai. C01 membuktikan kontrak/validasi/kompatibilitas yang diuji; S01 membuktikan storage; M01/N01 membuktikan engine/model; B01 membuktikan acceptance end-to-end. Dependensi yang belum aktif tetap ditandai. Kegagalan valid dalam scope harus diperbaiki tanpa meminta izin baru. Perubahan target benchmark memerlukan persetujuan sesuai [kebijakan benchmark](benchmark-policy.md); reviewer maupun implementer tidak boleh menurunkannya sepihak.
