$ErrorActionPreference = 'Stop'

$RepoRoot = Split-Path -Parent $PSScriptRoot
$OutDir = Join-Path $RepoRoot 'out/windows'
$DistDir = Join-Path $RepoRoot 'dist'
$AppExe = Join-Path $OutDir 'rtsp-virtual-cam-agent.exe'
$FfmpegPath = Join-Path $RepoRoot 'internal/assets/third_party/ffmpeg/ffmpeg.exe'
$DriverDll = Join-Path $RepoRoot 'internal/assets/third_party/driver/virtual-camera-installer.dll'
$BridgeExe = Join-Path $RepoRoot 'internal/assets/third_party/driver/virtual-camera-bridge.exe'

New-Item -ItemType Directory -Force -Path $OutDir | Out-Null
New-Item -ItemType Directory -Force -Path $DistDir | Out-Null

if (-not (Test-Path $FfmpegPath)) { 
    Write-Warning "Missing bundled ffmpeg.exe at $FfmpegPath. Run scripts/fetch-deps.ps1 first."
    exit 1
}
if (-not (Test-Path $DriverDll)) { 
    Write-Warning "Missing virtual camera DLL at $DriverDll. Run scripts/fetch-deps.ps1 first."
    exit 1
}

Push-Location $RepoRoot
try {
  Write-Host "Building Go app..."
  $env:GOOS = 'windows'
  $env:GOARCH = 'amd64'
  go build -trimpath -ldflags='-H=windowsgui -s -w' -o $AppExe ./cmd/app
  
  $Makensis = Get-Command makensis -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Source
  if (-not $Makensis) {
    $CommonPaths = @(
      "C:\Program Files (x86)\NSIS\makensis.exe",
      "C:\Program Files\NSIS\makensis.exe"
    )
    foreach ($Path in $CommonPaths) {
      if (Test-Path $Path) {
        $Makensis = $Path
        break
      }
    }
  }

  if ($Makensis) {
    Write-Host "Creating installer using $Makensis..."
    Push-Location "build/installer"
    try {
      & $Makensis installer.nsi
    } finally {
      Pop-Location
    }
  } else {
    Write-Warning "makensis not found in PATH or common locations. Please add NSIS to your PATH or install it to the default location."
  }
}
finally {
  Pop-Location
}
