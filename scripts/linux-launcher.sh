#!/usr/bin/env bash
set -euo pipefail

APP_NAME="RTSP Virtual Cam Agent"
CONFIG_DIR="${HOME}/.config/SYG/RTSPVirtualCamAgent"
CONFIG_FILE="${CONFIG_DIR}/config.json"
CACHE_DIR="${HOME}/.cache/SYG/RTSPVirtualCamAgent"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
APPDIR_ROOT="${APPDIR:-$(cd "${SCRIPT_DIR}/.." && pwd)}"
AGENT_BIN="${APPDIR_ROOT}/usr/bin/rtsp-virtual-cam-agent"
SETUP_HELPER="${APPDIR_ROOT}/usr/lib/rtsp-virtual-cam-agent/setup-v4l2loopback.sh"
CONFIG_TEMPLATE="${APPDIR_ROOT}/usr/lib/rtsp-virtual-cam-agent/config.example.json"

if [[ ! -x "${AGENT_BIN}" ]]; then
  AGENT_BIN="${APPDIR_ROOT}/app"
fi
if [[ ! -x "${SETUP_HELPER}" ]]; then
  SETUP_HELPER="${APPDIR_ROOT}/scripts/setup-v4l2loopback.sh"
fi
if [[ ! -f "${CONFIG_TEMPLATE}" ]]; then
  CONFIG_TEMPLATE="${APPDIR_ROOT}/config.example.json"
fi

mkdir -p "${CONFIG_DIR}" "${CACHE_DIR}"

ensure_config() {
  if [[ ! -f "${CONFIG_FILE}" ]]; then
    cp "${CONFIG_TEMPLATE}" "${CONFIG_FILE}" 2>/dev/null || true
  fi
}

load_config_field() {
  local field="$1"
  python3 - <<PY
import json
from pathlib import Path
try:
    cfg = json.loads(Path("${CONFIG_FILE}").read_text())
    print(cfg.get("${field}", ""))
except:
    print("")
PY
}

ensure_loopback() {
  ensure_config
  local device label
  device="$(load_config_field linux_video_device)"
  label="$(load_config_field target_virtual_camera)"
  
  if [[ -z "${device}" ]]; then device="/dev/video10"; fi
  if [[ -z "${label}" ]]; then label="SYG RTSP Camera"; fi

  if [[ -c "${device}" ]] && v4l2-ctl --device="${device}" --all 2>/dev/null | grep -qiE 'v4l2loopback|virtual camera'; then
    return 0
  fi

  local temp_setup="/tmp/syg-setup-v4l2loopback.sh"
  cp "${SETUP_HELPER}" "${temp_setup}"
  chmod +x "${temp_setup}"

  if command -v pkexec >/dev/null 2>&1; then
    pkexec "${temp_setup}" "${device}" "${label}"
  else
    sudo "${temp_setup}" "${device}" "${label}"
  fi
  rm -f "${temp_setup}"
  sleep 1
}

ensure_loopback
exec "${AGENT_BIN}" "$@"
