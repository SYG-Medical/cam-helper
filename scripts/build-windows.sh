#!/bin/bash
set -e

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="$REPO_ROOT/out/windows"
DIST_DIR="$REPO_ROOT/dist"
APP_EXE="$OUT_DIR/rtsp-virtual-cam-agent.exe"
FFMPEG_EXE="$REPO_ROOT/internal/assets/third_party/ffmpeg/ffmpeg.exe"
DRIVER_INSTALLER="$REPO_ROOT/internal/assets/third_party/driver/virtual-camera-installer.exe"
BRIDGE_EXE="$REPO_ROOT/internal/assets/third_party/driver/virtual-camera-bridge.exe"

mkdir -p "$OUT_DIR"
mkdir -p "$DIST_DIR"

# Check dependencies (Note: these might be missing in the repo but are required for packaging)
if [ ! -f "$FFMPEG_EXE" ]; then
    echo "Warning: Missing bundled ffmpeg.exe at $FFMPEG_EXE"
fi
if [ ! -f "$DRIVER_INSTALLER" ]; then
    echo "Warning: Missing virtual camera installer at $DRIVER_INSTALLER"
fi
if [ ! -f "$BRIDGE_EXE" ]; then
    echo "Warning: Missing virtual camera bridge at $BRIDGE_EXE"
fi

echo "Building Go application..."
CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc \
    go build -trimpath -ldflags="-H=windowsgui -s -w" -o "$APP_EXE" ./cmd/app

if command -v makensis &> /dev/null; then
    echo "Creating installer..."
    cd "$REPO_ROOT/build/installer"
    makensis installer.nsi
else
    echo "makensis not found, skipping installer creation."
fi

echo "Build complete: $APP_EXE"
