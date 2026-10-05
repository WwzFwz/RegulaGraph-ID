# Cara kerja setiap tahap: contoh data, alasan bisnis, dan trade-off

Dokumen ini melengkapi [arsitektur](01-architecture.md) dan [flow](03-flows.md)
dengan perjalanan data konkret pada **rancangan sistem lengkap**. Contoh di bawah
adalah ilustrasi, bukan data hukum, hasil eksekusi, atau kontrak API baru. Nama ID
dipendekkan supaya mudah dibaca; bentuk wire sebenarnya mengikuti
[system-contracts](../system-contracts.md) dan [Protobuf](../../src/contracts/proto/README.md).
Status implementasi tetap berada di [dokumen 7](07-implementation-status.md).
Pembahasan evaluasi, keandalan, observability, scalability dan batas desain lintas
tahap ada di [dokumen 9](09-engineering-depth.md).

Cara membaca setiap tahap: pahami fungsinya, lihat input, ikuti proses, lalu bandingkan
output. Sesudah itu, baca alasan bisnis dan harga keputusan tersebut. Semua JSON
adalah **contoh bentuk data yang disederhanakan**, bukan payload siap dikirim ke API.
Field seperti `meaning`, `concept`, `state`, serta isi string placeholder hanya
untuk penjelasan; enum, predicate ontology dan schema sebenarnya tidak diubah.
Daftar ranking/vector merupakan ilustrasi, bukan hasil run.

Alasan bisnis memakai hipotesis pengguna tim riset/compliance yang perlu menemukan,
memahami dan memeriksa aturan. Ini belum merupakan validasi pelanggan atau ROI
terukur. Manfaat yang dituju adalah waktu riset lebih singkat, bukti lebih lengkap,
sumber mudah diperiksa, pembaruan lebih konsisten, dan biaya operasi terkendali.

## 0. Gambaran sederhana sebelum masuk ke detail

```text
INGESTION / INDEXING
Portal -> PDF + metadata -> document processing -> struktur pasal/ayat
        -> binding -> structural chunking -> entity + relation extraction
        -> entity resolution + versi hukum
                  |                         |
                  v                         v
          BM25 + vector index        graph + evidence support
                  |                         |
                  +---- stage / verify -----+
                              |
                       publish snapshot

QUERY / ANSWERING
Question -> normalize -> classify / link / plan + scope snapshot/tanggal
                              |
                   +----------+----------+
                   v          v          v
                  BM25       Dense      Graph
                   +----------+----------+
                              |
                    Filter / Fusion
                              |
                Hydrate candidate text / Rerank
                              |
                 Context Builder / Evidence Check
                              |
                  cukup? ---- tidak -> targeted retrieval atau stop
                    | ya                 (budget tetap)
                    v
                   LLM -> Claim / Citation Validation
                              |
                    Final / Partial / Abstain
```

Ingestion menyiapkan bahan pencarian; query memilih dan menyusunnya menjadi jawaban.
Graph yang menunggu dense seeds tidak berjalan independen dari dense. Model bahasa
juga dapat dipakai ketika extraction/resolution, bukan baru pertama bekerja pada
generation. Diagram ini memadatkan dependency; tahap di bawah menjelaskan rinciannya.

## 1. Satu contoh yang dipakai sepanjang alur

Bayangkan corpus berisi dua **peraturan fiktif** berikut:

| Dokumen | Isi ilustratif | Informasi waktu bersumber |
| --- | --- | --- |
| Peraturan Badan Contoh Nomor 1 Tahun 2024, disingkat A | Pasal 5 ayat (1): “Penyelenggara wajib menyampaikan laporan paling lambat 30 hari setelah kegiatan berakhir.” Ayat (2): “Kewajiban pada ayat (1) tidak berlaku bagi kegiatan internal.” | Ketentuan awal berlaku mulai 1 Januari 2024 |
| Peraturan Badan Contoh Nomor 2 Tahun 2025, disingkat B | Pasal 2 mengganti batas pada A Pasal 5 ayat (1) menjadi 14 hari; ayat (2) tidak diubah | Perubahan berlaku mulai 1 Juli 2025 |

Pertanyaan contoh: **“Per 1 Agustus 2025, berapa batas waktu laporan kegiatan,
dan apakah kegiatan internal juga wajib?”** Jawaban yang diharapkan dari contoh
ini adalah 14 hari setelah kegiatan berakhir, dengan pengecualian kegiatan internal.
Jawaban harus menunjukkan sumber aturan, perubahan, dan pengecualiannya.

Identitas yang mengikuti contoh:

| Label ringkas | Makna |
| --- | --- |
| `src-A`, `src-B` | Sumber dokumen, disertai observation dan blob hash masing-masing |
| `reg-A` | Identitas regulasi A yang ditetapkan registry |
| `p5-1`, `p5-2` | Identitas ketentuan A Pasal 5 ayat (1) dan ayat (2) |
| `v5-1-old`, `v5-1-new`, `v5-2` | Versi lama ayat (1), versi hasil perubahan, dan versi ayat (2) |
| `snap-12` | Snapshot pengetahuan corpus yang sudah dipublikasikan |
| `gen-3` | Generation representasi indeks: model, tokenizer, analyzer, dictionary/statistik |

`snap-12` bukan tanggal hukum. `gen-3` bukan versi peraturan. Satu file PDF juga
dapat memuat banyak pasal, sedangkan satu versi rekonstruksi dapat memakai beberapa
sumber. Karena itu hash file, ID regulasi, ID versi, dan ID chunk tidak disatukan.

## 2. Persiapan corpus: dari portal sampai snapshot siap dicari

### I1. Discovery — Go acquisition

Menemukan dokumen tanpa pengguna harus membuka portal satu per satu.

**Input:** daftar portal/listing, konfigurasi connector, batas crawl dan checkpoint.

**Proses:** membaca halaman daftar/detail, mengikuti pagination yang diizinkan,
dan mengenali tautan dokumen.

**Output:** kandidat URL detail/PDF dan metadata portal.

**Contoh input:**

```json
{
  "portal": "portal-contoh",
  "listing": "halaman daftar peraturan",
  "checkpoint": "halaman terakhir yang sudah diperiksa"
}
```

**Contoh output:**

```json
{
  "candidates": [
    {
      "title": "Peraturan A",
      "detail_url": "https://contoh.invalid/detail/A",
      "pdf_url": "https://contoh.invalid/A.pdf"
    },
    {
      "title": "Peraturan B",
      "pdf_url": "https://contoh.invalid/B.pdf"
    }
  ]
}
```

Contoh: satu halaman listing menghasilkan kandidat A dan B, masing-masing dengan
judul, penerbit yang dinyatakan portal, dan URL unduh. Kandidat URL belum berarti
PDF berhasil diperoleh atau identitas hukumnya telah diverifikasi.

**Alasan bisnis:** Otomatisasi discovery ditujukan untuk mengurangi pekerjaan pencarian manual dan mempercepat masuknya aturan baru. Nilainya diukur dari coverage sumber dan jeda dari dokumen tersedia hingga ditemukan.

**Alternatif dan trade-off:** Daftar URL manual lebih sederhana untuk corpus kecil, tetapi mudah tertinggal. Connector otomatis memerlukan pemeliharaan ketika struktur portal berubah; banyak URL bukan bukti coverage lengkap.

### I2. Download dan audit — Go acquisition + artifact storage

Mengubah tautan menjadi file yang benar-benar dimiliki sistem dan dapat ditelusuri asalnya.

**Input:** kandidat URL dan policy download.

**Proses:** mengunduh dengan batas
ukuran/rate/retry, memeriksa respons/file, menghitung hash, menyimpan receipt.

**Output:** `SourceObservation`, `SourceBlob`, dan referensi artefak immutable.

**Contoh input:**

```json
{
  "candidate": "Peraturan A",
  "url": "https://contoh.invalid/A.pdf"
}
```

**Contoh output:**

```json
{
  "source": "src-A",
  "file": "A.pdf",
  "blob_hash": "sha256:<hash byte sebenarnya>",
  "receipt": {
    "portal": "portal-contoh",
    "download_status": "berhasil",
    "observed_at": "<timestamp UTC>"
  }
}
```

