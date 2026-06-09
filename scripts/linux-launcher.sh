#!/usr/bin/env bash
set -euo pipefail

APP_NAME="NystaVision"
CONFIG_DIR="${HOME}/.config/SYG/NystaVision"
CONFIG_FILE="${CONFIG_DIR}/config.json"
CACHE_DIR="${HOME}/.cache/SYG/NystaVision"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
APPDIR_ROOT="${APPDIR:-$(cd "${SCRIPT_DIR}/.." && pwd)}"
AGENT_BIN="${APPDIR_ROOT}/usr/bin/nystavision"
CONFIG_TEMPLATE="${APPDIR_ROOT}/usr/lib/nystavision/config.example.json"

if [[ ! -x "${AGENT_BIN}" ]]; then
  AGENT_BIN="${APPDIR_ROOT}/nystavision"
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

ensure_config
exec "${AGENT_BIN}" "$@"
