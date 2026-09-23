# Verifikasi scheduler N01 dan boundary jawaban A01

Dokumen ini mencatat bukti untuk dua irisan implementasi: scheduler batch native dan validasi struktural jawaban. Basis sebelum perubahan `78caa99190b229b4d9b1292d2eb8263c1c32763e`; fingerprint SHA-256 final: `batching.cpp` `74794C3C0873422EDD0CA2433E2EAC5575DC9DFBA7D9EC08C5F273E41245010A`, `batching.hpp` `95605E8ED0D46A9E9EEEC5974A9DF26F809D08FD12DE6FB8E9A032BA4CAC263C`, `validation.go` `9DC4C4A917E771483A2C9282A6673BCB4EF351AA9DFD7CDC72B0ED13FE8A3BAC`, dan `boundaries.go` `C6074F1DEA1A221F5ADB5C2B53FDE3D91E260886683352C38BEBD9B4B9E0F334`. Raw log berada di `artifacts/verification/n01-batching-20260924/` dan `artifacts/verification/a01-validation-20260924/` (diabaikan Git). Go 1.26.8, Cargo 1.87.0, CMake 3.30.1, Windows amd64; uji memakai fixture sintetis, bukan model/corpus gold.

| Pemeriksaan | Status | Expected dan aktual |
| --- | --- | --- |
| CMake build dan CTest `regulagraph_batching` | PASS, exit 0 | Batas batch/token, fairness query-bulk, overload, deadline, cancellation, dan wakeup sesuai fixture; log `cmake-build-final.log`, `ctest-final.log`. |
| `go test ./... -count=1` dan `go vet ./...` dari `src/server` | PASS, exit 0 | Semua paket Go teruji lulus dan vet tanpa temuan; log `go-test.log`, `go-vet.log`. |
| `git -c core.safecrlf=false diff --check` | PASS, exit 0 | Tidak ada whitespace error. |
| Review independen scheduler | PASS dalam cakupan scheduler | Reviewer menemukan masalah wakeup, slot expiry, dan kompleksitas scan; perbaikan serta kebijakan fail-stop ditinjau ulang, tanpa blocker scheduler tersisa. |
| Review independen retrieval–answer dan sitasi | PASS dalam cakupan struktural | Kasus tandingan context omission, path palsu, span klaim kosong, konflik palsu, schema mismatch, dan span sitasi multisumber ditolak setelah perbaikan; tanpa blocker struktural tersisa. |
| N01/A01 end-to-end, model parity, semantic entailment, p95/p99, dan target required | NOT_MEASURED | Model/session/transport, tokenizer tepercaya, gold, workload, serta hardware referensi belum siap. |

Scheduler adalah pustaka event-loop, belum terhubung ke worker model. Kegagalan alokasi/indeks membuat proses worker berhenti; supervisor dan retry klien harus diuji saat integrasi. Validasi jawaban memercayai EvidenceBundle dan URL lookup yang disediakan caller; `token_count` belum dihitung ulang dan teks di luar span klaim belum dibuktikan semantik. Sitasi span pada evidence multisumber sengaja ditolak sampai ada ikatan tepercaya dari text artifact ke source blob. Ambang `configs/benchmark-targets.yaml` tidak berubah dan tetap **REQUIRED_UNMEASURED**.
