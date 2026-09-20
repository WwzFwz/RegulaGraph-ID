# src/ingestion/src/knowledge_graph/validation

Pemeriksaan invariant struktural dan kualitas graph sebelum hasil dipublikasikan ke snapshot yang digunakan retrieval. Transformasi batch berjalan dalam Rust; inference semantik menggunakan adapter model. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Bukan pengganti evaluasi semantik manusia atau alasan memaksakan seluruh graph menjadi satu komponen terhubung. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../../AGENTS.md).

## Peran dan integrasi anak

Anak melaporkan endpoint hilang, merge mencurigakan, bukti terputus, konflik versi, dan duplikasi. Bedakan error pemblokir dari diagnosis; node terpisah bisa memang benar.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Berkas: [checks.rs](checks.rs), [mod.rs](mod.rs).

## Benchmark dan perhatian performa

**GRAPH.** Ukur validitas endpoint dan provenance, ketepatan predicate/arah, kelengkapan jalur bukti, waktu assembly/traversal, dan penggunaan memori. Gate: graph yang dipublikasikan tidak memiliki endpoint/bukti wajib yang hilang. Connectivity adalah diagnosis, bukan target memaksa satu komponen.

Ikuti [kebijakan benchmark](../../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Ini adalah scaffold struktur, dokumentasi, dan build lintas bahasa. Belum ada pipeline, database adapter, transport worker, atau model yang aktif. API Go tetap scaffold; CLI collect sudah mengunduh PDF/metadata sumber; Rust dan C++ menyediakan target library; protobuf belum memiliki message/service; tooling Python belum menjalankan model. Keberhasilan build tidak menyatakan target latency atau akurasi tercapai.
