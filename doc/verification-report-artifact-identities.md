# Verifikasi identitas fisik/logis batch worker

Laporan ini mencatat perbaikan boundary writer INDEX dan hidrasi sumber pada
8 Oktober 2026. Keluaran worker Rust disimpan dengan alamat berbasis hash,
sedangkan payload memiliki logical record ID. Kedua identitas kini dapat berbeda
untuk `DocumentBatch`/`IndexBatch` tanpa meniadakan gate provenance.
Paket perbaikan lulus tes yang dijalankan dan review independen; X01 keseluruhan
serta acceptance model/performa tetap belum selesai.

## Revision dan lingkungan

Baseline: `ea24fcb`. Implementasi dan regression tests: `60bc597`.
Windows amd64, Go 1.26.8. Tes memakai image lokal PostgreSQL 16.8 Alpine
(`3b057e1c2c6d`) dan Qdrant 1.18.0 (`b3063c673f39`), pada container disposable
tanpa volume aplikasi, port loopback 55448/56348. Kedua container dihentikan setelah
tes; ini tidak melakukan deployment aplikasi atau mengubah container proyek lain.

Raw logs dan fingerprint fixture/file berada di
`artifacts/verification/20261008-artifact-identities/`.
Fixture sumber `tests/fixtures/index-source-v1.pb` berasal dari CHUNK Rust sintetis;
plan/batch dari `wire-cases.json`. Checkpoint SQL, teks, statistik dan model output
tetap seed sintetis. PostgreSQL, Qdrant, publication, retrieval, hydration serta
workflow draft menggunakan implementasi produksi. Worker/model nyata tidak
dijalankan ulang pada paket ini.

## Perubahan dan hasil

| Pemeriksaan | Expected dan actual | Status |
| --- | --- | --- |
| Regression pada implementasi baseline | Ref fisik batch ditolak dengan `artifact identity/corpus mismatch`; loader juga menerima media type yang salah | Kegagalan yang diharapkan teramati, test exit 1; `before-regression.log` |
| Loader sesudah perbaikan | Ref DocumentBatch fisik maupun logical diterima; analyzer typed tetap exact-ID; corpus/type/hash/registration/budget/drift salah ditolak; cache tidak menghitung byte dua kali | PASS, exit 0 |
| Publication pada dua bentuk alamat | Source/batch dengan ID logical atau content-addressed mencapai publication dan hidrasi; output logical ID di luar plan tetap ditolak | PASS, exit 0 |
| Backend parsial/lost reply | Receipt Neo4j wajib yang hilang tetap memblokir publication; write reply hilang dapat di-retry tanpa publish parsial | PASS, exit 0 |
| Published RAG | Search hybrid, source spans/URLs dan snapshot sampai draft bersitasi; model/token counter sintetis tidak berubah menjadi klaim dukungan semantik | PASS, exit 0 |
| Regresi semua Go | `go test ./src/server/... -count=1` dengan endpoint disposable lulus; opt-in lain tanpa prasyarat tetap bukan bukti tes integrasi eksternal | PASS, exit 0; `go-all.log` |
| Static diagnostics | `go vet ./src/server/internal/indexing ./src/server/internal/retrieval ./src/server/internal/domain` tanpa diagnostic | PASS, exit 0; `go-vet.log` |
| Review independen | Agent `verify_worker_artifact_ids` memeriksa Rust producer, checkpoint binding, Go validators, diff dan regression tambahan | Tidak ada temuan penghalang |
| Kualitas model dan required latency/throughput | Gold, corpus/model run dan workload release tidak dijalankan | NOT_MEASURED |

Reviewer juga menjalankan unit loader/temporal admission dengan GOCACHE workspace,
exit 0, tanpa mengakses database. Saran cakupan tambahannya diterapkan: batch dengan
logical ID baru dan dependency ownership konsisten harus tetap ditolak secara
spesifik oleh immutable-plan admission. Reviewer memeriksa tambahan tersebut;
run semua Go sesudah perubahan lulus. Patch produksi tidak berubah sesudah review.

## Reproduksi dan batas

Gunakan PostgreSQL dan Qdrant **disposable**, lalu set
`REGULAGRAPH_TEST_POSTGRES_DSN` dan `REGULAGRAPH_TEST_QDRANT_ENDPOINT` sebelum
menjalankan perintah tes di atas. Tes indexing memakai schema/collection unik;
suite PostgreSQL lain dapat membersihkan schema default, sehingga jangan memakai
database aplikasi. Catatan run menyimpan perintah dan exit code, bukan credential.

Source checkpoint dan immutable plan tetap menentukan artefak yang sah; perbaikan
ini tidak membuat arbitrary registered bytes menjadi sumber yang boleh diterbitkan.
Plan serta analyzer/dictionary/statistik tetap menuntut identitas typed persis.
Tidak ada perubahan Protobuf, schema lock, algoritma ranking, model atau benchmark.

Langkah berikutnya tetap coordinator INDEX corpus nyata: inventory sumber
otoritatif, plan/dictionary/statistik terpin, dispatch worker, registration output,
dan publication dengan backend wajib profilnya. Graph penuh, incremental closures,
parent/exception expansion, generator/tokenizer nyata dan acceptance belum ditutup
oleh perbaikan ini. Durasi test bukan hasil benchmark latency query.
