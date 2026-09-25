# tooling/models

Persiapan ekspor dan pemeriksaan kesetaraan model antara Python referensi dan inference native. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Fungsi di luar cakupan ini mengikuti komponen pemiliknya. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../AGENTS.md).

## Peran dan integrasi anak

Ekspor menyimpan tokenizer, pooling, normalisasi, precision, shape, serta model version. Jangan menganggap ekspor berhasil berarti skor dan kualitas retrieval setara.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../src/contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Berkas: [__init__.py](__init__.py), [export.py](export.py), [parity.py](parity.py), [load.py](load.py).

## Benchmark dan perhatian performa

Ukur p50/p95/p99, throughput, waktu antre, serta RSS/VRAM sesuai workload. Ambang wajib ada di [target numerik wajib](../../configs/benchmark-targets.yaml) (REQUIRED_UNMEASURED); ukur cold/warm terpisah dan pertahankan kualitas sumber/versi.

Ikuti [kebijakan benchmark](../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Exporter ONNX, reference/parity CLI, dan load diagnostic sudah aktif untuk model XLM-RoBERTa embedding/reranker terpin. Hasil berasal dari runtime C++ yang sama dengan serving. Kualitas gold, full workload, OCR/model selection menyeluruh, dan acceptance tetap terpisah; lihat [panduan native](../../doc/native-inference.md).

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [export.py](export.py) | Export selected model plus tokenizer/pooling/normalization/precision/dynamic-shape manifest and content hashes. | Test unsupported operations and representative multilingual/long inputs; export success alone cannot authorize production parity. |
| [parity.py](parity.py) | Compare Python reference and actual C++ outputs using the same weights/tokenizer and representative batches. | Measure vector/score/rank deviations plus downstream quality, latency and memory; quantization requires evidence against unchanged gates. |

`load.py` mencatat seluruh offered arrival, termasuk capacity rejection dan kegagalan; jangan menyebut percentile diagnostik sebagai required gate PASS. Reference/cases dibekukan bersama hash byte awal dan keluaran native divalidasi sebelum metrik.
