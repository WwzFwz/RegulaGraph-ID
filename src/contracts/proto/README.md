# src/contracts/proto

Schema Protocol Buffers sebagai format deskripsi kontrak lintas bahasa. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Fungsi di luar cakupan ini mengikuti komponen pemiliknya. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../AGENTS.md).

## Peran dan integrasi anak

Wire contract hanya untuk boundary pekerjaan besar; proto tidak berarti setiap modul menjadi service/RPC.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Subfolder: [regulagraph/](regulagraph/README.md).

Folder ini baru mendefinisikan batas komponen; belum ada implementasi runtime.

## Benchmark dan perhatian performa

Ukur p50/p95/p99, throughput, waktu antre, serta RSS/VRAM sesuai workload. Ambang wajib ada di [target numerik wajib](../../../configs/benchmark-targets.yaml) (REQUIRED_UNMEASURED); ukur cold/warm terpisah dan pertahankan kualitas sumber/versi.

Ikuti [kebijakan benchmark](../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Status lintas repositori: collector/audit D01, kontrak/validator C01, evaluator E01, serta fondasi storage/publication S01 sudah tersedia. Pipeline parsing/graph/retrieval, mutasi backend, layanan model, gold dataset, dan acceptance produksi belum aktif. Status anak dijelaskan pada header masing-masing; audit integrity, build, dan fixture tidak membuktikan target kualitas atau latency.

Kontrak RESOLVE membawa context items serta rationale/supporting context IDs secara aditif. Gateway Go memakainya; binding lintas bahasa dibangkitkan dari schema bersama dan diperiksa lewat round trip.
