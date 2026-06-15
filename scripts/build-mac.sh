#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST_DIR="${REPO_ROOT}/dist"
APP_NAME="NystaVision"
APP_DIR="${DIST_DIR}/${APP_NAME}.app"
VERSION="${VERSION:-dev}"

echo "Creating App Bundle structure..."
rm -rf "${APP_DIR}"
mkdir -p "${APP_DIR}/Contents/MacOS"
mkdir -p "${APP_DIR}/Contents/Resources"
mkdir -p "${DIST_DIR}"

# Create Info.plist
cat <<EOF > "${APP_DIR}/Contents/Info.plist"
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleExecutable</key>
    <string>nystavision</string>
    <key>CFBundleIconFile</key>
    <string>icon.icns</string>
    <key>CFBundleIdentifier</key>
    <string>com.sygmedical.nystavision</string>
    <key>CFBundleName</key>
    <string>NystaVision</string>
    <key>CFBundlePackageType</key>
    <string>APPL</string>
    <key>CFBundleShortVersionString</key>
    <string>${VERSION}</string>
    <key>CFBundleVersion</key>
    <string>${VERSION}</string>
    <key>LSMinimumSystemVersion</key>
    <string>10.13</string>
    <key>NSHighResolutionCapable</key>
    <true/>
</dict>
</plist>
EOF

# Build Icon if logo.png exists
if [ -f "${REPO_ROOT}/logo.png" ]; then
    echo "Generating icon.icns..."
    ICONSET="${DIST_DIR}/NystaVision.iconset"
    mkdir -p "${ICONSET}"
    sips -z 16 16     "${REPO_ROOT}/logo.png" --out "${ICONSET}/icon_16x16.png"
    sips -z 32 32     "${REPO_ROOT}/logo.png" --out "${ICONSET}/icon_16x16@2x.png"
    sips -z 32 32     "${REPO_ROOT}/logo.png" --out "${ICONSET}/icon_32x32.png"
    sips -z 64 64     "${REPO_ROOT}/logo.png" --out "${ICONSET}/icon_32x32@2x.png"
    sips -z 128 128   "${REPO_ROOT}/logo.png" --out "${ICONSET}/icon_128x128.png"
    sips -z 256 256   "${REPO_ROOT}/logo.png" --out "${ICONSET}/icon_128x128@2x.png"
    sips -z 256 256   "${REPO_ROOT}/logo.png" --out "${ICONSET}/icon_256x256.png"
    sips -z 512 512   "${REPO_ROOT}/logo.png" --out "${ICONSET}/icon_256x256@2x.png"
    sips -z 512 512   "${REPO_ROOT}/logo.png" --out "${ICONSET}/icon_512x512.png"
    sips -z 1024 1024 "${REPO_ROOT}/logo.png" --out "${ICONSET}/icon_512x512@2x.png"
    iconutil -c icns "${ICONSET}"
    mv "${DIST_DIR}/NystaVision.icns" "${APP_DIR}/Contents/Resources/icon.icns"
    rm -rf "${ICONSET}"
fi

# Build binaries
echo "Building binaries..."

BUILD_AMD64=true
BUILD_ARM64=true

# Check if we are on macOS to compile with CGO target flags
if [ "$(uname)" = "Darwin" ]; then
    echo "Building for macOS Intel (amd64)..."
    if CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 CGO_CFLAGS="-target x86_64-apple-macos10.13" CGO_LDFLAGS="-target x86_64-apple-macos10.13" go build -ldflags="-X 'nystavision/internal/version.Version=${VERSION}' -s -w" -o "${DIST_DIR}/nystavision_amd64" ./cmd/app; then
        echo "amd64 build successful."
    else
        echo "amd64 build failed, skipping universal binary."
        BUILD_AMD64=false
    fi
    
    echo "Building for macOS Apple Silicon (arm64)..."
    if CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 CGO_CFLAGS="-target arm64-apple-macos11.0" CGO_LDFLAGS="-target arm64-apple-macos11.0" go build -ldflags="-X 'nystavision/internal/version.Version=${VERSION}' -s -w" -o "${DIST_DIR}/nystavision_arm64" ./cmd/app; then
        echo "arm64 build successful."
    else
        echo "arm64 build failed, skipping universal binary."
        BUILD_ARM64=false
    fi
    
    if [ "$BUILD_AMD64" = true ] && [ "$BUILD_ARM64" = true ]; then
        echo "Creating universal binary using lipo..."
        lipo -create -output "${APP_DIR}/Contents/MacOS/nystavision" "${DIST_DIR}/nystavision_amd64" "${DIST_DIR}/nystavision_arm64"
        rm "${DIST_DIR}/nystavision_amd64" "${DIST_DIR}/nystavision_arm64"
    elif [ "$BUILD_ARM64" = true ]; then
        echo "Falling back to native arm64 binary only..."
        mv "${DIST_DIR}/nystavision_arm64" "${APP_DIR}/Contents/MacOS/nystavision"
    elif [ "$BUILD_AMD64" = true ]; then
        echo "Falling back to native amd64 binary only..."
        mv "${DIST_DIR}/nystavision_amd64" "${APP_DIR}/Contents/MacOS/nystavision"
    else
        echo "Error: Both builds failed!"
        exit 1
    fi
else
    echo "Error: macOS builds must be compiled on a macOS host because Fyne requires CGO for OpenGL/GLFW."
    exit 1
fi

echo "Zipping App Bundle..."
cd "${DIST_DIR}"
zip -r "NystaVision-macOS.zip" "${APP_NAME}.app"
echo "macOS build completed: dist/NystaVision-macOS.zip"