Contoh: URL A menghasilkan `src-A`, blob dengan hash `sha256:...`, ukuran,
waktu observasi dan metadata redirect. Mirror dengan byte identik dapat berbagi
blob, tetapi observation asalnya tetap disimpan. Respons HTML error dari URL PDF
menjadi kegagalan akuisisi, bukan dokumen PDF sukses.

**Alasan bisnis:** Pengguna dapat membuka sumber untuk memeriksa jawaban; tim dapat mereproduksi masalah meskipun URL portal berubah. Deduplikasi byte juga menghindari biaya penyimpanan file mirror berulang.

**Alternatif dan trade-off:** Menyimpan URL saja lebih murah, tetapi kehilangan file ketika tautan mati. Menyimpan file memerlukan ruang, pemeriksaan integritas dan kebijakan retention; checksum bukan bukti bahwa isi dokumen benar.

### I3. Perencanaan job — Go coordinator

Memastikan pekerjaan besar dapat dilanjutkan tanpa mengulang seluruh proses setelah gangguan.

**Input:** source refs, fingerprint source/config/model, checkpoint dan dependency
sebelumnya.

**Proses:** menentukan reuse atau reprocess, membuat intent job dan
menetapkan attempt/lease/fence saat pekerjaan diambil.

**Output:** job durable
dan rencana tahap dengan referensi input terpin.

**Contoh input:**

```json
{
  "source": "src-A",
  "source_changed": true,
  "last_checkpoint": null,
  "config": "config-1"
}
```

**Contoh output:**

```json
{
  "job": "job-A",
  "action": "process",
  "next_stage": "PARSE",
  "input_source": "src-A",
  "producer_config": "config-1",
  "operation_key": "<idempotency key>"
}
```

Contoh: A baru memerlukan PARSE sampai INDEX. A identik yang dikirim ulang dapat
memakai artefak lama bila seluruh dependency relevan cocok. Mengganti model
embedding menginvalidasi representasi dense meskipun PDF tetap sama.

**Alasan bisnis:** Menghindari pengunduhan, parsing dan panggilan model berulang membantu mengendalikan biaya pembaruan corpus. Status job juga memberi kepastian apakah dokumen siap, gagal, atau menunggu review.

**Alternatif dan trade-off:** Skrip sekali jalan lebih cepat dibuat. Durable job menambah state/operasi database, tetapi memudahkan resume dan diagnosis ketika corpus atau lama proses membesar.

### I4. Parse dan OCR — Rust document worker

Membaca isi PDF sambil mempertahankan halaman asalnya.

**Input:** referensi PDF terverifikasi dan manifest parser/OCR.

**Proses:** membaca
text layer, reading order dan locator; halaman scan melalui OCR.

**Output:**
`TextArtifact` dengan teks sumber per halaman, locator dan status halaman.

**Contoh input:**

```json
{
  "file": "A.pdf",
  "source": "src-A",
  "page": 3
}
```

**Contoh output:**

```json
{
  "source": "src-A",
  "page": 3,
  "text": "Pasal 5\n(1) Penyelenggara wajib menyampaikan laporan paling lambat 30 hari setelah kegiatan berakhir.\n(2) Kewajiban pada ayat (1) tidak berlaku bagi kegiatan internal.",
  "text_artifact": "text-A-raw",
  "page_status": "parsed"
}
```

Contoh: halaman 3 A menghasilkan teks Pasal 5. Halaman scan yang gagal OCR
menghasilkan issue/page failure; sistem tidak mengklaim dokumen lengkap hanya
karena halaman lain berhasil. Tabel mempertahankan hubungan baris/kolom yang relevan.

**Alasan bisnis:** Dokumen scan tetap dapat masuk jangkauan pencarian melalui OCR. Locator membuat pengguna tidak perlu menelusuri seluruh PDF untuk menemukan sumber jawaban.

**Alternatif dan trade-off:** Text-layer-only lebih murah dan cepat tetapi tidak mencakup scan. OCR memerlukan compute dan dapat salah membaca angka; jalur dipilih per halaman, dengan kegagalan dicatat.

### I5. Normalisasi dan struktur — Rust document worker

Memberi komputer pemahaman lokasi suatu kalimat dalam struktur dokumen.

**Input:** teks sumber, page locators dan policy normalisasi.

**Proses:** merapikan
artefak layout tanpa membuang angka/negasi, memetakan teks normalisasi ke sumber,
lalu mengenali hierarchy.

**Output:** teks normalisasi, offset mapping dan
`StructureNode` untuk bab, pasal, ayat, huruf atau tabel.

**Contoh input:**

```json
{
  "text_artifact": "text-A-raw",
  "page_text": "Pasal 5\n(1) ...30 hari...\n(2) ...tidak berlaku..."
}
```

**Contoh output:**

```json
{
  "node": "node-p5",
  "label": "Pasal 5",
  "children": [
    {
      "node": "node-p5-1",
      "label": "Ayat (1)"
    },
    {
      "node": "node-p5-2",
      "label": "Ayat (2)"
    }
  ],
  "normalized_text": "text-A-normalized",
  "source_mapping": "map-A-normalized-to-raw"
}
```

Contoh: “Pasal 5”, “(1)” dan “(2)” menjadi parent dan dua child, bukan satu paragraf
tanpa struktur. Kata “tidak” pada ayat (2) tetap ada. Span wire menggunakan byte
UTF-8 start-inclusive/end-exclusive; offset token atau nomor karakter bukan pengganti.

**Alasan bisnis:** Struktur memungkinkan hasil menunjuk pasal/ayat dan membantu pengguna membaca konteks yang relevan. Menjaga negasi/angka mengurangi risiko jawaban lancar tetapi berbeda makna dari sumber.

**Alternatif dan trade-off:** Plain text saja lebih mudah disimpan. Parser hierarchy menambah kompleksitas untuk layout berbeda; manfaat harus diuji pada struktur dan kutipan, bukan hanya teks yang tampak rapi.

### I6. BIND dan identitas ketentuan — Go registry

Menetapkan dokumen dan ketentuan mana yang sedang diproses sebelum membuat unit retrieval.

**Input:** metadata bersumber, hierarchy dan registry revision.

**Proses:**
mencocokkan regulasi dalam scope penerbit/jenis/nomor/tahun, kemudian mengikat
ketentuan dan versi yang dapat ditetapkan.

**Output:** canonical bindings,
provision/version refs atau konflik yang perlu diselesaikan.

**Contoh input:**

```json
{
  "issuer": "Badan Contoh",
  "kind": "Peraturan",
  "number": "1",
  "year": 2024,
  "node": "node-p5-1"
}
```

**Contoh output:**

```json
{
  "regulation": "reg-A",
  "provision": "p5-1",
  "version": "v5-1-old",
  "structure_node": "node-p5-1",
  "registry_revision": "rev-7"
}
```

Contoh: A Pasal 5 ayat (1) terikat ke `reg-A/p5-1/v5-1-old`. Peraturan nomor 1
dari penerbit lain tidak digabung hanya karena nomornya sama. BIND menyediakan
identitas dokumen/ketentuan untuk CHUNK; resolusi semua mention semantik dilakukan
setelah EXTRACT. Registry Go memiliki assignment otoritatif.

**Alasan bisnis:** Identitas konsisten mencegah aturan penerbit berbeda tercampur dan mempertahankan tautan ketika judul atau lokasi PDF berubah. Ini mendukung kepercayaan pada pencarian dan audit sumber.

**Alternatif dan trade-off:** Menggunakan judul atau URL sebagai ID mudah, tetapi rapuh terhadap alias, mirror dan perubahan nama. Registry menambah lookup/review; konflik material tidak diselesaikan demi mempercepat ingestion.

### I7. Structural chunking — Rust document worker

Membuat unit pencarian yang cukup spesifik, tetapi masih dapat dipahami bersama induknya.

