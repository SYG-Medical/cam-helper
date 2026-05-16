#!/usr/bin/env bash
set -euo pipefail

DEVICE_PATH="${1:-/dev/video10}"
LABEL="${2:-SYG RTSP Camera}"
VIDEO_NR="${DEVICE_PATH#/dev/video}"

modprobe v4l2loopback devices=1 video_nr="${VIDEO_NR}" card_label="${LABEL}" exclusive_caps=1

cat > /etc/modules-load.d/rtsp-virtual-cam-agent.conf <<EOF
v4l2loopback
EOF

cat > /etc/modprobe.d/rtsp-virtual-cam-agent.conf <<EOF
options v4l2loopback devices=1 video_nr=${VIDEO_NR} card_label="${LABEL}" exclusive_caps=1
EOF
