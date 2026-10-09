# Mengganti model tanpa mengganti arsitektur

Dokumen ini menjelaskan pilihan format EXTRACT dan langkah pergantian model lokal
atau provider, agar hambatan model saat ini tidak menjadi ketergantungan permanen
pipeline. Konfigurasi model adalah profil terpin, bukan aturan hukum. Dokumen ini
tidak memilih model baru atau mengklaim kualitas/performa model sudah lulus.

[Kondisi percobaan EXTRACT](extraction-trial-limitations.md) mencatat model
Qwen2.5 7B/Q4_K_M, perbedaan run CPU/Ollama, dan hipotesis penggantian model
yang belum diuji. Model lebih besar atau hosted belum otomatis menjadi pilihan
yang terverifikasi; gunakan sumber dan gate yang sama untuk membandingkan.

## Pisahkan tugas dan dampaknya

| Tugas | Yang diganti | Dampak pada hasil lama |
| --- | --- | --- |
| GENERATE | JSON `REGULAGRAPH_ANSWER_CONFIG`: model, GGUF/tokenizer, backend, template, context dan prompt pins | Indeks tidak perlu dihitung ulang hanya karena model penulis jawaban berubah; jawabannya menjadi run baru |
| EXTRACT | Profil gateway/worker `REGULAGRAPH_WORKER_EXTRACTION_*`, prompt, schema dan producer | Fakta/proposal harus diekstrak dan diperiksa kembali dengan identitas run baru; graph lama tetap historis |
| RESOLVE | Profil terpisah `REGULAGRAPH_WORKER_RESOLUTION_*`, konteks kandidat dan producer | Proposal/review/keputusan lama tidak berubah otomatis; jalankan jalur resolusi/commit yang berlaku |
| Embedding | Bundle native dan manifest embedding | Bangun/publikasikan generasi indeks kompatibel; jangan query vektor lama dengan model/dimensi baru |

Model yang lebih kuat bisa memperbaiki kepatuhan format dan reasoning, tetapi
tidak menjamin source span, fakta, atau relasi benar. Pipeline tetap memerlukan
validation, provenance, ontology, snapshot dan pemeriksaan kualitas. Jangan
hardcode jawaban atau menonaktifkan validator untuk membuat model lama terlihat
berhasil. Pergantian model tidak harus menunggu optimasi prompt Qwen selesai.

## Pilihan format EXTRACT

Ketiga format memproyeksikan keluaran model ke C01 yang sama. Pilih pasangan
prompt/schema, bukan mengganti `$id` pada file versi lama.

| Versi | Pekerjaan model | Pekerjaan gateway | Trade-off |
| --- | --- | --- | --- |
| v1 | Menulis surface/quote dan offset byte relatif | Memastikan quote, surface dan byte sumber tepat | Ringkas, tetapi model harus menghitung offset UTF-8 dengan benar |
| v2 | Menyalin quote persis dan prefix/suffix untuk disambiguasi | Mencari satu kecocokan persis, menghitung offset | Menghindari hitung byte; newline/case dan penyalinan kutipan masih bisa salah |
| v3 opsional | Memilih `first_token`/`last_token` dari sumber bernomor serta menalar mention/relasi | Mengambil surface dan byte sumber persis dari rentang terpilih | Mengurangi kebutuhan menyalin teks; label menambah prompt, pilihan rentang masih bisa salah secara semantik |

v3 memberi nomor 1-based pada unit leksikal: run Unicode letter/mark/number,
sedangkan rune non-whitespace lainnya menjadi unit tersendiri. Misalnya `PT.X`
memiliki tiga unit dan `Pasal(2)` empat unit. Ini bukan token tokenizer model.
Unit tidak dapat memilih sebagian run huruf/angka; batas mention tetap perlu
dievaluasi. Semua whitespace, newline dan sumber asli dipertahankan dalam
`indexed_text`; marker disisipkan, bukan mengganti kata. Teks/marker yang berasal
dari dokumen tetap data tak tepercaya.

Rentang memakai ID awal/akhir **inklusif**; C01 tetap byte UTF-8 end-exclusive.
ID hanya berlaku pada satu item. Missing/null/fractional/negative/zero/reversed/
out-of-range ditolak, tidak dijepit atau diperbaiki. Schema v3 menolak quote,
surface dan offset buatan model. Semua entity type, endpoint, qualifier, support,
provenance dan C01 tetap melewati validator bersama. Rentang valid tidak otomatis
membuktikan entailment, kelengkapan fakta atau legal validity.

