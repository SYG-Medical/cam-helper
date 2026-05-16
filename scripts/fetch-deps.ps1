$ErrorActionPreference = 'Stop'

$RepoRoot = Split-Path -Parent $PSScriptRoot
$ThirdPartyDir = Join-Path $RepoRoot "internal/assets/third_party"
$FfmpegDir = Join-Path $ThirdPartyDir "ffmpeg"
$DriverDir = Join-Path $ThirdPartyDir "driver"

# Create directories
New-Item -ItemType Directory -Force -Path $FfmpegDir | Out-Null
New-Item -ItemType Directory -Force -Path $DriverDir | Out-Null

Write-Host "Downloading FFmpeg..."
$FfmpegZip = Join-Path $env:TEMP "ffmpeg.zip"
Invoke-WebRequest -Uri "https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-master-latest-win64-gpl.zip" -OutFile $FfmpegZip
Expand-Archive -Path $FfmpegZip -DestinationPath (Join-Path $env:TEMP "ffmpeg_extract") -Force
$ExtractedFfmpeg = Get-ChildItem -Path (Join-Path $env:TEMP "ffmpeg_extract") -Filter "ffmpeg.exe" -Recurse | Select-Object -First 1
Copy-Item $ExtractedFfmpeg.FullName -Destination (Join-Path $FfmpegDir "ffmpeg.exe")

Write-Host "Downloading Virtual Camera Driver (UnityCapture)..."
$DriverDllUrl = "https://github.com/schellingb/UnityCapture/raw/master/Install/UnityCaptureFilter64.dll"
Invoke-WebRequest -Uri $DriverDllUrl -OutFile (Join-Path $DriverDir "virtual-camera-installer.dll")

Write-Host "Dependencies downloaded successfully to $ThirdPartyDir"
