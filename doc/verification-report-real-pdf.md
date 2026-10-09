# Verifikasi PDF akuisisi melalui pipeline dokumen

Dokumen ini mencatat verifikasi metadata binding dan struktur PDF nyata hingga
CHUNK. Perannya membedakan bukti integrasi executable dari kualitas hukum/model
serta acceptance benchmark yang belum diukur.

## Input, perubahan dan hasil

Run 2026-10-09 memakai record BPK PP No. 12 Tahun 2006, PDF 14.757 bytes,
SHA-256 `c2c761194c0a15d1858c38c7c45308e045539a80be5fd67b23cd4a050f7dbbfb`.
Record lokal berada di
`data/acquisition/records/423b6a542356f377cf9c0bfe1f3382c91700e2464ab82a06946add0204bf91c9.json`;
hash record `be5ab4291295725939fae7d058090b1fda9c8fc5123ff5fb64769037d504c3e1`.
Sumber: <https://peraturan.bpk.go.id/Details/49045/pp-no-12-tahun-2006>.
PDF/record acquisition asli tidak diubah.

Run awal mengungkap tiga masalah yang kemudian diperbaiki:

1. `document_type` kategori katalog dianggap sama dengan `regulation_type` bentuk
   hukum; page heading yang disingkat berkonflik dengan structured title. Policy
   `regulation-metadata-fields:v2` memisahkan kedua field dan memilih structured
   title per observasi. Konflik antarobservasi tetap meminta review.
2. Ayat `(1)` dan butir `a.` yang berdiri sendiri tidak dikenali. Parser kini
   menerima marker tanpa isi sebaris; pasal Romawi menjadi pembungkus tekstual
   bagi pasal angka pada dokumen perubahan, dengan penjelasan dipisahkan.
3. Catchword `f. Nomor . . .` di akhir halaman menjadi butir duplikat. Parser
   hanya mengabaikan heading preview bila marker sama terkonfirmasi pada awal
   halaman berikut, melewati blank/numeric page header secara berbatas.
   Teks tetap dipertahankan dalam span. Ellipsis tanpa pengulangan tidak dihapus.

Reviewer menemukan versi pertama aturan catchword terlalu luas; contoh butir
valid dengan ellipsis pada page boundary ditambahkan sebagai regresi. Temuan
ditutup setelah review dan tes independen. Fingerprint STRUCTURE dan CHUNK kini
memuat `structure-v2`, sehingga perubahan perilaku tidak menyamar sebagai producer
lama. Stable identity namespace untuk node yang tidak berubah tetap dipertahankan.

**PASS_SCOPED:** import → native PARSE → native STRUCTURE → Go BIND PostgreSQL →
native CHUNK menghasilkan **35 structures, 35 provision versions, 35 chunks**.
Checkpoint tiap tahap committed/succeeded; bytes output dibaca ulang dengan
hash verification. Semua chunk mempunyai source span, structure reference,
token accounting dan referensi versi yang ada dalam batch keluaran.

## Bukti dan reproduksi

Base revision `2402b176bfde8ba770dbc505e8316cb4ad11fe0f` ditambah diff yang
fingerprint per filenya direkam dalam
`artifacts/verification/20261009-real-pdf/independent-results.json`.
Worker v3 SHA-256:
`1b4bb52c7a1f4c55b7af605724e5a611b381c803f48a5a4314319bd73ae0a3cd`.
Go 1.26.8 windows/amd64; Rust worker memakai PDFium 126.0.6462.0 dan tokenizer
BGE-M3 aktual. PDFium/tokenizer/ontology pins tercatat di runtime/report JSON.
PostgreSQL lokal memakai schema disposable per test; worker dan artefak berada
di root run tersendiri, bukan corpus terpublikasi.

| Pemeriksaan | Perintah / bukti | Hasil |
| --- | --- | --- |
| Go metadata/BIND independen | Targeted tests pada domain dan workflows; `../20261009-acquisition-import/independent-document-pipeline-results.json` | 6 tes PASS |
| Rust chunking independen | `cargo test -p regulagraph-ingestion --lib document::chunking` | 26 tes PASS |
| Rust worker binding independen | Targeted worker chunk persistence test | 1 tes PASS |
| PDF nyata independen | `go test ./src/server/internal/workflows -run '^TestAcquiredPDFThroughNativeDocumentPipeline$' -count=1 -v` | Exit 0, `independent-native.log` |
| Rust library setelah perbaikan | `cargo test -p regulagraph-ingestion --lib --locked` | Exit 0; 183 PASS, 2 ignored; `rust-final.log` |
| Go regression suite | `go test ./src/server/...` | Exit 0; `go-tests.log`; integrasi tanpa environment tetap SKIP |
| README utama | Local link dan tree check | Semua 80 folder komponen README tercakup, 112 node tree berdeskripsi; `readme-check.json` |

Log berada di `artifacts/verification/20261009-real-pdf/` kecuali disebut lain.
Percobaan gagal sebelumnya tetap tersimpan untuk menunjukkan expected vs actual
dan asal perbaikan; tidak dilaporkan sebagai PASS. Build/test Rust sandbox sempat
ditolak akses compiler; rerun lokal dengan izin build berhasil.

Reproduksi test PDF memerlukan environment berikut pada terminal test:

```text
REGULAGRAPH_TEST_POSTGRES_DSN=<database test disposable>
REGULAGRAPH_TEST_DOCUMENT_WORKER=127.0.0.1:55070
REGULAGRAPH_TEST_DOCUMENT_ARTIFACT_ROOT=<shared root worker run>/objects
REGULAGRAPH_TEST_ACQUISITION_ROOT=<repo>/data/acquisition
REGULAGRAPH_TEST_ACQUISITION_RECORD=<absolute record path di atas>
```

Rust worker harus sudah berjalan dengan PDFium, tokenizer, ontology dan artifact
root yang cocok. `REGULAGRAPH_TEST_EXTRACTION_PRODUCER` **tidak diset** pada run
ini; opsi EXTRACT dalam test tidak dinilai sebagai terverifikasi. Tanpa env worker,
test di-skip dan tidak menjadi bukti integrasi native. Candidate lookup policy
test dibatasi karena test berhenti di CHUNK, bukan policy resolusi seluruh corpus.

## Kompatibilitas dan batas

BIND lama yang mengandalkan `document_type` saja dapat gagal closed ketika
dependency direkonstruksi dengan metadata policy v2. Siapkan metadata bentuk
hukum yang eksplisit, rebind/review melalui job dan producer baru; jangan mengubah
artefak/hash historis agar lolos. STRUCTURE/BIND/CHUNK lama yang berbeda hierarki
harus diproses ulang bersama, bukan mencampurkan output versi parser berbeda.

Ini satu PDF text-layer, bukan gold representative set atau bukti OCR/tabel.
Containment pasal Romawi tidak menetapkan target perubahan, tanggal berlaku,
atau legal applicability. EXTRACT/model, RESOLVE, graph/index publication dan
answering pada PDF ini belum diverifikasi. Target numerik tetap
`configs/benchmark-targets.yaml`, REQUIRED_UNMEASURED; waktu smoke test bukan
acceptance latency/throughput. Deployment tidak dikerjakan.