**Mengapa structure-aware, bukan semantic chunking murni?** Regulasi sudah memiliki
unit rujukan seperti pasal, ayat dan huruf. Sistem memanfaatkan unit itu agar hasil
pencarian dapat menunjuk lokasi yang jelas. Kemiripan topik saja tidak cukup untuk
memutuskan bahwa dua ayat atau dua pasal boleh dilebur menjadi satu ketentuan.

```text
Pasal 5 (parent)
  +-- Ayat (1): kewajiban dan batas waktu   -> child chunk pertama
  +-- Ayat (2): pengecualian               -> child chunk kedua

Query menemukan ayat (1)
  -> hydrate label/parent yang relevan
  -> ambil ayat (2) bila pengecualian diperlukan
  -> susun konteks lengkap sesuai budget
```

Pasal pendek dapat menjadi satu unit jika policy mengizinkan dan seluruh versi/span
tetap tercatat. Pasal panjang dipecah pada boundary ayat/huruf terlebih dahulu.
Jika satu ayat masih melebihi batas model, perlu subchunk lebih kecil dengan pemetaan
sumber yang utuh; tidak benar bahwa pemotongan selalu bisa berhenti di batas ayat.
Semantic splitting dapat diuji sebagai opsi tambahan, bukan komponen wajib yang
otomatis diaktifkan. Ukur kehilangan syarat/pengecualian dan biaya embeddingnya.

Parent hydration juga bukan kewajiban memasukkan seluruh pasal ke setiap prompt.
Tujuannya mengambil konteks yang diperlukan tanpa membuat duplikasi dan token
berlebihan. **Precision child retrieval dan kelengkapan konteks adalah dua kebutuhan
berbeda**, sehingga diselesaikan pada tahap yang berbeda.

**Input:** hierarchy, teks beserta mapping, provision bindings dan tokenizer terpin.

**Proses:** membentuk unit retrieval mengikuti pasal/ayat, memecah unit terlalu
panjang dengan batas token, dan menyimpan parent refs.

**Output:** `Chunk` dengan
teks/span, versi ketentuan, node induk dan manifest chunker.

**Contoh input:**

```json
{
  "parent": "node-p5",
  "children": [
    "node-p5-1",
    "node-p5-2"
  ],
  "text": "aturan kewajiban dan pengecualian",
  "tokenizer": "tokenizer-terpin"
}
```

**Contoh output:**

```json
{
  "chunks": [
    {
      "id": "chunk-5-1-old",
      "version": "v5-1-old",
      "text": "Penyelenggara wajib menyampaikan laporan paling lambat 30 hari setelah kegiatan berakhir.",
      "parent": "node-p5"
    },
    {
      "id": "chunk-5-2",
      "version": "v5-2",
      "text": "Kewajiban pada ayat (1) tidak berlaku bagi kegiatan internal.",
      "parent": "node-p5"
    }
  ]
}
```

Contoh: `chunk-5-1-old` memuat aturan 30 hari dan `chunk-5-2` memuat pengecualian.
Keduanya menunjuk parent Pasal 5. Hubungan pengecualian yang ditemukan kemudian
dapat memperkaya dependency; kedekatan posisi saja tidak membuktikan makna exception.
Konteks induk dapat dirender untuk embedding atau diambil saat query sesuai policy.

**Alasan bisnis:** Pengguna membutuhkan jawaban tepat beserta syarat dan pengecualian, bukan paragraf yang kebetulan mirip. Chunk kecil membantu pencarian terarah; parent context mengurangi pekerjaan membaca ulang bagian sekitar.

**Alternatif dan trade-off:** Fixed-size murah dan berguna sebagai baseline. Semantic-only menambah model/threshold tanpa menjamin batas pasal. Struktur diprioritaskan; pemecahan unit panjang mengikuti boundary yang tersedia dan token budget, bukan klaim bahwa satu strategi selalu paling akurat.

### I8. Ekstraksi fakta dan relasi — semantic gateway + Rust/Go validation

Memisahkan siapa/apa yang disebut dari hubungan atau kewajiban yang dinyatakan teks.

**Entity extraction menjawab “siapa atau apa yang disebut?”.** Dari kalimat
contoh, sistem menemukan mention “Penyelenggara”, “laporan”, dan “kegiatan”, lengkap
dengan span sumber. Mention bukan otomatis entitas global baru; kata yang sama
di dokumen lain dapat menunjuk objek dengan scope berbeda.

**Relation extraction menjawab “hubungannya apa, dalam kondisi apa?”.** Mention
tadi dipakai sebagai endpoint proposal kewajiban menyampaikan laporan. “30 hari”
dan “setelah kegiatan berakhir” adalah qualifier penting, bukan dekorasi teks.
Ayat (2) menambahkan pengecualian. Bentuk konsepnya:

```text
Penyelenggara -- wajib menyampaikan --> laporan
                 batas: 30 hari
                 mulai dihitung: setelah kegiatan berakhir
                 pengecualian: kegiatan internal (dari ayat lain)
                 sumber: span ayat (1) dan ayat (2), masing-masing dipertahankan
```

Tanpa entity extraction, endpoint sulit diikat ke registry. Tanpa relation
extraction, graph hanya memiliki daftar nama tanpa penjelasan kewajiban. Kedua
fungsi dapat berbagi satu panggilan model terstruktur; pemisahan penjelasan ini
tidak mewajibkan dua RPC atau dua model call.

**Input:** chunk berbukti, ontology, schema serta model/prompt terpin.

**Proses:**
mengambil rujukan eksplisit dan proposal relasi semantik, kemudian memeriksa bentuk
serta kecocokan evidence terhadap sumber.

**Output:** mention, proposal assertion,
support spans dan issue; identitas mention masih dapat provisional.

**Contoh input:**

```json
{
  "chunk": "chunk-5-1-old",
  "text": "Penyelenggara wajib menyampaikan laporan paling lambat 30 hari setelah kegiatan berakhir."
}
```

**Contoh output:**

```json
{
  "entity_mentions": [
    {
      "id": "m1",
      "surface": "Penyelenggara",
      "concept": "pelaku"
    },
    {
      "id": "m2",
      "surface": "laporan",
      "concept": "objek laporan"
    },
    {
      "id": "m3",
      "surface": "kegiatan",
      "concept": "kegiatan"
    }
  ],
  "relation_proposals": [
    {
      "subject": "m1",
      "meaning": "wajib menyampaikan",
      "object": "m2",
      "qualifiers": {
        "time_limit": "30 hari",
        "starts_after": "kegiatan berakhir"
      },
      "support": "span teks pada chunk-5-1-old"
    }
  ]
}
```

Contoh konseptual: `penyelenggara -> wajib menyampaikan -> laporan`, dengan
qualifier `batas=30 hari` dan `pemicu=kegiatan berakhir`. Ayat (2) memberi
pengecualian kegiatan internal; B memberi proposal perubahan batas. Nama panah ini
penjelasan makna, bukan tambahan predicate baru di ontology. Output yang mengaku
“7 hari” tanpa span sumber ditolak atau dikarantina, bukan langsung menjadi fakta.

**Alasan bisnis:** Fakta terstruktur memungkinkan pengguna menelusuri pelaku, kewajiban, pemicu dan rujukan lintas dokumen. Qualifier penting karena kehilangan pemicu waktu dapat mengubah arti kewajiban.

**Alternatif dan trade-off:** Entity-only lebih murah tetapi belum menjelaskan hubungan. Triple polos lebih ringkas tetapi bisa kehilangan syarat/pengecualian. LLM memperluas cakupan ekstraksi dengan tambahan biaya dan kebutuhan validasi sumber.

### I9. Resolusi entitas dan rujukan — Rust proposals + Go registry

Menghubungkan mention ke entitas yang tepat tanpa menggabungkan dua entitas berbeda.

**Input:** mention, kandidat berscope, evidence kedua sisi dan registry revision.

**Proses:** exact matching bila cukup pasti; model membantu ambiguity dengan
konteks, lalu keputusan divalidasi dan dicatat.

**Output:** `ResolutionProposal`
diikuti `ResolutionDecision`/canonical assignment atau DEFER/review.

