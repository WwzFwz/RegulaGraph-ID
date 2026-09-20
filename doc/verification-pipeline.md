# Verifikasi input, proses, output seluruh pipeline

Dokumen ini menetapkan jejak pembuktian end-to-end per paket RegulaGraph-ID. Perannya mencegah komponen yang lulus unit test sendiri menghasilkan pipeline dengan identitas, versi, atau bukti tidak konsisten. Ikuti [verification.md](verification.md), [development-plan](development-plan.md), dan skenario T01–T24; tabel ini tidak mengurangi cakupan desain.

## Akuisisi sampai publication

| Paket | Input yang diverifikasi | Proses yang diperiksa | Output/bukti selesai |
| --- | --- | --- | --- |
| D01 sources | URL resmi, redirect/host, seed vs detail, batas volume | Rate/retry/timeout, stream bytes, hash, dedup, resume, metadata provenance | PDF utuh + receipt/hash; gagal/partial/ditunda tercatat; URL unik tidak diklaim canonical unik |
| C01 contracts | Seluruh record/API/RPC/event desain | Schema DAG, codegen, validators, compatibility | Binding terkompilasi; kasus valid/invalid lintas bahasa; batas kemampuan validator tertulis |
| S01 storage | Operation key, parent snapshot, expected revision/fence | Stage/validate/publish, ledger, visibility, durable/search-ready | Pointer aktif hanya berpindah setelah semua backend siap; crash/retry/abort diuji dengan DB nyata |
| M01 engine/model | Sampel format terstratifikasi, model/tokenizer/precision manifest | Bandingkan engine; warm/cold; reference vs native | Artefak/versi dipin beserta hasil kualitas, waktu, memori; pilihan engine tidak berasal dari asumsi saja |
| I01 parsing | Raw blob berhash, edisi, parser manifest | Reading order, OCR selective, tabel, page errors, offset mapping | TextArtifact yang bisa dipetakan kembali ke halaman; partial tidak menjadi complete |
| I01 structure/chunk | Teks dan mapping tervalidasi | Hierarki bab/pasal/ayat/lampiran, parent links, boundary/token policy | Chunk menyebut versi dan node sumber; nomor, negasi, syarat, pengecualian terjaga |
| I01 temporal | Change event bersumber, tanggal known/unknown/conflict | Amendment sebagian, insert/repeal/renumber, lineage | Historical versions tetap ada; satu ayat berubah tidak menghapus seluruh pasal |
| K01 extraction | Chunk/parent, ontology/model/prompt version | Proposal terstruktur, span support, qualifier/exception, explicit/inferred | Mentions/assertions dengan bukti; malformed/unsupported proposal dikarantina |
| K01 resolution | Mention, identity keys, candidate scope/revision | Blocking coverage, disambiguasi, registry assignment, merge/split | Stable canonical ID bersumber; false merge dan candidate miss dinilai terpisah |
| K01 assembly | Resolved proposals, existing snapshot/supports | Validasi endpoints, inverse dependency, support dedup, closure | GraphDelta tanpa orphan; mirror bukan independent support; delete satu sumber tidak menghapus bukti lain |
| X01 indexing | Chunks/version refs + model/lexical generation | Dense batch, sparse BM25 dengan stats frozen, counts/checksum | IndexBatch kompatibel snapshot; query tidak mencampur generasi |

## Query sampai jawaban

| Paket | Input yang diverifikasi | Proses yang diperiksa | Output/bukti selesai |
| --- | --- | --- | --- |
| Q01 query | Query asli, corpus/izin, tanggal/snapshot | Normalisasi tanpa merusak nomor/negasi; entity linking; pin snapshot sekali | RetrievalPlan auditabel, deadline dan stage budgets eksplisit |
| Q01 retrieval | Plan/model/index generation | Dense/BM25/graph, failure branches, fusion/filter/rerank satu proses Go | EvidenceBundle dengan candidate provenance, path support, completeness jujur |
| A01 context | Selected evidence + required parent/exception/path refs | Batch hydration snapshot sama, token accounting, rendering | ContextBundle menyatakan omitted required refs; tidak menjawab lengkap dari bukti terpotong |
| A01 answering | Context tervalidasi, model/prompt/generation budget | Grounding, streaming provisional, claim/citation mapping, final checks | Jawaban atau abstain/conflict/clarification dengan sumber pasal/ayat dan satu terminal event |
| U01 update | Source/dependency delta + base snapshot | Fixed-point closure, dependency negatif, reuse, stale-writer fencing | Hasil logis setara full rebuild dengan manifest sama; pembaca lama tetap konsisten |
| O01 operasi | Konfigurasi/secret/model/image dipin | Startup/readiness, shutdown, resource isolation, restore | Liveness berbeda dari readiness; backup/restore snapshot serasi; query dan ingestion mendapat kapasitas |
| G01/E01/B01 | Gold, split groups, manifest workload, frozen targets | Ukur kualitas/latency/throughput/error/cost bersama | Raw results dan status tiap gate; required applicable semuanya lulus sebelum release |

## Skenario wajib lintas komponen

Jalankan satu dokumen kecil dari raw blob sampai citation untuk membuktikan integrasi, lalu perluas ke input sulit dan workload penuh. Ini strategi pengujian terhadap desain lengkap, bukan pembatasan sistem menjadi desain minimal. Sampel kecil tidak membuktikan gate skala produksi.

T01–T24 di development-plan meliputi mirror, false merge, Unicode, scan partial, perubahan ayat, tanggal ambigu, alias, split registry, support bersama, exception multi-hop, budget habis, backend gagal, stream gagal, publish bersamaan query, recovery/fencing, unchanged reuse, dependency negatif, model drift, GC, restore, typo/code-switch, dan split leakage. Setiap skenario memiliki expected state sebelum test dibuat. Ukur resource dan waktu tanpa menghapus kasus gagal dari denominator.

## Hal yang tidak boleh disimpulkan

PDF selesai diunduh belum berarti teks berhasil diparsing. Parser menghasilkan teks belum berarti struktur pasal benar. Graph tersimpan belum berarti relasi benar. Retrieval mendapat dokumen belum berarti bukti cukup. Citation memiliki URL belum berarti mendukung klaim. Benchmark cepat pada satu query belum berarti p95/p99 memenuhi beban. Reviewer harus memeriksa transisi tersebut secara eksplisit, dengan bukti yang sesuai tahapnya.
