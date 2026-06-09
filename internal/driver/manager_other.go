//go:build !windows

package driver

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"nystavision/internal/config"
	"nystavision/internal/logging"
)

type Manager struct {
	cfg    config.Config
	logger *logging.Logger
}

func New(cfg config.Config, logger *logging.Logger) (*Manager, error) {
	return &Manager{cfg: cfg, logger: logger}, nil
}

func (m *Manager) EnsureInstalled(ctx context.Context) error {
	_ = ctx
	if runtime.GOOS != "linux" {
		return fmt.Errorf("driver integration is only implemented for windows and linux")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return fmt.Errorf("ffmpeg is not installed or not in PATH")
	}
	device := m.effectiveLinuxDevice()
	if _, err := os.Stat(device); err != nil {
		return fmt.Errorf("linux virtual camera device not found: %s", device)
	}
	// Check exclusive_caps — Chrome requires it.
	// If not set, log a warning but don't fail; user-facing setup handles this.
	if !m.deviceHasExclusiveCaps(device) {
		if m.logger != nil {
			m.logger.Printf("warning: %s does not have exclusive_caps=1; Chrome/Firefox may not see this camera", device)
		}
	}
	return nil
}

// IsDeviceBusy returns true only if another NON-ffmpeg process is writing to the device.
// Stale ffmpeg processes are killed automatically by evictStaleWriter.
func (m *Manager) IsDeviceBusy() (string, bool, error) {
	device := m.FFmpegOutputTarget()
	if device == "" {
		return "", false, nil
	}

	// Kill any leftover ffmpeg writing to our device before declaring it busy.
	m.evictStaleWriter(device)

	// Now try to open for writing. If still EBUSY, something else owns it.
	f, err := os.OpenFile(device, os.O_WRONLY, 0)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "device or resource busy") {
			user := m.findWriter(device)
			if user == "" {
				user = "another process"
			}
			return user, true, nil
		}
		// Device not found, permission error etc — not "busy"
		return "", false, err
	}
	f.Close()
	return "", false, nil
}

// evictStaleWriter kills any ffmpeg process that has our device open for writing.
// This handles the case where a previous instance of the agent crashed/was killed
// without properly stopping ffmpeg.
func (m *Manager) evictStaleWriter(devicePath string) {
	var targetStat syscall.Stat_t
	if err := syscall.Stat(devicePath, &targetStat); err != nil {
		return
	}

	procDirs, err := filepath.Glob("/proc/[0-9]*")
	if err != nil {
		return
	}

	selfPid := fmt.Sprintf("%d", os.Getpid())

	for _, procDir := range procDirs {
		pid := filepath.Base(procDir)
		if pid == selfPid {
			continue
		}

		// Only target ffmpeg processes
		comm, err := os.ReadFile(procDir + "/comm")
		if err != nil {
			continue
		}
		if !strings.Contains(strings.ToLower(string(comm)), "ffmpeg") {
			continue
		}

		fds, err := filepath.Glob(procDir + "/fd/*")
		if err != nil {
			continue
		}

		for _, fd := range fds {
			var fdStat syscall.Stat_t
			if err := syscall.Stat(fd, &fdStat); err != nil {
				continue
			}
			if fdStat.Rdev != targetStat.Rdev {
				continue
			}
			// This ffmpeg has our device open — kill it gracefully, then hard.
			if m.logger != nil {
				m.logger.Printf("evicting stale ffmpeg process PID %s from %s", pid, devicePath)
			}
			pidInt, _ := strconv.Atoi(pid)
			syscall.Kill(pidInt, syscall.SIGTERM)
			// Give it 500ms to die
			for i := 0; i < 5; i++ {
				time.Sleep(100 * time.Millisecond)
				if _, err := os.Stat(procDir); err != nil {
					break
				}
			}
			syscall.Kill(pidInt, syscall.SIGKILL)
			break
		}
	}
}