**Contoh input:**

```json
{
  "mention": "Peraturan Badan Contoh Nomor 1 Tahun 2024",
  "source": "src-B",
  "candidates": [
    "reg-A",
    "reg-other"
  ],
  "expected_revision": "rev-7"
}
```

**Contoh output:**

```json
{
  "proposal": {
    "mention": "mention-reference-in-B",
    "candidate": "reg-A",
    "action": "LINK",
    "supports": [
      "reference-span-B",
      "identity-span-A"
    ]
  },
  "registry_decision": "LINK setelah validasi pada revision yang sesuai"
}
```

Contoh: frasa “Peraturan Badan Contoh Nomor 1 Tahun 2024” dalam B dihubungkan ke
`reg-A`, dan target perubahan ke `p5-1`. Alias yang menunjuk dua entitas menghasilkan
beberapa kandidat. Model tidak memperoleh izin merge hanya dari kemiripan nama;
proposal terhadap registry revision yang sudah berubah perlu diperiksa ulang.

**Alasan bisnis:** Pencarian tidak terpecah hanya karena nama berbeda, dan relasi lintas dokumen tidak mengarah ke objek salah. False merge dapat mencemari banyak jawaban sehingga biaya koreksinya lebih besar dari satu duplikat nama.

**Alternatif dan trade-off:** Exact-only dapat melewatkan alias; similarity-only dapat salah merge. Kombinasi scope, evidence dan model proposal memerlukan review untuk ambiguity yang belum terselesaikan.

### I10. Transformasi versi — Rust transform + Go validation

Membedakan bunyi ketentuan lama, perubahan dan kondisi pada tanggal yang ditanyakan.

**Input:** teks lama, change-event bersumber, target ketentuan yang sudah terikat
dan tanggal berlaku yang memiliki dukungan.

**Proses:** menerapkan perubahan pada
unit yang terkena dan menyimpan lineage rekonstruksi.

**Output:** provision version,
interval legal, supporting events dan status konflik/unknown bila diperlukan.

**Contoh input:**

```json
{
  "old_version": "v5-1-old",
  "change_source": "src-B",
  "change": "30 hari menjadi 14 hari",
  "effective_from": "2025-07-01"
}
```

**Contoh output:**

```json
{
  "versions": [
    {
      "id": "v5-1-old",
      "limit": "30 hari",
      "from": "2024-01-01",
      "to_exclusive": "2025-07-01"
    },
    {
      "id": "v5-1-new",
      "limit": "14 hari",
      "from": "2025-07-01",
      "supports": [
        "src-A",
        "src-B"
      ]
    }
  ],
  "unchanged": "v5-2"
}
```

Contoh: `v5-1-old` berlaku pada [2024-01-01, 2025-07-01), sedangkan `v5-1-new`
mulai 2025-07-01. `v5-2` tetap dipertahankan karena B tidak mengubah ayat (2).
Teks rekonstruksi 14 hari menelusuri A dan B; ia tidak diklaim sebagai kutipan
verbatim A lama. Jika target/tanggal ambigu, versi hasil rekonstruksi ditahan untuk
review. Urutan tahap dapat berulang bila penyelesaian versi memerlukan RESOLVE;
diagram linear bukan alasan memaksa dependency yang belum tersedia.

**Alasan bisnis:** Pengguna dapat membedakan kewajiban pada periode berbeda dan memahami dasar perubahan. Jawaban berdasarkan aturan lama berisiko menyesatkan meskipun kutipannya memang ada.

**Alternatif dan trade-off:** Latest-document-only lebih sederhana tetapi tidak cukup untuk perubahan parsial atau pertanyaan historis. Versioning memerlukan storage, lineage dan review tanggal; dokumen baru tidak otomatis menggantikan seluruh dokumen lama.

### I11. Assembly graph — Rust, dengan commit oleh Go

Menyusun hubungan yang telah diselesaikan menjadi graph yang bisa ditelusuri.

**Input:** canonical assignments, assertion/support tervalidasi dan versi ketentuan.

**Proses:** merakit endpoint, relasi bertipe, support serta dependency tanpa
mencampur pohon struktur dengan graph semantik.

**Output:** `GraphDelta` dan manifest.

**Contoh input:**

```json
{
  "canonical_regulation": "reg-A",
  "change": "B mengubah p5-1",
  "versions": [
    "v5-1-new",
    "v5-2"
  ],
  "supports": [
    "support-B-p2",
    "support-A-p5-2"
  ]
}
```

**Contoh output:**

```json
{
  "paths": [
    "B -> perubahan p5-1 -> v5-1-new",
    "p5-2 -> pengecualian terhadap kewajiban p5-1"
  ],
  "support_by_path": [
    "support-B-p2",
    "support-A-p5-2"
  ],
  "artifact": "graph-delta-12"
}
```

Contoh: jalur yang menghubungkan B, perubahan ayat (1), dan pengecualian ayat (2)
tetap membawa support sumber masing-masing. Menghapus satu observation tidak
menghapus relasi yang masih mempunyai dukungan valid lain. Edge inferred tetap
dibedakan dari pernyataan eksplisit sumber.

**Alasan bisnis:** Graph membantu menemukan hubungan yang harus dibaca bersama, sehingga pengguna tidak harus mengikuti setiap rujukan secara manual. Nilainya adalah kelengkapan bukti, bukan banyaknya node.

**Alternatif dan trade-off:** Search teks saja lebih sederhana. Graph memerlukan pemeliharaan edge, support dan filter versi; edge salah dapat menambah jawaban salah. Penambahan graph harus dibandingkan dengan baseline tanpa graph.

### I12. Pembuatan indeks — Rust + native inference C++

Membuat dua representasi pencarian dari teks yang sama sebelum pengguna bertanya.

**Input:** chunk terpilih, rendered text, model/tokenizer, dictionary/statistik
BM25 dan generation terpin.

**Proses:** mengirim batch embedding, membentuk
representasi sparse lexical, dan menyiapkan payload filter.

**Output:** `IndexBatch`
dense/sparse, mapping record-evidence-version, serta dependency manifest.

**Contoh input:**

```json
{
  "chunk": "chunk-5-1-new",
  "text": "Penyelenggara wajib ... 14 hari setelah kegiatan berakhir.",
  "version": "v5-1-new",
  "generation": "gen-3"
}
```

**Contoh output:**

```json
{
  "record": "idx-5-1-new",
  "dense": "[v1, v2, ..., v1024]",
  "lexical": "term IDs + bobot BM25 menurut statistik gen-3",
  "payload": {
    "chunk": "chunk-5-1-new",
    "version": "v5-1-new",
    "generation": "gen-3"
  }
}
```

Contoh: teks ayat (1) menghasilkan vector dense 1024 dimensi untuk konfigurasi
BGE-M3, bukan ringkasan kalimat. BM25 memakai term ID dan bobot menurut statistik
generation yang sama. Vector dan skor tidak menentukan kebenaran hukum. Hasil
batch dipasangkan melalui item ID, bukan semata urutan respons. Session model
digunakan ulang dan kapasitas bulk tidak boleh menghabiskan antrean query.

**Alasan bisnis:** Precomputation memindahkan pekerjaan berat ke ingestion, sehingga pengguna tidak menunggu seluruh corpus di-encode saat bertanya. Lexical dan dense melayani cara pengguna menyebut istilah yang berbeda.

**Alternatif dan trade-off:** Dua representasi menambah storage dan biaya indexing. Dense-only dapat melewatkan identifier; lexical-only sulit menghadapi sebagian parafrasa. Keduanya perlu dievaluasi dengan beban dan model yang dipin.

### I13. Staging, readiness, publication — Go + storage backends

Menjadikan hasil batch tersedia sebagai satu keadaan corpus yang konsisten.

**Input:** batch dokumen/graph/index, expected counts/hashes, base snapshot dan
operation identity.

**Proses:** validasi, staging backend, readback/search readiness,
kemudian pemindahan pointer committed sesuai protokol.

**Output:** receipts,
`PublicationManifest`, katalog dan snapshot aktif yang konsisten.

