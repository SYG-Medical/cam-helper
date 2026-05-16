//go:build !windows

package driver

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"rtsp-virtual-cam-agent/internal/config"
	"rtsp-virtual-cam-agent/internal/logging"
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
	if _, err := os.Stat(m.effectiveLinuxDevice()); err != nil {
		return fmt.Errorf("linux virtual camera device not found: %s", m.effectiveLinuxDevice())
	}
	return nil
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
		if _, err := os.Stat(m.cfg.LinuxVideoDevice); err == nil {
			return m.cfg.LinuxVideoDevice
		}
	}

	discovered := discoverLoopbackDevice()
	if discovered != "" {
		if m.logger != nil && discovered != m.cfg.LinuxVideoDevice {
			m.logger.Printf("configured linux video device unavailable, using detected loopback device: %s", discovered)
		}
		return discovered
	}

	return m.cfg.LinuxVideoDevice
}

func discoverLoopbackDevice() string {
	cmd := exec.Command("v4l2-ctl", "--list-devices")
	out, err := cmd.Output()
	if err == nil {
		lines := strings.Split(string(out), "\n")
		inLoopbackBlock := false
		for _, raw := range lines {
			line := strings.TrimSpace(raw)
			if line == "" {
				inLoopbackBlock = false
				continue
			}
			if !strings.HasPrefix(raw, "\t") && !strings.HasPrefix(raw, " ") {
				lower := strings.ToLower(line)
				inLoopbackBlock = strings.Contains(lower, "v4l2loopback") || strings.Contains(lower, "virtual camera")
				continue
			}
			if inLoopbackBlock && strings.HasPrefix(line, "/dev/video") {
				return line
			}
		}
	}

	matches, err := filepath.Glob("/dev/video*")
	if err == nil {
		for _, match := range matches {
			base := filepath.Base(match)
			namePath := filepath.Join("/sys/class/video4linux", base, "name")
			nameBytes, readErr := os.ReadFile(namePath)
			if readErr != nil {
				continue
			}
			name := strings.ToLower(strings.TrimSpace(string(nameBytes)))
			if strings.Contains(name, "loopback") || strings.Contains(name, "virtual camera") {
				return match
			}
		}
	}

	return ""
}
