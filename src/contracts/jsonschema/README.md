# src/contracts/jsonschema

Folder ini menampung schema structured-output yang dipin dan di-hash untuk boundary provider model. Schema di sini bukan wire contract paralel: gateway Go memvalidasi keluaran provider lalu memproyeksikannya ke protobuf C01 pada `src/contracts/proto`.

`extraction-output-v1.json` mendefinisikan proposal lokal per chunk. Offset provider adalah byte UTF-8 relatif terhadap teks item; gateway wajib memetakan dan memverifikasinya terhadap span absolut sebelum membuat `Mention` atau `SupportRecord`. Provider tidak menentukan corpus ID, canonical ID, provenance, manifest, review state, atau visibility.

Perubahan schema menghasilkan hash dan versi baru, memperbarui prompt/model manifest, serta memerlukan evaluasi ulang extraction pada split beku. Jangan mengganti file versi lama setelah dipakai artefak. Ukur schema-failure rate, token output, latency, dan precision/recall sesuai `configs/benchmark-targets.yaml`; status tetap **REQUIRED_UNMEASURED**.


`resolution-output-v1.json` adalah schema provider untuk LINK/DEFER kontekstual. Gateway memeriksa field wajib/non-null, duplikat key, kandidat, dan konteks sebelum memproyeksikan ke ResolutionProposal. `rationale` serta `supporting_context_ids` disimpan untuk audit, bukan review approval.
