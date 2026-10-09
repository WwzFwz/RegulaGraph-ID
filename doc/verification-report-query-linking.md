# Verifikasi query alias linking

Laporan ini melacak pemeriksaan seed discovery dan pengikatannya ke graph setelah
revision `2a8e5db`, 2026-10-09. Cakupan berada pada
[query-entity-linking.md](query-entity-linking.md). Raw logs/fingerprints disimpan
di `artifacts/verification/20261009-query-linking/`; Go 1.26.8 windows/amd64.

Implementer menjalankan `go test ./src/server/...` (exit 0, `go-test.log`) dan
`go vet ./src/server/...` (exit 0, `go-vet.log`). Targeted query/workflow/indexing
regressions tercatat di `unit-final.log` (exit 0). Native run
`go test ./src/server/internal/indexing -run '^TestNativeGraphAssemblyPipeline$' -count=1 -v`
lulus (`native.log`), dengan worker Rust, PostgreSQL, Neo4j dan Qdrant nyata.
Alias/model/token counts tetap sintetis; native run tidak membuktikan kualitas corpus.

Expected: pertanyaan menghasilkan lookup terpin dan seed tanpa ID yang disuplai
fixture, empat profil tetap kompatibel, ambiguity tidak menjadi identity merge,
dan query tanpa alias abstain pada graph-only. Actual: kedua profil graph memakai
lookup PostgreSQL, fusion, hidrasi sumber, reranking dan draft bersitasi. Missing
alias tidak memanggil provider. Unit mencakup scoped alternatives, Unicode offset,
parenthetical legal suffix, revision/key/corpus mismatch, alias closure, duplicate,
budget, expired/cancelled call serta policy/callback ownership.

Review menemukan callback report perlu admission sebelum clone, frontier tiap
path harus ikut uncertainty, dan final-position `Pasal 1(2)?` harus mempertahankan
intermediate punctuation variants. Ketiganya diperbaiki dengan regression checks;
scope ber-whitespace dan final deadline juga diperketat. Reviewer `verify_index_jobs`
memberi PASS_SCOPED setelah unit dan native rerun (`independent-unit.log`,
`independent-native.log`, keduanya exit 0). Fingerprint final pada
`independent-results.json` cocok dengan file implementer sebelum commit.

Belum dinilai: real entity-linking recall, false exclusions, legal applicability,
model quality, p95/p99/throughput/RSS acceptance. API/CLI graph configuration dan
semantic/model disambiguation belum bagian paket ini. Target benchmark tidak berubah.
