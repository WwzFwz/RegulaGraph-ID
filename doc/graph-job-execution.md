# Eksekusi job ASSEMBLE

Dokumen ini menjelaskan processor, executor dan wiring daemon untuk inventory graph
yang sudah dijadwalkan. Go memiliki admission, lease, retry dan commit metadata;
Rust menghasilkan GraphDelta. STAGED belum berarti Neo4j siap dipakai query.

## Startup dan scope

Aktifkan `REGULAGRAPH_ASSEMBLE_ENABLED=true` pada `ingestion-worker` setelah migration
0020 dan seluruh pendahulunya diterapkan. Gunakan konfigurasi coordinator yang sama
untuk PostgreSQL, shared artifact root, endpoint worker loopback, auth scope, lease,
call timeout, cancellation polling dan retry. Ontology path/hash harus sama pada
coordinator, plan serta worker Rust. ASSEMBLE tidak memerlukan semantic-model endpoint;
bootstrap binary Rust existing tetap membutuhkan PDFium/tokenizer untuk stage dokumen.

`cmd/ingestion-worker/graph.go` menyusun factory admission PostgreSQL, processor dan
executor. Loop merotasi ASSEMBLE bersama dokumen/BIND/INDEX/RESOLVE yang diaktifkan.
`ClaimGraphJobInScope` memilih inventory lewat scope immutable base source snapshot;
worker tidak mengklaim lalu menggagalkan job scope lain. Claim global masih tersedia
untuk pemanggil internal, tetapi daemon selalu memakai claim berscope.

Enablement tidak menyiapkan source receipts/plan, membuat inventory atau memberi
persetujuan resolution. Preparation/scheduling masih library operator, belum satu
perintah CLI graph lengkap. Migration/Neo4j publication tetap terpisah.

## Input, proses, output

`GraphJobProcessor` menerima claim RUNNING ASSEMBLE. Locator memeriksa job/corpus/
owner/attempt/fence/expiry sebelum mengembalikan publication. Processor mem-pin snapshot
aktif dan admission memastikan snapshot tersebut sama dengan base plan. Pin mempunyai
deadline terbatas dan dilepas dengan bounded cleanup; error cleanup dilog dan lease
akan kedaluwarsa, tanpa membatalkan hasil STAGED yang sudah committed.

Pada cold restore, inventory dibaca dan divalidasi. Source binding menentukan original
CHUNK, bound CHUNK, original EXTRACT/RESOLVE dan canonical view. Semua refs harus sama
dengan metadata terdaftar; bytes diperiksa hash/size sebelum decode. Kandidat resolution
hanya dibaca untuk extraction nonempty melalui immutable intent. Aggregate input
restore dibatasi 64 MiB; factory storage memverifikasi kembali receipt, registry dan
exact plan. Ontology serta auth scope harus sesuai konfigurasi daemon.

Cache menyimpan satu admission inventory tanpa menyimpan seluruh bytes input. Tiap
pemakaian memerlukan live authorization. Jika gagal, processor melakukan satu fresh
restore/admission; tidak mengubah plan atau keputusan lama. Antrean akses cache
menghormati context cancellation. Per-job dispatch masih membaca dan memverifikasi
plan/input/output; cache tidak menghilangkan guard source/registry/lease.

Request ID bergantung pada job/attempt/fence. `ExecuteGraphAssembly` menjalankan RPC,
source projection dan output admission; `VerifiedGraphOutput.Commit` mendaftarkan
artefak/dependency/checkpoint/STAGED atomik. Shared `batch_executor.go` menyediakan
deadline, cancellation monitor dan bounded exponential retry bagi INDEX dan ASSEMBLE.
Sukses processor berarti commit, sehingga polling yang melihat lease sudah dilepas
tidak mengubahnya menjadi kegagalan.

## Recovery dan kegagalan

Bila claim mempunyai checkpoint, processor menuntut checkpoint sukses ASSEMBLE milik
job/corpus yang sama, ID latest exact, fence lama lebih kecil dan producer yang cocok.
Output harus terdaftar dan hash-nya sama dengan checkpoint. Response lokal dibentuk
untuk claim baru; checkpoint mendapat ID/fence baru. Payload output dan checkpoint
historis tidak ditulis ulang. Response tetap melewati source/hash/projection admission
lengkap dan commit; jalur ini tidak memanggil RPC atau model.

Checkpoint/output hilang, malformed, hash mismatch, atau projection tidak cocok
menghasilkan `ErrPersistentIntegrity` dan job FAILED. Kasus ini tidak boleh terus
berputar pada jalur recovery yang mempertahankan attempt budget. Database/network
unavailable serta timeout tetap dibedakan dari kerusakan immutable dan boleh retry.
Cancellation durable mengalahkan hasil gagal; lease yang sudah hilang tidak ditulis
oleh pemilik lama. Crash sebelum checkpoint tersimpan dapat mengulang assembly
Rust deterministik, tetapi tidak mengulang EXTRACT/RESOLVE model.

## Bukti dan batas

[Laporan executor](verification-report-graph-executor.md) memisahkan cold restore
PostgreSQL/Qdrant dari workflow recovery menggunakan output Rust dengan fake ports.
Belum ada run gabungan actual RPC Rust dan PostgreSQL sampai commit dari sumber
nonempty. Daemon telah diwiring dan startup/control flow diuji, bukan bukti seluruh
corpus atau Hybrid GraphRAG sudah aktif. Neo4j publication, graph retrieval, canonical
CREATE/MERGE/SPLIT dan reaffirmation lintas revision masih pekerjaan berikutnya.

Ukur cold restore dan warm dispatch secara terpisah: bytes/RSS, cache hit/refresh,
SQL pool/lock, queue, RPC, commit, retry, cancellation lag dan p95/p99. Configured
lease/call timeout tetap membatasi batch, belum ada automatic heartbeat untuk ASSEMBLE.
Target configs/benchmark-targets.yaml tetap REQUIRED_UNMEASURED sampai run eligible.
