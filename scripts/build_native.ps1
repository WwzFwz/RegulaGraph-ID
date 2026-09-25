# Builds the native tokenizer and C++ inference service against operator-supplied pinned SDKs.
# Integration: protobuf/protoc 34.1, gRPC 1.76 and ONNX Runtime 1.22.0; no implicit downloads.
# Artifacts stay in .cache. Build/CTest results do not establish required model benchmarks.
[CmdletBinding()]
param(
    [Parameter(Mandatory=$true)][string]$OrtRoot,
    [Parameter(Mandatory=$true)][string[]]$DependencyPrefixes,
    [Parameter(Mandatory=$true)][string]$Protoc,
    [string]$Generator='Visual Studio 16 2019',
    [int]$Jobs=4
)
$ErrorActionPreference='Stop'
function Invoke-NativeChecked {
    param([string]$Executable,[string[]]$Arguments)
    $savedPreference=$ErrorActionPreference
    try {
        # Windows PowerShell 5 treats redirected native stderr as ErrorRecords, even on success.
        $ErrorActionPreference='Continue'
        & $Executable @Arguments
        $nativeExit=$LASTEXITCODE
    } finally { $ErrorActionPreference=$savedPreference }
    if ($nativeExit -ne 0) { throw "$Executable failed with exit code $nativeExit" }
}
$repoRoot=Split-Path -Parent $PSScriptRoot
Push-Location $repoRoot
try {
    if ($Jobs -lt 1 -or $Jobs -gt 64) { throw 'Jobs must be 1..64' }
    $resolvedOrt=(Resolve-Path -LiteralPath $OrtRoot).Path
    $resolvedProtoc=(Resolve-Path -LiteralPath $Protoc).Path
    $prefixes=@($DependencyPrefixes | ForEach-Object { (Resolve-Path -LiteralPath $_).Path })
    $tokenTarget=Join-Path $repoRoot '.cache/native-tokenizer'
    Get-Command cargo,cmake,ctest -ErrorAction Stop | Out-Null
    Invoke-NativeChecked cargo @('build','-p','regulagraph-inference-tokenizer','--release','--locked','--offline','--target-dir',$tokenTarget)
    $tokenLibrary=Join-Path $tokenTarget 'release/regulagraph_inference_tokenizer.lib'
    $build=Join-Path $repoRoot '.cache/native-model-build'
    Invoke-NativeChecked cmake @('-S','src/inference','-B',$build,'-G',$Generator,'-A','x64','-DREGULAGRAPH_MODEL_RUNTIME=ON',
        "-DCMAKE_PREFIX_PATH=$($prefixes -join ';')","-DREGULAGRAPH_ORT_ROOT=$resolvedOrt",
        "-DREGULAGRAPH_TOKENIZER_LIBRARY=$tokenLibrary","-DREGULAGRAPH_PROTOC=$resolvedProtoc")
    Invoke-NativeChecked cmake @('--build',$build,'--config','Release','-j',"$Jobs")
    Invoke-NativeChecked ctest @('--test-dir',$build,'-C','Release','--output-on-failure')
} finally { Pop-Location }
