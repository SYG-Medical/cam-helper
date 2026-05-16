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
	appDir    = "RTSPVirtualCamAgent"
	fileName  = "config.json"
)

type Config struct {
	RTSPURL             string `json:"rtsp_url"`
	AutoStart           bool   `json:"auto_start"`
	TargetVirtualCamera string `json:"target_virtual_camera"`
	LinuxVideoDevice    string `json:"linux_video_device"`
	Width               int    `json:"width"`
	Height              int    `json:"height"`
	FPS                 int    `json:"fps"`
	DriverMode          string `json:"driver_mode"`
	BridgePort          int    `json:"bridge_port"`
	DriverInstaller     string `json:"driver_installer"`
	DriverBridge        string `json:"driver_bridge"`
	FFmpegPath          string `json:"ffmpeg_path"`
	LogLevel            string `json:"log_level"`
}

func Default() Config {
	return Config{
		RTSPURL:             "rtsp://172.28.6.234:5544/live0.264",
		AutoStart:           true,
		TargetVirtualCamera: "SYG RTSP Camera",
		LinuxVideoDevice:    "/dev/video10",
		Width:               1280,
		Height:              720,
		FPS:                 30,
		DriverMode:          "bridge",
		BridgePort:          18080,
		DriverInstaller:     `third_party\\driver\\virtual-camera-installer.exe`,
		DriverBridge:        `third_party\\driver\\virtual-camera-bridge.exe`,
		FFmpegPath:          `third_party\\ffmpeg\\ffmpeg.exe`,
		LogLevel:            "info",
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

	cfg.normalize()
	return cfg, path, nil
}

func Save(cfg Config, path string) error {
	cfg.normalize()
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

func (c *Config) normalize() {
	if c.Width <= 0 {
		c.Width = 1280
	}
	if c.Height <= 0 {
		c.Height = 720
	}
	if c.FPS <= 0 {
		c.FPS = 30
	}
	if c.BridgePort <= 0 {
		c.BridgePort = 18080
	}
	if c.TargetVirtualCamera == "" {
		c.TargetVirtualCamera = "SYG RTSP Camera"
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
}

func configPath() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config dir: %w", err)
	}
	return filepath.Join(root, vendorDir, appDir, fileName), nil
}
