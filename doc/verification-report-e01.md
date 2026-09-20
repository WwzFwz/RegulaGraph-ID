# Verifikasi evaluator offline E01

Dokumen ini mencatat bukti penyelesaian paket E01 pada 2026-09-20. Perannya mengikat implementasi evaluator,
hasil pengujian, dan review agent independen ke revision yang dapat ditinjau. PASS dalam laporan ini hanya
berlaku untuk perilaku evaluator dan penolakan evidence yang tidak eligible; kualitas model, latency,
throughput, corpus terstruktur, dan validitas gold produksi tetap **NOT_MEASURED**.

## Identitas revision dan bukti

Revision implementasi final yang direview adalah `d4bc929c84bf7fa53e30a70fb1abea0fef818521`. Rangkaian E01 juga
mencakup commit `23f68de`, `428a02b`, `486b23f`, dan `a6de2c6`. Verification agent memberi fingerprint
gabungan Python `evaluation/**` dan target YAML sebesar
`456d0d83802e2f83b23fcbfbf9709849b02a87342c9429e02f072fc0c9c3eec0` sebelum commit final; commit tidak
mengubah isi file yang telah difingerprint. Raw command log lokal berada di
`artifacts/verification/e01-20260920/` dan diabaikan Git sesuai protokol verifikasi.

Target numerik dan workload dalam `configs/benchmark-targets.yaml` tidak diubah. Status suite tetap
`REQUIRED_UNMEASURED`; laporan ini tidak mengubah gate, denominator, hardware asumsi, atau kriteria lulus.

## Cakupan dan hasil

| Pemeriksaan | Hasil | Batas pembuktian |
| --- | --- | --- |
| Loader konfigurasi dan profile | PASS | Menolak field asing, target yang dilonggarkan, unit/operator salah, dan ketidaksesuaian profile |
| Dataset/corpus eligibility | PASS | Membaca GoldQuestion NDJSON serta corpus manifest nyata, memeriksa hash/snapshot/split/count/slice |
| Inventory gold dan invariant | PASS | Parsing, graph, chunk, edge, citation, request, event, dan wire fixture diikat ke ID aktual |
| Evaluasi 59 gate | PASS | Setiap gate menghasilkan status; missing evidence tidak menjadi PASS; setiap run dinilai sendiri |
| Telemetry request | PASS | Scheduled arrival, deadline, failed/rejected/unfinished request, stage queue, token, dan corpus identity divalidasi |
| Mixed-load dan throughput | PASS | Rasio tidak menyembunyikan absolute latency/success; open-loop schedule, deadline, warm-up, dan measured window terikat |
| Numeric/error boundary | PASS | Negative, NaN/infinity, integer ekstrem, overflow pembagian, dan denominator di luar uint64 diblokir tanpa crash |
| Artefak output | PASS | Input/hash/media type diverifikasi; protobuf/JSONL/report ditulis atomik dan hasil lama tidak ditimpa |
| Python unit discovery | PASS, 40 tes | Termasuk validator C01 yang dipakai evaluator; fixture sintetis tidak membuktikan kualitas produksi |
| Python integration runner | PASS, 3 tes | Bundle nyata pada filesystem, artifact tamper, output typed, dan larangan evidence telemetry manual |
| Go package tests | PASS | Memastikan perubahan Python/dokumentasi tidak merusak package server yang tersedia |
| Rust workspace tests | PASS | Satu unit PASS; satu interop test tetap ignored karena dijalankan melalui harness C01 |
| Contract descriptor check | PASS | 157 message, 31 enum, empat service; baseline tidak ditulis ulang |
| Benchmark produksi | NOT_MEASURED | Pipeline/model/storage/gold acceptance dan deployment referensi belum tersedia |

Perintah implementer yang dijalankan:

```powershell
$env:PYTHONPATH = ".cache/contracts/python;."
python -m unittest discover -s tests/unit -p "test_*.py"
python -m unittest discover -s tests/integration -p "test_evaluation_runner.py"
python -m compileall -q evaluation tests
go test ./src/server/...
cargo test --workspace --locked --offline
python scripts/check_contracts.py
git diff --check
```

## Review independen

Agent `verify_c01` bekerja read-only dan melakukan tujuh putaran audit sampai tidak ada temuan material
tersisa. Audit terakhir menjalankan 40 tes pada enam modul E01 dan 20 counterexample independen. Seluruhnya
PASS. Counterexample mencakup evidence singleton terhadap inventory besar, ID gold palsu, snapshot berbeda,
request unfinished atau melewati deadline, baseline infinite, durasi aktif yang tidak sama dengan measured
window, unit denominator token/page, citation yang hilang dari source mapping, precision/recall yang tidak
koheren, serta numeric overflow.

Temuan audit menghasilkan perubahan berikut:

- ukuran sampel dan denominator diikat ke dataset, corpus, workload run, atau inventory aktual;
- outcome parsing, graph, dan invariant dihitung ulang per ID, termasuk seluruh citation terbit;
- mixed-load merekonsiliasi jadwal, window, completion deadline, success, p95/p99 absolut, dan ingestion;
- parity memakai populasi answerable yang sesuai definisi Recall/nDCG;
- malformed structure dan bilangan ekstrem menghasilkan BLOCKED/error input terstruktur, bukan exception;
- manifest model, prompt, environment, versi runtime, dan supporting artifact wajib konsisten.

Verdict reviewer adalah **PASS untuk scope audit E01 dan regresi temuan sebelumnya**. Reviewer secara
eksplisit menyatakan bahwa verdict tersebut bukan bukti target performa atau kualitas produksi.

## Keterbatasan dan kelanjutan

Runner E01 mengevaluasi bundle frozen; ia belum menjalankan workload Go/Rust/C++, menghasilkan gold label,
atau menilai kebenaran hukum. Adapter benchmark produksi masih harus menghasilkan Observation, outcome
per-ID, inventory invariant, dan supporting artifact sesuai format dalam
[panduan runner](evaluation-runner.md). Human review tetap diperlukan untuk label relevance, claim,
relation, canonical identity, temporal correctness, dan citation support.

Paket berikut harus mengikuti dependency pada [panduan implementasi](implementation-guide.md). Setiap
acceptance run wajib memakai corpus snapshot, dataset, model/prompt, environment, dan suite hash yang
dibekukan. Target yang gagal diperbaiki melalui profiling/implementasi; perubahan angka atau workload tetap
memerlukan persetujuan pengguna.
