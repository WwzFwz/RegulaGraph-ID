# Verifikasi admission generator dan CLI jawaban

Laporan ini mencatat perubahan setelah revision `bd0bcd2`, 2026-10-09. Raw logs dan
fingerprint file: `artifacts/verification/20261009-generator-admission/`.
Toolchain Go 1.26.8 windows/amd64; worker Rust, PostgreSQL, Qdrant, Neo4j dan
llama.cpp CPU b11515-3d65c90d0 nyata. Model Qwen2.5 7B Q4_K_M memakai GGUF hash
`2bada8a7450677000f678be90653b85d364de7db25eb5ea54136ada5f3933730`
serta template hash
`4e9918361c284a93880606d182d64da6a9fe97cdc1f5c5a78c1c8840444246fc`.

`go test ./src/server/...` dan `go vet ./src/server/...` lulus, exit 0
(`go-test.log`, `go-vet.log`). Targeted unit/config/CLI tests memeriksa hash/magic
GGUF, detached model pins, alias/path/build/template/window drift, property
mutation/sleep, file drift, cancellation, exact config fields, invalid budgets,
explicit draft mode, secret redaction, missing generation, snapshot mismatch dan
larangan promosi status. Metadata dicek sebelum serta sesudah generation.

Native command
`go test ./src/server/internal/indexing -run '^TestNativeGraphAssemblyPipeline$' -count=1 -v`
diaktifkan dengan `REGULAGRAPH_TEST_NATIVE_GRAPH=1`, binary CLI yang dibangun dari
source, dan `REGULAGRAPH_TEST_LLAMA_GRAPH_ANSWER=1`. Exit 0 pada
`native-graph-answer.log`. Expected: source/graph publication nyata, pin query
sendiri, alias/traversal/hydration, exact model admission/counting/generation,
lalu output bersnapshot sama tanpa fabricated answer. Actual: graph evidence
tersambung ke Qwen; model memilih ABSTAIN, 2716 input/13 output tokens. Test wall
time 134.358 detik; prompt processing CPU dominan. Ini smoke integration, bukan
required latency acceptance pada profil referensi.

Reviewer independen membangun ulang CLI dan mengulangi pipeline: exit 0,
`independent-native.log` (lihat nama log final pada `independent-results.json`).
Model kembali abstain (2744/12 token); perbedaan ID corpus/evidence fixture antar
run mengubah prompt. Reviewer juga mengulangi
`go test ./src/server/internal/answering -run '^TestNativeLlamaCitedDraft$' -count=1 -v`
melalui admitted provider: 302 input/49 output tokens, satu cited PARTIAL draft.
Expected/actual dan fingerprint final tersimpan pada `independent-results.json`;
tidak ada blocker review terbuka.

Opt-in graph/model smoke memiliki deadline fixture lima menit dan read lease
empat menit agar inference CPU tercakup. Perubahan hanya pada fixture eksplisit;
budget produksi dan target `configs/benchmark-targets.yaml` tidak diubah. CLI
tetap membatasi request paling lama lima menit dan menjaga lease sampai selesai.

Admission mengikat trusted local server melalui hash startup dan warm file stat
(identity/size/mtime) serta `/props`. Tidak mengklaim remote RAM attestation atau
rehashing multi-GB setiap query. EXTRACT/review/alias/evidence fixture tetap
sintetis. HTTP answering, streaming, corpus/gold correctness, semantic quality
dan required performance acceptance belum selesai. PASS berlaku untuk behavior
yang diuji, bukan kelulusan release Hybrid GraphRAG.
