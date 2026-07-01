package system

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/godbus/dbus/v5"
)

// Restart restarts the application executable process.
func Restart() {
	var exe string
	var err error

	if appImage := os.Getenv("APPIMAGE"); appImage != "" {
		exe = appImage
	} else {
		exe, err = os.Executable()
		if err != nil {
			fmt.Printf("Failed to get executable for restart: %v\n", err)
			os.Exit(0)
		}
	}

	cmd := exec.Command(exe)
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		fmt.Printf("Failed to restart app: %v\n", err)
	}
	os.Exit(0)
}

// OpenPath opens the target path using the operating system's default protocol handler.
func OpenPath(target string) {
	cleaned := filepath.Clean(target)
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", cleaned)
	case "linux":
		cmd = exec.Command("xdg-open", cleaned)
	case "darwin":
		cmd = exec.Command("open", cleaned)
	default:
		return
	}
	_ = cmd.Start()
}

// InstallLinuxIcon writes the provided icon bytes into standard Linux icons paths.
func InstallLinuxIcon(iconData []byte) error {
	if runtime.GOOS != "linux" {
		return nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	// Always ensure standard "nystavision" name is installed
	names := []string{"nystavision"}

	// Also install using current executable base name (stripped of extensions and lowercased)
	if exePath, err := os.Executable(); err == nil {
		exeName := filepath.Base(exePath)
		exeName = strings.ToLower(strings.TrimSuffix(exeName, filepath.Ext(exeName)))
		if exeName != "nystavision" && exeName != "" {
			names = append(names, exeName)
		}
	}

	for _, name := range names {
		iconDir := filepath.Join(homeDir, ".local", "share", "icons", "hicolor", "256x256", "apps")
		if err := os.MkdirAll(iconDir, 0755); err != nil {
			return err
		}
		iconPath := filepath.Join(iconDir, name+".png")
		if err := os.WriteFile(iconPath, iconData, 0644); err != nil {
			return err
		}
	}
	return nil
}

// SendLinuxDBusNotification fires desktop notifications using org.freedesktop.Notifications.
func SendLinuxDBusNotification(title, message string, timeoutMs int32) error {
	conn, err := dbus.SessionBus()
	if err != nil {
		return err
	}
	obj := conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications")
	call := obj.Call("org.freedesktop.Notifications.Notify", 0,
		"NystaVision",              // app_name
		uint32(0),                  // replaces_id
		"nystavision",              // app_icon
		title,                      // summary
		message,                    // body
		[]string{},                 // actions
		map[string]dbus.Variant{},  // hints
		timeoutMs,                  // expire_timeout in ms
	)
	return call.Err
}
