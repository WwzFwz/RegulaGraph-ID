# Laporan verifikasi K01: document dan provision binding

Dokumen ini mencatat verifikasi independen untuk materializer registry-bound pada domain Go dan binding provision-version per structure node pada library Rust. Statusnya **PASS untuk correctness submilestone library binding dokumen/pasal**. Workflow durable, persistence hasil binding, stage CHUNK produksi, kualitas hukum, dan benchmark produksi belum dibuktikan. Seluruh target numerik pada `configs/benchmark-targets.yaml` tetap **REQUIRED_UNMEASURED**.

## Cakupan dan identitas bukti

Verifikasi dimulai dari base commit `b0ad20132bc5bac5657f932ffc111959a9828754`. Fingerprint enam file perilaku final adalah `81f5e58bd0679f164d7ef98c6982ed672bcfe464ca29bc03e2f46b50fe186b48`. Fixture, harness lintas bahasa, raw log, hash, dan verdict machine-readable disimpan di `artifacts/verification/k01-document-binding-20260921/`; direktori tersebut merupakan artefak lokal yang diabaikan Git.

| Pemeriksaan | Hasil | Bukti utama |
| --- | --- | --- |
| Registry handoff issuer→regulation | PASS | Claim exact direkomputasi; assignment hilang, duplikat, drift, stale, dan forged ditolak |
| Materialisasi dokumen | PASS | `Regulation`, `DocumentEdition`, `Provision`, dan `ProvisionVersion` deterministik memakai canonical assignment dan registry revision |
| Provenance dan temporal uncertainty | PASS | Edition menunjuk observation, issuer menjadi external dependency, metadata tetap unreviewed, dan legal dates/status tetap unknown |
| Structural closure | PASS | Tepat satu root DOCUMENT full-coverage per text artifact; parent/child, sibling order, span containment, overlap, cycle, depth, dan path bytes divalidasi |
| Completeness | PASS | Source bound tanpa structured text menghasilkan PARTIAL dan `STRUCTURED_TEXT_MISSING`; source tanpa regulation binding tetap dikarantina eksplisit |
| Stable output identity | PASS | Perubahan input protobuf atau producer config mengubah batch ID; replay identik tetap deterministik |
| Binding chunk Rust | PASS | Setiap structure node wajib memiliki tepat satu provision-version valid dan coverage diperiksa sebelum tokenisasi |
| Go→Rust protobuf roundtrip | PASS, 4/4 kasus adversarial Rust | Output COMPLETE dan PARTIAL dari Go lolos semantic reference closure Rust; missing/foreign binding ditolak |
| Go adversarial overlay | PASS, 21 kasus | Mutation, provenance, partial assignment, false coverage, overlap, cycle, stale plan, dan config identity |
| Full Go dan static analysis | PASS | 104 test event lulus; lima skip karena environment PostgreSQL/worker/wire fixture dan privilege symlink Windows; `go vet ./src/server/...` lulus |
| Rust workspace dan static analysis | PASS | 99 unit dan satu native PDFium lulus, satu fixture wire diabaikan; Clippy lulus |
| Scaling probe sintetis | PASS sebagai guard complexity | Sibling probe 1.000→5.000 node tumbuh 9,46→47,19 ms dan depth 70 ditolak; angka ini bukan benchmark produksi |
| Benchmark produksi | NOT_MEASURED | Belum ada run corpus referensi untuk latency, throughput, RSS, false merge/split, atau dampak retrieval |

## Semantik yang dibuktikan

Planner regulation mengikat sumber ke issuer canonical dalam dua tahap. Materializer kemudian memeriksa ulang candidate terhadap observation saat ini sebelum membuat record. Identitas provision berasal dari canonical regulation dan structural path lengkap; alokasi ID tetap milik registry. Satu source dapat mempertahankan edition provenance meski parsing belum tersedia, tetapi hasil tersebut tidak boleh dinyatakan COMPLETE.

Setiap text artifact terstruktur harus mempunyai satu root DOCUMENT yang menutup seluruh normalized byte range. Span terurut dan tidak tumpang tindih memungkinkan validasi containment linear. Kedalaman dan ukuran structural path dibatasi sebelum cache path dialokasikan. Output menyimpan input batch dan registry revision sebagai dependency, serta menyimpan canonical issuer sebagai external dependency agar semantic closure sama di Go dan Rust.

Builder Rust menerima map structure-node→provision-version dengan exact coverage. Missing atau foreign key ditolak sebelum pemanggilan tokenizer. Proyeksi wire mempertahankan version ID milik node masing-masing dan hasil aktual dari materializer Go telah didecode serta divalidasi oleh semantic validator Rust.

## Batas hasil dan pekerjaan lanjutan

Fungsi ini masih library boundary. Scheduler belum memiliki stage BIND/CHUNK, assignment belum dipersist dan dipublikasikan melalui transaction/fence, tokenizer produksi belum dipilih, dan chunk belum dibuat oleh worker dari output materializer. Resolusi issuer semantik, keputusan merge/split reversible, legal change extraction, serta gold quality tetap pekerjaan K01/I01 berikutnya. Hasil unit, roundtrip, dan probe sintetis tidak menggantikan required benchmark corpus referensi.
