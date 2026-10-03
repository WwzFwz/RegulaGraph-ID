# Verifikasi allocator dictionary BM25 X01

Dokumen ini mencatat batas bukti allocator PostgreSQL sebelum stage INDEX memakainya.
Dasar kode `8c7859c`, commit implementasi `b55271b`; fingerprint SHA-256 file implementasi yang diuji adalah
`index_dictionary.go` `df162bc37389e86b45756fc5d174b3cd0846e16cddc8a174b027aac71bfb6033`,
migration 0013 `0d8f1b434dbf8714af5873ff2828b0d58cdf5b4171cb425187b49d4074c0c41e`,
dan tes `index_dictionary_test.go` `4edcb427467447eade7d657f6973dc02715b240d1948fb3355b59756e3b97b87`.
Raw log tersimpan di `artifacts/verification/20261003-x01-lexical-allocator/` dan
diabaikan Git. Lingkungan: Windows amd64, Go 1.26.8, PostgreSQL 18.0 pada cluster
sementara port 55439; fixture memakai corpus/analyzer sintetis, tanpa model.

| Pemeriksaan | Expected → actual |
| --- | --- |
| `go test ./internal/adapters/postgres -count=1` dengan `REGULAGRAPH_TEST_POSTGRES_DSN` menunjuk cluster sementara | Migration 0013 diterapkan, ID stabil per corpus/analyzer, revisi lama tetap terbaca, replay identik tidak menulis ulang, perubahan payload/stale CAS ditolak, batas hasil dan trigger append-only aktif → PASS, exit 0 (`go-test-postgres-final.txt`). |
| `go test ./internal/adapters/postgres -run '^TestLexicalDictionaryAgainstPostgres$' -count=5 -v` pada cluster yang sama | Dua operasi dari expected revision sama tidak sama-sama menang; retry yang kalah mendapat ID baru tanpa celah → PASS 5 kali, exit 0 (`go-test-concurrency.txt`). Sinkronisasi goroutine tidak menjamin SQLSTATE 40001 benar-benar terpicu. |
| `go test ./...` dari `src/server` tanpa DSN | Seluruh suite Go yang tersedia lulus → PASS, exit 0 (`go-test-all-final.txt`); tes PostgreSQL dilewati pada run ini, sedangkan run khusus di atas menjalankannya. |
| Review agent independen read-only | Tidak menemukan blocker korupsi data pada migration/allocator/test. Reviewer menjalankan unit validation PASS; tes PostgreSQL-nya SKIP karena tidak memiliki DSN. Reviewer menandai contention/40001 dan benchmark sebagai gap, bukan PASS. |

Allocator mengurutkan term sebelum assignment dan memakai satu `INSERT ... SELECT`
untuk term baru, dengan batas 10.000 term per operasi. Operation key yang sama
memerlukan hash input sama dan mengembalikan revisi historis. Revision baru
hanya dibuat bila ada term baru. `LoadLexicalDictionary` menolak revisi masa
depan dan hasil yang melebihi batas pemanggil. Caller INDEX harus memastikan
term berasal dari analyzer yang terpin, membuat artefak dictionary bertipe,
serta mengulang keseluruhan operasi idempotent dengan anggaran terbatas bila
PostgreSQL mengembalikan SQLSTATE 40001/40P01.

Belum diuji: benturan transaksi terpaksa yang memicu SQLSTATE 40001, korupsi
persisten di luar adapter, lock/pool wait dan p95/p99 pada 10.000 term, lineage
artefak lintas Rust/Go, serta kesiapan publication. Required benchmark kualitas,
latency, throughput, dan memory tetap **NOT_MEASURED**; submilestone X01 belum selesai.
