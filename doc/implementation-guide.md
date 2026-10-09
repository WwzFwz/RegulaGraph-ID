# Panduan melanjutkan implementasi

Dokumen ini memandu agent implementasi berikutnya, termasuk Sol 5.6, untuk merealisasikan seluruh rancangan RegulaGraph-ID secara terukur. Perannya menghubungkan dependency pekerjaan dengan rekomendasi pada header file dan README folder. Rekomendasi menjelaskan pekerjaan yang belum dilakukan; tidak boleh dibaca sebagai klaim bahwa fitur tersebut sudah tersedia.

## Mulai setiap sesi

Checkpoint ASSEMBLE: inventory/admission, output commit atomik serta
[processor/executor/daemon opt-in](graph-job-execution.md) tersedia. Cold restore,
cache reauthorization, snapshot pin cleanup dan checkpoint recovery diuji terpisah
pada backend nyata serta fixture output Rust. Lanjutkan actual Rust RPC bersama
PostgreSQL hingga commit dari source nonempty, operator graph preparation/scheduling,
lalu Neo4j publication dan graph retrieval. Reaffirmation lintas registry revision,
canonical mutations dan acceptance kualitas/performa K01 tetap terbuka.

[Registry history](registry-history.md) kini memiliki binding publication immutable
dan lookup di bawah live snapshot lease. Migration 0018 membatasi history corpus lama
sejak revision upgrade. Library ini belum mengikat GraphDelta/generation atau entity
linker request; langkah integrasi tersebut harus memakai revision yang sama.

Assembly Rust kini memiliki `assemble_canonical_relations`: endpoint materialization
diikuti dedup assertion/support dengan mapping, exception closure acyclic dan budget
input/output. [Kontrak](graph-canonical-identity.md) membatasi identitas temporal dan
unknown fields. [GraphDelta upsert](graph-delta.md) kini mengomposisikan source-bound
EXTRACT, registry rows, teks normalisasi dan canonicalization dengan dependencies serta
visibility. [Exporter registry view dan gate plan](graph-assembly-inputs.md) tersedia;
[worker ASSEMBLE](assembly-worker.md) mengeksekusi plan melalui artefak terverifikasi.
Lanjutkan persistence plan/view, receipt dan wiring coordinator/Neo4j. Library
ini belum worker/publication atau closure incremental.

Checkpoint lanjutan K01: [review-resolution](semantic-review.md) kini menyediakan
inspeksi dan acceptance lokal terikat hash/revision. Approval, intent dan resume atomik;
executor memakai intent tanpa model call ulang. Lanjutkan integrasi registry view,
perubahan canonical dan ASSEMBLE/Neo4j. Fixture bukan review manusia terhadap corpus.

Checkpoint 2026-10-09: population vocabulary/statistik Rust, allocator dictionary
Go, plan/inventory child INDEX durable, dan checkpoint/STAGED atomik tersedia.
Processor/executor serta daemon opt-in menyambungkan dispatch dan commit;
lihat [kontrak INDEX](index-job-inventory.md). Source envelope ber-receipt,
recovery tanpa inference ulang, pengumpulan output STAGED lengkap, dan CLI
publication dense/BM25 kini tersambung; lihat [kontrak](index-source-publication.md).
`prepare-snapshot` kini mengekspor source refs dan manifest nyata untuk population
Rust. `prepare-index` mengimpor statistik, memasang model/generation, mengekspor
manifest model biner untuk worker, dan menjadwalkan inventory lengkap. Kelanjutan
terdekat adalah menjalankan seluruh corpus nyata. Jalur native dengan source fixture
Rust sudah lulus sampai HTTP evidence, termasuk dua query concurrent dan warm reuse;
lihat [laporan native](verification-report-native-index.md) dan
[laporan API](verification-report-evidence-api.md). Graph assembly/review remote, answering penuh,
incremental serta acceptance seluruh proyek tetap diperlukan; deployment tetap
di luar scope pengerjaan yang diminta. Gold ditunda sesuai arahan pengguna,
bukan diganti dengan klaim kelulusan fixture.

