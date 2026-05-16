# Driver Integration Contract

The app assumes a bundled third-party virtual camera payload with two pieces:

- `virtual-camera-installer.exe`: silently installs the device driver
- `virtual-camera-bridge.exe`: receives a local media feed and forwards it to the installed virtual camera

## Expected bridge CLI

```powershell
virtual-camera-bridge.exe \
  --camera-name "SYG RTSP Camera" \
  --listen "udp://127.0.0.1:18080" \
  --width 1280 \
  --height 720 \
  --fps 30
```

## Responsibilities

- The bridge must block while serving frames.
- Exiting non-zero indicates failure and triggers restart logic.
- The installer should support silent install switches.
- The installed camera should be visible to Chrome/Edge `getUserMedia`.

## If your vendor differs

Adjust these parts:

- `internal/driver/manager_windows.go`: install checks and bridge launch
- `internal/stream/manager.go`: FFmpeg output arguments and local transport
- `build/installer/installer.nsi`: bundled filenames and silent install flags