**Contoh input:**

```json
{
  "graph_delta": "graph-delta-12",
  "index_batch": "index-batch-12",
  "target_snapshot": "snap-12",
  "generation": "gen-3"
}
```

**Contoh output:**

```json
{
  "required_backends": "ready setelah validasi",
  "publication": "committed",
  "active_snapshot": "snap-12",
  "generation": "gen-3",
  "manifest": "publication-manifest-12"
}
```

Contoh: `snap-12/gen-3` baru dibaca query setelah backend wajib profilnya siap.
PostgreSQL menyimpan authority/catalog, Qdrant representasi search, Neo4j graph,
dan blob storage byte sumber/artefak. Bila salah satu backend wajib gagal, pointer
lama bertahan; retry memakai intent/checkpoint. Ini bukan transaksi ACID tunggal
lintas semua backend. Worker Rust tidak mengumumkan snapshot aktif sendiri.

**Alasan bisnis:** Pengguna tidak menerima jawaban yang mengambil teks baru tetapi relasi lama. Readiness dan recovery juga mengurangi pekerjaan perbaikan manual ketika satu backend gagal di tengah update.

**Alternatif dan trade-off:** Langsung menulis semua backend lebih mudah tetapi dapat memperlihatkan data setengah jadi. Staging/readback menambah waktu publish dan ruang sementara; biaya itu membeli konsistensi, bukan skor retrieval.

## 3. Query: dari pertanyaan sampai jawaban bersitasi

### Q1. Admission dan snapshot pin — Go API/workflow

Menentukan batas pertanyaan dan keadaan data yang boleh digunakan.

**Input:** pertanyaan contoh, permintaan tanggal/mode, identitas pengguna yang
divalidasi server, dan konfigurasi profil.

**Proses:** validasi ukuran/scope,
deadline/budget, lalu pilih snapshot committed dan tahan read lease.

**Output:** request context dengan `snap-12`, `gen-3`, tanggal 2025-08-01 dan budget.

**Contoh input:**

```json
{
  "question": "Per 1 Agustus 2025, berapa batas waktu laporan kegiatan, dan apakah kegiatan internal juga wajib?",
  "requested_date": "2025-08-01"
}
```

**Contoh output:**

```json
{
  "request": "req-1",
  "authorized_corpus": "corpus-contoh",
  "snapshot": "snap-12",
  "generation": "gen-3",
  "effective_date": "2025-08-01",
  "budget": "budget request dari profil",
  "read_lease": "lease-1"
}
```

Contoh: user meminta kondisi pada 1 Agustus 2025 tetapi pengetahuan dibaca dari
`snap-12`. Request tidak boleh memaksakan corpus di luar hak akses melalui field
JSON. Semua putaran berikutnya menggunakan snapshot yang sama.

**Alasan bisnis:** Jawaban dapat direproduksi pada keadaan corpus yang sama. Admission juga melindungi kapasitas layanan agar satu permintaan terlalu besar tidak menghabiskan sumber daya pengguna lain.

**Alternatif dan trade-off:** Membaca latest setiap tahap lebih mudah tetapi dapat mencampur versi ketika update berlangsung. Pin/lease memerlukan state dan cleanup; pembatasan kapasitas dapat menghasilkan penolakan eksplisit saat penuh.

### Q2. Normalisasi — Go query preparation

Meratakan variasi penulisan yang aman tanpa diam-diam mengganti maksud pengguna.

**Input:** pertanyaan asli.

**Proses:** normalisasi bentuk teks sesuai policy,
pertahankan identifier, tanggal dan negasi.

**Output:** query asli + normalisasi,
token lexical dan catatan transformasi.

**Contoh input:**

```json
{
  "raw_query": "  Per 1 Agustus 2025,  berapa batas waktu laporan kegiatan, dan apakah kegiatan internal juga wajib?  "
}
```

**Contoh output:**

```json
{
  "normalized_query": "Per 1 Agustus 2025, berapa batas waktu laporan kegiatan, dan apakah kegiatan internal juga wajib?",
  "retained_original": true,
  "changes": [
    "spasi awal/akhir dan spasi berulang"
  ],
  "date": "2025-08-01"
}
```

Contoh: variasi spasi dibersihkan, tetapi “apakah kegiatan internal juga wajib”
tidak diubah menjadi klaim bahwa kegiatan internal wajib. Ekspansi typo/sinonim
dicatat dan diuji; tidak otomatis mengganti nomor regulasi.

**Alasan bisnis:** Pengguna tidak perlu mengikuti format pengetikan kaku. Menyimpan teks asli membantu diagnosis ketika transformasi query justru membuat hasil buruk.

**Alternatif dan trade-off:** Rewrite agresif dapat memperbaiki kecocokan istilah sekaligus mengarang peran atau maksud. Normalisasi aman dipisahkan dari semantic expansion; kandidat interpretasi tidak dianggap fakta pengguna.

### Q3. Classify, entity linking dan retrieval planning — Go

Mengubah kebutuhan pertanyaan menjadi rencana pencarian, bukan langsung menjadi jawaban.

**Input:** query, scope temporal, registry alias/candidate lookup dan profil.

**Proses:** mengenali kebutuhan bukti, ambiguity dan seed entitas; tetapkan branch
beserta dependency/budget.

**Output:** `RetrievalPlan`, linked candidates dan
missing/ambiguous scope yang perlu klarifikasi.

**Contoh input:**

```json
{
  "query": "batas waktu laporan dan pengecualian kegiatan internal pada 2025-08-01",
  "scope": "corpus-contoh"
}
```

**Contoh output:**

```json
{
  "needs": [
    "factual",
    "relational",
    "temporal"
  ],
  "candidate_entity": "reg-A jika scope/evidence cukup",
  "branches": [
    "BM25",
    "dense",
    "graph sesuai seed"
  ],
  "temporal_filter": "2025-08-01 pada semua branch relevan",
  "evidence_needs": [
    "aturan batas waktu",
    "sumber perubahan",
    "pengecualian"
  ]
}
```

Contoh: kebutuhan `factual + relational + temporal`, dengan tanggal 2025-08-01.
Jika corpus contoh membuat rujukan A jelas, seed menuju `reg-A`; jika ada beberapa
aturan laporan yang sama-sama mungkin, sistem meminta klarifikasi atau mencari
kandidat tanpa mengarang satu canonical ID. Classifier bukan pemilih kelas tunggal.

**Alasan bisnis:** Routing mengalokasikan compute ke kebutuhan pertanyaan sambil mempertahankan peluang menemukan bukti. Klarifikasi lebih berguna daripada menjawab pasti untuk objek yang sebenarnya ambigu.

**Alternatif dan trade-off:** Selalu menjalankan semua branch lebih sederhana tetapi bisa mahal. Classifier dapat salah; pruning branch tidak boleh dianggap optimasi gratis. Ukur bukti yang terlewat dan latency pada pertanyaan yang sama.

### Q4. BM25 — Go lexical encoder + Qdrant

Mencari kecocokan kata/identifier yang dapat dijelaskan secara langsung.

**Input:** token query, analyzer/dictionary/statistik `gen-3` dan filter scope.

**Proses:** bentuk sparse query yang kompatibel, cari kecocokan lexical.

**Output:** ranked candidates berisi evidence key, versi, rank dan skor branch.

**Contoh input:**

```json
{
  "terms": [
    "laporan",
    "kegiatan",
    "internal"
  ],
  "generation": "gen-3",
  "date_filter": "2025-08-01"
}
```

**Contoh output:**

```json
{
  "illustrative_ranking": [
    {
      "rank": 1,
      "evidence": "e-exception",
      "version": "v5-2"
    },
    {
      "rank": 2,
      "evidence": "e-limit",
      "version": "v5-1-new"
    }
  ],
  "retriever": "BM25",
  "score_kind": "BM25, bukan confidence"
}
```

Contoh: kata “laporan”, “kegiatan”, “internal” menemukan `chunk-5-2` dan kandidat
aturan batas waktu. Output ini daftar calon bukti, belum jawaban. Nomor/istilah
eksplisit terbantu oleh lexical; skor BM25 bukan confidence kebenaran.

