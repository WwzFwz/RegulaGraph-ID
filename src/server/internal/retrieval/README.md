# src/server/internal/retrieval

Pengubahan pertanyaan menjadi kumpulan kandidat bukti melalui pencarian lexical, dense, graph, fusion, filtering, dan reranking. Implementasi runtime berada di Go. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Tidak menyusun jawaban akhir atau membangun ulang graph pada jalur pertanyaan. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../AGENTS.md).

## Peran dan integrasi anak

Anak mengembalikan domain Evidence dengan sumber, versi, ranking asal, dan jalur graph bila ada. Skor berbagai retriever tidak dianggap sebanding; filter temporal diterapkan konsisten sebelum bukti dipakai.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Subfolder: [graph/](graph/README.md), [query/](query/README.md).

Berkas: [dense.go](dense.go), [filters.go](filters.go), [fusion.go](fusion.go), [lexical.go](lexical.go), [reranking.go](reranking.go).

## Benchmark dan perhatian performa

**RETRIEVAL.** Ukur Recall@k, nDCG@k, kelengkapan semua bukti multi-hop, serta latency p50/p95 per jenis pertanyaan. Catat jumlah kandidat, hop, snapshot, dan kondisi cold/warm. Ambang wajib mengikuti [target numerik wajib](../../../../configs/benchmark-targets.yaml); jangan menganggap batas hop/kandidat atau graph selalu meningkatkan kualitas.

**RERANK.** Bandingkan nDCG@k dan kelengkapan bukti sebelum/sesudah reranking; ukur p50/p95, batch size, panjang pasangan, dan truncation. Kandidat yang hilang sebelum reranking tidak dapat dipulihkan. Target mutu dan budget latency wajib mengikuti [target numerik wajib](../../../../configs/benchmark-targets.yaml); perubahan memerlukan persetujuan pengguna.

**VERSIONING.** Ukur ketepatan pemilihan versi pada pertanyaan bertanggal, kebocoran versi yang tidak berlaku, dan penanganan status tidak diketahui. Gate: tanggal observasi tidak menggantikan tanggal berlaku; fixtures perubahan/pencabutan/unknown menghasilkan status yang ditentukan label.

Ikuti [kebijakan benchmark](../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Ini adalah scaffold struktur, dokumentasi, dan build lintas bahasa. Belum ada pipeline, database adapter, transport worker, atau model yang aktif. Go entry point hanya memberi status scaffold; Rust dan C++ menyediakan target library; protobuf belum memiliki message/service; tooling Python belum menjalankan model. Keberhasilan build tidak menyatakan target latency atau akurasi tercapai.
