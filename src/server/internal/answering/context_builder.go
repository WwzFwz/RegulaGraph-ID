// Menggabungkan bukti terpilih dengan konteks induk, kondisi, pengecualian, dan jalur graph.
//
// Peran dalam komponen:
// Menyiapkan konteks sumber yang dapat dipakai generator dan citation mapper.
//
// Kontrak integrasi dan perhatian implementasi:
// Pemulihan parent harus memakai versi yang sama; de-duplikasi dan pemangkasan mengikuti budget token dengan pelaporan bukti yang terpotong.
//
// Benchmark dan gate penerimaan:
// [CONTEXT] Ukur cakupan gold evidence, kelengkapan jalur graph, duplikasi, token count, dan waktu membangun konteks. Gate: tiap item konteks dapat dipetakan ke sumber dan versi; pemotongan/ketidakcukupan bukti dilaporkan. Context budget tidak boleh diam-diam menghapus syarat penting.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: scaffold dokumentasi; perilaku modul belum diimplementasikan.
package answering