**Alasan bisnis:** Pengguna yang sudah tahu istilah resmi atau nomor pasal dapat menemukan rujukan secara terarah. Lexical juga menyediakan baseline yang relatif mudah diperiksa ketika hasil sistem dipertanyakan.

**Alternatif dan trade-off:** Exact terms kuat tetapi kosakata sehari-hari dapat tidak cocok. Dense membantu kasus itu dengan biaya model tambahan; BM25 bukan pengganti penalaran tentang kewajiban.

### Q5. Query embedding dan dense search — C++ + Go/Qdrant

Mencari kedekatan makna ketika kata pengguna dan dokumen berbeda.

**Input:** query text, model manifest yang kompatibel dengan `gen-3`, filter scope.

**Proses:** encode query sekali lalu ANN search atas vector dokumen yang sudah
dibangun.

**Output:** query vector dan ranked dense candidates dengan provenance.

**Contoh input:**

```json
{
  "query": "batas waktu laporan kegiatan",
  "model": "BGE-M3 sesuai gen-3"
}
```

**Contoh output:**

```json
{
  "query_vector": "[q1, q2, ..., q1024]",
  "illustrative_ranking": [
    {
      "rank": 1,
      "evidence": "e-limit"
    },
    {
      "rank": 2,
      "evidence": "e-exception"
    }
  ],
  "retriever": "dense"
}
```

Contoh: “batas waktu laporan” dapat menemukan teks “paling lambat 14 hari”. Query
vector berupa array angka; tidak berisi ID pasal secara langsung. BM25 dapat
berjalan saat embedding/dense sedang dikerjakan. Extraction seluruh PDF tidak diulang.

**Alasan bisnis:** Pengguna dapat mencari dengan bahasa yang dipahaminya tanpa harus mengetahui seluruh istilah resmi. Manfaatnya diukur pada parafrasa, bahasa informal dan code-switch, bukan diasumsikan dari model multilingual.

**Alternatif dan trade-off:** Embedding memerlukan compute/memori dan dapat mendekatkan teks yang berbeda negasi. Kesamaan vector bukan entailment; model lebih besar belum tentu memberi manfaat sebanding biayanya.

### Q6. Graph retrieval — Go traversal + Neo4j

Mengikuti hubungan dari bukti awal menuju sumber terkait yang dibutuhkan.

**Input:** entity/provision seeds yang sah, predicate/path policy, snapshot, tanggal
dan budget hop/kandidat.

**Proses:** telusuri relasi/support yang memenuhi scope.

**Output:** kandidat evidence tambahan dan path beserta dukungannya.

**Contoh input:**

```json
{
  "seed": "reg-A/p5-1",
  "date": "2025-08-01",
  "snapshot": "snap-12"
}
```

**Contoh output:**

```json
{
  "candidate_evidence": [
    "e-limit",
    "e-amendment",
    "e-exception"
  ],
  "paths": [
    "B -> perubahan p5-1 -> versi 14 hari",
    "p5-2 -> pengecualian kewajiban p5-1"
  ],
  "supports": [
    "support-B-p2",
    "support-A-p5-2"
  ]
}
```

Contoh: dari aturan laporan A, traversal menemukan perubahan oleh B serta ayat
pengecualian. Seed dari alias dapat dipakai lebih awal; seed yang berasal dari
dense hits menunggu dense selesai. Jumlah edge/hop bukan ukuran kecukupan jawaban.

**Alasan bisnis:** Pertanyaan prosedural atau lintas dokumen membutuhkan lebih dari satu potongan relevan. Graph ditujukan untuk mengurangi rujukan dan pengecualian yang terlewat saat pengguna menyusun jawaban sendiri.

**Alternatif dan trade-off:** Lebih banyak hop menambah biaya dan noise. Seed salah mengarahkan traversal ke objek salah; support, filter versi dan pembatasan terukur lebih penting daripada menampilkan graph yang besar.

### Q7. Filter, deduplikasi dan fusion — Go retrieval

Menyatukan hasil beberapa pencarian tanpa menyamakan skala skornya.

**Input:** ranked lists lexical/dense/graph.

**Proses:** pastikan eligibility
snapshot/temporal, deduplikasi evidence pada versi sama, gabungkan rank dengan RRF.

**Output:** satu ranked candidate set, tetap menyimpan rank/provenance tiap branch.

**Contoh input:**

```json
{
  "BM25": [
    "e-exception",
    "e-limit"
  ],
  "dense": [
    "e-limit",
    "e-exception"
  ],
  "graph": [
    "e-limit",
    "e-amendment",
    "e-exception"
  ]
}
```

**Contoh output:**

```json
{
  "fused_candidates": [
    {
      "evidence": "e-limit",
      "origins": [
        "BM25",
        "dense",
        "graph"
      ]
    },
    {
      "evidence": "e-exception",
      "origins": [
        "BM25",
        "dense",
        "graph"
      ]
    },
    {
      "evidence": "e-amendment",
      "origins": [
        "graph"
      ]
    }
  ],
  "ranking_policy": "RRF dengan bobot dan konstanta terpin"
}
```

Contoh: bukti batas 14 hari yang ditemukan dense dan graph menjadi satu kandidat
dengan dua asal. Versi 30 hari tidak eligible sebagai aturan berlaku untuk tanggal
contoh; ia dapat muncul sebagai konteks historis hanya jika diberi peran yang jelas.
Filter yang didukung backend dipasang sejak pencarian, bukan baru sesudah fusion.

**Alasan bisnis:** Satu bukti tidak menghabiskan beberapa slot hanya karena ditemukan banyak branch. Provenance memungkinkan tim menjelaskan apakah graph benar-benar menyumbang bukti tambahan.

**Alternatif dan trade-off:** RRF mudah dipakai lintas skala tetapi mengabaikan jarak raw score. Dukungan banyak retriever meningkatkan skor ranking sesuai formula, bukan otomatis probabilitas kebenaran; branch juga dapat saling berkorelasi.

### Q8. Hidrasi teks kandidat dan reranking — Go + C++

Memeriksa relevansi pasangan pertanyaan-teks setelah pencarian awal membatasi kandidat.

**Input:** kandidat, katalog record/artifact, query dan manifest cross-encoder.

**Proses:** ambil teks terverifikasi, cek hash/span/version, lalu nilai pasangan
query-passage dalam batch.

**Output:** kandidat dengan skor/urutan reranker dan teks
yang terikat pada evidence ID.

**Contoh input:**

```json
{
  "query": "batas waktu dan pengecualian kegiatan internal",
  "pairs": [
    {
      "id": "pair-1",
      "evidence": "e-limit"
    },
    {
      "id": "pair-2",
      "evidence": "e-exception"
    }
  ]
}
```

**Contoh output:**

```json
{
  "pair_scores": "skor cross-encoder berkorelasi melalui pair ID",
  "selected_evidence": [
    "e-limit",
    "e-exception",
    "e-amendment bila dibutuhkan sebagai support"
  ],
  "verified_text": true
}
```

Contoh: pasangan pertanyaan dengan ayat pengecualian dinilai bersama, bukan hanya
membandingkan dua vector. Pair ID mencegah skor tertukar. Raw logit bukan probabilitas
kebenaran. Reranker tidak bisa mengembalikan bukti yang hilang dari candidate set.

**Alasan bisnis:** Konteks generator diutamakan pada bukti yang relevan sehingga token tidak banyak dihabiskan untuk kandidat lemah. Dampaknya harus diukur pada kualitas jawaban serta latency total.

**Alternatif dan trade-off:** Reranking menambah inference dan dapat salah mengurutkan. Skor rendah tidak otomatis membenarkan penghapusan evidence wajib; top-k dan selection policy memerlukan evaluasi.

### Q9. Pelengkapan bukti dan context packing — Go answering/workflow

Menyusun bahan yang dapat dibaca LLM sambil menjaga sumber dan konteksnya.

**Input:** hasil rerank, parent/exception/path refs dan budget tokenizer generator.

