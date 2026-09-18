"""
Mendefinisikan kontrak record benchmark: pertanyaan, kategori, group ID, split, jawaban rujukan, dan gold evidence.

Peran dalam komponen:
Menjaga label dapat dipakai lintas eksperimen dan metrik.

Kontrak integrasi dan perhatian implementasi:
Gold evidence mencakup pasal, versi, serta seluruh rantai yang dibutuhkan; catat reviewer dan ketidakpastian label. Contoh tanpa jawaban harus eksplisit.

Benchmark dan gate penerimaan:
[EVAL] Gate: setiap run merekam dataset/split, corpus snapshot, model/prompt/config version, seed bila relevan, serta biaya. Laporkan ukuran sampel dan ketidakpastian, hindari tuning pada test set, dan pisahkan kualitas retrieval, graph, jawaban, serta waktu/biaya.
Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.

Status: scaffold dokumentasi; perilaku modul belum diimplementasikan.
"""
