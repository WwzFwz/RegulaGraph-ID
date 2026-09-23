# Verifikasi Q01/A01: fusion dan konteks bukti

Dokumen ini mencatat uji dan review dua komponen dasar pada jalur query Go: reciprocal-rank fusion dan packing konteks bukti. **PASS hanya berlaku pada perilaku dua fungsi ini**; Q01/A01 end-to-end, kualitas jawaban, dan benchmark produksi belum selesai.

Basis perubahan adalah commit `10a0882e9f265cd5db2f0a12abd786cb48cc5b31`. Fingerprint SHA-256 file final: `fusion.go` `5596842192e3ecab09113f1011f3fa9f724dd30cacce92ced3c42e56d0f6eebd`, `fusion_test.go` `0e68265bb24a0fcc816dfea118cbfeba1f26560f71ce163b7ede928e720b5c17`, `context_builder.go` `3f80f18c77af1617c346ae0c88f53179e66ca24c2c44e0ad2f30dfc08717be2a`, dan `context_builder_test.go` `26efdaed52b94410c92914a39d4ae380b551989d61bbfb3070fea2df3b8f8c3a`. Raw log ada di `artifacts/verification/q01-a01-core-20260924/` dan diabaikan Git. Lingkungan: Go 1.26.8 windows/amd64; fixture sintetis, tanpa model, database search, atau corpus gold.

| Pemeriksaan | Status | Hasil aktual |
| --- | --- | --- |
| `go test ./... -count=1` dari `src/server` dengan `GOCACHE` dalam workspace | PASS, exit 0 | Semua paket Go yang memiliki tes lulus; `go-test.log`. |
| `go vet ./...` | PASS, exit 0 | Tidak ada temuan; `go-vet.log`. |
| `git -c core.safecrlf=false diff --check` | PASS, exit 0 | Tidak ada whitespace error. |
| Fusion RRF | PASS pada fixture | Rank/provenance lintas cabang, tie deterministik, duplikasi, batas input, skor nonfinite, dan keputusan filter absen/ditolak diuji. |
| Context packing | PASS pada fixture | Budget, versi sumber, snapshot campuran, parent/dependency terlewat, path ID tanpa proof, tokenizer gagal, dan status tanpa bukti diuji. |
| Review independen | PASS pada cakupan dua fungsi | Reviewer menemukan tiga celah correctness; perbaikan dan regression test diperiksa ulang, tanpa blocker tersisa di cakupan ini. |
| Recall/nDCG, faithfulness, sitasi, p95/p99, serta parity tokenizer | NOT_MEASURED | Retriever/backend/model/gold dan workload referensi belum tersedia untuk run sah. |

Fusion tidak membuktikan bahwa keputusan filter dari caller otentik atau seluruh cabang memakai snapshot yang sama; adapter dan workflow harus memverifikasi itu. Context builder sengaja menandai jalur graph dan parent yang belum dihidrasi sebagai terlewat, sehingga tidak mengklaim bukti lengkap dari ID saja. Penghitungan ulang token pada konteks bertambah berbiaya kuadratik terhadap jumlah fragmen pada implementasi sekarang; pemilihan tokenizer generator dan profiling wajib sebelum target latency dinilai. Angka required pada `configs/benchmark-targets.yaml` tidak berubah dan tetap REQUIRED_UNMEASURED.
