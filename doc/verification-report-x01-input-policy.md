# Verifikasi policy input embedding pada IndexGeneration X01

Dokumen ini merekam pemeriksaan kontrak aditif dan admission reader/writer pada
2026-09-26. Raw log implementer berada di
`artifacts/verification/20260926-x01-policy-contract/`; raw review independen
di `artifacts/verification/x01-input-policy-20260926/`. Lingkungan Windows
amd64; protoc 34.1 dan plugin Go terpin dari `scripts/generate_contracts.py`.
Fixture menggunakan data sintetis, bukan corpus regulasi maupun server Qdrant
hidup.

| Pemeriksaan | Expected dan aktual | Status |
| --- | --- | --- |
| `python scripts/generate_contracts.py`; `python scripts/check_contracts.py` | Binding Go/Python/C++ dan descriptor diperbarui; descriptor review menunjukkan nol perubahan incompatible. Schema lock sengaja diperbarui setelah review addition terdahulu dan tag 8; checker ulang exit 0. | PASS |
| `python tests/integration/wire_roundtrip.py` setelah build C++ | 55 fixture lintas Go/Rust/C++/Python, termasuk `IndexGeneration.embedding_input_policy`, lulus. | PASS |
| `go test -count=1 ./...` dari `src/server` | Seluruh paket lulus; validator menolak policy kosong/lama/tidak dikenal, Qdrant menolak metadata policy berbeda. Log `go-test.log`, exit 0. | PASS |
| `cargo test -p regulagraph-ingestion --offline` | 156 unit dan 1 integrasi PDF lulus, 2 opt-in ignored; reuse key memeriksa policy generation. Log `cargo-test.log`, exit 0. | PASS |
| Review independen C01 | 195 leaf tests Go, 11 probe adversarial, 4 tes Rust reuse, vet, dan descriptor compatibility lulus. | PASS pada boundary yang diperiksa |
| Old reader route, publication, Qdrant nyata, benchmark kualitas/latency | Belum diuji/dijalankan dalam pipeline produksi. | NOT_MEASURED |

`IndexGeneration.embedding_input_policy` memakai tag 8 aditif tanpa aturan
required pada wire. Reader/writer X01 baru memerlukan `structure-labels-v1` secara
stage-specific. Collection Qdrant mengikat nilai itu di metadata; Rust hanya
membuat key reuse dari generation dengan policy yang cocok. Reader binary lama
dapat mengabaikan tag 8 dan masih menerima generation tersebut. Karena itu
publication generation baru wajib menunggu upgrade semua route pembaca atau
isolasi route lama yang dibuktikan; kompatibilitas decode saja tidak cukup.

Refresh `schema-lock.json` mencatat seluruh addition yang tertunda sejak lock
sebelumnya, bukan hanya tag 8: paired filter X01 (`IndexGeneration` tag 7,
`FilterMetadata` tag 7, `IndexProvisionFilter`, `IndexFilterFormat`) dan konteks
resolusi K01 (`ResolutionProposal` tag 11/12,
`ResolutionCandidateContext`, `AmbiguousMention` tag 6/7). Semuanya additive;
detail sebelum refresh ada di `schema-compatibility.json` raw review. Baseline
tidak dipakai untuk meloloskan perubahan incompatible.

Pemeriksaan ini belum membuktikan bahwa operation writer mem-pin generation
secara durable, bahwa semua route pembaca sudah di-upgrade, atau bahwa
`structure-labels-v1` meningkatkan Recall@k. Target required tetap
**REQUIRED_UNMEASURED**.