**Proses:** ambil dependency relevan, pertahankan provenance, susun konteks dengan
ruang untuk system prompt, query, template dan output.

**Output:** `EvidenceBundle`
beserta completeness/missing dependencies dan konteks generator.

**Contoh input:**

```json
{
  "ranked_evidence": [
    "e-limit",
    "e-exception"
  ],
  "dependencies": [
    "support-B-p2",
    "parent-p5"
  ],
  "generator_tokenizer": "tokenizer-generator-terpin"
}
```

**Contoh output:**

```json
{
  "context": [
    {
      "citation": "E1",
      "content": "14 hari setelah kegiatan berakhir",
      "version": "v5-1-new",
      "supports": [
        "A Pasal 5 ayat (1)",
        "B Pasal 2"
      ]
    },
    {
      "citation": "E2",
      "content": "tidak berlaku bagi kegiatan internal",
      "version": "v5-2",
      "supports": [
        "A Pasal 5 ayat (2)"
      ]
    }
  ],
  "missing_dependencies": []
}
```

Contoh: konteks berisi `E1=aturan 14 hari + sumber perubahan` dan
`E2=pengecualian kegiatan internal`, berikut label pasal/ayat dan locator. Parent
Pasal 5 membantu interpretasi. Jika E2 wajib tetapi tidak muat, status partial
dicatat; ayat itu tidak dipotong diam-diam sambil mengklaim jawaban lengkap.

**Alasan bisnis:** Pengguna membutuhkan syarat, pengecualian dan asal pernyataan secara utuh. Context builder mengurangi risiko kalimat yang benar secara lokal berubah makna karena bagian penjelas tidak disertakan.

**Alternatif dan trade-off:** Memasukkan seluruh dokumen mudah secara konsep tetapi mahal dan dibatasi context window. Parent hydration terpilih menambah I/O/token namun dapat mempertahankan makna; panjang konteks bukan ukuran kecukupan.

### Q10. Pemeriksaan kecukupan dan retrieve again — Go workflow

Memutuskan apakah sistem mempunyai bahan cukup untuk menjawab atau perlu mencari bagian tertentu.

**Input:** evidence bundle, kebutuhan query, missing dependencies dan sisa budget.

**Proses:** tentukan apakah bukti cukup, gap dapat dicari, atau perlu berhenti.

**Output:** lanjut generation, retrieval plan tambahan, klarifikasi atau abstention.

**Contoh input:**

```json
{
  "have": [
    "e-limit"
  ],
  "missing": [
    "pengecualian kegiatan internal"
  ],
  "snapshot": "snap-12",
  "remaining_budget": "tersedia"
}
```

**Contoh output:**

```json
{
  "action": "retrieve_again",
  "target": "A Pasal 5 ayat (2) atau support pengecualian",
  "snapshot": "snap-12",
  "budget_reset": false
}
```

Contoh: putaran awal hanya mendapat batas waktu. Workflow menargetkan ayat (2)
atau hubungan pengecualian, bukan mengulang query identik. Tidak ada tambahan bukti
relevan atau budget habis memicu berhenti. Required evidence saat runtime berasal
dari kebutuhan/dependency yang dapat dikenali, **bukan membaca jawaban gold**;
ketidakpastian semantic completeness tetap dinyatakan.

**Alasan bisnis:** Sistem dapat memperbaiki kekurangan bukti sebelum memberikan jawaban yang terlalu yakin. Penghentian berbatas menjaga pengguna tidak menunggu tanpa kepastian dan menjaga biaya per pertanyaan.

**Alternatif dan trade-off:** Satu putaran lebih cepat tetapi bisa tidak lengkap; retry menambah latency dan belum tentu menemukan bukti. Kecukupan semantik adalah penilaian yang dapat salah, sehingga ada jalur partial, klarifikasi atau abstain.

### Q11. Generation — Go provider adapter + LLM

Mengubah bukti terpilih menjadi jawaban yang langsung menjawab pertanyaan pengguna.

**Input:** query, konteks terpilih, citation IDs yang tersedia, model/prompt terpin
dan batas output.

**Proses:** model menyusun klaim dengan rujukan atau abstain.

**Output:** draft terstruktur berisi klaim, citation refs dan informasi ketidakpastian.

**Contoh input:**

```json
{
  "question": "batas waktu dan pengecualian per 2025-08-01",
  "evidence": [
    "E1",
    "E2"
  ],
  "instruction": "klaim harus merujuk evidence yang mendukung"
}
```

**Contoh output:**

```json
{
  "draft_claims": [
    {
      "id": "C1",
      "text": "Batasnya 14 hari setelah kegiatan berakhir.",
      "citations": [
        "E1"
      ]
    },
    {
      "id": "C2",
      "text": "Kewajiban tersebut tidak berlaku bagi kegiatan internal.",
      "citations": [
        "E2"
      ]
    }
  ],
  "state": "draft"
}
```

Contoh bentuk penjelasan, bukan payload API:

```text
Klaim C1: Pada tanggal yang ditanyakan, batasnya 14 hari setelah kegiatan berakhir.
Rujukan: E1 (ketentuan dan perubahan yang mendasarinya).
Klaim C2: Kewajiban tersebut tidak berlaku bagi kegiatan internal.
Rujukan: E2 (Pasal 5 ayat (2)).
```

Model tidak boleh menciptakan `E99` atau menjawab 30 hari dari pengetahuan internal
ketika evidence yang eligible menunjukkan 14 hari. Prompt membantu mengarahkan
perilaku, tetapi tidak membuktikan model selalu mematuhinya.

**Alasan bisnis:** Pengguna mendapat sintesis yang mudah dibaca beserta jalur pemeriksaan sumber, bukan hanya daftar hasil pencarian. Ini ditujukan untuk mengurangi waktu menyusun jawaban dari beberapa dokumen.

**Alternatif dan trade-off:** Search-only lebih sederhana dan tidak menambah risiko generasi. LLM menambah biaya, latency dan kemungkinan salah interpretasi; bila pengguna hanya butuh sumber, evidence-only tetap berguna.

### Q12. Validasi klaim dan sitasi — Go + pemeriksaan semantik sesuai policy

Memeriksa klaim yang dibuat model terhadap bukti sebelum dianggap jawaban final.

**Input:** draft, evidence bundle dan request context.

**Proses:** periksa schema,
ID/span/source/version, dukungan klaim, negasi dan pengecualian.

**Output:** jawaban
yang lolos pemeriksaan, issue/revision request, retrieval tambahan atau abstention.

**Contoh input:**

```json
{
  "claim": "Kegiatan internal juga wajib melapor.",
  "citation": "E2",
  "source_text": "Kewajiban pada ayat (1) tidak berlaku bagi kegiatan internal."
}
```

**Contoh output:**

```json
{
  "structural_check": "ID citation ada",
  "semantic_issue": "klaim berlawanan dengan negasi sumber",
  "action": "revisi atau buang klaim; jangan finalkan sebagai didukung"
}
```

Contoh: “kegiatan internal juga wajib” mempunyai ID citation valid tetapi
bertentangan dengan E2. Pemeriksaan struktural saja tidak mendeteksinya. Revisi
interpretasi lebih tepat jika bukti cukup; mencari ulang diperlukan bila dukungan
memang hilang. Bila pemeriksaan memakai model tambahan, biaya masuk budget request.

**Alasan bisnis:** Pemeriksaan menargetkan kesalahan yang merusak kepercayaan, seperti aktor tertukar atau pengecualian hilang. Jejak klaim-sumber juga mempercepat review dan diagnosis keluhan pengguna.

**Alternatif dan trade-off:** Validator ID murah tetapi terbatas. Semantic checker menambah biaya dan dapat false accept/reject; ia tidak memberi jaminan bebas hallucination. Perlu gold dan pemeriksaan kasus sulit.

### Q13. Respons dan cleanup — Go API/workflow

Menyampaikan hasil beserta sumber dan batasnya dalam bentuk yang dapat digunakan.

**Input:** hasil validasi, semantic/completion status, citation locators dan trace.

