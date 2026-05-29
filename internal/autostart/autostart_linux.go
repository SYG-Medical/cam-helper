//go:build linux

package autostart

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	desktopFile = "rtsp-virtual-cam-agent.desktop"
)

func IsEnabled() (bool, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return false, fmt.Errorf("resolve home dir: %w", err)
	}
	path := filepath.Join(home, ".config", "autostart", desktopFile)
	_, err = os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, fmt.Errorf("check desktop file: %w", err)
}

func SetEnabled(enabled bool) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home dir: %w", err)
	}
	autostartDir := filepath.Join(home, ".config", "autostart")
	path := filepath.Join(autostartDir, desktopFile)

	if !enabled {
		err := os.Remove(path)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove desktop file: %w", err)
		}
		return nil
	}

	if err := os.MkdirAll(autostartDir, 0755); err != nil {
		return fmt.Errorf("create autostart dir: %w", err)
	}

	// For AppImage, we want the AppImage path, not the temporary extracted executable
	exe := os.Getenv("APPIMAGE")
	if exe == "" {
		var err error
		exe, err = os.Executable()
		if err != nil {
			return fmt.Errorf("resolve executable: %w", err)
		}
		exe, err = filepath.Abs(exe)
		if err != nil {
			return fmt.Errorf("resolve absolute path: %w", err)
		}
	}

	content := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=SYG Camera Helper
Comment=SYG Camera Helper Background Agent
Exec="%s"
Icon=rtsp-virtual-cam-agent
Terminal=false
X-GNOME-Autostart-enabled=true
Categories=Video;Utility;
`, exe)

	return os.WriteFile(path, []byte(content), 0644)
}
