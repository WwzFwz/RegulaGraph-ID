# configs/prompts

Folder ini menyimpan prompt produksi yang dipin untuk tugas semantik internal. Byte file menjadi bagian `ModelManifest.prompt_hash`; perubahan sekecil apa pun menghasilkan identitas prompt baru dan harus dievaluasi sebagai konfigurasi model baru.

`extraction-v1.md` mengarahkan model menghasilkan proposal graph berbukti dengan offset UTF-8 relatif terhadap teks input. Semantic Gateway mengirim dokumen sebagai data JSON terpisah dari system prompt, sedangkan validator Go dan Rust tetap menjadi pemilik identitas, provenance, span absolut, dan keputusan menerima atau menolak output.

Jangan menaruh rahasia, data corpus, atau hasil eksperimen di folder ini. Sebelum prompt dipakai untuk run yang dinilai, bekukan model/provider/schema/ontology bersamanya, catat hash di manifest, lalu ukur kualitas, latency, token, dan biaya menurut `configs/benchmark-targets.yaml`. Target masih **REQUIRED_UNMEASURED**.

`resolution-v1.md` menilai identitas kandidat dengan konteks mention serta bukti dokumen kandidat dan meminta LINK/DEFER, alasan, serta ID konteks pendukung. LINK wajib mengutip kedua sisi; bukti kandidat yang tidak cukup mengharuskan DEFER. Prompt tidak memberikan otoritas commit registry. Hash prompt/schema/model harus dipin bersama dan dihitung ulang saat bytes prompt berubah; lihat [integrasi resolusi](../../doc/semantic-resolution.md).
