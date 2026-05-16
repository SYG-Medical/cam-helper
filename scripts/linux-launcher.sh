#!/usr/bin/env bash
set -euo pipefail

APP_NAME="RTSP Virtual Cam Agent"
APP_ID="rtsp-virtual-cam-agent"
CONFIG_DIR="${HOME}/.config/SYG/RTSPVirtualCamAgent"
CONFIG_FILE="${CONFIG_DIR}/config.json"
LOG_DIR="${CONFIG_DIR}/logs"
CACHE_DIR="${HOME}/.cache/SYG/RTSPVirtualCamAgent"
PID_FILE="${CACHE_DIR}/agent.pid"
AUTOSTART_DIR="${HOME}/.config/autostart"
AUTOSTART_FILE="${AUTOSTART_DIR}/rtsp-virtual-cam-agent.desktop"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
APPDIR_ROOT="${APPDIR:-$(cd "${SCRIPT_DIR}/.." && pwd)}"
AGENT_BIN="${APPDIR_ROOT}/usr/bin/rtsp-virtual-cam-agent"
SETUP_HELPER="${APPDIR_ROOT}/usr/lib/rtsp-virtual-cam-agent/setup-v4l2loopback.sh"
CONFIG_TEMPLATE="${APPDIR_ROOT}/usr/lib/rtsp-virtual-cam-agent/config.example.json"
APPIMAGE_PATH="${APPIMAGE:-${SCRIPT_DIR}/rtsp-virtual-cam-agent.AppImage}"

if [[ ! -x "${AGENT_BIN}" ]]; then
  AGENT_BIN="${APPDIR_ROOT}/app"
fi
if [[ ! -x "${SETUP_HELPER}" ]]; then
  SETUP_HELPER="${APPDIR_ROOT}/scripts/setup-v4l2loopback.sh"
fi
if [[ ! -f "${CONFIG_TEMPLATE}" ]]; then
  CONFIG_TEMPLATE="${APPDIR_ROOT}/config.example.json"
fi

mkdir -p "${CONFIG_DIR}" "${LOG_DIR}" "${CACHE_DIR}" "${AUTOSTART_DIR}"

notify() {
  local message="$1"
  if command -v zenity >/dev/null 2>&1; then
    zenity --info --title="${APP_NAME}" --text="${message}" >/dev/null 2>&1 || true
  else
    printf '%s\n' "${message}"
  fi
}

error_box() {
  local message="$1"
  if command -v zenity >/dev/null 2>&1; then
    zenity --error --title="${APP_NAME}" --text="${message}" >/dev/null 2>&1 || true
  else
    printf '%s\n' "${message}" >&2
  fi
}

is_running() {
  [[ -f "${PID_FILE}" ]] || return 1
  local pid
  pid="$(cat "${PID_FILE}")"
  [[ -n "${pid}" ]] || return 1
  kill -0 "${pid}" 2>/dev/null
}

ensure_config() {
  if [[ ! -f "${CONFIG_FILE}" ]]; then
    cp "${CONFIG_TEMPLATE}" "${CONFIG_FILE}" 2>/dev/null || true
  fi
  if [[ ! -f "${CONFIG_FILE}" ]]; then
    cat > "${CONFIG_FILE}" <<'EOF'
{
  "rtsp_url": "rtsp://172.28.6.234:5544/live0.264",
  "auto_start": true,
  "target_virtual_camera": "SYG RTSP Camera",
  "linux_video_device": "/dev/video10",
  "width": 1280,
  "height": 720,
  "fps": 30,
  "driver_mode": "bridge",
  "bridge_port": 18080,
  "driver_installer": "third_party\\driver\\virtual-camera-installer.exe",
  "driver_bridge": "third_party\\driver\\virtual-camera-bridge.exe",
  "ffmpeg_path": "ffmpeg",
  "log_level": "info"
}
EOF
  fi
}

load_config_field() {
  local field="$1"
  python3 - <<PY
import json
from pathlib import Path
cfg = json.loads(Path("${CONFIG_FILE}").read_text())
print(cfg.get("${field}", ""))
PY
}

save_config() {
  local rtsp_url="$1"
  local linux_video_device="$2"
  local width="$3"
  local height="$4"
  local fps="$5"
  local auto_start="$6"
  python3 - <<PY
import json
from pathlib import Path
path = Path("${CONFIG_FILE}")
cfg = json.loads(path.read_text())
cfg["rtsp_url"] = "${rtsp_url}"
cfg["linux_video_device"] = "${linux_video_device}"
cfg["width"] = int("${width}")
cfg["height"] = int("${height}")
cfg["fps"] = int("${fps}")
cfg["auto_start"] = "${auto_start}".lower() == "true"
cfg["ffmpeg_path"] = "ffmpeg"
path.write_text(json.dumps(cfg, indent=2) + "\\n")
PY
}

