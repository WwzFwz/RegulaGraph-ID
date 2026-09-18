# Keputusan 0001: batas scaffold dan kontrak dokumentasi

Catatan ini merekam keputusan organisasi proyek yang telah disepakati melalui percakapan dengan pemilik proyek. Perannya menjaga agar pengembangan berikutnya tidak mengubah cakupan atau menganggap hipotesis optimasi sebagai keputusan final.

Status: diterima untuk scaffold awal; pilihan runtime Python telah digantikan oleh [keputusan 0002](0002-polyglot-runtime.md). Tanggal: 2026-09-18. Aturan batas fungsional dan dokumentasi tetap berlaku. Uraian Python di bawah adalah riwayat keputusan awal.

## Keputusan

Gunakan satu aplikasi Python modular dengan pemisahan domain, ingestion, knowledge_graph, indexing, retrieval, answering, workflows, infrastructure, api, dan CLI. Evaluasi dan tests berada di luar paket aplikasi. Setiap folder yang dikelola memiliki README serta setiap file kode memiliki kontrak fungsional pada bagian paling atas.

Chunk mengikuti struktur regulasi dan dapat memulihkan konteks induk. Entity resolution serta canonical ID menghubungkan penyebutan objek yang sama. Identitas pasal dan versi teksnya dibedakan. Pemrosesan incremental menginvalidasi seluruh artefak terdampak, termasuk dependensi lintas dokumen.

Jika komponen baru berada di luar cakupan folder, pengguna memilih apakah membuat folder baru atau memperluas cakupan yang ada setelah menerima penjelasan konkret. Aturan ini tidak menambah konfirmasi untuk pekerjaan rutin yang sudah sesuai cakupan.

## Konsekuensi integrasi

Infrastruktur vendor diisolasi, workflow mengoordinasikan proses, dan evaluator memakai implementasi produksi yang sama. Provenance dan snapshot menjadi kontrak lintas anak. Dokumentasi perlu diperbarui bersama perilaku, bukan dibiarkan sebagai komentar scaffold.

## Yang belum menjadi keputusan

Tidak ada hard limit hop, top-k final, pilihan provider/model final, deployment aktif, job backend, atau target SLA. Graph dapat menerima seed dari linking maupun retrieval. BM25, learned sparse BGE-M3, dan dense adalah representasi berbeda. LangGraph tetap opsi orchestration.

## Validasi dan perubahan

Ikuti [kebijakan benchmark](../benchmark-policy.md) untuk membuktikan kualitas/biaya sebelum optimasi diterima sebagai default. Perubahan batas folder mengikuti [AGENTS.md](../../AGENTS.md); perubahan keputusan substantif dicatat melalui dokumen keputusan berikutnya.


Kebijakan angka benchmark pada catatan historis ini telah diperbarui oleh [keputusan 0003](0003-required-benchmark-targets.md): target numerik kini required pada profil referensi asumsi, dengan hasil belum diukur.
