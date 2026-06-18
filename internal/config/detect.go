//go:build !windows

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

// DetectWebcams scans /sys/class/video4linux/ for real camera devices
// (filtering out v4l2loopback virtual cameras).
func DetectWebcams() []CameraSource {
	matches, err := filepath.Glob("/sys/class/video4linux/video*")
	if err != nil {
		return nil
	}

	var cameras []CameraSource
	idx := 0

	for _, sysPath := range matches {
		base := filepath.Base(sysPath) // "video0", "video2", ...
		devPath := filepath.Join("/dev", base)

		// Check the device actually exists
		if _, err := os.Stat(devPath); err != nil {
			continue
		}

		// Read the device name
		nameBytes, err := os.ReadFile(filepath.Join(sysPath, "name"))
		if err != nil {
			continue
		}
		name := strings.TrimSpace(string(nameBytes))

		// Skip v4l2loopback devices (virtual cameras)
		if isV4L2Loopback(sysPath, name) {
			continue
		}

		// Only include capture-capable devices (not output-only devices)
		// v4l2 devices with "capture" in device_caps or lacking "output" are capture devices
		if !isCaptureDev(sysPath) {
			continue
		}

		idx++
		cameras = append(cameras, CameraSource{
			ID:      fmt.Sprintf("webcam-%s", base),
			Name:    name,
			Type:    "webcam",
			Device:  devPath,
			Width:   1280,
			Height:  720,
			FPS:     30,
			Enabled: true,
		})
	}

	return cameras
}

// isV4L2Loopback checks if the device is a v4l2loopback virtual camera.
func isV4L2Loopback(sysPath, name string) bool {
	lowerName := strings.ToLower(name)

	// v4l2loopback devices often have names like "Dummy video device" or custom names
	// The reliable way is to check the driver via the device symlink
	driverLink := filepath.Join(sysPath, "device", "driver")
	if target, err := os.Readlink(driverLink); err == nil {
		driverName := filepath.Base(target)
		if strings.Contains(strings.ToLower(driverName), "v4l2loopback") {
			return true
		}
	}

	// Fallback: check if v4l2loopback module claims this device
	if data, err := os.ReadFile("/sys/module/v4l2loopback/parameters/card_label"); err == nil {
		labels := strings.Split(string(data), "\n")
		for _, label := range labels {
			if strings.TrimSpace(label) == name {
				return true
			}
		}
	}

	// Heuristic: common v4l2loopback names
	if strings.Contains(lowerName, "loopback") ||
		strings.Contains(lowerName, "dummy") ||
		strings.Contains(lowerName, "syg") ||
		strings.Contains(lowerName, "obs") ||
		strings.Contains(lowerName, "virtual") {
		return true
	}

	return false
}

// isCaptureDev checks if a video device supports capture (not just output).
func isCaptureDev(sysPath string) bool {
	const (
		vidiocQuerycap        = 0x80685600
		capVideoCapture       = 0x00000001
		capVideoCaptureMPlane = 0x00001000
	)

	type v4l2Capability struct {
		Driver       [16]byte
		Card         [32]byte
		BusInfo      [32]byte
		Version      uint32
		Capabilities uint32
		DeviceCaps   uint32
		Reserved     [3]uint32
	}

	// Check device_caps for V4L2_CAP_VIDEO_CAPTURE (0x00000001)
	// or V4L2_CAP_VIDEO_CAPTURE_MPLANE (0x00001000)
	capsPath := filepath.Join(sysPath, "device_caps")
	if data, err := os.ReadFile(capsPath); err == nil {
		capsStr := strings.TrimSpace(string(data))
		var caps uint64
		if _, err := fmt.Sscanf(capsStr, "0x%x", &caps); err == nil {
			return caps&capVideoCapture != 0 || caps&capVideoCaptureMPlane != 0
		}
	}

	// Fallback: try to open the device and check capability flags using ioctl
	base := filepath.Base(sysPath)
	devPath := filepath.Join("/dev", base)
	fd, err := syscall.Open(devPath, syscall.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return false
	}
	defer syscall.Close(fd)

	var cap v4l2Capability
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), uintptr(vidiocQuerycap), uintptr(unsafe.Pointer(&cap)))
	if errno != 0 {
		return false
	}

	caps := cap.Capabilities
	if cap.Capabilities&0x80000000 != 0 { // V4L2_CAP_DEVICE_CAPS
		caps = cap.DeviceCaps
	}

	return caps&(capVideoCapture|capVideoCaptureMPlane) != 0
}
