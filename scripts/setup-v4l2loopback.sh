#!/usr/bin/env bash
set -euo pipefail

# This script is called automatically by the launcher via pkexec.
# It forces the creation of a Chrome-compatible virtual camera device.

DEVICE_PATH="${1:-/dev/video5}"
LABEL="${2:-SYG RTSP Camera}"

echo "Configuring virtual camera ${DEVICE_PATH} with label ${LABEL}..."

# 1. Kill any process holding ANY loopback device to prevent "busy" errors
# This is aggressive but necessary to fix the "ffplay is playing but Chrome doesn't see" issue.
for dev in /dev/video*; do
  [[ -c "${dev}" ]] || continue
  if v4l2-ctl --device="${dev}" --all 2>/dev/null | grep -qi "v4l2loopback"; then
    echo "Closing processes using ${dev}..."
    fuser -k "${dev}" 2>/dev/null || true
  fi
done
sleep 1

# 2. Dynamic creation using v4l2loopback-ctl
if command -v v4l2loopback-ctl >/dev/null 2>&1; then
  # Delete if exists
  v4l2loopback-ctl delete "${DEVICE_PATH}" 2>/dev/null || true
  sleep 0.5
  
  # Create fresh with EXCLUSIVE CAPS (This is the fix for Chrome!)
  # -x 1 tells the driver to hide the "Output" capability from readers like Chrome.
  v4l2loopback-ctl add -x 1 -n "${LABEL}" "${DEVICE_PATH}"
  echo "Device created successfully with exclusive_caps=1"
else
  # Fallback to module reload if ctl is missing
  modprobe -r v4l2loopback || true
  modprobe v4l2loopback devices=1 video_nr="${DEVICE_PATH#/dev/video}" card_label="${LABEL}" exclusive_caps=1
  echo "Module reloaded with exclusive_caps=1"
fi

# 3. Persist for reboots
mkdir -p /etc/modprobe.d /etc/modules-load.d
echo "v4l2loopback" > /etc/modules-load.d/rtsp-virtual-cam-agent.conf
cat > /etc/modprobe.d/rtsp-virtual-cam-agent.conf <<EOF
options v4l2loopback devices=4 video_nr=0,1,2,5 card_label="OBS Virtual Camera,Virtual Camera 1,Virtual Camera 2,${LABEL}" exclusive_caps=1,1,1,1
EOF
