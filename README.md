# RTSP Virtual Cam Agent

Cross-platform RTSP-to-webcam agent for SYG Medical with:

- Linux AppImage launcher flow
- Windows installer flow
- shared Go streaming core

## What this repo includes

- Go streaming core with config, autostart, health supervision, and rotating logs
- FFmpeg process management for RTSP ingest and local relay
- Windows packaging assets for installer-based deployment
- Windows driver integration surface for bundling a third-party virtual camera package
- Linux `v4l2loopback` mode for direct RTSP-to-virtual-webcam output
- Linux AppImage launcher scripts for configure/start/stop/autostart
- unified `Makefile` targets

## What this repo does not include yet

- A proprietary or third-party Windows virtual camera driver binary
- A signed Windows installer artifact
- Bundled `ffmpeg.exe` for Windows packaging

Windows packaging binaries must be placed into `third_party/driver/` and `third_party/ffmpeg/` before packaging.

## Runtime model

1. The tray app loads config from the platform config directory.
2. On Windows, it verifies that the bundled virtual camera bridge/driver payload exists.
3. On Linux, it expects a ready `v4l2loopback` device such as `/dev/video10`.
4. It launches FFmpeg against the configured RTSP URL.
5. Windows relays FFmpeg output to the local bridge endpoint that feeds the virtual camera driver.
6. Linux writes FFmpeg output directly to the configured `v4l2loopback` device.
7. The tray app restarts components automatically if they exit unexpectedly.

## Default config keys

```json
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
  "driver_installer": "third_party/driver/virtual-camera-installer.exe",
  "driver_bridge": "third_party/driver/virtual-camera-bridge.exe"
}
```

## Build prerequisites

- Go 1.22+
- FFmpeg
- Linux: `v4l2loopback` and a desktop session that supports `systray`
- Windows packaging: NSIS (`makensis`), bundled `ffmpeg.exe`, bundled virtual camera installer, bundled bridge executable

## Linux end-user flow

Build:

```bash
make package-linux
```

Result:

- `dist/RTSPVirtualCamAgent-x86_64.AppImage`

Runtime:

1. Double-click the AppImage.
2. Choose `Configure` and enter the RTSP URL.
3. Choose `Start`.
4. If `v4l2loopback` is not active yet, the launcher asks for admin rights and sets it up automatically.
5. Browser apps should then see the created virtual webcam.

Notes:

- The Linux launcher prefers `yad`, then `zenity`.
- The launcher installs an autostart desktop entry instead of asking users to edit config manually.
- Kernel-module setup still requires an admin prompt once; this cannot be removed entirely because `v4l2loopback` is a kernel module.

## Build targets

```bash
make build-linux
make build-windows
make build-distrobox-windows
make package-linux
make package-windows
```

## Suggested packaging flow

1. Put `ffmpeg.exe` in `third_party/ffmpeg/`.
2. Put the driver installer and bridge executable in `third_party/driver/`.
3. Run `scripts/build-windows.ps1`.
4. Test the generated installer on a clean Windows machine.

## Notes on the virtual camera driver

This repo intentionally keeps the driver integration generic. The Go app expects a third-party package that can:

- install a webcam-visible virtual camera device silently
- expose a helper executable or bridge that accepts a local UDP/MPEG-TS feed
- route that feed into the installed virtual camera

If your chosen driver uses a different ingestion model, update `internal/driver` and `internal/stream` together.
