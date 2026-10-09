# Replay completion EXTRACT lintas restart

Dokumen ini menjelaskan checkpoint provider completion untuk menghindari sampling
ulang item yang telah selesai ketika gateway atau worker diulang. Completion
adalah JSON keluaran provider beserta usage historis, bukan fakta graph yang sudah
disahkan, keputusan registry, checkpoint job, atau snapshot terpublikasi.

## Alur dan identitas

Go semantic gateway memvalidasi request, kemudian mencari completion per item.
Replay key mengikat fingerprint request semantik lengkap, producer aktual dan
exact chat envelope. Fingerprint mencakup corpus, auth scope, snapshot bila ada,
operation key, seluruh item/text/provenance, model, ontology serta schema;
request ID, trace ID dan deadline dikecualikan agar retry pekerjaan sama dapat
memakai hasil lama. Producer mencakup versi software/config, model/prompt,
ontology context, completion budget dan policy replay. Perubahan batch partition
juga mengubah identitas: reuse lintas partition belum diklaim.

Jika tidak ada checkpoint, provider dipanggil melalui adapter normal, termasuk
admission/token guard bila mode llama.cpp diaktifkan. Hanya respons provider
yang berhasil dikembalikan adapter disimpan; timeout/refusal/truncation tidak
disimpan. Completion JSON tersimpan **tetap dapat gagal semantic validation**.
Setiap load melewati projector, exact source span, ontology dan C01 lagi sebelum
proposal diberikan ke Rust. JSON schema/ontology salah tidak menjadi fakta hanya
karena completion ada di storage.

Store PostgreSQL menulis sebelum acknowledgement, append-only dengan
`INSERT ... ON CONFLICT DO NOTHING`, lalu membaca pemenang commit pertama.
Writer bersamaan dapat memanggil model dua kali pada cache miss, tetapi tidak
mengganti completion lama; semua memakai pemenang yang sama. Tidak ada transaksi
database yang menunggu inference. Crash sebelum commit dapat memerlukan sampling
ulang; crash setelah commit dapat direplay. Ini bukan jaminan exactly-once inference.

Checksum mengikat key, JSON bytes dan input/output tokens. Payload maksimal 4 MiB;
load membatasi transfer dan memeriksa checksum. Storage unavailable menghasilkan
error transient tanpa fallback diam-diam ke model. Corruption menghasilkan error
terminal. Coordinator tetap memvalidasi seluruh artefak/dependency, lease/fence
serta kelengkapan sumber sebelum commit batch dan publication.

## Mengaktifkan

Terapkan migration dengan DSN database yang benar sebelum mengaktifkan mode:

```powershell
# REGULAGRAPH_POSTGRES_DSN sudah diisi melalui environment terminal ini.
go run ./src/server/cmd/cli migrate -dir migrations -timeout 5m
$env:REGULAGRAPH_SEMANTIC_REPLAY = 'postgres'
```

Set konfigurasi model/schema/ontology/gateway seperti pada
[README gateway](../src/server/cmd/semantic-gateway/README.md). Ekspor producer
dengan `REGULAGRAPH_SEMANTIC_PRINT_PRODUCER=true`, pin bytes hasilnya, lalu
nonaktifkan mode print dan jalankan gateway. Mode print memvalidasi/fingerprint
policy tanpa membuka PostgreSQL atau memanggil model; itu bukan readiness probe.
DSN/credential tidak masuk fingerprint. Pool runtime dibatasi empat koneksi.

Default `REGULAGRAPH_SEMANTIC_REPLAY=disabled` mempertahankan cache memori.
Mode PostgreSQL saat ini hanya EXTRACT; RESOLVE menolak konfigurasi ini. Producer
baru wajib dipakai pada job baru, tanpa mengubah producer historis.

## Operasi dan batas

Checkpoint mentah memungkinkan gateway/Rust mengulang validasi tanpa sampling
ulang semua item. Ia tidak mengubah granularitas scheduler job menjadi per chunk.
Item dengan completion valid transport tetapi fakta tidak valid akan tetap
ditolak saat replay; perbaikan prompt/model/config memerlukan producer/key baru.
OutputToken cap diperiksa kembali setelah load. Tidak ada bypass gold atau target.

Token usage yang dikembalikan adalah usage historis completion terpilih, **bukan
tagihan inference baru untuk setiap replay**. Pengukuran biaya harus memakai log
panggilan model/telemetry aktual; runtime belum menyediakan seluruh metrik biaya
replay. Ukur hit rate, duplicate sampling, latency DB/p95/p99, row/payload growth
dan antrean bersama workload required. Tidak ada GC otomatis atau kuota global
table; retensi dan backup perlu kebijakan operasi tersendiri.

Data Go internal `ModelCompletion` dipakai adapter/provider/storage, tanpa schema
wire kedua untuk proposal. Model output tetap schema JSON terpin; proposal
lintas Go/Rust tetap C01 yang sama. [Bukti](verification-report-durable-extraction.md)
mencatat cakupan tes, termasuk pemulihan koneksi/service dan batas pembuktiannya.
