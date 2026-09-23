# src/ingestion/src/bin

Worker EXTRACT memuat ontology JSONC dari `REGULAGRAPH_ONTOLOGY_PATH`, menolak beda SHA-256/version, dan membagikan objek immutable tersebut ke validator batch. Ini menjaga aturan graph Rust sejalan dengan Go.

Executable entry point untuk proses worker Rust. Binary di folder ini hanya melakukan konfigurasi eksplisit, inisialisasi resource sekali, dan wiring service; kebijakan domain serta transformasi tetap berada di module library agar dapat diuji tanpa membuka listener.

`regulagraph-worker.rs` melayani gRPC pada loopback, memuat PDFium serta tokenizer Hugging Face hash-pinned sekali saat startup, membatasi message/concurrency/status registry, dan melakukan shutdown terkontrol. Deployment lintas host wajib menambahkan termination TLS terautentikasi sebelum listener boleh diperluas dari loopback.

Ukur startup/cold load terpisah dari latency warm, lalu catat queue time, p95/p99, throughput, RSS, cancellation lag, dan error per stage. Angka wajib tetap mengacu pada `configs/benchmark-targets.yaml`; belum ada acceptance benchmark produksi.
