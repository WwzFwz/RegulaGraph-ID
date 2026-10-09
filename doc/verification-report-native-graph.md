# Verifikasi RPC graph nonempty dan recovery durable

Dokumen ini mencatat integrasi registry RESOLVE, preparation/inventory ASSEMBLE,
worker Rust nyata dan output commit PostgreSQL. Perannya membedakan bukti lintas
proses dari kualitas model/hukum serta publication Neo4j yang belum tersedia.

## Revision, fixture dan lingkungan

Tanggal 2026-10-09, baseline `3f84fd9e9539b30231746720365f2c47116bacaf`.
Implementasi tes tersimpan pada commit `d70239d`.
Fingerprint file dan executable disimpan pada
`artifacts/verification/20261009-native-graph/results.json`; raw log di direktori
yang sama. Go 1.26.8 windows/amd64, Rust/Cargo 1.87.0, PostgreSQL 16.8-alpine,
Qdrant 1.18.0. Worker dibangun ulang dari source saat ini menggunakan
`cargo build -p regulagraph-ingestion --bin regulagraph-worker` (exit 0).
Worker menggunakan PDFium 126.0.6462.0, tokenizer lokal BGE-M3 dan ontology JSONC
repo dengan hash byte tepat. ASSEMBLE sendiri tidak memanggil PDFium atau model.

`TestNativeGraphAssemblyPipeline` memakai schema PostgreSQL/collection Qdrant
unik per run dan satu root artefak bersama worker. Initial CHUNK/BIND metadata,
normalized text, vectors, EXTRACT dan review approvals adalah fixture sintetis.
Tidak ada klaim bahwa label organisasi/relasi fixture benar secara hukum atau
berasal dari model sungguhan. Review rows dan checkpoint EXTRACT disisipkan untuk
mengisolasi boundary RESOLVE–ASSEMBLE; workflow review manusia diuji terpisah.

Allocator canonical, registrasi alias, lookup kandidat, intent sebelum CAS,
commit LINK, output/checkpoint RESOLVE, source binding, registry export, admission,
scheduling, claim, RPC Rust, validasi delta Go dan commit STAGED memakai kode
produksi. Output berisi dua canonical entities, dua mentions, satu assertion
dan satu support. Dua endpoint harus berbeda dan evidence tetap memakai source
version serta byte span terverifikasi.

## Hasil

| Pemeriksaan | Expected dan actual | Bukti |
| --- | --- | --- |
| Rust build saat ini | PASS exit 0 | `rust-build.log` |
| Run RPC awal dengan graph kosong | PASS exit 0; satu RPC, commit, dua checkpoint setelah recovery | `pipeline.log` |
| Run RPC nonempty | PASS exit 0; dua LINK committed, graph berisi assertion/support, output terdaftar, exact checkpoint ack | `nonempty-fifth.log` |
| Restart processor/reclaim | PASS dalam run nonempty; fence 1 ke 2, output ref identik, checkpoint baru, dua checkpoint historis, nol RPC recovery | `nonempty-fifth.log` |
| Reader lease cleanup dan terminal state | PASS; jumlah reader pins kembali semula, STAGED tidak dapat diklaim ulang | `nonempty-fifth.log` |
| `go test ./src/server/...` tanpa env backend | PASS exit 0; backend-only suites skip dan diuji terpisah | `go-all.log` |
| `go vet ./src/server/...` | PASS exit 0 | `vet.log` |
| Review independen `/root/verify_index_jobs` | PASS exit 0; native nonempty dan varian receipt lama diuji ulang, tidak ada blocker ditemukan | `independent.log` |
| Gold, required latency/throughput, Neo4j publication/retrieval | NOT_MEASURED / belum diimplementasikan pada milestone ini | — |

Log awal nonempty dipertahankan. Fixture sempat gagal compile, memperluas fault
injection ke profile canonical yang baru, lupa mengirim candidate bytes ke
admission, dan memakai job ID tetap pada corpus baru. Perbaikan membatasi fault
ke issuer BIND, memasok kandidat terdaftar dan mengikat job ID ke corpus unik.
Penolakan worker terhadap job ID lintas corpus dipertahankan; tidak ada validator
produksi atau target benchmark yang dilonggarkan untuk meluluskan tes.

Reviewer memeriksa diff empat file tes secara read-only dan menjalankan
`go test ./src/server/internal/indexing -run '^(TestNativeGraphAssemblyPipeline|TestPublishedGraphSourceMembershipAgainstStores)$' -count=1 -v`
dengan endpoint nyata di atas. PASS dibatasi pada boundary integrasi ini;
persetujuan tersebut bukan acceptance K01, kualitas hukum atau Hybrid GraphRAG.

## Reproduksi

Siapkan PostgreSQL dan Qdrant disposable, terapkan prasyarat worker sesuai
[assembly-worker.md](assembly-worker.md), bangun executable Rust dan jalankan pada
loopback. Pin ontology repo saat startup dan gunakan artifact root yang sama
dengan tes. Tidak perlu endpoint semantic/native model untuk ASSEMBLE.

```powershell
$env:REGULAGRAPH_TEST_NATIVE_GRAPH = '1'
$env:REGULAGRAPH_TEST_POSTGRES_DSN = '<DSN PostgreSQL disposable>'
$env:REGULAGRAPH_TEST_QDRANT_ENDPOINT = '<URL Qdrant disposable>'
$env:REGULAGRAPH_TEST_WORKER_ENDPOINT = '127.0.0.1:55069'
$env:REGULAGRAPH_TEST_GRAPH_ARTIFACT_ROOT = '<absolute shared artifact root>'
go test ./src/server/internal/indexing -run '^TestNativeGraphAssemblyPipeline$' -count=1 -v
```

Tanpa opt-in, tes skip. Setelah opt-in, prasyarat hilang menghasilkan FAIL;
skip bukan bukti native PASS. Root khusus boleh menampung artefak historis untuk
audit; test membersihkan schema/collection miliknya. Proses worker dimiliki
operator test dan dihentikan sesudah pemeriksaan, bukan oleh package init.

## Batas dan kelanjutan

Integrasi ini menutup celah RPC nyata dan cold recovery sukses dari source
nonempty. Ini belum menjalankan daemon terus-menerus atau PDF→LLM→jawaban corpus
pengguna. Operator preparation/scheduling graph, Neo4j writer/readiness/publication,
graph retrieval, context path/answering, canonical CREATE/MERGE/SPLIT dan
reaffirmation lintas revision tetap terbuka. Angka benchmark tidak berubah;
acceptance kualitas dan performa tetap memerlukan gold serta workload eligible.
