# Starts the loopback interview preview from the repository root.
# Builds Go, prepares a bounded offline sample once, and creates a separate Ollama
# profile from existing weights. No deployment or required benchmark changes.
param([switch]$SearchOnly, [string]$Listen = "127.0.0.1:8096")
$ErrorActionPreference = "Stop"
$demoRoot = Split-Path -Parent $PSScriptRoot
Push-Location $demoRoot
try {
    # Reuse this already-running preview instead of overwriting its executable.
    $demoStatus = $null
    try {
        $demoStatus = Invoke-RestMethod -Uri "http://$Listen/api/status" -TimeoutSec 2
    } catch { }
    if ($null -ne $demoStatus -and $demoStatus.mode -eq "local_bm25_preview") {
        if ([bool]$SearchOnly -ne [string]::IsNullOrEmpty($demoStatus.model)) {
            throw "Mode demo aktif berbeda. Hentikan proses lama untuk mengganti mode, atau gunakan checkbox model pada UI."
        }
        Write-Host "Demo sudah berjalan: http://$Listen (reuse; kode tidak di-reload)."
        Write-Host "Setelah perubahan kode, hentikan proses lama lalu jalankan script kembali."
        return
    }
    $env:GOCACHE = Join-Path $demoRoot ".cache/go-build"
    if (-not (Test-Path -LiteralPath "artifacts/interview-demo/SHA256SUMS")) {
        python -m tooling.corpus.prepare_demo --documents 24 --max-pages 150
        if ($LASTEXITCODE -ne 0) { throw "Demo corpus preparation failed" }
    }
    go build -o .cache/regulagraph-demo.exe ./src/server/cmd/cli
    if ($LASTEXITCODE -ne 0) { throw "Go build failed" }
    if ($SearchOnly) {
        & .cache/regulagraph-demo.exe demo -listen $Listen -model=
    } else {
        ollama create regulagraph-demo:latest -f configs/demo.Modelfile
        if ($LASTEXITCODE -ne 0) { throw "Start Ollama first; qwen2.5:7b must already be installed" }
        & .cache/regulagraph-demo.exe demo -listen $Listen
    }
    if ($LASTEXITCODE -ne 0) { throw "Demo exited with an error" }
} finally { Pop-Location }
