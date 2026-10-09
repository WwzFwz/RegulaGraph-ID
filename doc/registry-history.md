# Registry historis dan binding snapshot

Dokumen ini menjelaskan primitive PostgreSQL untuk membaca alias/entitas pada revisi
registry yang tetap, serta mengikat revisi itu ke publication. Perannya melindungi
query snapshot lama dari alias yang baru committed. Ini belum wiring graph worker,
Neo4j, perubahan canonical CREATE/MERGE/SPLIT, atau entity linker request.

## Input, proses, output

Namespace `lookup:` milik alias versioned. `AdvanceLookupScope` generik menolaknya
termasuk ketika scope belum ada, agar negative lookup tidak berubah tanpa registry
stamp. Gunakan writer alias yang menyimpan revision/count history atomik; generic
scope nonalias tetap tersedia. [Verifikasi](verification-report-graph-job-inventory.md)
mencatat regresi jalur ini dan kaitannya dengan admission inventory.

Coordinator memanggil `BindPublicationRegistry(publication, corpus, fence,
expectedRevision)` ketika publication masih STAGING/VALIDATING. Adapter mengunci
snapshot lalu corpus, memastikan fence publisher dan revision saat ini cocok,
lalu menyimpan binding immutable. Exact replay mengembalikan sukses tanpa mengganti
binding, termasuk setelah registry maju atau publication terminal. Replay tidak
membuka kembali publication dan tidak membuktikan graph siap dibaca. Caller tetap
bertanggung jawab mengautentikasi operator dan memvalidasi sumber/receipt GraphDelta.

Pembaca query menggunakan `LookupPinnedCanonicalAliases(pin, scopes, limits)`.
Lease harus cocok dengan owner, corpus, snapshot, sequence dan expiry. Binding hanya
dibaca dari snapshot PUBLISHED; binding hilang menghasilkan error tanpa fallback ke
registry terbaru. Hasil berupa kandidat, alias, revision setiap scope (termasuk
hasil kosong), serta revision registry yang terpin. Profil dan alias dibaca dengan
interval `[from_revision, to_revision)`, bukan waktu berlaku hukum.

`LookupCanonicalAliasesAtRevision` adalah reader internal untuk tooling/assembly,
bukan bukti otorisasi. `LookupCanonicalAliases` tetap membaca latest dalam satu
transaksi repeatable-read untuk penyusunan kandidat RESOLVE. Jalur pinned memakai
read-committed karena revision tetap immutable, supaya pemeriksaan lease setelah
query bisa melihat pelepasan lease concurrent. Deadline dibatasi expiry caller;
database memeriksa expiry dan pemilik sebenarnya sebelum dan setelah lookup.

Contoh: snapshot A terikat registry 3 dengan satu alias `old name`. Registry 4
menambahkan `new name` dan registry 5 menambah dukungan alias kedua untuk `old name`.
Snapshot A tetap melihat satu alias lama dan hasil kosong untuk nama baru. Snapshot B
yang terikat registry 5 melihat dua alias lama dan satu alias baru. Membuka ulang
repository tidak mengubah hasil karena binding dan history disimpan di PostgreSQL.

## Migrasi dan invariant

[Migration 0018](../migrations/0018_registry_history.up.sql) menyimpan history
count/revision scope serta binding publication. Writer alias merekam history dalam
transaksi yang sama dengan alias/profil dan revision corpus. Writer lookup generik
tidak boleh mengubah scope yang sudah dimiliki registry alias. Hasil diurutkan,
ambiguity dipertahankan, jumlah hasil harus sesuai count historis, dan hash/payload
serta kolom identity tetap diverifikasi oleh reader yang sama dengan jalur latest.

Corpus existing mendapat `registry_history_floor` sebesar revision saat migrasi.
Count yang diketahui disalin sebagai baseline; revision sebelum floor ditolak.
Count legacy NULL tidak diartikan nol. Corpus baru mulai dari floor 1. Tidak ada
backfill binding untuk snapshot lama karena revision historisnya tidak boleh ditebak.
Terapkan migration dan writer baru secara terkoordinasi; writer lama tidak mencatat
history. Rehearsal waktu lock dan ukuran tabel produksi belum dilakukan.

Binding bersifat opt-in pada API library ini: publication indeks lexical/dense yang
belum memakai registry view tetap dapat terpublikasi. Integrasi graph selanjutnya
harus mengikat input assembly dan generation ke binding yang sama serta mewajibkan
readiness pada profil graph. Binding saja tidak memberi bukti seluruh source atau
support pada graph lengkap.

## Resource dan verifikasi

Lookup tetap satu query batch, memiliki batas scope, alias per scope, total alias
dan bytes payload; overflow menghasilkan error tanpa truncation. Index history
`(corpus_id, scope_key, revision)` mendukung pemilihan revision terakhir sebelum pin.
Ledger bertambah menurut scope yang berubah dan publication; retention/GC belum
tersedia. Ukur p95/p99, pool/lock wait, bytes, pertumbuhan index dan memori pada corpus
referensi. Target [benchmark-targets.yaml](../configs/benchmark-targets.yaml) tetap
REQUIRED_UNMEASURED. Tes fixture dan review independen mengikuti
[verification.md](verification.md), bukan bukti akurasi hukum atau kualitas model.
