# src/ingestion/src/bin

Worker EXTRACT memuat ontology JSONC dari `REGULAGRAPH_ONTOLOGY_PATH`, menolak beda SHA-256/version, dan membagikan objek immutable tersebut ke validator batch. Ini menjaga aturan graph Rust sejalan dengan Go.

Pin ontology yang sama mengaktifkan ASSEMBLE tanpa endpoint model. Handler membaca
plan/source/view terverifikasi dan mempersist delta/checkpoint; coordinator receipt dan
publication tetap pekerjaan Go. Lihat [worker ASSEMBLE](../../../../doc/assembly-worker.md).

Executable entry point untuk proses worker Rust. Binary di folder ini hanya melakukan konfigurasi eksplisit, inisialisasi resource sekali, dan wiring service; kebijakan domain serta transformasi tetap berada di module library agar dapat diuji tanpa membuka listener.

INDEX opt-in memasang channel native reusable dan model C01 protobuf hash-pinned
melalui variabel `REGULAGRAPH_WORKER_NATIVE_ENDPOINT`, `REGULAGRAPH_WORKER_EMBED_MANIFEST`
dan `REGULAGRAPH_WORKER_EMBED_MANIFEST_SHA256`; lihat [kontrak INDEX](../../../../doc/index-build.md).

`regulagraph-worker.rs` melayani gRPC pada loopback, memuat PDFium serta tokenizer Hugging Face hash-pinned sekali saat startup, membatasi message/concurrency/status registry, dan melakukan shutdown terkontrol. Deployment lintas host wajib menambahkan termination TLS terautentikasi sebelum listener boleh diperluas dari loopback.

Ukur startup/cold load terpisah dari latency warm, lalu catat queue time, p95/p99, throughput, RSS, cancellation lag, dan error per stage. Angka wajib tetap mengacu pada `configs/benchmark-targets.yaml`; belum ada acceptance benchmark produksi.

`regulagraph-lexical.rs` adalah worker offline untuk vocabulary/freeze BM25 dari
referensi C01 terpin. Ia memakai library population, membaca dictionary allocation
Go dan menyimpan statistik immutable; tidak membuka listener atau melakukan
publication. Output baru dan exit code wajib diperiksa sebelum consumption.
[Panduan operator](../../../../doc/lexical-population.md) menjelaskan dua pass.