// findWriter finds only processes that have the device open for WRITING (O_WRONLY or O_RDWR).
func (m *Manager) findWriter(devicePath string) string {
	var targetStat syscall.Stat_t
	if err := syscall.Stat(devicePath, &targetStat); err != nil {
		return "unknown process"
	}

	// Read /proc/<pid>/fdinfo to check open flags instead of just presence.
	procDirs, err := filepath.Glob("/proc/[0-9]*")
	if err != nil {
		return "unknown process"
	}

	selfPid := fmt.Sprintf("%d", os.Getpid())

	for _, procDir := range procDirs {
		pid := filepath.Base(procDir)
		if pid == selfPid {
			continue
		}

		fds, err := filepath.Glob(procDir + "/fd/*")
		if err != nil {
			continue
		}

		for _, fd := range fds {
			var fdStat syscall.Stat_t
			if err := syscall.Stat(fd, &fdStat); err != nil {
				continue
			}
			if fdStat.Rdev != targetStat.Rdev {
				continue
			}

			// Check if this fd is open for writing via fdinfo
			fdNum := filepath.Base(fd)
			fdinfoPath := fmt.Sprintf("%s/fdinfo/%s", procDir, fdNum)
			fdinfoData, err := os.ReadFile(fdinfoPath)
			if err != nil {
				continue
			}

			// flags field: O_WRONLY=1, O_RDWR=2, O_RDONLY=0
			// We only care about writers
			isWriter := false
			for _, line := range strings.Split(string(fdinfoData), "\n") {
				if strings.HasPrefix(line, "flags:") {
					var flags uint64
					_, _ = fmt.Sscanf(strings.TrimSpace(strings.TrimPrefix(line, "flags:")), "%o", &flags)
					// O_WRONLY = 01, O_RDWR = 02
					if flags&0x3 != 0 {
						isWriter = true
					}
					break
				}
			}

			if isWriter {
				comm, _ := os.ReadFile(fmt.Sprintf("%s/comm", procDir))
				if len(comm) > 0 {
					return fmt.Sprintf("%s (PID: %s)", strings.TrimSpace(string(comm)), pid)
				}
				return fmt.Sprintf("PID: %s", pid)
			}
		}
	}
	return ""
}

// deviceHasExclusiveCaps checks the kernel parameter for the given video device.
func (m *Manager) deviceHasExclusiveCaps(devicePath string) bool {
	// Find the index of this video device
	base := filepath.Base(devicePath) // e.g. "video10"
	capPath := fmt.Sprintf("/sys/class/video4linux/%s/dev_sysfs_exclusive_caps", base)
	if data, err := os.ReadFile(capPath); err == nil {
		return strings.TrimSpace(string(data)) == "1"
	}
	// Alternative: check via the module parameter array
	capsData, err := os.ReadFile("/sys/module/v4l2loopback/parameters/exclusive_caps")
	if err != nil {
		return false
	}
	// Get the device number to find its index
	var devStat syscall.Stat_t
	if err := syscall.Stat(devicePath, &devStat); err != nil {
		return false
	}
	minor := devStat.Rdev & 0xff

	// List all v4l2 devices to find the index of this one
	devs, err := filepath.Glob("/dev/video*")
	if err != nil {
		return false
	}
	idx := -1
	for i, dev := range devs {
		var s syscall.Stat_t
		if err := syscall.Stat(dev, &s); err != nil {
			continue
		}
		if s.Rdev&0xff == minor {
			idx = i
			break
		}
	}

	if idx < 0 {
		return false
	}

	caps := strings.Split(strings.TrimSpace(string(capsData)), ",")
	if idx < len(caps) {
		return strings.TrimSpace(caps[idx]) == "Y"
	}
	return false
}

func (m *Manager) VirtualCameraPresent(ctx context.Context) (bool, error) {
	_ = ctx
	if runtime.GOOS != "linux" {
		return false, nil
	}
	if _, err := os.Stat(m.effectiveLinuxDevice()); err != nil {
		return false, nil
	}
	return true, nil
}

func (m *Manager) UpdateConfig(cfg config.Config) {
	m.cfg = cfg
}

func (m *Manager) BridgePath() string {
	return ""
}

func (m *Manager) StartBridge(ctx context.Context) (*exec.Cmd, error) {
	_ = ctx
	return nil, nil
}

func (m *Manager) UseBridge() bool {
	return false
}

func (m *Manager) FFmpegOutputTarget() string {
	return m.effectiveLinuxDevice()
}

func (m *Manager) effectiveLinuxDevice() string {
	if m.cfg.LinuxVideoDevice != "" {
		return m.cfg.LinuxVideoDevice
	}
	// Only auto-discover if no explicit device configured.
	discovered := discoverOurDevice()
	if discovered != "" {
		return discovered
	}
	return "/dev/video10"
}

// discoverOurDevice finds a v4l2loopback device named after the SYG camera.
func discoverOurDevice() string {
	matches, err := filepath.Glob("/dev/video*")
	if err != nil {
		return ""
	}
	for _, match := range matches {
		base := filepath.Base(match)
		namePath := filepath.Join("/sys/class/video4linux", base, "name")
		nameBytes, err := os.ReadFile(namePath)
		if err != nil {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(string(nameBytes)))
		if strings.Contains(name, "syg") || strings.Contains(name, "rtsp") {
			return match
		}
	}
	return ""
}

func (m *Manager) OpenWriter() error {
	return nil
}

func (m *Manager) CloseWriter() {
}

func (m *Manager) WriteFrame(width, height int, pix []byte) error {
	return nil
}
