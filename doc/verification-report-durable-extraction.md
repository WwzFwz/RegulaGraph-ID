# Verifikasi completion replay durable EXTRACT

Dokumen ini merekam implementasi penyimpanan completion provider per item dan
replay sesudah instance gateway dibuat ulang. Batasnya adalah recovery inference;
full-PDF extraction, kualitas graph dan benchmark required belum tercapai.

Base revision `d519c74` ditambah diff paket ini. Fingerprint final dan hasil
independen berada di `artifacts/verification/20261009-durable-extraction/`.
Toolchain Go 1.26.8 windows/amd64; provider fixture deterministik, PostgreSQL
aktual disposable pada localhost. Tidak ada deployment.

## Perubahan

Migration 0024 membuat tabel completion immutable, payload bounded dan checksum
terikat key/usage/JSON. Domain memiliki DTO Go/store contract; adapter PostgreSQL
mengembalikan first committed winner. Gateway memakai mode opsional `postgres`,
config fingerprint dan producer pin berubah, DSN tetap rahasia environment.
Replay key mengikat seluruh input/scope/provenance dan actual producer, bukan
operation key saja. Semua completion direvalidasi terhadap source/ontology/C01.
[Kontrak dan cara mengaktifkan](semantic-completion-replay.md).

Suite menyeluruh juga menemukan loader konfigurasi jawaban mengandalkan
`LlamaModelBinding.Validate` untuk membatasi task GENERATE. Setelah shared binding
mendukung EXTRACT/RESOLVE, loader terlalu longgar. `LoadAnswerGenerator` kini
menolak task selain GENERATE secara eksplisit; tes existing `model-task` kembali
lulus. Perbaikan dipisahkan dalam commit tersendiri.

## Pemeriksaan dan hasil

| Pemeriksaan | Hasil | Bukti / batas |
| --- | --- | --- |
| Service baru memakai completion tersimpan tanpa provider call | PASS | `unit.log`; request/trace/deadline berubah, proposal tetap identik |
| Scope/text/source-artifact/config berubah | PASS | Cache miss; completion lama tidak dipakai lintas identity |
| Store read gagal, commit ack hilang, JSON replay invalid semantik | PASS | Tidak fallback diam-diam; retry sesudah ack hilang memakai hasil committed; semantic validation tetap menolak JSON salah |
| PostgreSQL migration replay, pool close/reopen, gateway baru | PASS | `postgres-bound-hash.log`; model double dibuat gagal bila dipanggil saat replay |
| Concurrent save | PASS | Dua completion berbeda bersaing; kedua caller menerima satu winner yang sama |
| Mutation dan corruption | PASS | UPDATE ditolak trigger; privileged fault injection usage tanpa rehash terdeteksi |
| Full `go test ./src/server/...` | PASS, exit 0 | `go-tests.log`; opt-in integrasi lain tetap SKIP tanpa konfigurasi |
| `go vet ./src/server/...` | PASS, exit 0 | `go-vet.log` |
| Review independen + inference/gateway/config/domain, PostgreSQL opt-in | PASS_SCOPED, exit 0 | `independent-results.json`, `independent.log`; reviewer tidak menganggap provider fixture sebagai kualitas model |

Initial PostgreSQL fixture gagal karena perubahan `RuntimeParams` setelah parsing
tidak tercermin dalam `ConnString()`: query verifikasi schema tidak menemukan
table pada schema target. Initial run memakai tabel baru pada schema default
database disposable dan menulis completion fixture sintetis; tidak mengubah
source/job/registry. Dua completion sintetis dari run awal telah dibersihkan dengan
guard jumlah, exact byte payload dan usage di bawah exclusive table lock;
`initial-fixture-cleanup.log` mencatat hasilnya. Schema aplikasi tetap tersedia.
Tes diperbaiki dengan parameter `search_path` eksplisit pada
URL DSN. Run final membuat serta membersihkan schema unik. Log awal dipertahankan
di `postgres-initial-failure.log`. Kegagalan awal suite generator juga disimpan
di `go-tests-initial-failure.log`, bukan diubah menjadi PASS.

## Batas pembuktian

Recreated service dan reopened pool membuktikan hasil di PostgreSQL, bukan
recovery cache memori. Pengujian ini belum kill proses OS di tengah inference,
belum menjalankan full PDF melewati restart worker/gateway, dan belum mengukur
kualitas model. Lost commit acknowledgement diuji dengan fault-injecting store;
concurrent PostgreSQL writer diuji nyata. Cache tidak memberi authority atas job
atau publication; guard coordinator sebelumnya tetap wajib.

RESOLVE durable replay, retry orchestration otomatis, garbage collection,
quota/retention global dan biaya replay telemetry lengkap masih terbuka. Token
usage replay historis tidak boleh dihitung sebagai biaya inference baru. Semua
required benchmark tetap unmeasured; hasil fixture tidak menggantikan gold.
