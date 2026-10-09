# Reaffirmation keputusan graph lintas revision

Dokumen ini menetapkan cara menggunakan hasil RESOLVE historis pada registry view
yang lebih baru tanpa mengubah keputusan model atau menyamakan seluruh revision
dengan angka terbaru. Fitur ini dipakai otomatis oleh
[prepare-graph](graph-preparation.md) saat dependensi sumber masih identik.

## Kontrak input dan keputusan reuse

Input terdiri dari CHUNK asli, envelope CHUNK published, EXTRACT/RESOLVE asli,
candidate batch dari immutable semantic intent, source checkpoint dan target
publication. Semua references harus terdaftar, hash/ukuran cocok, dan membership
berasal dari snapshot indeks yang dipin. Target revision harus sudah ada dan
masih dalam retained history; rollback ke revision sebelum RESOLVE ditolak.

Perubahan angka global registry saja tidak berarti keputusan menjadi salah.
Contoh: dokumen A selesai RESOLVE pada R=4, lalu dokumen B menambahkan entitas
yang tidak bersinggungan pada R=5. A dapat dipakai pada view R=5 jika seluruh
konteks kandidat dan identitas dokumennya masih sama. Jika alias baru mengubah
lookup negatif menjadi positif, profil kandidat berubah, atau identitas pasal
ditutup, reuse ditolak dan sumber harus direncanakan ulang. Kandidat yang tidak
dipilih model tetap diperiksa. Sistem tidak memilih ulang canonical ID diam-diam.

Reader merekonstruksi exact RESOLVE dari operation/decision ledger, request,
approval dan kandidat asli. Ia memeriksa seluruh scope positif/negatif, revision
scope, payload alias dan profil canonical pada target view. BIND document identity
dan dependency EXTRACT ikut diperiksa; dependency eksternal yang belum didukung
tetap ditolak. Mention-free RESOLVE tidak mempunyai ledger semantik buatan, tetapi
checkpoint, dependency dan retained revision tetap wajib valid. DEFER tetap tidak
dijadwalkan ke ASSEMBLE.

## Artefak turunan dan receipt durable

Policy lama `graph-source-snapshot-envelope-v1` mempertahankan hasil byte persis
untuk sumber pada revision yang sama. Policy baru
`graph-source-registry-reaffirmation-v1` menghasilkan envelope RESOLVE turunan:

| Field | Contoh hasil |
| --- | --- |
| RESOLVE asli | RegistryRevision=4; immutable, tidak ditulis ulang |
| Decision/proposal dalam envelope | Payload dan recorded revision tetap sama dengan asli |
| Model/prompt/producer | Tetap manifest inference asli |
| RegistryRevision envelope | 5, sebagai view ASSEMBLE yang telah diperiksa |
| Root ID, request/trace, dependency | Identitas deterministik mengikat policy, hash envelope sumber dan target revision |
| Bukti/provenance | Original refs, keputusan, span dan dependency lookup tetap dipertahankan |

Perubahan target menjadi R=6 menghasilkan identitas envelope berbeda. Diagnostic
reference yang menunjuk root envelope mengikuti root baru; record bukti anak tidak
diganti. Transform murni ini sendiri tidak memberi authority.

`RegisterGraphSourceBinding` memeriksa keputusan/kandidat/dokumen sebelum lock,
kemudian mengunci snapshot, corpus, dan source job. Stamp registry revision serta
history floor harus sama dengan sebelum pemeriksaan. Publication fence/parent,
latest RESOLVE checkpoint, cancellation, registered refs dan live pin diperiksa
di dalam transaksi. Receipt immutable yang sudah ada pada migration 0019 menyimpan
policy, target revision dan refs asli/turunan secara atomik. Tidak ada migration
atau Protobuf baru; field wire yang ada sudah membolehkan decision revision lebih
lama daripada batch view. Reader Go lama menolak policy baru; upgrade coordinator
yang membaca receipt sebelum menggunakannya untuk inventory baru.

Jika registry berubah saat writer menunggu lock, transaksi ditolak. Retry membaca
ulang seluruh authority; tidak menganggap pemeriksaan sebelumnya masih valid.
Job admission/dispatch/publication tetap memverifikasi sumber dan view tersebut.

## Pemilihan view dan replay

Publication baru memakai current retained revision sebelum preflight. Semua sumber
boleh berasal dari revision berbeda yang tidak lebih baru daripada target.
Reservation dan registry binding tetap CAS: perubahan registry selama preparation
dapat menggagalkan binding. Publication yang sudah mempunyai registry binding
memakai view tersebut saat retry, meskipun registry global telah bergerak lagi.

Receipt lama tidak ditimpa. Jika publication lama sudah memiliki receipt policy
lain yang tidak cocok, pilih publication baru setelah menangani target lama;
command tidak memutasi receipt untuk memaksakan kompatibilitas.

## Verifikasi dan batas

Native fixture menjalankan real canonical allocation di antara RESOLVE dan
preparation, menghasilkan envelope R=5 dari keputusan R=4, lalu mengulang command
setelah registry maju lagi. Rust ASSEMBLE, checkpoint recovery, Neo4j/Qdrant
publication dan retrieval mengonsumsi hasil tersebut. Single-connection replay,
candidate corruption dan registry lock race diuji. Lihat
[laporan verifikasi](verification-report-graph-reaffirm.md).

Tidak ada model inference baru dalam reaffirmation. Ini menghemat panggilan model
ketika konteks tidak berubah, tetapi masih membutuhkan hash/decode/SQL checks.
Ukur p95/p99, jumlah source reused/replanned, lock wait, bytes dan RSS terhadap
[benchmark required](../configs/benchmark-targets.yaml). Test fixture tidak
membuktikan akurasi extraction, kebenaran hukum, atau target performa corpus penuh.
Perubahan semantik kandidat, canonical merge/split dan update corpus incremental
tetap membutuhkan workflow masing-masing.
