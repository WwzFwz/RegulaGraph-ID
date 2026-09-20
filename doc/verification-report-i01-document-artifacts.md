# Verifikasi artefak dokumen I01

Dokumen ini mencatat bukti verifikasi boundary `TextArtifact` dan `DocumentBatch` I01 pada 2026-09-20.
Perannya mengikat implementasi offset raw-normalized, semantic reference closure, persistence immutable,
dan audit adversarial ke revision yang dapat ditinjau. PASS hanya berlaku untuk scope library lokal ini;
benchmark produksi tetap **REQUIRED_UNMEASURED**.

## Identitas revision

Implementasi yang diverifikasi tersimpan dalam rangkaian commit berikut:

- `a30e172bf9e995eac7f8d477045bf504c94ad8af` — mapping bukti halaman ke normalized text;
- `8bbeb507e4b513d6275db094c66b6062f7086c93` — semantic validation `DocumentBatch`;
- `790fbee4bef9b0b5fba4b77c311c014952877bd0` — confinement dan bounded read artifact store.

Assembler dan persistence awal yang diaudit berada pada rentang `fd06313` sampai `d7a7ea8`. Target,
workload, denominator, serta asumsi hardware dalam `configs/benchmark-targets.yaml` tidak diubah.

## Invariant yang dibuktikan

| Boundary | Hasil | Bukti utama |
| --- | --- | --- |
| Raw ke normalized page evidence | PASS | CRLF, soft-hyphen, UTF-8 boundary, dan form-feed diproyeksikan tanpa offset keluar artefak |
| Page coverage | PASS | Span dimulai pada byte 0, kontinu tanpa gap/overlap, berakhir pada ukuran normalized text, dan halaman sukses memiliki bukti nonkosong |
| Typed reference closure | PASS | Local ID bertipe salah, missing ref, collision dependency/local, dan descriptor alias ditolak |
| Artifact identity | PASS | `ArtifactRef` lokal harus cocok penuh; dependency eksternal harus cocok fingerprint |
| Batch completeness | PASS | Failed page wajib memiliki issue; completeness dihitung ulang saat assemble, persist, dan load |
| Resource limits | PASS | Record, dependency/lookup edge, page, span, serta read size diperiksa sebelum hasil diterima |
| Structure validation | PASS | Parent-child simetris, child tunggal, cycle, dan lookup linear divalidasi |
| Filesystem confinement | PASS | Path traversal, symlink/reparse point, junction keluar root, non-file, size, dan hash ditolak |

Generated page issue memakai indeks `HashSet`, sehingga deduplikasi dan pemeriksaan coverage berjalan
linear terhadap jumlah issue/page. Batas record diperiksa sebelum issue hasil derivasi ditambahkan.
Pembacaan object dibatasi pada ukuran descriptor ditambah satu byte untuk mendeteksi pertumbuhan file.

## Pemeriksaan implementer

Perintah berikut lulus pada working tree yang sama dengan implementasi final:

```powershell
cargo test --workspace --all-targets
cargo clippy --workspace --all-targets -- -D warnings
$env:PYTHONPATH = ".cache/contracts/python;."
python -m unittest discover -s tests/unit -p "test_*.py"
python -m unittest discover -s tests/integration -p "test_*.py"
go test ./src/server/...
go vet ./src/server/...
python scripts/check_contracts.py
git diff --check
```

Hasil Rust adalah 71 unit PASS, satu interop ignored yang dijalankan melalui harness kontrak, dan satu
native PDFium integration PASS. Python menghasilkan 51 unit dan tiga integration PASS. Package Go dan
vet PASS. Descriptor kontrak tetap 157 message, 31 enum, dan empat service.

## Review independen

Agent `verify_c01` bekerja read-only dan menjalankan 16 counterexample adversarial. Seluruhnya PASS pada
audit terakhir. Kasusnya mencakup junction escape, UTF-8 split, normalized/raw offset mismatch, span page
overlap/gap/kosong, typed-ID confusion, descriptor mismatch, out-of-bounds span, duplicate page, dependency
cap bypass, forged completeness, missing failed-page issue, dan protobuf tersimpan dengan hash valid tetapi
semantik palsu. Fingerprint file yang diaudit:

- `document_batch.rs`: `c55e19aaff18c79721e87dcb59f06482ada2f8fed35b0cccc143cb0abee7a6fb`;
- `text_artifact_wire.rs`: `43229b8ee4fffda856523b6ec2f6faf25a9593bf0179280dcbe7a560b4d64a33`.

Verdict reviewer adalah **PASS untuk scope library wire/artifact I01**.

## Batas pembuktian

Audit ini belum membuktikan durability object storage produksi, perlindungan terhadap proses bermusuhan
yang dapat mengganti direktori operator-owned saat operasi berlangsung, remote storage, worker RPC,
publication end-to-end, kualitas corpus, throughput, latency, atau peak RSS pada workload referensi.
Semua target numerik terkait tetap **REQUIRED_UNMEASURED** sampai acceptance run resmi tersedia.