Baca AGENTS.md, [development-plan](development-plan.md), [verification](verification.md), README seluruh induk folder yang akan diubah, dan catatan verifikasi terbaru. Periksa git status, diff, serta hasil tes aktual; konteks percakapan atau nama model bukan bukti status repo. Pertahankan perubahan pengguna. Jangan mengubah file generated secara manual atau menyalin algoritma produksi ke evaluator.

Untuk satu paket, tulis input–proses–output, kontrak yang dipakai, dependency yang benar-benar tersedia, invariant, resource budget, dan expected failure sebelum coding. Gunakan desain penuh yang telah disepakati. Urutan implementasi mengikuti dependency; tidak harus menuntaskan semua file dalam satu subtree lebih dahulu.

## Urutan pekerjaan yang disarankan setelah C01

| Paket | Hasil konkret | Syarat untuk melangkah |
| --- | --- | --- |
| E01 | Loader target/workload/manifest, raw observations, evaluator gate, telemetry | Invalid run/denominator kosong tidak menghasilkan PASS; pengukuran antrean/error ikut tercatat |
| S01 | Metadata/registry/job storage, artifact adapter, ledger, snapshot visibility, publication | Uji transaksi/retry/crash/fence dengan backend nyata; bukan hanya mock repository |
| D01/G01 persiapan | Audit corpus yang sudah diunduh, strata teks/scan/tabel/lampiran/perubahan, rencana label | Sampel punya provenance; split groups dan rantai rujukan dapat ditinjau manusia |
| M01 | Pembuktian parser/OCR dan model/native backend | Manifest dipin setelah hasil quality/parity/latency/memory tersedia |
| I01 | Parser, mapping, struktur, versi, chunk dengan parent | Output bisa ditelusuri ke PDF; halaman gagal dan tanggal ambigu eksplisit |
| K01/N01/X01 | Graph, native model service, indeks | Ikuti dependency spesifik development-plan; registry dan representasi tidak bercabang |
| Q01/A01/U01 | Retrieval, jawaban bersumber, update incremental | Evidence dan historical versions konsisten; failure paths teruji |
| O01/B01 | Deployment/operasi dan acceptance menyeluruh | Semua required applicable lulus pada profil yang disepakati |

E01 dan S01 dapat dikerjakan sebagai pekerjaan terpisah setelah kontrak terkait tersedia; setiap hasil integrasi tetap memiliki owner. Tidak ada instruksi menjalankan banyak agent implementasi tanpa kebutuhan; agent verifikasi independen mengikuti otorisasi dan aturan verification.md.

## Cara membaca rekomendasi per file

Header file menyatakan langkah berikutnya, dependensi/integrasi, dan bukti selesai yang spesifik. README folder merangkum integrasi antar anak serta risiko utama komponen. Berkas module initializer hanya mengekspos submodul/types; tidak perlu diisi algoritma. Berkas build/codegen perlu reproducibility, bukan logika bisnis. Folder kosong seperti migrations diisi sesuai paket pemiliknya setelah desain storage diterjemahkan.

Rekomendasi adalah panduan teknis dalam scope yang disetujui, bukan izin mengganti target atau memperluas fungsi folder. Jika temuan nyata mengubah pilihan implementasi, jelaskan bukti dan dampak, perbarui dokumentasi/decision yang relevan, lalu verifikasi. Hanya perubahan benchmark/ruang lingkup yang memerlukan persetujuan sesuai aturan repositori; perbaikan bug/optimasi dalam scope sudah diotorisasi.

## Prinsip implementasi yang tidak boleh hilang

Go mengatur request/workflow, fusion/filter/context/citation, penjadwalan, dan publication. Rust menghasilkan batch transformasi dokumen/graph/index. C++ mengelola sesi inference/tokenization/batching. Python dipakai offline untuk evaluasi/tooling. Jangan menambah LangGraph Python pada jalur request atau memindahkan extraction graph ke waktu query.

