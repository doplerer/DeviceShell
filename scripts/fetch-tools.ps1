# Downloads the external tools DeviceShell runs with (ADB, scrcpy-server and the FFmpeg
# libraries, all shipped in the official scrcpy release) into <Root>/tools and writes
# tools/install.json. The archive is checked against its pinned SHA-256.
#
#   scripts/fetch-tools.ps1               into dist/
#   scripts/fetch-tools.ps1 -Root <dir>   into another installation
param([string]$Root = (Join-Path (Split-Path $PSScriptRoot -Parent) 'dist'))
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$version = 'v5.0.1'
$asset = "scrcpy-win64-$version.zip"
$sha256 = 'B12A2C4EE8BE317422451FC7DCF8EE20A71B5EA7EF9AD73DDD825A25316227A5'
$source = "https://github.com/Genymobile/scrcpy/releases/tag/$version"

$tools = Join-Path $Root 'tools'
$folder = Join-Path $tools ([IO.Path]::GetFileNameWithoutExtension($asset))
if (Test-Path -LiteralPath (Join-Path $folder 'adb.exe')) {
    Write-Host "scrcpy $version is already in $folder"
    return
}
New-Item -ItemType Directory -Path $tools -Force | Out-Null
$archive = Join-Path $tools $asset
Write-Host "Downloading $asset..."
Invoke-WebRequest -Uri "https://github.com/Genymobile/scrcpy/releases/download/$version/$asset" -OutFile $archive -UseBasicParsing
try {
    $hash = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash
    if ($hash -ne $sha256) { throw "SHA-256 mismatch for ${asset}: got $hash" }
    # The archive holds a single top folder named like the asset.
    Expand-Archive -LiteralPath $archive -DestinationPath $tools -Force
} finally {
    Remove-Item -LiteralPath $archive -Force -ErrorAction SilentlyContinue
}
if (-not (Test-Path -LiteralPath (Join-Path $folder 'adb.exe'))) { throw "adb.exe not found in $folder after extraction" }
[ordered]@{
    Version     = $version
    Source      = $source
    Asset       = $asset
    SHA256      = $sha256
    Verified    = $true
    InstalledAt = (Get-Date).ToString('o')
} | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $tools 'install.json') -Encoding UTF8
Write-Host "Tools installed in $folder"
