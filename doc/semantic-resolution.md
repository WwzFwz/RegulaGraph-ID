# Resolusi entitas dengan konteks dan model

Dokumen ini menjelaskan jalur proposal resolusi model, batas integrasinya, dan cara menyiapkan gateway lokal. Model menilai identitas dari kandidat serta konteks dokumen; registry tetap menjadi pemilik canonical ID, revision, keputusan, dan audit. Pengguna menyetujui pendekatan hybrid pada 2026-09-24 dan memprioritaskan endpoint lokal. Model/versi final belum dipilih; penyebutan DeepSeek atau Luna belum merupakan manifest model produksi.

## Input, proses, output

`SemanticResolutionHandoff.ProposeWithModel` menerima claim RESOLVE, referensi kandidat terdaftar, SemanticBatchContext dan ProducerManifest terpin, serta client model. Workflow memverifikasi checkpoint EXTRACT, revision/fence, candidate closure, DocumentBatch, dan bytes teks normalisasi. Seluruh structural chunk yang mencakup mention menjadi konteks; versi/sumber chunk dipertahankan terpisah dari evidence mention. Kandidat berasal dari lookup yang sudah dibekukan dan tidak dipotong diam-diam.

`Semantic.ResolveBatch` pada gateway memvalidasi corpus, source, tipe, revision, byte span, model, schema, dan ontology. Payload dikirim sebagai data, terpisah dari system prompt. Provider mengembalikan LINK atau DEFER, candidate ID, rationale, dan context item IDs. Gateway menolak field hilang/null/duplikat, kandidat atau konteks buatan, serta action di luar kontrak. LINK memilih satu kandidat; DEFER mempertahankan seluruh kandidat untuk pekerjaan lanjutan. Tidak ada confidence yang diasumsikan sebagai probabilitas terkalibrasi.

ID proposal, provenance, dan expected revision dibentuk gateway. `ResolutionProposal.rationale` serta `supporting_context_ids` mempertahankan penjelasan model dan rujukan ke request audit. Referensi konteks bukan bukti hukum independen dan rationale bukan persetujuan reviewer.

Workflow memeriksa kembali correlation, producer/model, coverage, candidate closure, dan revision setelah model selesai. Request/response yang berhasil disimpan sebagai protobuf immutable beserta dependency ke EXTRACT dan kandidat. Output menyediakan RegistryResolveRequest untuk handoff commit yang sudah ada. Model tidak diberi otoritas menulis registry; LINK masih memerlukan review tersimpan yang diharuskan boundary registry saat ini.

## Replay, batas resource, dan pengukuran

Operation fingerprint mencakup input semantik, producer, serta referensi sumber/kandidat; request ID, trace ID, deadline, dan operation key transport dikeluarkan. Retry dapat menggunakan response persisten tanpa sampling ulang, termasuk sesudah restart. Artefak request yang tersimpan boleh memiliki deadline lama karena merupakan catatan audit; panggilan baru memakai deadline claim/request aktif. Crash setelah blob ditulis tetapi sebelum metadata response terdaftar masih dapat memerlukan panggilan ulang; ini bukan jaminan penagihan exactly-once.

Cache gateway terbatas oleh jumlah entry dan bytes, menyatukan operasi concurrent, serta memakai kembali item terminal ketika item lain gagal sementara. Cancellation tidak disimpan sebagai kegagalan permanen. Replay terminal persisten dimiliki workflow; reuse item parsial lintas restart belum tersedia.

Fresh source/candidate/document/text reads memiliki budget agregat. Audit input dan output masing-masing memiliki batas byte terpisah agar replay tidak gagal hanya karena membaca ulang catatan audit. Ekspansi teks, metadata mention/evidence, context provenance, dan canonical candidate dicadangkan sebelum copy/clone; coverage scan memiliki work budget. Input terlalu besar menghasilkan error eksplisit, bukan truncation tersembunyi. Peak memory tetap merupakan beberapa kali budget karena encoded bytes dan decoded protobuf hidup bersamaan; ukur RSS pada workload nyata.

Response melaporkan total duration termasuk queue serta duration antre/provider per item. Usage pada response yang direplay adalah usage output logis yang tersimpan, bukan tagihan baru pada percobaan tersebut. Untuk akuntansi biaya semua attempt diperlukan pencatatan provider request/usage tersendiri. Required metrics tetap mengikuti [benchmark-targets.yaml](../configs/benchmark-targets.yaml) dan berstatus REQUIRED_UNMEASURED.

## Menjalankan gateway lokal

Gateway memakai satu task terpin per proses. EXTRACT tetap default. Untuk RESOLVE jalankan proses lain dengan `REGULAGRAPH_SEMANTIC_TASK=RESOLVE`, listener loopback berbeda (misalnya `127.0.0.1:50053`), dan `REGULAGRAPH_SEMANTIC_PROMPT_PATH=./configs/prompts/resolution-v1.md`. Jalankan `go run ./src/server/cmd/semantic-gateway` dari root sesudah environment disiapkan.

Isi `REGULAGRAPH_WORKER_RESOLUTION_OUTPUT_SCHEMA`, `PROMPT_SHA256`, `MODEL_ID`, `MODEL_VERSION`, `WEIGHTS_SHA256`, `TOKENIZER_SHA256`, `MAX_TOKENS`, `PRECISION`, `BACKEND`, dan `ONTOLOGY_VERSION` dengan prefix `REGULAGRAPH_WORKER_RESOLUTION_`. File schema adalah `src/contracts/jsonschema/resolution-output-v1.json`; prompt hash dihitung dari bytes file yang benar-benar digunakan, termasuk line endings. Pengaturan ontology/hash, build ID, provider dan batas resource mengikuti environment gateway yang sudah ada.

`REGULAGRAPH_SEMANTIC_PROVIDER_ENDPOINT` adalah base URL server yang melayani `/v1/chat/completions` dengan structured JSON Schema output dan token usage; adapter menambahkan path tersebut. API key boleh kosong untuk server lokal. Dukungan protokol model/backend lokal harus diuji; belum ada klaim semua runtime lokal mendukung schema yang diperlukan. `NewSemanticResolutionClient` menggunakan koneksi gRPC reusable yang disediakan caller.

## Batas milestone dan pekerjaan berikutnya

Paket ini menyediakan RPC gateway, adapter client, serta workflow proposal yang dapat dipanggil dengan claim dan kandidat terverifikasi. Dispatch otomatis RESOLVE pada daemon ingestion belum dihubungkan. Pemilihan scope/alias untuk candidate generation masih menggunakan rencana caller; kandidat kanonik baru, MERGE/SPLIT, kebijakan approval otomatis, dan pengambilan bukti/deskripsi dari dokumen kandidat lain belum dituntaskan. Konteks model saat ini berasal dari dokumen mention dan metadata canonical candidates.

Langkah berikut adalah candidate planning yang menjaga recall saat scope ambigu, hidrasi bukti kandidat lintas dokumen, dan wiring coordinator dengan policy keputusan yang dapat dievaluasi. Sesudah model lokal dan manifest dipilih, jalankan gold same/different pairs, false merge/split, coverage kandidat, dampak multi-hop, latency/throughput, memori, serta biaya. Hasil fixture/build pada [laporan verifikasi](verification-report-contextual-resolution.md) tidak menggantikan pengukuran tersebut.
