# Verifikasi HTTP jawaban lokal

Dokumen ini mencatat milestone API jawaban setelah revision `4c0eaf7`, pada
2026-10-09. Raw logs dan fingerprint tersimpan di
`artifacts/verification/20261009-answer-api/`. Toolchain Go 1.26.8 windows/amd64;
fixture memakai Rust worker, PostgreSQL, Qdrant, Neo4j dan llama.cpp CPU nyata.

`go test ./src/server/...` dan `go vet ./src/server/...` lulus, exit 0
(`go-test.log`, `go-vet.log`). Tes terarah API/cmd memeriksa opt-in, hash config
wajib, timeout, auth/origin, shared concurrency evidence/question/readiness,
cancel, provider error redaction, no fallback, missing draft, corpus/snapshot/date
drift dan larangan promosi status. Native config library telah diverifikasi pada
[milestone admission](verification-report-generator-admission.md).

Command native:
`go test ./src/server/internal/indexing -run '^TestNativeGraphAssemblyPipeline$' -count=1 -v`.
Opt-in `REGULAGRAPH_TEST_NATIVE_GRAPH=1`, `REGULAGRAPH_TEST_GRAPH_API=1` dan
`REGULAGRAPH_TEST_LLAMA_API_ANSWER=1`, beserta backend/model pins pada hasil raw.
Expected: publish graph/index, dua HTTP evidence query concurrent dan warm reads,
readiness, actual answer request melalui generator terpin, output snapshot sama,
log tanpa kredensial/query serta semua lease request dilepas. Actual: exit 0,
2740 input/13 output tokens, ABSTAIN, tanpa citation fiktif dan tanpa lease bocor
(`native.log`). Test wall time 140.492 detik mencakup setup/fixture dan generation
CPU; ini bukan latency acceptance pada workload referensi.

Review independen menemukan konfigurasi handler dapat mengiklankan jawaban
ketika runtime hanya menyediakan evidence, karena tipe runtime tetap mempunyai
method Answer. Perbaikan menambahkan capability aktual `AnswerEnabled()` dan
menolak mismatch saat konstruksi. Regression
`TestAnswerServerRejectsEvidenceOnlyRuntime` serta affected API suites lulus
(`capability-regression.log`, `independent-unit-final.log`). Hasil final review,
reproduksi native dan fingerprint dicatat pada `independent-results.json`.
Reproduksi final reviewer lulus dengan 2745 input/13 output tokens, ABSTAIN,
tanpa lease bocor (`independent-native.log`, test wall 117.061 detik). Perbedaan
ID fixture per run memengaruhi prompt; dua waktu run ini bukan eksperimen optimasi.

Draft generation yang berhasil tidak membuktikan entailment hukum. Source,
alias dan graph input fixture sintetis; model/tokenizer nyata. Status claim tetap
UNREVIEWED, jawaban PARTIAL/ABSTAIN. Streaming, corpus/gold/model quality,
production workload p95/p99, reranking konfigurasi HTTP dan required release
acceptance belum dibuktikan oleh milestone ini. Target benchmark tidak diubah.