**Proses:** render jawaban/sumber, terbitkan event terminal, lepaskan lease/resource.

**Output:** jawaban atau status partial/abstain/clarification/error yang eksplisit.

**Contoh input:**

```json
{
  "claims": [
    "C1",
    "C2"
  ],
  "citations": [
    "E1",
    "E2"
  ],
  "snapshot": "snap-12",
  "effective_date": "2025-08-01"
}
```

**Contoh output:**

```json
{
  "answer": "Batasnya 14 hari setelah kegiatan berakhir. Kegiatan internal dikecualikan dari kewajiban tersebut.",
  "sources": [
    "A Pasal 5 ayat (1) dan B Pasal 2",
    "A Pasal 5 ayat (2)"
  ],
  "as_of": "2025-08-01",
  "snapshot": "snap-12",
  "completion": "berhasil pada contoh ilustratif"
}
```

Contoh: pengguna melihat dua klaim di atas beserta dokumen, pasal/ayat, halaman,
snapshot dan tanggal acuannya. Mode stream mengirim token sebagai provisional;
FINAL atau ERROR menjadi terminal. Putus koneksi membatalkan pekerjaan turunan.
Model timeout adalah kegagalan proses, bukan bukti bahwa corpus tidak memiliki jawaban.

**Alasan bisnis:** Pengguna dapat membaca jawaban, membuka sumber, dan mengetahui kondisi tanggal yang dipakai. Status partial atau gagal yang jelas mencegah pengguna mengira pencarian sudah lengkap ketika belum.

**Alternatif dan trade-off:** Streaming membuat keluaran lebih cepat terlihat, tetapi token awal belum tentu lolos pemeriksaan akhir. UI harus membedakan provisional dan final; cleanup/cancellation mencegah compute terbuang setelah pengguna pergi.

## 4. Alur pendamping yang tetap bagian dari arsitektur

| Tahap/pemilik | Input | Proses dan contoh | Output |
| --- | --- | --- | --- |
| Review resolusi/versi — Go + reviewer | Proposal ambigu, kandidat, evidence, expected revision | Reviewer memeriksa dua kemungkinan penerbit; keputusan dan alasan direkam, revision stale ditolak | Keputusan audited atau tetap DEFER; job dapat dilanjutkan sesuai kontrak |
| Incremental update — Go plan + Rust batch | Observation baru, dependency fingerprints dan base snapshot | Dokumen C mengubah ayat (2); hitung closure dependency, recompute yang terdampak, pertahankan versi lama dan support lain | UpdatePlan, batch baru dan snapshot baru setelah publication |
| Recovery — Go coordinator | Intent, checkpoint, receipts dan state publication | Qdrant sudah menulis tetapi respons hilang; readback/retry idempotent menentukan langkah berikutnya | Job pulih atau gagal eksplisit; tidak ada duplicate mutation atau pointer setengah siap |
| Model preparation — Python tooling + C++ admission | Model/tokenizer, export config dan reference outputs | Ekspor bundle, verifikasi hash, uji parity token/vector/logit sebelum pemakaian | ModelManifest/bundle dan laporan parity; bukan otomatis PASS kualitas retrieval |
| Gold preparation — Python tooling + annotator | PDF/snapshot, pertanyaan dan kandidat evidence | Label jawaban, bukti/alternatif valid, kasus tanpa jawaban; kelompokkan paraphrase satu split | Dataset terpisah train/dev/test dengan provenance/review |
| Evaluation — Python runner | Output API/artefak produksi, gold, RunManifest dan benchmark suite | Ukur retrieval, graph, jawaban, citation dan runtime; bandingkan profil dengan budget tercatat | Metric results, eligibility/gate status dan raw artifacts; prerequisite kurang menjadi NOT_MEASURED/BLOCKED |
| Telemetry — seluruh runtime, agregasi Go/evaluator | Trace/span, tokens, queue time, error dan model/config identity | Pisahkan waktu query embedding, retrieval, rerank, generation serta tiap retry | Diagnosis bottleneck dan distribusi p50/p95/p99, bukan hanya satu angka rata-rata |

Keputusan bisnis untuk pekerjaan pendamping juga memiliki harga:

| Bagian | Manfaat yang dituju | Harga keputusan / alternatif | Cara memeriksa manfaat |
| --- | --- | --- | --- |
| Review | Mengendalikan konflik identitas/versi yang berdampak ke banyak jawaban | Tenaga reviewer dan waktu tunggu; otomatisasi penuh lebih cepat tetapi dapat meneruskan kesalahan material | False merge, hasil adjudikasi, lama antrean dan volume review |
| Incremental | Corpus tetap mutakhir tanpa menghitung semuanya ulang | Dependency tracking lebih rumit daripada full rebuild | Biaya update, freshness lag dan kesetaraan hasil terhadap rebuild |
| Recovery | Mengurangi gangguan layanan serta pekerjaan operator setelah crash | Checkpoint, state machine dan tes fault menambah biaya pengembangan | Waktu pemulihan, pekerjaan terulang, orphan dan konsistensi pembacaan |
| Model preparation | Menghindari pergantian model yang diam-diam mengubah kualitas/representasi | Export/parity dan pemeliharaan bundle; memakai API saja memindahkan sebagian operasi ke provider | Parity, compatibility, kualitas domain dan biaya pada workload yang sama |
| Gold preparation | Keputusan produk punya acuan kualitas yang dapat diperiksa | Anotasi/review ahli membutuhkan waktu; penilaian model saja bisa bias | Agreement, adjudikasi, cakupan tipe pertanyaan dan kebocoran split |
| Evaluation | Mencegah fitur baru tampak menarik tetapi menurunkan kualitas atau latency | Waktu eksperimen dan compute; demo saja tidak menangkap distribusi kegagalan | Regresi per slice, required gates, error/abstention dan cost per workload |
| Telemetry | Tim memperbaiki bottleneck yang benar dan menjelaskan insiden | Instrumentasi, storage serta overhead pengamatan | Kelengkapan trace, overhead terukur dan waktu diagnosis |

Contoh incremental: request yang sudah mem-pin `snap-12` tetap membaca snapshot
itu meskipun `snap-13` dipublikasikan di tengah generation. Retention/read lease
menjaga sumber yang masih dipakai; versi lama tidak dihapus demi pembaruan satu sumber.

Evaluasi menguji apakah sistem menemukan bukti yang benar; gold tidak dimasukkan
ke prompt produksi agar hasil tampak bagus. Fixture contoh A/B di dokumen ini
tidak menggantikan gold regulasi nyata atau workload required.

## 5. Ringkasan perubahan bentuk data dan integrasi

```text
URL -> PDF + receipt -> text + locator -> hierarchy + source mapping
    -> registry bindings -> chunks -> mentions/assertions + support
    -> resolution + legal versions -> graph delta + dense/sparse index batch
    -> readiness receipts -> committed snapshot

question + scope -> pinned request -> normalized query + retrieval plan
    -> ranked candidates + graph paths -> eligible fused candidates
    -> verified text + rerank -> evidence bundle + context
    -> draft claims + citation IDs -> validated result/status -> response
```

Setiap boundary mempertahankan ID, schema/producer manifest, scope dan failure
status yang relevan. File besar dipindahkan lewat referensi artefak beserta hash;
operasi komputasi menggunakan batch. Tidak semua kotak menjadi RPC atau service:
fusion, filter, context, citation dan workflow tetap berada dalam proses Go.

Untuk lokasi implementasi tiap pemilik gunakan [peta kode](05-code-map.md).
Ringkasan keputusan dari sisi pengguna, biaya, dan cara membuktikan nilainya ada
di [alasan bisnis arsitektur](02-decisions.md#5-alasan-bisnis-dari-masalah-pengguna-ke-keputusan-arsitektur).
Untuk arah pengaruh parameter terhadap latency/akurasi gunakan
[matematika dan performa](04-math-and-performance.md). Target numerik tetap di
[benchmark-targets.yaml](../../configs/benchmark-targets.yaml); contoh ini tidak
menambah target, mengubah schema, atau menyatakan fitur sudah aktif.
