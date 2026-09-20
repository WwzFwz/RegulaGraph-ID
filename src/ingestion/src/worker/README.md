# src/ingestion/src/worker

Boundary transport gRPC dan orkestrasi proses batch Rust. Folder ini menerima message C01, memvalidasi deadline serta attempt/fence, menjalankan transformasi melalui processor terbatas, dan hanya mengembalikan referensi artefak immutable. Go tetap memiliki status durable, retry policy, dan publication.

Anak folder tidak boleh menambahkan schema transport sendiri atau melakukan publication backend. Konversi Tonic dan rust-protobuf harus melalui byte Protobuf dari schema yang sama. Proses besar berjalan per batch, dibatasi ukuran pesan dan concurrency, serta memeriksa cancellation di antara item. Perubahan kontrak harus dimulai dari `src/contracts/proto`.

Ukur waktu antre, latency p50/p95/p99, throughput dokumen/byte, peak RSS, cancellation lag, serta retry/dedup. Target tetap berasal dari `configs/benchmark-targets.yaml` dan berstatus **REQUIRED_UNMEASURED** sampai benchmark corpus resmi dijalankan.

Saat ini transport ProcessBatch/GetStatus/Cancel dan processor tahap PARSE tersedia. Tahap struktur, extraction, resolution, graph assembly, dan indexing harus menunggu input identitas hukum yang eksplisit; worker menolak stage tersebut dan tidak menciptakan identitas sintetis.

Jalankan `cargo run -p regulagraph-ingestion --bin regulagraph-worker` setelah mengisi `REGULAGRAPH_WORKER_ARTIFACT_ROOT`, `REGULAGRAPH_WORKER_PDFIUM_LIBRARY`, `REGULAGRAPH_WORKER_PDFIUM_SHA256`, dan `REGULAGRAPH_WORKER_PDFIUM_VERSION`. Listener default adalah `127.0.0.1:50051`; message maksimum, ukuran artefak, concurrency, dan kapasitas registry dapat diatur melalui variabel pada `.env.example`. Binary menolak bind non-loopback sampai TLS termination tersedia.
