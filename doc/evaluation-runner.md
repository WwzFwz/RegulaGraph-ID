# Runner evaluasi E01

Dokumen ini menjelaskan input, proses, output, status, dan cara menjalankan evaluator offline E01. Perannya
adalah membuat agent berikutnya dapat menghasilkan bundle yang dapat diaudit tanpa menebak format atau
menganggap fixture sintetis sebagai hasil benchmark produksi.

## Batas dan posisi arsitektur

`evaluation.runner` membaca hasil dari komponen produksi dan tidak menjalankan retrieval, parsing, graph,
atau inference versi Python. Pipeline/load generator menyimpan manifest dan observation C01, kemudian runner
menilai seluruh gate pada `configs/benchmark-targets.yaml`. Hash target, konfigurasi, dataset, workload, dan
artefak fisik diperiksa sebelum status PASS dapat dibuat.

Target tetap **REQUIRED_UNMEASURED** sampai workload referensi benar-benar dijalankan. Unit/integration test
runner hanya membuktikan bahwa input salah, data kurang, atau gate gagal tidak berubah menjadi PASS.

## Bentuk bundle input

Root JSON schema version 1 memakai field berikut dan menolak field asing:

| Field | Isi dan invariant |
| --- | --- |
| `profile_id` | Salah satu `vector_rag`, `hybrid_rag`, `graph_rag`, atau `hybrid_graphrag`. |
| `evaluation_config_sha256` | SHA-256 byte persis `configs/evaluation.yaml`. |
| `target_suite_sha256` | SHA-256 byte persis target YAML yang dipilih konfigurasi. |
| `dataset_manifest` | ProtoJSON `DatasetManifest`; hash dan snapshot harus sama dengan `RunManifest`. |
| `run_manifest` | ProtoJSON `RunManifest` dengan software/config/model/dataset/target/hardware/workload terpin. |
| `protocol` | Semua field machine-readable pada `protocol`, selain teks `pass_rule`, `eligibility`, `quality_floor`, dan `confidence_reporting`. |
| `workloads` | Satu deklarasi per workload: `status`, `reason`, dan `facts` yang cocok dengan target YAML. |
| `measurements` | Map gate ke `workload`, `statistic`, `unit`, serta run berisi `run_id`, `sample_count`, `evidence`, dan optional `uncertainty`. |
| `observation_runs` | Run berisi `run_id`, `workload`, dan seluruh `Observation` ProtoJSON dari scheduled arrival. |
| `supporting_artifacts` | `ArtifactRef` untuk dataset, runtime config, dan exact workload ref; file, size, dan SHA-256 harus cocok. |

Evidence manual dan telemetry-derived tidak boleh mengisi gate yang sama. `sample_count` setiap run harus
memenuhi minimum workload. Quality run wajib membawa grouped-bootstrap uncertainty. Tiga run independen
dibutuhkan oleh suite saat ini; nilai terburuk dibandingkan ke threshold. Unfinished latency dari telemetry
menjadi infinity dan menghasilkan FAIL tanpa mencoba menyimpan angka non-finite dalam protobuf.

## Output dan status

Runner membuat direktori baru `artifacts/evaluation/<run-id>/` secara default dan tidak menimpa hasil lama.
Isinya `input.json`, `observations.pb`, `gate-results.pb`, `gate-results.jsonl`, serta `report.json`. Input yang
sudah disalin menjadi raw artifact setiap `GateResult` terukur, sehingga keputusan dapat ditelusuri ke
evidence. Direktori `.partial` dipertahankan jika penulisan/evaluasi gagal agar kegagalan tidak tersembunyi.

Status per gate adalah PASS, FAIL, BLOCKED, NOT_MEASURED, atau NOT_APPLICABLE. Profil Hybrid GraphRAG memakai
status keseluruhan untuk acceptance release. Tiga baseline selalu `REPORT_ONLY`, walaupun status evaluasinya
tetap ditulis untuk perbandingan. Exit code CLI adalah 0 untuk PASS/report-only yang berhasil dievaluasi,
1 untuk release yang belum lulus, dan 2 untuk bundle/configuration error.

## Menjalankan dan memverifikasi

Generate binding C01 terlebih dahulu sesuai [implementasi kontrak](contracts-implementation.md), kemudian:

```powershell
$env:PYTHONPATH = ".cache/contracts/python;."
python -m evaluation.runner --config configs/evaluation.yaml --input path/to/bundle.json
```

Pengujian deterministik E01 dijalankan dengan:

```powershell
$env:PYTHONPATH = ".cache/contracts/python;."
python -m unittest tests.unit.test_evaluation_config tests.unit.test_evaluation_metrics tests.unit.test_evaluation_gates tests.unit.test_evaluation_telemetry tests.integration.test_evaluation_runner
```

Saat adapter produksi dibuat, verifikasi jumlah arrival terhadap `sample_count`, duration monotonic dan queue,
request rejected/timeout, output token distribution, serta file hash dari mesin benchmark. Untuk inter-token
latency, bundle sementara memakai evidence manual yang dapat diaudit; kontrak Observation saat ini belum
menyimpan timestamp setiap token. Perubahan kontrak tersebut harus dilakukan lintas binding C01, bukan
didefinisikan khusus di Python.
