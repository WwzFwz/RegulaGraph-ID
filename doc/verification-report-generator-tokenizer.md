# Verifikasi tokenizer dan generator lokal

Laporan ini mencatat boundary shared prompt/counting setelah revision `f3be306`,
2026-10-09. Raw logs/fingerprint berada di
`artifacts/verification/20261009-generator-tokenizer/`. Go 1.26.8 windows/amd64;
llama.cpp CPU `b11515-3d65c90d0`, Qwen2.5 7B Q4_K_M dari berkas lokal Ollama.
Container GGUF SHA-256
`2bada8a7450677000f678be90653b85d364de7db25eb5ea54136ada5f3933730`
dibaca dan diverifikasi saat native test; tokenizer tertanam di GGUF.

`go test ./src/server/...` dan `go vet ./src/server/...` lulus, exit 0
(`go-test.log`, `go-vet.log`). Unit boundary menguji kesamaan bytes body counting
dan generation, auth/route, escaped UTF-8, batas encoded bytes, jumlah token
terpisah dari bytes, invalid accounting, redirect, cancellation dan error redaction.

Perintah native
`go test ./src/server/internal/answering -run '^TestNativeLlamaCitedDraft$' -count=1 -v`
dengan environment dalam [kontrak tokenisasi](generator-tokenization.md) lulus,
exit 0 (`native.log`). Expected: konteks ditokenisasi model, complete prompt count
cocok persis dengan usage, dan draft memperoleh citation dari sumber tepercaya.
Actual: 64 context tokens, 302 full prompt tokens, 49 output tokens; satu claim
dan satu citation, teks `Isi pasal pertama.`, status UNREVIEWED/PARTIAL.

Reviewer independen menjalankan ulang package tests dan native test setelah
perbaikan, exit 0 (`independent-final.log`, `independent-native-final.log`). Ia
menemukan decoding `null` dalam array integer diterima sebagai nol; parser kini
membaca ID satu per satu, menolak null/noninteger, membatasi jumlah sebelum
alokasi slice dan memeriksa cancellation selama/setelah decoding. Regression
`[null]`/`[1,null]` lulus. Fingerprint final dan temuan tertutup berada pada
`independent-results.json`.

Binary CPU diunduh dari release resmi b11515, archive SHA-256
`8e9fee23be0615f0a358ff7b11b740b5d4a8275e87084efe43cf28a6f694da91`
sesuai digest release sebelum diekstrak. Server test memakai loopback port 55101,
context 4096, satu slot dan empat thread, tanpa context shift. Properti/log server
disimpan bersama raw results. Kondisi ini bukan profil benchmark referensi.

Bukti hanya mencakup satu fixture evidence sintetis dengan model/tokenizer nyata.
Belum membuktikan recall, faithfulness, hukum, kualitas corpus, throughput/latency
required atau release. Counter mengandalkan admission model/template dari caller;
admission produksi dan wiring command/API jawaban belum tersedia pada paket ini.
Tidak ada perubahan target benchmark ataupun klaim deployment selesai.