configure_dialog() {
  ensure_config
  local rtsp_url linux_video_device width height fps auto_start
  rtsp_url="$(load_config_field rtsp_url)"
  linux_video_device="$(load_config_field linux_video_device)"
  width="$(load_config_field width)"
  height="$(load_config_field height)"
  fps="$(load_config_field fps)"
  auto_start="$(load_config_field auto_start)"

  if command -v yad >/dev/null 2>&1; then
    local result
    result="$(yad --form --title="${APP_NAME}" \
      --field="RTSP URL" "${rtsp_url}" \
      --field="Linux Video Device" "${linux_video_device}" \
      --field="Width" "${width}" \
      --field="Height" "${height}" \
      --field="FPS" "${fps}" \
      --field="Autostart:CHK" "${auto_start}" \
      --button="Cancel:1" --button="Save:0")" || return 1
    IFS='|' read -r rtsp_url linux_video_device width height fps auto_start <<< "${result}"
  else
    rtsp_url="$(zenity --entry --title="${APP_NAME}" --text="RTSP URL" --entry-text="${rtsp_url}")" || return 1
    linux_video_device="$(zenity --entry --title="${APP_NAME}" --text="Linux Video Device" --entry-text="${linux_video_device}")" || return 1
    width="$(zenity --entry --title="${APP_NAME}" --text="Width" --entry-text="${width}")" || return 1
    height="$(zenity --entry --title="${APP_NAME}" --text="Height" --entry-text="${height}")" || return 1
    fps="$(zenity --entry --title="${APP_NAME}" --text="FPS" --entry-text="${fps}")" || return 1
    if zenity --question --title="${APP_NAME}" --text="Start automatically on login?"; then
      auto_start="TRUE"
    else
      auto_start="FALSE"
    fi
  fi

  [[ "${auto_start}" == "TRUE" || "${auto_start}" == "true" || "${auto_start}" == "1" ]] && auto_start=true || auto_start=false
  save_config "${rtsp_url}" "${linux_video_device}" "${width}" "${height}" "${fps}" "${auto_start}"
}

ensure_loopback() {
  ensure_config
  local device label
  device="$(load_config_field linux_video_device)"
  label="$(load_config_field target_virtual_camera)"
  if v4l2-ctl --list-devices 2>/dev/null | grep -qiE 'v4l2loopback|virtual camera'; then
    return 0
  fi

  if command -v pkexec >/dev/null 2>&1; then
    pkexec "${SETUP_HELPER}" "${device}" "${label}"
  else
    sudo "${SETUP_HELPER}" "${device}" "${label}"
  fi
}

start_agent() {
  ensure_config
  if [[ -z "$(load_config_field rtsp_url)" ]]; then
    configure_dialog || exit 1
  fi
  ensure_loopback
  if is_running; then
    notify "Agent already running."
    return 0
  fi
  nohup "${AGENT_BIN}" >/dev/null 2>&1 &
  echo "$!" > "${PID_FILE}"
  notify "Agent started."
}

stop_agent() {
  if is_running; then
    kill "$(cat "${PID_FILE}")" 2>/dev/null || true
    rm -f "${PID_FILE}"
    notify "Agent stopped."
  else
    notify "Agent is not running."
  fi
}

install_autostart() {
  cat > "${AUTOSTART_FILE}" <<EOF
[Desktop Entry]
Type=Application
Name=${APP_NAME}
Exec="${APPIMAGE_PATH}" --autostart-start
Terminal=false
X-GNOME-Autostart-enabled=true
EOF
  notify "Autostart enabled."
}

remove_autostart() {
  rm -f "${AUTOSTART_FILE}"
  notify "Autostart disabled."
}

open_logs() {
  xdg-open "${LOG_DIR}" >/dev/null 2>&1 || true
}

show_menu() {
  if ! command -v zenity >/dev/null 2>&1; then
    printf '%s\n' "1) Start" "2) Stop" "3) Configure" "4) Open Logs" "5) Enable Autostart" "6) Disable Autostart" "7) Exit"
    read -r -p "Choice: " choice
    case "${choice}" in
      1) start_agent ;;
      2) stop_agent ;;
      3) configure_dialog ;;
      4) open_logs ;;
      5) install_autostart ;;
      6) remove_autostart ;;
      *) exit 0 ;;
    esac
    return
  fi

  local choice
  choice="$(zenity --list --title="${APP_NAME}" --text="Choose an action" \
    --column="Action" \
    "Start" "Stop" "Configure" "Open Logs" "Enable Autostart" "Disable Autostart" "Exit")" || exit 0

  case "${choice}" in
    Start) start_agent ;;
    Stop) stop_agent ;;
    Configure) configure_dialog ;;
    "Open Logs") open_logs ;;
    "Enable Autostart") install_autostart ;;
    "Disable Autostart") remove_autostart ;;
    *) exit 0 ;;
  esac
}

case "${1:-}" in
  --autostart-start)
    start_agent
    ;;
  --start)
    start_agent
    ;;
  --stop)
    stop_agent
    ;;
  --configure)
    configure_dialog
    ;;
  *)
    show_menu
    ;;
esac
