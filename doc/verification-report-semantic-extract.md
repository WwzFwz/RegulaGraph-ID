# Laporan verifikasi Semantic EXTRACT Gateway

Dokumen ini merekam pemeriksaan boundary kritis Semantic EXTRACT setelah implementasi adapter provider, proyeksi graph berbukti, cache idempotensi, executable gRPC, dan qualifier provisional. Laporan ini membuktikan perilaku deterministik yang diuji; laporan ini tidak membuktikan kualitas model, latency provider, biaya, atau target benchmark produksi.

## Cakupan dan hasil

Pemeriksaan mencakup pemisahan system prompt dari dokumen tidak tepercaya, pin model/prompt/schema/ontology, exact UTF-8 source span, provenance, support closure, response envelope provider, token accounting presence, deadline, cancellation, overload admission, redirect, klasifikasi status, operation-key collision, retry parsial, dan cache replay. Regression test resmi berada di `src/server/internal/adapters/inference/*_test.go` dan `src/server/cmd/semantic-gateway/main_test.go`.

Verifier independen mula-mula menemukan kegagalan pada deadline body, redirect lintas endpoint, JSON kosong/null, HTTP 503 non-JSON, retry parsial, model/finish reason/usage envelope, nested required field, tanggal ilegal, admission overload, dan shared response alias. Implementasi kemudian diperbaiki dan kasus tersebut ditambahkan sebagai regression test permanen. Raw log audit berada di `artifacts/verification/semantic-extract-20260921/`.

| Gate | Hasil | Bukti dan batas |
| --- | --- | --- |
| Go unit/integration package | PASS | `go test ./...`; provider eksternal diganti HTTP fixture/double deterministik |
| Go static analysis | PASS | `go vet ./...` |
| Contract compatibility | PASS | descriptor 159 message, 31 enum, 4 service; perubahan qualifier append-only |
| Rust consumer | PASS | 109 unit test + 1 integration test; 1 wire fixture test ignored sesuai harness; Clippy `-D warnings` PASS |
| Adversarial verifier overlay | PASS setelah perbaikan | 20 kasus PASS, termasuk deadline, redirect, malformed/nested output, envelope, partial retry, admission, cache alias, eviction, dan 50 iterasi replay bersamaan |
| Race detector | BLOCKED | Toolchain Cygwin lokal tidak mendukung target race Windows; perlu Go dengan compiler MinGW atau CI Linux |
| Model quality/latency/cost | NOT_MEASURED | Provider/model produksi, frozen evaluation split, dan hardware referensi belum ditentukan |

## Invariant yang dipertahankan

Gateway menolak output tanpa array wajib, field span wajib, exact quote, support untuk setiap assertion, dan graph contract per item. Kesalahan satu item menjadi `OperationError` item tersebut sehingga tidak menggagalkan item lain. Mention qualifier memakai `mention_id` sampai RESOLVE; gateway tidak menerbitkan canonical assignment.

Provider HTTP tidak mengikuti redirect, memeriksa status sebelum membaca body sukses, dan menerima hasil hanya bila model tepat sama, `finish_reason` selesai, usage hadir, serta structured output valid. Cache menyimpan clone immutable, mempertahankan fingerprint operation-key, menggunakan kembali item terminal pada retry parsial, dan dibatasi entry serta byte. Replay durable lintas restart masih menjadi pekerjaan coordinator.

Verifier merekam fingerprint source yang ditinjau `8ed446948844bc970ff99e14f079a190b7b242e4e6d0fcc559cb6882681bbcdb`. Evidence ringkas tersedia pada `artifacts/verification/semantic-extract-20260921/checks.json`; lifecycle executable gRPC nyata tetap perlu smoke test deployment setelah provider dipilih.
