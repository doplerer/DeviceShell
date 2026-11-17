# Validates (gofmt, go vet, go test) and builds deviceshell.exe offline, then installs it
# into dist/. The Go toolchain pinned in scripts/toolchain.json is downloaded into
# .toolchain/ the first time and checked against its SHA-256.
#
#   scripts/build.ps1                 validate, build and install into dist/
#   scripts/build.ps1 -ValidateOnly   validate only
param([switch]$ValidateOnly)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$project = Split-Path $PSScriptRoot -Parent
$toolchainRoot = Join-Path $project '.toolchain'
$go = Join-Path $toolchainRoot 'go\bin\go.exe'
$gofmt = Join-Path $toolchainRoot 'go\bin\gofmt.exe'
$cache = Join-Path $project '.cache'
$candidate = Join-Path $cache 'build\deviceshell.exe'
$dist = Join-Path $project 'dist'
$destination = Join-Path $dist 'deviceshell.exe'
$legacy = Join-Path $dist 'botfarm.exe'

function Install-Toolchain {
    $pin = Get-Content -Raw -LiteralPath (Join-Path $PSScriptRoot 'toolchain.json') | ConvertFrom-Json
    $archive = Join-Path $cache $pin.filename
    New-Item -ItemType Directory -Path $cache -Force | Out-Null
    Write-Host "Downloading $($pin.version) into .toolchain/..."
    Invoke-WebRequest -Uri "https://go.dev/dl/$($pin.filename)" -OutFile $archive -UseBasicParsing
    $hash = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash
    if ($hash -ne $pin.sha256.ToUpperInvariant()) {
        Remove-Item -LiteralPath $archive -Force
        throw "SHA-256 mismatch for $($pin.filename): got $hash"
    }
    if (Test-Path -LiteralPath $toolchainRoot) { Remove-Item -LiteralPath $toolchainRoot -Recurse -Force }
    Expand-Archive -LiteralPath $archive -DestinationPath $toolchainRoot
    Remove-Item -LiteralPath $archive -Force
}

$environmentNames = @('GOCACHE','GOMODCACHE','GOTOOLCHAIN','CGO_ENABLED','GOPROXY','GOSUMDB','GOFLAGS')
$previousEnvironment = @{}
foreach ($name in $environmentNames) { $previousEnvironment[$name] = [Environment]::GetEnvironmentVariable($name, 'Process') }
try {
    if (-not (Test-Path -LiteralPath $go -PathType Leaf)) { Install-Toolchain }
    $env:GOCACHE = Join-Path $cache 'go-build'
    $env:GOMODCACHE = Join-Path $cache 'go-mod'
    $env:GOTOOLCHAIN = 'local'
    $env:CGO_ENABLED = '0'
    $env:GOPROXY = 'off'
    $env:GOSUMDB = 'off'
    $env:GOFLAGS = '-mod=vendor'
    Push-Location $project
    try {
        $unformatted = & $gofmt -l cmd internal
        if ($unformatted) { throw "Files not formatted with gofmt:`n$($unformatted -join "`n")" }
        & $go vet ./...
        if ($LASTEXITCODE -ne 0) { throw 'go vet found problems' }
        $env:GOOS = 'linux'
        & $go vet ./...
        $env:GOOS = $null
        if ($LASTEXITCODE -ne 0) { throw 'go vet (GOOS=linux) found problems' }
        & $go test ./...
        if ($LASTEXITCODE -ne 0) { throw 'Go tests failed' }
        if ($ValidateOnly) { return }
        New-Item -ItemType Directory -Path (Split-Path $candidate -Parent) -Force | Out-Null
        & $go build -trimpath -o $candidate ./cmd/deviceshell
        if ($LASTEXITCODE -ne 0) { throw 'Build failed' }
    } finally {
        $env:GOOS = $null
        Pop-Location
    }
    if (-not (Test-Path -LiteralPath $dist)) {
        New-Item -ItemType Directory -Path $dist | Out-Null
        Copy-Item -LiteralPath (Join-Path $project 'configs\config.example.json') -Destination (Join-Path $dist 'config.json')
        Copy-Item -Path (Join-Path $project 'configs\routines') -Destination (Join-Path $dist 'routines') -Recurse
        Write-Host "New installation created in $dist. Run scripts/fetch-tools.ps1 to add ADB and scrcpy."
    }
    $running = Get-Process -Name deviceshell, botfarm -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq $destination -or $_.Path -eq $legacy }
    if ($running) {
        Write-Warning "DeviceShell is still running. Validated build: $candidate. Close it and run this script again to install it."
        return
    }
    Copy-Item -LiteralPath $candidate -Destination $destination -Force
    # Installations from before the rename still carry the old executable.
    if (Test-Path -LiteralPath $legacy) { Remove-Item -LiteralPath $legacy -Force }
    Write-Host "Validated and installed: $destination"
} finally {
    foreach ($name in $environmentNames) { [Environment]::SetEnvironmentVariable($name, $previousEnvironment[$name], 'Process') }
}
