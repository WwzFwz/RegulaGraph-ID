# Review identitas provisional dari sumber

Dokumen ini menjelaskan mode `review-alias -create-provisional`: membuat inventory
canonical awal untuk entitas semantik yang belum mempunyai kandidat exact. Mode ini
melanjutkan [review alias bersumber](sourced-alias-review.md), dengan input EXTRACT
sukses, policy corpus terpin, teks asli dan persetujuan operator. Outputnya adalah
identity, profil **UNREVIEWED**, alias bersumber, lookup revision dan review receipt
dalam satu transaksi. Ini bukan keputusan bahwa dua occurrence berbeda merupakan
satu entitas hukum, bukan model `ResolutionAction.CREATE`, dan bukan publication.

## Alur dan batas keputusan

1. EXTRACT sudah lengkap dan masuk evidence catalog checkpoint sukses. Inspect
   membaca source DocumentBatch dan teks berhash; offset UTF-8 dan surface wajib
   sama persis. DocumentBatch membuktikan inventory BIND sumber, bukan target
   canonical yang sebelumnya ada.
2. Candidate policy menentukan seluruh scope lookup. Semua lookup exact harus
   lengkap dan kosong pada revision yang dipilih. Hasil kosong hanya membuktikan
   ketiadaan alias exact, tidak membuktikan ketiadaan entitas dengan nama berbeda.
3. Operator memeriksa kutipan, konteks, tipe, scope dan label. Mode awal ini berlaku
   untuk tipe ontology selain `regulation`, `organization`, dan `provision`.
   Ketiga tipe tersebut memakai target BIND yang diverifikasi secara independen;
   lookup alias kosong tidak cukup untuk meniadakan identitas BIND existing.
4. Accept membangun ulang preview, mengautentikasi checkpoint/policy/inventory,
   memeriksa CAS revision dan seluruh lookup di bawah lock corpus, lalu menulis
   identity/profile/alias/lookup/review bersama pada E+1. Tidak ada sampling model
   atau pembacaan file di dalam transaksi.
5. Bangun ulang candidate batch pada revision baru. Alias menjadi kandidat untuk
   RESOLVE LINK/DEFER biasa dengan bukti sumber. Review/receipt RESOLVE tetap
   diperlukan sebelum ASSEMBLE; registrasi ini tidak mengisi decision otomatis.

Namespace `source-occurrence-provisional:v1` memakai hash length-prefixed atas
corpus, tipe, scope, source blob ID, hash teks normalisasi, rentang byte,
manifest parser/normalizer, dan seluruh provision version yang menaungi rentang
tersebut dari DocumentBatch. ID mention/model/chunk, job, actor dan snapshot tidak
menjadi identitas. Subset SourceRefs pilihan model tidak menentukan anchor.
Perubahan evidence/policy/label tetap mengubah hash review walaupun ID occurrence
sama. ID preview belum authoritative sampai transaksi committed.

## Menjalankan

Terapkan migration **0026** setelah 0025 melalui CLI `migrate`. Gunakan konfigurasi
operator pada [panduan alias](sourced-alias-review.md). Perintah berikut memakai
placeholder; tidak menyatakan corpus produksi sudah berhasil EXTRACT:

```powershell
$selection = @(
  '-create-provisional',
  '-source', '<EXTRACT artifact ID>',
  '-mention', '<mention ID>',
  '-scope', '<scope dalam policy corpus>',
  '-label', '<label yang diperiksa operator>'
)
go run ./src/server/cmd/cli review-alias @selection

# Setelah manusia meninjau seluruh evidence pada output inspect:
go run ./src/server/cmd/cli review-alias @selection -action accept `
  -expected-revision <revision inspect> `
  -operation 'provisional-review:unique-operation' `
  -plan-sha256 '<hash inspect>' `
  -reason '<alasan provisional berdasarkan occurrence bersumber>'
```

Jangan memberikan `-canonical` atau `-target-document` pada mode ini. Inspect
menampilkan `create_provisional: true`, `identity_basis: source_occurrence_only`
serta `target_document_role: source_bind_inventory`. Profil menyebut revision
prospektif E+1; `expected_revision` tetap E untuk CAS. Accept tidak mempublikasikan
graph. Gunakan ID `entity.meta.record_id` dari preview untuk pelacakan berikutnya.

