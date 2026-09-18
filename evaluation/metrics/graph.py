"""
Mengukur kualitas extraction, resolution, predicate, arah relasi, dan provenance.

Peran dalam komponen:
Menilai kualitas graph sebelum pengaruhnya tercampur generation.

Kontrak integrasi dan perhatian implementasi:
Jangan mengabaikan predicate atau menganggap satu komponen terhubung sebagai bukti benar; ukur false merge dan false split tersendiri.

Benchmark dan gate penerimaan:
[EVAL] Gate: setiap run merekam dataset/split, corpus snapshot, model/prompt/config version, seed bila relevan, serta biaya. Laporkan ukuran sampel dan ketidakpastian, hindari tuning pada test set, dan pisahkan kualitas retrieval, graph, jawaban, serta waktu/biaya.

[EXTRACTION] Ukur precision/recall/F1 entitas dan relasi dengan predicate, arah, kondisi, serta bukti sumber; catat kegagalan schema, token, biaya/dokumen, dan waktu p50/p95. Target wajib mengikuti configs/benchmark-targets.yaml dan memerlukan gold set valid; schema valid tidak dianggap fakta benar.

[RESOLUTION] Ukur pairwise precision/recall/F1, false merge, false split, mention yang hilang, waktu per batch, dan biaya. Gate: semua mention tetap terlacak; pasal dari peraturan berbeda tidak digabung hanya karena nama sama. Blocking harus diukur juga terhadap pasangan benar yang terlewat.
Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.

Status: scaffold dokumentasi; perilaku modul belum diimplementasikan.
"""
