# Worker ASSEMBLE

Dokumen ini menjelaskan handler Rust yang menjalankan [plan ASSEMBLE](graph-assembly-inputs.md)
menjadi GraphDelta immutable. Ia memperluas transformasi worker; Go tetap memiliki
receipt registry, checkpoint durable, retry, live fence dan publication backend.

## Konfigurasi dan input

Binary `regulagraph-worker` mengaktifkan handler ketika `REGULAGRAPH_ONTOLOGY_PATH`
dan `REGULAGRAPH_ONTOLOGY_SHA256` terisi. Ontology JSONC dibatasi 1 MiB, diperiksa
hash, diparse sekali dan dibagikan immutable. Semantic Gateway tidak diperlukan
untuk ASSEMBLE; EXTRACT memakai ontology yang sama ketika endpoint-nya dikonfigurasi.
Bootstrap worker tetap membutuhkan PDFium/tokenizer untuk stage dokumen existing.

Request harus berisi tepat stage ASSEMBLE, ref GraphAssemblyPlan bertipe, serta
empat sources dengan urutan document, extraction, resolution, registry-view sesuai
plan. Manifest, corpus, base snapshot, auth scope dan config fingerprint harus cocok.
Request deadline dapat diperbarui saat dispatch/retry; service memeriksa deadline,
attempt/fence dan fingerprint keseluruhan request. Request `registry` generik,
`index_build_plan` dan observations ditolak agar role graph tidak ambigu.

Plan/view menggunakan logical ID yang harus cocok dengan metadata message.
Storage key wajib memakai layout content-addressed yang diterima ArtifactStore Rust;
bukan sembarang path relatif meskipun aman. Pemanggil menyimpan bytes terverifikasi
dengan hash/size/schema yang sama. Boundary ini membaca storage key yang dinyatakan,
tidak mencoba mencari artefak lain ketika checksum salah.

## Proses, keluaran dan kegagalan

Handler membatasi aggregate bytes plan, empat role dan normalized text hingga 16 MiB
sebelum pembacaan berikutnya, selain gate per-artefak/domain. Batas runtime ini lebih
ketat dari maksimum deklarasi role plan 64 MiB; coordinator perlu membagi pekerjaan
menjadi batch yang memenuhi keduanya. Wire bytes bukan estimasi tepat peak RSS.
Setiap object diperiksa hash/size/path/schema, di-decode dengan descriptor C01 sesuai
role, lalu diperiksa oleh wrapper assembly. Hanya normalized text yang diperlukan
mention/support dibaca; sumbernya harus ada tanpa duplicate ID pada DocumentBatch.

Builder memeriksa exact canonical selection, revision/ontology, referensi source dan
provenance/UTF-8; tidak memanggil model atau memuat ulang bobot. Keluaran GraphDelta
disimpan dengan media `application/x-protobuf; message=regulagraph.v1.GraphDelta`.
Respons sukses membawa checkpoint ASSEMBLE yang mengikat ID dan hash output,
job/attempt/fence serta terminal status. Retry identik menghasilkan output yang sama;
WorkerService juga menyimpan respons terminal sementara dan menolak perubahan plan
pada identitas job/attempt yang sama.

Cancellation diperiksa sebelum/sesudah pembacaan, sebelum/sesudah assembly serta
sesudah write. Panggilan assembly sinkron belum preemptible di tengah; ukur cancellation
lag pada batch maksimum. Pembatalan setelah write dapat meninggalkan blob immutable
tanpa respons/checkpoint sukses. Coordinator tidak boleh memublikasikan blob orphan.
ID checkpoint menggunakan hash plan; admission durable Go harus tetap mengikat job,
attempt/fence dan source checkpoint sehingga replay lintas job tidak tertukar.

## Status dan verifikasi

Processor dan FileStore nyata diuji dengan source/registry fixture sintetis untuk
replay, corruption, role/context drift, aggregate budget serta cancellation. Service
Tonic in-process menguji konversi Prost/rust-protobuf dan replay pada processor yang
sama; ini bukan uji jaringan lintas executable. Tidak ada claim kualitas hukum/model
atau publication graph. Coordinator persistence/inventory/receipt/admission dan Neo4j
masih perlu disambungkan sebelum menjalankan seluruh corpus.

Ukur queue wait, read/decode/assembly/write latency p50/p95/p99, throughput, RSS,
cancellation lag dan orphan rate sesuai [target required](../configs/benchmark-targets.yaml).
Angka target tetap **REQUIRED_UNMEASURED**; fixture PASS hanya membuktikan invariant
yang benar-benar diuji. Ikuti [protokol verifikasi](verification.md).
