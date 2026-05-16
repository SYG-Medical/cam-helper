$ErrorActionPreference = 'Stop'

$RepoRoot = Split-Path -Parent $PSScriptRoot
$OutDir = Join-Path $RepoRoot 'out/windows'
$DistDir = Join-Path $RepoRoot 'dist'
$AppExe = Join-Path $OutDir 'rtsp-virtual-cam-agent.exe'
$FfmpegPath = Join-Path $RepoRoot 'third_party/ffmpeg/ffmpeg.exe'
$DriverInstaller = Join-Path $RepoRoot 'third_party/driver/virtual-camera-installer.exe'
$BridgeExe = Join-Path $RepoRoot 'third_party/driver/virtual-camera-bridge.exe'

New-Item -ItemType Directory -Force -Path $OutDir | Out-Null
New-Item -ItemType Directory -Force -Path $DistDir | Out-Null

if (-not (Test-Path $FfmpegPath)) { throw "Missing bundled ffmpeg.exe at $FfmpegPath" }
if (-not (Test-Path $DriverInstaller)) { throw "Missing virtual camera installer at $DriverInstaller" }
if (-not (Test-Path $BridgeExe)) { throw "Missing virtual camera bridge at $BridgeExe" }

Push-Location $RepoRoot
try {
  $env:GOOS = 'windows'
  $env:GOARCH = 'amd64'
  go build -trimpath -ldflags='-H=windowsgui -s -w' -o $AppExe ./cmd/app
  makensis .\build\installer\installer.nsi
}
finally {
  Pop-Location
}
