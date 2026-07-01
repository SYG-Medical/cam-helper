package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/jeandeaual/go-locale"
)

const (
	vendorDir = "SYG"
	appDir    = "NystaVision"
	fileName  = "config.json"

	MinCameras = 2
	MaxCameras = 4
)

// Camera role constants.
const (
	CameraRoleEnvironment = "environment" // Ortam / Environment
	CameraRoleGlasses     = "glasses"     // Gözlük / Glasses

	EyeSideRight = "right"
	EyeSideLeft  = "left"
	EyeSideBoth  = "both"
)

// CameraSource describes a single camera input (RTSP stream or local webcam).
type CameraSource struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`     // "rtsp" or "webcam"
	RTSPURL string `json:"rtsp_url"` // used when Type == "rtsp"
	Device  string `json:"device"`   // used when Type == "webcam", e.g. "/dev/video0"
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	FPS         int    `json:"fps"`
	PixelFormat string `json:"pixel_format,omitempty"`
	DevicePath  string `json:"device_path,omitempty"`
	Enabled     bool   `json:"enabled"`

	// Camera role for medical recording
	CameraRole string `json:"camera_role,omitempty"` // "environment" or "glasses"
	EyeSide    string `json:"eye_side,omitempty"`    // "right", "left", or "both" (only for glasses)
}

// SavedLayout stores a named layout with full camera+source information.
type SavedLayout struct {
	Name         string         `json:"name"`
	Cameras      []CameraSource `json:"cameras"`
	WindowWidth  int            `json:"window_width,omitempty"`
	WindowHeight int            `json:"window_height,omitempty"`
	SplitOffsets []float64      `json:"split_offsets,omitempty"`
}

// Config holds the application configuration.
type Config struct {
	// General settings
	AutoStart     bool   `json:"auto_start"`
	FFmpegPath    string `json:"ffmpeg_path"`
	LogLevel        string `json:"log_level"`
	Language        string `json:"language"`
	TutorialShown   bool   `json:"tutorial_shown"`
	RecordingsDir   string `json:"recordings_dir"`

	// Deprecated driver fields — kept for backward compat, not written by Normalize
	TargetVirtualCamera string `json:"target_virtual_camera,omitempty"`
	LinuxVideoDevice    string `json:"linux_video_device,omitempty"`
	DriverMode          string `json:"driver_mode,omitempty"`
	BridgePort          int    `json:"bridge_port,omitempty"`
	DriverInstaller     string `json:"driver_installer,omitempty"`
	DriverBridge        string `json:"driver_bridge,omitempty"`

	// Feature flags
	DisableHardwareAccel bool `json:"disable_hw_accel"`
	CompositeRecording   bool `json:"composite_recording"` // true = record all cameras into a single composite video instantly

	// Multi-camera fields
	Cameras          []CameraSource `json:"cameras"`
	SavedLayouts     []SavedLayout  `json:"saved_layouts"`
	ActiveLayoutName string         `json:"active_layout_name"`
	RTSPServerCamera string         `json:"rtsp_server_camera"` // camera ID whose stream is served over HTTP

	// Layout size and split values
	WindowWidth  int            `json:"window_width,omitempty"`
	WindowHeight int            `json:"window_height,omitempty"`
	SplitOffsets []float64      `json:"split_offsets,omitempty"`

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
		FFmpegPath:          "",
		CompositeRecording:  true,
		LogLevel:      "info",
		Language:      detectSystemLanguage(),
		TutorialShown:   false,
		RecordingsDir:   defaultRecordingsDir(),
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

func defaultRecordingsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}

	docsDir := filepath.Join(home, "Documents", "NystaVision")
	if runtime.GOOS == "windows" {
		if docs := os.Getenv("USERPROFILE"); docs != "" {
			docsDir = filepath.Join(docs, "Documents", "NystaVision")
		}
	}
	return docsDir
}

// detectSystemLanguage reads the OS locale and maps it to a supported language code.
func detectSystemLanguage() string {
	locales, err := locale.GetLocales()
	if err != nil || len(locales) == 0 {
		return "en"
	}
	tag := strings.ToLower(locales[0])
	if len(tag) >= 2 {
		switch tag[:2] {
		case "tr":
			return "tr"
		case "ar":
			return "ar"
		default:
			return "en"
		}
	}
	return "en"
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
	// Override language detection for existing configs (we don't want to change it)
	cfg.Language = ""
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, "", fmt.Errorf("parse config: %w", err)
	}
	if cfg.Language == "" {
		cfg.Language = detectSystemLanguage()
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

	// Apply active layout or defaults only if the config cameras list is invalid/broken
	if len(cfg.Cameras) < MinCameras {
		loaded := false
		// Try active layout first
		if cfg.ActiveLayoutName != "" {
			for _, l := range cfg.SavedLayouts {
				if l.Name == cfg.ActiveLayoutName && len(l.Cameras) >= MinCameras {
					cfg.Cameras = make([]CameraSource, len(l.Cameras))
					copy(cfg.Cameras, l.Cameras)
					loaded = true
					break
				}
			}
		}
		// If active layout was invalid/missing, try first saved layout
		if !loaded && len(cfg.SavedLayouts) > 0 {
			for _, l := range cfg.SavedLayouts {
				if len(l.Cameras) >= MinCameras {
					cfg.Cameras = make([]CameraSource, len(l.Cameras))
					copy(cfg.Cameras, l.Cameras)
					cfg.ActiveLayoutName = l.Name
					loaded = true
					break
				}
			}
		}
		// If still invalid, fallback to default camera list
		if !loaded {
			cfg.Cameras = Default().Cameras
			cfg.ActiveLayoutName = ""
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

// defaultCameraRole returns the backward-compatible role for a camera at a given
// 0-based slot index. This is applied only when a camera has no CameraRole set,
// ensuring that existing configs (which have no role field) are silently upgraded.
//
// Mapping (same for old 2-camera defaults):
//   slot 0 → environment  (Ortam 1)
//   slot 1 → glasses/right (Sağ Göz Kamerası)
//   slot 2 → glasses/left  (Sol Göz Kamerası)
//   slot 3 → environment-2 (Ortam 2)
func defaultCameraRole(slotIdx int) (role, eyeSide string) {
	switch slotIdx {
	case 0:
		return CameraRoleEnvironment, ""
	case 1:
		return CameraRoleGlasses, EyeSideRight
	case 2:
		return CameraRoleGlasses, EyeSideLeft
	default:
		return CameraRoleEnvironment, ""
	}
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
		// Backward-compat migration: assign a default role if none is set.
		// This silently upgrades configs that predate the camera-role feature.
		if cam.CameraRole == "" {
			cam.CameraRole, cam.EyeSide = defaultCameraRole(i)
		}
		// Glasses role must have an EyeSide; default to "right"
		if cam.CameraRole == CameraRoleGlasses && cam.EyeSide == "" {
			cam.EyeSide = EyeSideRight
		}
	}

	if c.FFmpegPath == "" {
		if runtime.GOOS == "windows" {
			c.FFmpegPath = `third_party\ffmpeg\ffmpeg.exe`
		} else {
			c.FFmpegPath = "ffmpeg"
		}
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	if c.Language == "" {
		c.Language = detectSystemLanguage()
	}
	if c.RecordingsDir == "" {
		c.RecordingsDir = defaultRecordingsDir()
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