Jika stdout/ack hilang, ulangi **accept dengan seluruh argumen asli**, termasuk E,
operation, hash, actor OS dan reason. Replay memverifikasi receipt sebelum lookup
kosong saat ini; alias milik transaksi sendiri tidak menggagalkan replay. Inspect
baru tanpa revision akan menolak pembuatan ulang karena alias sudah ada. Bila
review lain mengubah revision, lakukan inspect baru; jangan mengganti angka E
pada approval lama. Kegagalan transaksi tidak meninggalkan identity tanpa profil.

## Menambahkan alias ke provisional existing

Sesudah migration **0027**, `-target-provisional` menambahkan alias bersumber ke
canonical provisional existing. Operator memberikan ID target; workflow mengambil
receipt CREATE provisional asli dan menghidrasi occurrence pembentuknya. Tidak
memilih target hanya karena label mirip atau source regulation sama.

```powershell
$aliasSelection = @(
  '-target-provisional',
  '-source', '<EXTRACT artifact ID mention baru>',
  '-mention', '<mention ID baru>',
  '-canonical', '<canonical provisional existing>',
  '-scope', '<scope profil existing>',
  '-label', '<preferred label profil existing, bukan surface alias baru>'
)
go run ./src/server/cmd/cli review-alias @aliasSelection

# Baca occurrence baru DAN occurrence target sebelum menerima:
go run ./src/server/cmd/cli review-alias @aliasSelection -action accept `
  -expected-revision <revision inspect> `
  -operation 'alias-review:variant-unique-operation' `
  -plan-sha256 '<hash inspect>' `
  -reason '<alasan kedua occurrence menunjuk referent yang sama>'
```

Mode ini melarang `-create-provisional` dan `-target-document`. Inspect menampilkan
`mention`/`contexts` baru dan `target_mention`/`target_contexts` asal beserta
`target_origin_operation`, `target_document` dan kedua source context. Label,
scope, tipe dan identity keys profil existing dipertahankan. Seluruh file dibaca
dengan budget bersama 64 MiB; konteks masing-masing sisi maksimal 4 MiB, sehingga
total kutipan tampilan dapat mencapai 8 MiB. Deadline CLI tetap berlaku.

Hash review baru memakai versi `sourced-provisional-alias-review:v1` dan mengikat
hash approval asal. Hash asal direkonstruksi dari revision, artefak, profil dan
fingerprint policy asli. Storage memeriksa ulang source catalog, ingest pins asli,
operation payload dan historical rows di bawah CAS. Policy sekarang harus tetap
mengizinkan tipe/scope target; policy baru tidak mengganti approval historis.
Receipt baru mereferensikan operation pembentukan asli melalui foreign key,
bukan alias terakhir dalam rantai. Alias tersimpan atomik dengan lookup/review;
identity dan profil tidak ditulis ulang. Replay memakai semua argumen acceptance
lama yang identik. Registry history dan artefak asal harus masih retained.

Penyamaan makna tetap keputusan operator berdasarkan bukti kedua sisi. Penambahan
alias tidak menaikkan profil dari UNREVIEWED dan tidak otomatis membuat keputusan
RESOLVE LINK atau publikasi graph. Bangun ulang kandidat pada revision terbaru,
lalu gunakan alur review/receipt RESOLVE yang berlaku. Lihat
[verifikasi target provisional](verification-report-alias-target.md).

## Integrasi yang belum selesai

Semantic CREATE melalui model, merge/split, dan evaluasi salah gabung/salah pisah
masih terbuka. Perbedaan nama tanpa exact match dapat membentuk beberapa identity
provisional sampai direview. Jangan memakai kosongnya lookup sebagai approval
penyamaan makna atau keunikan global.

Uji PostgreSQL membuktikan transaksi, restart replay, kandidat/evidence hydration
dan export canonical untuk ASSEMBLE. Rangkaian durable LINK sampai worker Rust
ASSEMBLE dari fixture baru ini belum dibuktikan sebagai satu run. EXTRACT corpus
nyata masih bergantung pada model yang valid; tidak ada perubahan benchmark.
Ukur false split/merge, lookup coverage, latency p95/p99 dan lock wait menurut
`configs/benchmark-targets.yaml`; status required tetap **NOT_MEASURED**.
