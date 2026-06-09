#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APPDIR="${REPO_ROOT}/AppDir"
OUT_DIR="${REPO_ROOT}/dist"
APP_NAME="nystavision"
APPIMAGE_TOOL="${REPO_ROOT}/.cache/tools/appimagetool.AppImage"

rm -rf "${APPDIR}"
mkdir -p "${APPDIR}/usr/bin" "${APPDIR}/usr/lib/${APP_NAME}" "${APPDIR}/usr/share/applications" "${APPDIR}/usr/share/icons/hicolor/scalable/apps" "${OUT_DIR}" "${REPO_ROOT}/.cache/tools"

pushd "${REPO_ROOT}" >/dev/null
go build -ldflags="-X 'nystavision/internal/version.Version=${VERSION:-dev}'" -o "${APPDIR}/usr/bin/${APP_NAME}" ./cmd/app
popd >/dev/null

cp "${REPO_ROOT}/scripts/linux-launcher.sh" "${APPDIR}/usr/bin/${APP_NAME}-launcher"
cp "${REPO_ROOT}/config.example.json" "${APPDIR}/usr/lib/${APP_NAME}/config.example.json"
cp "${REPO_ROOT}/build/linux/nystavision.desktop" "${APPDIR}/usr/share/applications/"
cp "${REPO_ROOT}/build/linux/nystavision.desktop" "${APPDIR}/"
cp "${REPO_ROOT}/build/linux/nystavision.svg" "${APPDIR}/usr/share/icons/hicolor/scalable/apps/"
cp "${REPO_ROOT}/build/linux/nystavision.svg" "${APPDIR}/"
cp "${REPO_ROOT}/build/linux/AppRun" "${APPDIR}/AppRun"

chmod +x "${APPDIR}/AppRun" "${APPDIR}/usr/bin/${APP_NAME}" "${APPDIR}/usr/bin/${APP_NAME}-launcher"

if [[ ! -x "${APPIMAGE_TOOL}" ]]; then
  if command -v curl >/dev/null 2>&1; then
    curl -L "https://github.com/AppImage/AppImageKit/releases/download/continuous/appimagetool-x86_64.AppImage" -o "${APPIMAGE_TOOL}"
    chmod +x "${APPIMAGE_TOOL}"
  else
    echo "appimagetool missing and curl not available" >&2
    exit 1
  fi
fi

rm -f "${OUT_DIR}/NystaVision-x86_64.AppImage"
if APPIMAGE_EXTRACT_AND_RUN=1 "${APPIMAGE_TOOL}" --appimage-version >/dev/null 2>&1; then
  APPIMAGE_EXTRACT_AND_RUN=1 ARCH=x86_64 "${APPIMAGE_TOOL}" "${APPDIR}" "${OUT_DIR}/NystaVision-x86_64.AppImage"
else
  EXTRACT_DIR="${REPO_ROOT}/.cache/tools/appimagetool-extracted"
  rm -rf "${EXTRACT_DIR}"
  pushd "${REPO_ROOT}/.cache/tools" >/dev/null
  APPIMAGE_EXTRACT_AND_RUN=1 "${APPIMAGE_TOOL}" --appimage-extract >/dev/null
  popd >/dev/null
  mv "${REPO_ROOT}/.cache/tools/squashfs-root" "${EXTRACT_DIR}"
  ARCH=x86_64 "${EXTRACT_DIR}/AppRun" "${APPDIR}" "${OUT_DIR}/NystaVision-x86_64.AppImage"
fi
