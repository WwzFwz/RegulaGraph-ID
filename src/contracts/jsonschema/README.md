# src/contracts/jsonschema

Folder ini menampung schema structured-output yang dipin dan di-hash untuk boundary provider model. Schema di sini bukan wire contract paralel: gateway Go memvalidasi keluaran provider lalu memproyeksikannya ke protobuf C01 pada `src/contracts/proto`.

`extraction-output-v2.json` memakai locator `quote`, `prefix`, `suffix` sebagai
string wajib. Gabungan ketiganya harus tepat muncul sekali dalam teks item;
prefix/suffix adalah konteks langsung, maksimum 256 karakter/1024 byte UTF-8 per
field. Offset dihitung gateway hanya untuk quote, bukan konteks disambiguasinya.
Kutipan hilang/ambigu, whitespace berbeda, offset v1, field null/hilang dan key
duplikat ditolak. ID schema v2 memilih projector v2; schema hash dan prompt/model
pin baru tetap wajib. C01 tetap memakai span byte absolut yang sama, sehingga
worker Rust/storage tidak menerima kontrak paralel atau offset perkiraan.

`extraction-output-v1.json` mendefinisikan proposal lokal per chunk. Offset provider adalah byte UTF-8 relatif terhadap teks item; gateway wajib memetakan dan memverifikasinya terhadap span absolut sebelum membuat `Mention` atau `SupportRecord`. Provider tidak menentukan corpus ID, canonical ID, provenance, manifest, review state, atau visibility.

Perubahan schema menghasilkan hash dan versi baru, memperbarui prompt/model manifest, serta memerlukan evaluasi ulang extraction pada split beku. Jangan mengganti file versi lama setelah dipakai artefak. Ukur schema-failure rate, token output, latency, dan precision/recall sesuai `configs/benchmark-targets.yaml`; status tetap **REQUIRED_UNMEASURED**.


`resolution-output-v1.json` adalah schema provider untuk LINK/DEFER kontekstual. Gateway memeriksa field wajib/non-null, duplikat key, kandidat, dan konteks sebelum memproyeksikan ke ResolutionProposal. `rationale` serta `supporting_context_ids` disimpan untuk audit, bukan review approval.
