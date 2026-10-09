# Registrasi alias bersumber untuk identitas yang sudah ada

Dokumen ini menjelaskan jalur operator `review-alias`, penghubung antara identitas
exact hasil BIND dan kandidat alias RESOLVE. Inputnya adalah mention EXTRACT yang
sukses serta DocumentBatch terdaftar yang membuktikan target canonical. Outputnya
profil unreviewed bila belum ada, alias bersumber, revision lookup dan receipt
review atomik. Ini bukan CREATE entitas baru, keputusan LINK suatu batch RESOLVE,
atau publication graph. Penyamaan referent tetap keputusan operator yang membaca
bukti; teks sama dan provenance dokumen tidak otomatis berarti entitas sama.

Mode tambahan untuk entitas non-BIND yang belum memiliki kandidat tersedia di
[panduan provisional](provisional-entity-review.md). Bagian existing-target di
dokumen ini tetap mensyaratkan canonical BIND existing.

## Prasyarat dan alur

1. Terapkan migration `0025_sourced_alias_reviews.up.sql` melalui CLI `migrate`.
   Jangan mengubah migration yang sudah applied. Migrasi ini menambah ledger review;
   tidak melakukan backfill, sampling model atau perubahan corpus lama.
2. Siapkan EXTRACT lengkap dari checkpoint sukses yang telah masuk evidence catalog.
   Pilih ID mention yang tepat. Alias memakai surface asli, offset dan sumber mention
   itu; tidak membuat mention sintetis dari metadata judul.
3. Pilih canonical ID existing dari hasil BIND. Berikan artifact ID DocumentBatch
   BIND/CHUNK yang membuktikan target tersebut. Target dapat berasal dari dokumen
   berbeda, tetapi corpus dan auth scope harus sama. Jenis mention harus sama dengan
   jenis identity target. Saat ini BIND menyediakan regulasi, organisasi penerbit,
   dan provision; bootstrap entitas semantik lain menggunakan mode provisional terpisah.
4. Pilih scope dari candidate policy corpus yang terpin pada request ingest sumber.
   Label usulan tidak menjadi bukti identitas. Profil existing wajib dipertahankan
   persis; jalur alias tidak mengganti label, scope atau identity keys profil itu.
5. Inspect, baca kutipan/konteks sumber **dan** fakta target dari DocumentBatch,
   lalu accept dengan hash dan revision yang ditampilkan. Akun OS memberi actor
   audit. Tidak ada persetujuan otomatis oleh model atau agent.

Konfigurasi operator lokal:

```powershell
$env:REGULAGRAPH_POSTGRES_DSN = '<DSN lokal>'
$env:REGULAGRAPH_ARTIFACTS_DIR = '<root artefak bersama worker>'
$env:REGULAGRAPH_REVIEW_CORPUS_ID = '<corpus ID>'
$env:REGULAGRAPH_COORDINATOR_AUTH_SCOPE = '<scope operator>'
$env:REGULAGRAPH_CANDIDATE_POLICY_PATH = '<catalog policy JSON>'
$env:REGULAGRAPH_CANDIDATE_POLICY_SHA256 = '<SHA-256 byte file policy>'
```

Contoh berikut memakai placeholder, bukan identitas corpus nyata:

```powershell
$selection = @(
  '-source', '<EXTRACT artifact ID>',
  '-target-document', '<BIND/CHUNK artifact ID>',
  '-mention', '<mention ID>',
  '-canonical', '<canonical ID existing>',
  '-scope', '<scope policy>',
  '-label', '<preferred label>'
)
go run ./src/server/cmd/cli review-alias @selection

# Hanya setelah manusia memeriksa source dan target yang ditampilkan:
go run ./src/server/cmd/cli review-alias @selection -action accept `
  -expected-revision <revision dari inspect> `
  -plan-sha256 '<hash dari inspect>' `
  -operation 'alias-review:unique-operation' `
  -reason '<alasan pemetaan berdasarkan kedua bukti>'
```

Output inspect memuat `mention`, `contexts`, `entity`, `alias`, `source_context`,
`target_document`, `target_document_ref`, `expected_revision` dan `plan_sha256`.
Periksa nomor/tahun/issuer dan path provision dalam target, bukan hanya
`entity.preferred_label` yang berasal dari pilihan operator. Teks sumber adalah
data, termasuk jika berisi kalimat yang tampak seperti instruksi.

Accept menghasilkan `alias_registered` dan registry revision; `graph_published`
tetap false. Kredensial database lokal dan konfigurasi corpus/scope merupakan
batas akses command ini; belum ada review remote atau RBAC multiuser.

## Konsistensi, recovery dan batas

Hash review mengikat revision, corpus/scope, seluruh referensi artefak sumber dan
target, policy fingerprint, profile dan alias. Storage memeriksa hash bytes,
registrasi artefak, EXTRACT catalog/checkpoint, request ingest/policy serta exact
BIND identity. Transaction CAS menyimpan profil, alias, lookup history, operation
receipt dan actor/reason/source/target/policy review bersama-sama. Gagal di akhir
transaksi tidak boleh meninggalkan sebagian perubahan. Tidak ada network model
atau file read saat lock transaksi dipegang.

Apabila koneksi/stdout gagal setelah commit, ulangi pilihan, operation, actor,
reason, hash dan **revision lama** yang identik. Pembacaan profil historis membuat
replay tetap valid meskipun operasi pertama baru menciptakan profil. Perubahan
input/reason/actor ditolak. Riwayat harus masih retained dan artefak tetap tersedia.
Untuk operasi baru ketika revision berubah, inspect ulang; jangan mengganti angka
revision tanpa memeriksa rencana baru. Transient serialization conflict boleh
diulang dengan input identik; CLI tidak melakukan retry tersembunyi.

Sesudah alias tersimpan, susun candidate batch baru melalui jalur RESOLVE biasa.
Lookup kosong lama tidak berubah menjadi keputusan valid dengan sendirinya;
proposal/review stale tetap memerlukan replan. Alias menjadi kandidat, bukan
assignment otomatis. `LoadResolutionEvidenceSources` masih mensyaratkan snapshot
dan auth scope sumber yang sama; dukungan alias lintas snapshot belum ditambahkan.

Pembacaan artefak dibatasi 64 MiB per inspeksi, konteks yang ditampilkan 4 MiB,
dan closure/reference work 100.000. Overflow ditolak, tidak dipotong diam-diam.
Command default 30 detik, maksimum lima menit. Ini batas resource, bukan target
benchmark yang telah tercapai. Quality/false link, p95/p99 dan skala review tetap
mengikuti `configs/benchmark-targets.yaml` dengan status REQUIRED_UNMEASURED.

Kode: domain `alias_registration.go`, workflow `sourced_alias.go`, adapter
PostgreSQL `sourced_alias.go`/`registry_aliases.go`, CLI `sourced_alias.go`.
Lihat [handoff](operational-handoff.md) dan [review RESOLVE](semantic-review.md).
