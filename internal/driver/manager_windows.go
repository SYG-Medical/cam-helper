//go:build windows

package driver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"rtsp-virtual-cam-agent/internal/config"
	"rtsp-virtual-cam-agent/internal/logging"
)

type Manager struct {
	cfg    config.Config
	root   string
	logger *logging.Logger
}

func New(cfg config.Config, logger *logging.Logger) (*Manager, error) {
	root, err := executableRoot()
	if err != nil {
		return nil, err
	}
	return &Manager{cfg: cfg, root: root, logger: logger}, nil
}

func (m *Manager) EnsureInstalled(ctx context.Context) error {
	if ok, _ := m.VirtualCameraPresent(ctx); ok {
		return nil
	}

	// Try multiple locations for the UnityCapture DLL
	candidates := []string{
		"third_party/driver/virtual-camera-installer.dll",
		"internal/assets/third_party/driver/virtual-camera-installer.dll",
	}

	for _, candidate := range candidates {
		dllPath := m.resolve(candidate)
		if _, err := os.Stat(dllPath); err == nil {
			m.logger.Printf("registering virtual camera filter: %s", dllPath)
			// regsvr32 /s requires admin, which the app should have if it got this far or via the installer
			cmd := exec.CommandContext(ctx, "regsvr32", "/s", dllPath)
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("register filter (%s): %w", dllPath, err)
			}
			return nil
		}
	}

	// Fallback to legacy installer if exists
	installer := m.resolve(m.cfg.DriverInstaller)
	if _, err := os.Stat(installer); err == nil {
		m.logger.Printf("running legacy driver installer: %s", installer)
		cmd := exec.CommandContext(ctx, installer, "/S")
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		return cmd.Run()
	}

	return errors.New("virtual camera driver not found and no installer available. Please run the setup installer as Administrator.")
}

func (m *Manager) VirtualCameraPresent(ctx context.Context) (bool, error) {
	if m.cfg.TargetVirtualCamera == "" {
		return false, errors.New("target virtual camera is empty")
	}

	script := fmt.Sprintf(`Get-PnpDevice -Class Camera,Image | Where-Object { $_.FriendlyName -like '*%s*' } | Select-Object -ExpandProperty FriendlyName`, escapePowerShellLike(m.cfg.TargetVirtualCamera))
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("query cameras: %w", err)
	}

	return strings.TrimSpace(string(out)) != "", nil
}

func (m *Manager) UpdateConfig(cfg config.Config) {
	m.cfg = cfg
}

func (m *Manager) BridgePath() string {
	return m.resolve(m.cfg.DriverBridge)
}

func (m *Manager) StartBridge(ctx context.Context) (*exec.Cmd, error) {
	// If we find a way to avoid the bridge, we return nil here.
	// For now, we still try to resolve it but allow it to be missing.
	bridge := m.BridgePath()
	if _, err := os.Stat(bridge); err != nil {
		m.logger.Printf("virtual camera bridge not found, attempting direct mode")
		return nil, nil 
	}

	args := []string{
		"--camera-name", m.cfg.TargetVirtualCamera,
		"--listen", fmt.Sprintf("udp://127.0.0.1:%d", m.cfg.BridgePort),
		"--width", fmt.Sprintf("%d", m.cfg.Width),
		"--height", fmt.Sprintf("%d", m.cfg.Height),
		"--fps", fmt.Sprintf("%d", m.cfg.FPS),
	}

	cmd := exec.CommandContext(ctx, bridge, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd, nil
}

func (m *Manager) UseBridge() bool {
	// If bridge exists, use it. Otherwise, try direct dshow output.
	_, err := os.Stat(m.BridgePath())
	return err == nil
}

func (m *Manager) IsDeviceBusy() (string, bool, error) {
	return "", false, nil
}

func (m *Manager) FFmpegOutputTarget() string {
	if m.UseBridge() {
		return fmt.Sprintf("udp://127.0.0.1:%d?pkt_size=1316", m.cfg.BridgePort)
	}
	// Fallback to DirectShow output if bridge is missing
	// Note: This requires a DirectShow sink filter named exactly like TargetVirtualCamera
	return fmt.Sprintf("video=%s", m.cfg.TargetVirtualCamera)
}

func (m *Manager) resolve(candidate string) string {
	if filepath.IsAbs(candidate) {
		return candidate
	}
	return filepath.Join(m.root, filepath.FromSlash(candidate))
}

func executableRoot() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable: %w", err)
	}
	return filepath.Dir(exe), nil
}

func escapePowerShellLike(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}