Implementasi membatasi source 2 MiB, 65.536 unit, render 4 MiB dan aggregate
expanded quote/surface 4 MiB per item. Input yang melebihi batas ditolak seluruhnya;
tidak ada truncation tersembunyi. Batas resource ini bukan angka acceptance
benchmark. Versi algoritma dan Unicode tables dipin pada producer; source asli,
producer dan envelope yang tepat mengikat completion replay. Jangan memakai
ID rentang dari item atau profil model lain.

Lokasi: `src/server/internal/adapters/inference/semantic_indexed_source.go`,
`semantic_indexed_spans.go`, encoder `llm_request.go`, serta gateway `semantic.go`.
Default `.env.example` tetap v1; penambahan v3 tidak mengubah job/model aktif.

## Langkah penggantian profil

1. Pilih model/version nyata dan runtime; bekukan weights/tokenizer, precision,
   context limit dan backend. Untuk llama.cpp lokal, pin GGUF, server build serta
   chat template melalui `REGULAGRAPH_SEMANTIC_ADMISSION=llama.cpp` dan variabel
   `REGULAGRAPH_SEMANTIC_GGUF_PATH`, `REGULAGRAPH_SEMANTIC_LLAMA_BUILD`,
   `REGULAGRAPH_SEMANTIC_TEMPLATE_SHA256`. Jangan menganggap nama alias sebagai
   bukti weights tetap sama.
2. Pasangkan prompt/schema. Contoh pemilihan v3 pada environment yang sudah
   lengkap (bukan konfigurasi model lengkap):

   ```powershell
   $env:REGULAGRAPH_SEMANTIC_TASK = 'EXTRACT'
   $env:REGULAGRAPH_SEMANTIC_PROMPT_PATH = './configs/prompts/extraction-v3.md'
   $env:REGULAGRAPH_WORKER_EXTRACTION_OUTPUT_SCHEMA = './src/contracts/jsonschema/extraction-output-v3.json'
   $env:REGULAGRAPH_WORKER_EXTRACTION_PROMPT_SHA256 = (Get-FileHash $env:REGULAGRAPH_SEMANTIC_PROMPT_PATH).Hash.ToLowerInvariant()
   ```

3. Set model ID/version/weights/tokenizer/context/precision/backend pada prefix
   `REGULAGRAPH_WORKER_EXTRACTION_`, endpoint dan credential provider melalui
   environment lokal. Jalur generik chat-compatible tersedia untuk EXTRACT/RESOLVE,
   tetapi belum menjamin exact prompt counting; mode llama.cpp memeriksa hitungan
   penuh dan usage parity. Jalur GENERATE saat ini memakai konfigurasi llama.cpp
   tersendiri; jangan menganggap provider baru langsung kompatibel semua tugas.
4. Bekukan completion cap dan concurrency sesuai runtime. Prompt lengkap plus
   output reserve harus muat; context packing saja tidak cukup. Ekspor producer
   baru melalui `REGULAGRAPH_SEMANTIC_PRINT_PRODUCER=true`, simpan UTF-8 tanpa BOM,
   lalu nonaktifkan print mode saat menyalakan gateway. Samakan manifest/pin di
   worker dan job baru. Jangan mengubah payload atau hasil job historis.
5. Jalankan chunk gagal yang sama, lalu seluruh PDF melalui pipeline dengan profil
   baru. Periksa output lengkap, mentions/relations/supports dan lokasi sumber;
   hasil kosong atau JSON valid bukan acceptance graph. Completion replay memakai
   identitas baru, sehingga hasil model lama tidak diam-diam diadopsi.
6. Setelah RESOLVE/review, lakukan ASSEMBLE dan publication graph melalui jalur
   biasa. Simpan model lama dan artefak kegagalannya. Gold/performance acceptance
   tetap terpisah; target tidak berubah karena model diganti.

Rujukan: [gateway](../src/server/cmd/semantic-gateway/README.md),
[replay](semantic-completion-replay.md), [resolusi](semantic-resolution.md),
[GENERATION](local-answer.md), dan [handoff](operational-handoff.md).
