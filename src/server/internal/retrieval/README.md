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

Status lintas repositori: collector PDF dan kontrak/validator C01 sudah tersedia; lihat [cakupan implementasi C01](../../../../doc/contracts-implementation.md). Pipeline parsing/graph/retrieval, adapter storage, layanan model dan evaluator benchmark masih belum aktif. Status anak dijelaskan pada header masing-masing; build dan fixture tidak membuktikan target kualitas atau latency.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [dense.go](dense.go) | Encode query with the index-compatible model and retrieve bounded snapshot/temporal candidates via Qdrant adapter. | Measure Recall@k and p95/p99 including embedding queue; test missing generation and representation mismatch. |
| [filters.go](filters.go) | Apply consistent corpus/snapshot/effective-date/visibility policy before evidence acceptance; retain explicit unknown/conflict dates. | Test boundary dates, repeals, historical snapshots and unknown-date policy; measure filter selectivity and false exclusion. |
| [fusion.go](fusion.go) | Fuse ranked lists deterministically, preserve source ranks and deduplicate by evidence/version; expose configurable RRF baseline. | Test ties, empty branches and duplicate evidence with different supports; compare recall/nDCG and candidate cost on fixed corpus. |
| [lexical.go](lexical.go) | Implement BM25 query path using the pinned analyzer/statistics generation; keep learned sparse as a distinct representation. | Test exact legal identifiers, typo/code-switch strata and empty queries; evaluate recall and latency without merging score scales. |
| [reranking.go](reranking.go) | Select budgeted candidate pairs, call reranker in batches and map scores back without losing required multi-hop evidence. | Test truncation, partial results and stable ties; measure ranking quality together with queue-inclusive latency. |
