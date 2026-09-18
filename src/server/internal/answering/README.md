# src/server/internal/answering

Penyusunan konteks, pembuatan jawaban, dan pemeriksaan citation menggunakan bukti hasil retrieval. Implementasi runtime berada di Go. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Tidak menyimpan fakta baru ke graph atau menganggap kelancaran bahasa sebagai bukti kebenaran. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../AGENTS.md).

## Peran dan integrasi anak

Anak mempertahankan hubungan claim-evidence, versi pasal, dan cakupan corpus. Bukti tidak cukup atau bertentangan harus dapat menghasilkan jawaban terbatas; pengecekan referensi berbeda dari pengecekan dukungan semantik.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Berkas: [citations.go](citations.go), [context_builder.go](context_builder.go), [generator.go](generator.go), [validation.go](validation.go).

## Benchmark dan perhatian performa

**CONTEXT.** Ukur cakupan gold evidence, kelengkapan jalur graph, duplikasi, token count, dan waktu membangun konteks. Gate: tiap item konteks dapat dipetakan ke sumber dan versi; pemotongan/ketidakcukupan bukti dilaporkan. Context budget tidak boleh diam-diam menghapus syarat penting.

**ANSWER.** Ukur ketepatan jawaban, citation precision/coverage, ketepatan versi, abstention pada pertanyaan tanpa bukti, faithfulness, latency p50/p95/p99, dan token/biaya. Gate deterministik hanya validitas referensi; dukungan semantik harus dievaluasi tersendiri dengan review manusia terkalibrasi.

Ikuti [kebijakan benchmark](../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Ini adalah scaffold struktur, dokumentasi, dan build lintas bahasa. Belum ada pipeline, database adapter, transport worker, atau model yang aktif. Go entry point hanya memberi status scaffold; Rust dan C++ menyediakan target library; protobuf belum memiliki message/service; tooling Python belum menjalankan model. Keberhasilan build tidak menyatakan target latency atau akurasi tercapai.
