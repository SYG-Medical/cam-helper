package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

const (
	vendorDir = "SYG"
	appDir    = "CameraHelper"
	fileName  = "config.json"

	MinCameras = 2
	MaxCameras = 9
)

// CameraSource describes a single camera input (RTSP stream or local webcam).
type CameraSource struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`     // "rtsp" or "webcam"
	RTSPURL string `json:"rtsp_url"` // used when Type == "rtsp"
	Device  string `json:"device"`   // used when Type == "webcam", e.g. "/dev/video0"
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	FPS     int    `json:"fps"`
	Enabled bool   `json:"enabled"`
}

// SavedLayout stores a named layout with full camera+source information.
type SavedLayout struct {
	Name    string         `json:"name"`
	Cameras []CameraSource `json:"cameras"`
}

// Config holds the application configuration.
type Config struct {
	// General settings
	AutoStart           bool   `json:"auto_start"`
	TargetVirtualCamera string `json:"target_virtual_camera"`
	LinuxVideoDevice    string `json:"linux_video_device"`
	DriverMode          string `json:"driver_mode"`
	BridgePort          int    `json:"bridge_port"`
	DriverInstaller     string `json:"driver_installer"`
	DriverBridge        string `json:"driver_bridge"`
	FFmpegPath          string `json:"ffmpeg_path"`
	LogLevel            string `json:"log_level"`

	// Multi-camera fields
	Cameras          []CameraSource `json:"cameras"`
	SavedLayouts     []SavedLayout  `json:"saved_layouts"`
	ActiveLayoutName string         `json:"active_layout_name"`
	RTSPServerCamera string         `json:"rtsp_server_camera"` // camera ID whose stream is served over HTTP

	// Legacy field for backward compat migration (not persisted in new configs)
	legacyRTSPURL string
}

// legacyConfigJSON is used only to detect old single-camera config files during loading.
type legacyConfigJSON struct {
	RTSPURL string `json:"rtsp_url"`
}

func Default() Config {
	return Config{
		AutoStart:           false,
		TargetVirtualCamera: "SYG Camera",
		LinuxVideoDevice:    "/dev/video10",
		DriverMode:          "bridge",
		BridgePort:          18080,
		DriverInstaller:     `third_party\\driver\\virtual-camera-installer.exe`,
		DriverBridge:        `third_party\\driver\\virtual-camera-bridge.exe`,
		FFmpegPath:          `third_party\\ffmpeg\\ffmpeg.exe`,
		LogLevel:            "info",
		Cameras: []CameraSource{
			{
				ID:      "cam-1",
				Name:    "Kamera 1",
				Type:    "rtsp",
				RTSPURL: "",
				Width:   1280,
				Height:  720,
				FPS:     30,
				Enabled: true,
			},
			{
				ID:      "cam-2",
				Name:    "Kamera 2",
				Type:    "rtsp",
				RTSPURL: "",
				Width:   1280,
				Height:  720,
				FPS:     30,
				Enabled: true,
			},
		},
	}
}

func LoadOrCreate() (Config, string, error) {
	path, err := configPath()
	if err != nil {
		return Config{}, "", err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Config{}, "", fmt.Errorf("create config dir: %w", err)
	}

	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		cfg := Default()
		if err := Save(cfg, path); err != nil {
			return Config{}, "", err
		}
		return cfg, path, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, "", fmt.Errorf("read config: %w", err)
	}

	cfg := Default()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, "", fmt.Errorf("parse config: %w", err)
	}

	// Migrate legacy single-camera config: if "rtsp_url" is present and cameras is empty
	var legacy legacyConfigJSON
	_ = json.Unmarshal(data, &legacy)
	if legacy.RTSPURL != "" && len(cfg.Cameras) == 0 {
		cfg.Cameras = []CameraSource{
			{
				ID:      "cam-1",
				Name:    "Kamera 1",
				Type:    "rtsp",
				RTSPURL: legacy.RTSPURL,
				Width:   1280,
				Height:  720,
				FPS:     30,
				Enabled: true,
			},
			{
				ID:      "cam-2",
				Name:    "Kamera 2",
				Type:    "rtsp",
				RTSPURL: "",
				Width:   1280,
				Height:  720,
				FPS:     30,
				Enabled: true,
			},
		}
	}

	cfg.Normalize()
	return cfg, path, nil
}

func Save(cfg Config, path string) error {
	cfg.Normalize()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func ConfigPath() (string, error) {
	return configPath()
}

func LogsDir() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config dir: %w", err)
	}
	return filepath.Join(root, vendorDir, appDir, "logs"), nil
}

// NextCameraID returns the next available camera ID like "cam-3", "cam-4", etc.
func NextCameraID(cameras []CameraSource) string {
	maxNum := 0
	for _, c := range cameras {
		var n int
		if _, err := fmt.Sscanf(c.ID, "cam-%d", &n); err == nil && n > maxNum {
			maxNum = n
		}
	}
	return fmt.Sprintf("cam-%d", maxNum+1)
}

func (c *Config) Normalize() {
	// Ensure minimum 2 cameras
	for len(c.Cameras) < MinCameras {
		id := NextCameraID(c.Cameras)
		c.Cameras = append(c.Cameras, CameraSource{
			ID:      id,
			Name:    fmt.Sprintf("Kamera %d", len(c.Cameras)+1),
			Type:    "rtsp",
			Width:   1280,
			Height:  720,
			FPS:     30,
			Enabled: true,
		})
	}

	// Ensure max 9 cameras
	if len(c.Cameras) > MaxCameras {
		c.Cameras = c.Cameras[:MaxCameras]
	}

	// Normalize each camera
	for i := range c.Cameras {
		cam := &c.Cameras[i]
		if cam.ID == "" {
			cam.ID = NextCameraID(c.Cameras[:i])
		}
		if cam.Name == "" {
			cam.Name = fmt.Sprintf("Kamera %d", i+1)
		}
		if cam.Type == "" {
			cam.Type = "rtsp"
		}
		if cam.Width <= 0 {
			cam.Width = 1280
		}
		if cam.Height <= 0 {
			cam.Height = 720
		}
		if cam.FPS <= 0 {
			cam.FPS = 30
		}
	}

	if c.BridgePort <= 0 {
		c.BridgePort = 18080
	}
	if c.TargetVirtualCamera == "" {
		c.TargetVirtualCamera = "SYG Camera"
	}
	if c.LinuxVideoDevice == "" {
		c.LinuxVideoDevice = "/dev/video10"
	}
	if c.DriverMode == "" {
		c.DriverMode = "bridge"
	}
	if c.DriverInstaller == "" {
		c.DriverInstaller = `third_party\\driver\\virtual-camera-installer.exe`
	}
	if c.DriverBridge == "" {
		c.DriverBridge = `third_party\\driver\\virtual-camera-bridge.exe`
	}
	if c.FFmpegPath == "" {
		if runtime.GOOS == "windows" {
			c.FFmpegPath = `third_party\\ffmpeg\\ffmpeg.exe`
		} else {
			c.FFmpegPath = "ffmpeg"
		}
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}

	// Ensure RTSPServerCamera is set to a valid camera ID, prioritizing RTSP camera
	var rtspCamID string
	for _, cam := range c.Cameras {
		if cam.Type == "rtsp" {
			rtspCamID = cam.ID
			break
		}
	}

	if rtspCamID != "" {
		c.RTSPServerCamera = rtspCamID
	} else {
		found := false
		for _, cam := range c.Cameras {
			if cam.ID == c.RTSPServerCamera {
				found = true
				break
			}
		}
		if !found && len(c.Cameras) > 0 {
			c.RTSPServerCamera = c.Cameras[0].ID
		}
	}
}

func configPath() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config dir: %w", err)
	}
	return filepath.Join(root, vendorDir, appDir, fileName), nil
}
