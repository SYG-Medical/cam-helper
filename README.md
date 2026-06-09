# SYG Camera Helper (RTSP Virtual Cam Agent)

A cross-platform multi-camera helper and RTSP-to-webcam routing agent built with Go, Fyne UI, and FFmpeg. 

This tool allows you to monitor multiple camera inputs (RTSP streams and local webcams) in a configurable grid, save/load custom layouts, and record composite streams.

## Features

- **Multi-Camera Grid**: Configurable grid layout displaying live streams from both RTSP cameras and local physical webcams.
- **Layout Manager**: Save, load, and manage custom layout configurations.
- **Grid Recording**: Record the composite grid of all live streams into a single video file.
- **Automatic GPU Fallback**: Tests GPU/GLFW capabilities on startup and falls back to software rendering (`FYNE_RENDER=software` / `LIBGL_ALWAYS_SOFTWARE=1`) if hardware acceleration is unavailable.
- **Low-Latency Streaming**: Pre-tuned FFmpeg arguments (`-fflags nobuffer` and `-flags low_delay`) minimize latency for both RTSP streams and local webcam feeds.

## Default Configuration File Structure

The application settings are saved in `~/.config/SYG/NystaVision/config.json` (Linux) or `%APPDATA%\SYG\NystaVision\config.json` (Windows).

```json
{
  "auto_start": false,
  "target_virtual_camera": "SYG Camera",
  "linux_video_device": "/dev/video10",
  "driver_mode": "bridge",
  "bridge_port": 18080,
  "driver_installer": "third_party/driver/virtual-camera-installer.dll",
  "driver_bridge": "third_party/driver/virtual-camera-bridge.exe",
  "ffmpeg_path": "ffmpeg",
  "log_level": "info",
  "cameras": [
    {
      "id": "cam-1",
      "name": "Kamera 1",
      "type": "rtsp",
      "rtsp_url": "rtsp://192.168.1.100:554/live",
      "device": "",
      "width": 1280,
      "height": 720,
      "fps": 30,
      "enabled": true
    },
    {
      "id": "cam-2",
      "name": "Kamera 2",
      "type": "webcam",
      "rtsp_url": "",
      "device": "/dev/video0",
      "width": 1280,
      "height": 720,
      "fps": 30,
      "enabled": true
    }
  ],
  "saved_layouts": [],
  "active_layout_name": "",
  "rtsp_server_camera": "cam-1"
}
```

## Build & Package Prerequisites

- Go 1.22+
- FFmpeg
- **Linux**: `v4l2loopback` kernel module, X11 or Wayland display server session.
- **Windows Packaging**: NSIS (`makensis`), bundled `ffmpeg.exe`, virtual camera installer payloads.

## Build Targets

Compile and package targets via `Makefile`:

```bash
# Build binary on Linux
make build-linux

# Build binary for Windows (cross-compile or local)
make build-windows

# Build Windows binary inside a dev-backend Distrobox container
make build-distrobox-windows

# Build Linux AppImage
make package-linux

# Build Windows Installer (requires NSIS and assets)
make package-windows
```

## Linux AppImage Flow

1. **Build**:
   ```bash
   make package-linux
   ```
    This generates `dist/NystaVision-x86_64.AppImage`.

2. **Execution**:
   - Double-click the AppImage or run `./dist/NystaVision-x86_64.AppImage`.
   - The main Fyne UI opens showing the multi-camera monitor.
   - You can add cameras, switch selected camera streams, save layouts, start/stop streams, or record them.

3. **Nvidia GPU Acceleration on Hybrid Laptops/Bazzite**:
   If you use a hybrid graphics laptop (Nvidia + Intel/AMD) or an immutable OS like Bazzite, force rendering onto the dedicated Nvidia GPU by running:
   ```bash
   __NV_PRIME_RENDER_OFFLOAD=1 __GLX_VENDOR_LIBRARY_NAME=nvidia ./dist/NystaVision-x86_64.AppImage
   ```

## Troubleshooting & Logs

Logs are written to:
- **Linux**: `~/.config/SYG/NystaVision/logs/agent.log`
- **Windows**: `%APPDATA%\SYG\NystaVision\logs\agent.log`
