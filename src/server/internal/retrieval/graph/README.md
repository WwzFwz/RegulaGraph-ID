# src/server/internal/retrieval/graph

Pemilihan titik masuk, traversal, dan pembentukan kandidat bukti dari hubungan graph saat menjawab pertanyaan. Implementasi runtime berada di Go. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Tidak membangun graph atau mengganti teks pasal dengan triple tanpa sumber. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../../AGENTS.md).

## Peran dan integrasi anak

Anak mendukung seed dari entity linking maupun hasil pencarian, mempertahankan arah/predicate, versi, dan jalur bukti. Batas hop dan kandidat bersifat konfigurabel dan dievaluasi; truncation dilaporkan.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Berkas: [evidence.go](evidence.go), [traversal.go](traversal.go),
[neighborhood.go](neighborhood.go), dan [traversal_test.go](traversal_test.go).
Traversal node-simple berjalan breadth-first dalam batch, mempertahankan jalur
alternatif, arah assertion, qualifiers/exception refs dan seluruh support record.
Kontrak GraphPath memilih satu support deterministik per edge; alternate support
tetap tersedia di result. Keluaran adalah discovery, bukan Evidence final.

## Benchmark dan perhatian performa

**RETRIEVAL.** Ukur Recall@k, nDCG@k, kelengkapan semua bukti multi-hop, serta latency p50/p95 per jenis pertanyaan. Catat jumlah kandidat, hop, snapshot, dan kondisi cold/warm. Ambang wajib mengikuti [target numerik wajib](../../../../../configs/benchmark-targets.yaml); jangan menganggap batas hop/kandidat atau graph selalu meningkatkan kualitas.

**GRAPH.** Ukur validitas endpoint dan provenance, ketepatan predicate/arah, kelengkapan jalur bukti, waktu assembly/traversal, dan penggunaan memori. Gate: graph yang dipublikasikan tidak memiliki endpoint/bukti wajib yang hilang. Connectivity adalah diagnosis, bukan target memaksa satu komponen.

Ikuti [kebijakan benchmark](../../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Discovery traversal snapshot-bound tersedia dan diuji dengan Neo4j serta output
Rust aktual. `evidence.go` kini memetakan support ke teks terautentikasi, menjaga
span UTF-8, source/version, seluruh alternatif support dan missing dependencies.
`PathEvidence` mewajibkan seluruh item tetap ada setelah context selection.
Source hydration tersedia; assertion applicability/dependency, query seed linking,
fusion branch graph dan acceptance belum lengkap. Lihat [pemetaan bukti](../../../../../doc/graph-evidence.md),
[kontrak](../../../../../doc/graph-traversal.md) serta
[verifikasi](../../../../../doc/verification-report-graph-traversal.md).

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [evidence.go](evidence.go) | Integrasikan membership seluruh PathEvidence ke fusion/context; lengkapi dependency applicability tanpa menghilangkan status partial. | Regresi span/source/version, alternate support dan batas ukuran tersedia; ukur coverage serta latency pada corpus/gold nyata. |
| [traversal.go](traversal.go), [neighborhood.go](neighborhood.go) | Sambungkan seed linking terpin dan hasil discovery ke hidrasi temporal/sumber; jangan mengubah discovery PARTIAL menjadi jawaban lengkap. | Ukur path completeness dan p95/p99 pada corpus/gold nyata; uji deadline/fan-out bersama beban query. |
