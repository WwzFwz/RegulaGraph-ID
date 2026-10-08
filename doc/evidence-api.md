# API evidence lokal

Dokumen ini menjelaskan layanan HTTP Go untuk query pada indeks yang sudah
dipublikasikan. API memanggil workflow produksi yang sama dengan `query-evidence`,
memakai satu corpus/profile yang diotorisasi operator. Respons adalah C01
EvidenceBundle dengan teks, versi, span, ranking provenance dan status completeness.
API ini belum menghasilkan jawaban LLM, traversal graph atau stream token.

## Prasyarat dan menjalankan

Selesaikan [persiapan dan publication](index-source-publication.md), terapkan
migrasi sampai 0016, dan jalankan native embedding dengan model yang sama persis
dengan generation katalog. Qdrant dan PostgreSQL harus tetap tersedia. Pool Go,
FileStore, gRPC channel dan HTTP transport dibuka sekali; model tetap di proses C++.

Set konfigurasi pada terminal PowerShell yang akan menjalankan API. Ganti nilai
contoh sesuai corpus yang benar-benar sudah published; tidak ada corpus otomatis.

```powershell
$env:REGULAGRAPH_POSTGRES_DSN = '<DSN lokal Anda>'
$env:REGULAGRAPH_ARTIFACTS_DIR = '<root artifact yang dipakai worker>'
$env:REGULAGRAPH_QDRANT_URL = 'http://127.0.0.1:6333'
$env:REGULAGRAPH_QUERY_NATIVE_ENDPOINT = '127.0.0.1:50054'
$env:REGULAGRAPH_QUERY_CORPUS_ID = 'corpus:example'
$env:REGULAGRAPH_API_AUTH_SCOPE = 'operator:corpus'
$env:REGULAGRAPH_API_PROFILE = 'hybrid'
$env:REGULAGRAPH_BUILD_ID = 'local-dev'
$env:REGULAGRAPH_API_LISTEN = '127.0.0.1:8097'
$tokenBytes = New-Object byte[] 32
$tokenGenerator = [Security.Cryptography.RandomNumberGenerator]::Create()
$tokenGenerator.GetBytes($tokenBytes)
$tokenGenerator.Dispose()
$env:REGULAGRAPH_API_TOKEN = [Convert]::ToBase64String($tokenBytes)
go run ./src/server/cmd/api
```

Bearer token hanya disimpan pada lingkungan lokal; jangan commit atau kirim ke
chat. `REGULAGRAPH_QDRANT_API_KEY` digunakan bila backend membutuhkannya. Tidak
ada API key provider LLM untuk endpoint evidence ini. Aplikasi tidak membaca
`.env` otomatis. Untuk pemanggilan dari terminal lain, set token yang sama secara
lokal atau jalankan executable terkompilasi di terminal proses yang terpisah.

Pilihan profile `vector` atau `hybrid` wajib eksplisit. Reranking opsional yang
tersedia pada CLI belum dikonfigurasi di entry point API ini. Graph profiles,
CURRENT/COMPARE dan generation jawaban ditolak, tanpa fallback diam-diam.

## Request dan response

`GET /livez` hanya memeriksa bahwa proses HTTP merespons. `GET /readyz` memerlukan
bearer token dan memeriksa active snapshot/catalog, native capabilities dan
collection Qdrant. Readiness bukan bukti kualitas hukum atau required benchmark.

```powershell
$headers = @{ Authorization = "Bearer $env:REGULAGRAPH_API_TOKEN" }
Invoke-RestMethod http://127.0.0.1:8097/readyz -Headers $headers
$question = @{
  corpus_id = $env:REGULAGRAPH_QUERY_CORPUS_ID
  question = 'Apa ketentuan izin usaha?'
  response_mode = 'RESPONSE_MODE_COMPLETE'
  requested_profile = 'RETRIEVAL_PROFILE_HYBRID_RAG'
  temporal_scope = @{
    mode = 'TEMPORAL_MODE_AS_OF'
    effective_at = @{ year = 2026; month = 1; day = 1 }
    unresolved_policy = 'UNRESOLVED_POLICY_REPORT'
  }
} | ConvertTo-Json -Depth 5
Invoke-RestMethod http://127.0.0.1:8097/v1/evidence -Method Post -Headers $headers -ContentType application/json -Body $question
```

Pilih tanggal hukum secara eksplisit. Corpus request harus sama dengan konfigurasi;
caller tidak dapat memasok RequestContext tepercaya, auth scope, routing backend,
config fingerprint atau model melalui JSON. `snapshot_id` dapat dipasang untuk
meminta snapshot aktif tertentu; historical snapshot lain belum dilayani.

HTTP 200 membawa bukti, termasuk PARTIAL/NONE bila memang demikian. Itu bukan
jawaban terverifikasi. Kesalahan tidak dikonversi menjadi daftar bukti sukses:
400 input/mode salah, 401 token salah, 403 corpus/origin ditolak, 413 body terlalu
besar, 415 media salah, 429 overload, 502 keluaran internal invalid, 503 dependency
tidak siap, dan 504 deadline. Response dan log tidak menyalin error mentah backend.

## Batas dan lifecycle

Entry point saat ini menggunakan 20 kandidat per branch, maksimal 8 handler
evidence/readiness bersamaan, timeout 30 detik, body 64 KiB, header server 16 KiB,
dan output JSON maksimal 16 MiB. Kelebihan concurrency ditolak dengan 429; tidak
ada antrean tak terbatas. Angka ini konfigurasi layanan, bukan revisi target suite.
Endpoint hanya bind IP loopback; semua Origin browser ditolak dan tidak ada CORS.

Cache menyimpan satu set artefak/generation immutable. Request baru tetap mendapat
lease snapshot baru; perubahan publication/generation membutuhkan preparation
ulang, dan request lama mempertahankan resource yang telah dipinnya. Hasil query
dan lease tidak dicache. Bundle ID mengikat lease, snapshot dan pertanyaan;
identitas item bukti tetap mengikuti record sumber/index. `X-Request-ID` dibuat
server, `X-Evidence-ID` mengidentifikasi bundle hasil. Log JSON menghubungkan keduanya
dengan route/status/duration tanpa query text/token/credentials. Duration handler
bukan keseluruhan waktu network/handshake ataupun required latency acceptance.

Tekan **Ctrl+C** pada terminal API. Server menghentikan koneksi baru, memberi
kesempatan request aktif selesai, lalu menutup resource. Native/worker/backend
adalah proses terpisah; menghentikan API tidak menghentikan proses tersebut.
Menjalankan layanan loopback ini bukan deployment. Bukti tes dan keterbatasan
tersedia pada [laporan API](verification-report-evidence-api.md).