Chunk mengikuti struktur dan membawa konteks induk. Canonical identity bukan daftar sinonim tanpa scope; ambiguity, merge/split, dan revision harus terlacak. Tanggal hukum berbeda dari waktu observasi serta knowledge snapshot. Pembaruan harus mempertahankan historical versions dan bukti bersama, serta mencatat dependency negatif. Optimasi harus menjaga kualitas; jangan mengurangi kandidat/hop/kasus sulit tanpa evaluasi dampak dan pelaporan konfigurasi.

Angka benchmark berasal satu kali dari configs/benchmark-targets.yaml. Jangan menyebut kecepatan “sangat baik” sebagai hasil sebelum pengukuran. Jika data/hardware/model belum siap, laporkan NOT_MEASURED/BLOCKED untuk gate terkait dan lanjutkan pekerjaan independen. Seluruh temuan agent verifikasi yang berada dalam paket aktif harus diperbaiki dan diuji sebelum klaim selesai.

## Serah terima setiap paket

Catat perubahan perilaku, file pemilik, perintah verifikasi, hasil/exit code, raw log, keterbatasan, serta pekerjaan selanjutnya. Sebutkan apakah schema, helper, adapter, atau layanan benar-benar sudah aktif. Jangan menjadikan komentar TODO yang banyak sebagai pengganti implementasi ataupun laporan keberhasilan.

## Titik lanjut resolusi kontekstual

Gateway `Semantic.ResolveBatch`, client reusable, workflow `ProposeWithModel`, planner kandidat lintas scope, loader policy terpin, CLI submit durable, serta katalog/hidrasi bukti kandidat lintas dokumen tersedia. Lihat [semantic-resolution.md](semantic-resolution.md): dispatch RESOLVE opt-in sampai antrean proposal tersedia; nilai scope produksi, review/resume remote, keputusan canonical baru/merge/split, serta acceptance model lokal masih perlu diselesaikan. Smoke Ollama menguji protokol pada fixture sintetis; proposal model bukan review tersimpan.

## Handoff native model

Embedding/reranker native sudah memiliki runtime/model bundle dan client Go/Rust; gunakan [panduan native](native-inference.md), lalu integrasikan ke indexing/retrieval tanpa menduplikasi tokenization, pooling, atau normalization. Pemeriksaan native tidak menggantikan acceptance gold/workload lengkap; [laporan native](verification-report-native-models.md) menjelaskan prasyarat yang masih terbuka.

Pengguna memilih fokus implementasi kode sebelum anotasi gold lengkap. Ikuti [rencana X01](x01-implementation-plan.md) untuk pembagian komponen, fungsi, dan matriks validasi. Lanjutkan implementasi dengan fixture/integration test serta dokumen nyata; persiapkan provenance dan telemetry sejak awal. G01 lengkap lalu evaluasi/optimasi kualitas dijalankan setelah pipeline tersambung, dengan benchmark required tetap berlaku dan tanpa meluluskan kualitas dari tes sintetis.

Handoff lanjutan tersedia pada [rencana K01](k01-implementation-plan.md),
[rencana Q01](q01-implementation-plan.md), dan [rencana A01](a01-implementation-plan.md).
Implementasikan sesuai dependency: X01 menyediakan indeks, K01 menyediakan graph
berbukti; keduanya bertemu di Q01 sebelum A01 menyusun jawaban. Fungsi/library yang
independen dapat dikerjakan lebih awal, tetapi integrasi tidak dianggap selesai dari
mock. Audit bersama kontrak required evidence/path sets, temporal clarification,
support assessment, dan snapshot readiness sebelum menambah validator yang berbeda
di tiap komponen. Pertahankan header file dan perbarui README induk sekali pada akhir
paket koheren, kecuali perubahan kontrak/integrasi yang harus dijelaskan bersama kode.
