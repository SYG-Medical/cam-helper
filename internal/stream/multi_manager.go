package stream

import (
	"fmt"
	"sync"

	"nystavision/internal/config"
	"nystavision/internal/logging"
)

// MultiManager orchestrates multiple camera stream managers.
type MultiManager struct {
	streams map[string]*Manager // cameraID → Manager
	mu      sync.Mutex
	logger  *logging.Logger
	cfg     *config.Config
	cfgPath string
}

// NewMultiManager creates a new multi-camera manager.
func NewMultiManager(cfg *config.Config, cfgPath string, logger *logging.Logger) *MultiManager {
	mm := &MultiManager{
		streams: make(map[string]*Manager),
		logger:  logger,
		cfg:     cfg,
		cfgPath: cfgPath,
	}

	// Create a Manager for each camera in the config
	for _, cam := range cfg.Cameras {
		enableHTTP := cam.ID == cfg.RTSPServerCamera
		mgr := NewFromCamera(cam, *cfg, logger, enableHTTP)
		mm.streams[cam.ID] = mgr
	}

	// Always start all enabled RTSP streams immediately in the background
	for _, cam := range cfg.Cameras {
		if cam.Enabled && cam.Type == "rtsp" && cam.RTSPURL != "" {
			mgr := mm.streams[cam.ID]
			go func(c config.CameraSource, m *Manager) {
				if err := m.Start(); err != nil {
					logger.Printf("Failed to auto-start RTSP stream %q at init: %v", c.Name, err)
				}
			}(cam, mgr)
		}
	}

	return mm
}

// AddCamera adds a new camera and creates its stream manager.
func (mm *MultiManager) AddCamera(cam config.CameraSource) error {
	mm.mu.Lock()
	defer mm.mu.Unlock()

	if len(mm.streams) >= config.MaxCameras {
		return fmt.Errorf("maximum %d cameras reached", config.MaxCameras)
	}

	if _, exists := mm.streams[cam.ID]; exists {
		return fmt.Errorf("camera %q already exists", cam.ID)
	}

	// Update config first
	mm.cfg.Cameras = append(mm.cfg.Cameras, cam)
	mm.cfg.Normalize()

	enableHTTP := cam.ID == mm.cfg.RTSPServerCamera
	mgr := NewFromCamera(cam, *mm.cfg, mm.logger, enableHTTP)
	mm.streams[cam.ID] = mgr

	_ = config.Save(*mm.cfg, mm.cfgPath)

	// Auto-start if source is configured
	if cam.Enabled && ((cam.Type == "rtsp" && cam.RTSPURL != "") || (cam.Type == "webcam" && cam.Device != "")) {
		go func() {
			if err := mgr.Start(); err != nil {
				mm.logger.Printf("Failed to auto-start camera %q: %v", cam.Name, err)
			}
		}()
	}

	return nil
}

// RemoveCamera stops and removes a camera.
func (mm *MultiManager) RemoveCamera(cameraID string) error {
	mm.mu.Lock()
	defer mm.mu.Unlock()

	if len(mm.streams) <= config.MinCameras {
		return fmt.Errorf("minimum %d cameras required", config.MinCameras)
	}

	mgr, exists := mm.streams[cameraID]
	if !exists {
		return fmt.Errorf("camera %q not found", cameraID)
	}

	mgr.Close()
	delete(mm.streams, cameraID)

	// Update config
	for i, c := range mm.cfg.Cameras {
		if c.ID == cameraID {
			mm.cfg.Cameras = append(mm.cfg.Cameras[:i], mm.cfg.Cameras[i+1:]...)
			break
		}
	}
	_ = config.Save(*mm.cfg, mm.cfgPath)

	return nil
}

// StartCamera starts a single camera stream.
func (mm *MultiManager) StartCamera(cameraID string) error {
	mm.mu.Lock()
	mgr, exists := mm.streams[cameraID]
	mm.mu.Unlock()

	if !exists {
		return fmt.Errorf("camera %q not found", cameraID)
	}
	return mgr.Start()
}

// StopCamera stops a single camera stream.
func (mm *MultiManager) StopCamera(cameraID string) {
	mm.mu.Lock()
	mgr, exists := mm.streams[cameraID]
	mm.mu.Unlock()

	if exists {
		mgr.Stop()
	}
}

// StartAll starts all camera streams.
func (mm *MultiManager) StartAll() {
	mm.mu.Lock()
	managers := make([]*Manager, 0, len(mm.streams))
	for _, mgr := range mm.streams {
		managers = append(managers, mgr)
	}
	mm.mu.Unlock()

	for _, mgr := range managers {
		if mgr.cam.Enabled {
			if err := mgr.Start(); err != nil {
				mm.logger.Printf("Failed to start camera: %v", err)
			}
		}
	}
}

// StopAll stops all webcam streams but keeps RTSP streams running.
func (mm *MultiManager) StopAll() {
	mm.mu.Lock()
	managers := make([]*Manager, 0, len(mm.streams))
	for _, mgr := range mm.streams {
		managers = append(managers, mgr)
	}
	mm.mu.Unlock()

	for _, mgr := range managers {
		if mgr.cam.Type == "webcam" {
			mgr.Stop()
		}
	}
}

// Close stops all streams and closes all managers (shutting down HTTP servers).
func (mm *MultiManager) Close() {
	mm.mu.Lock()
	managers := make([]*Manager, 0, len(mm.streams))
	for _, mgr := range mm.streams {
		managers = append(managers, mgr)
	}
	mm.streams = make(map[string]*Manager)
	mm.mu.Unlock()

	for _, mgr := range managers {
		mgr.Close()
	}
}

// GetState returns the state of a specific camera stream.
func (mm *MultiManager) GetState(cameraID string) State {
	mm.mu.Lock()
	mgr, exists := mm.streams[cameraID]
	mm.mu.Unlock()

	if !exists {
		return State{}
	}
	return mgr.State()
}

// SetOnFrame sets the frame callback for a specific camera.
func (mm *MultiManager) SetOnFrame(cameraID string, cb func(width, height int, pix []byte)) {
	mm.mu.Lock()
	mgr, exists := mm.streams[cameraID]
	mm.mu.Unlock()

	if exists {
		mgr.SetOnFrame(cb)
	}
}

// GetManager returns the Manager for a specific camera.
func (mm *MultiManager) GetManager(cameraID string) *Manager {
	mm.mu.Lock()
	defer mm.mu.Unlock()
	return mm.streams[cameraID]
}

// CameraCount returns the number of cameras.
func (mm *MultiManager) CameraCount() int {
	mm.mu.Lock()
	defer mm.mu.Unlock()
	return len(mm.streams)
}

// CameraIDs returns all camera IDs.
func (mm *MultiManager) CameraIDs() []string {
	mm.mu.Lock()
	defer mm.mu.Unlock()
	ids := make([]string, 0, len(mm.streams))
	for id := range mm.streams {
		ids = append(ids, id)
	}
	return ids
}

// UpdateCamera updates a camera's configuration and restarts it.
func (mm *MultiManager) UpdateCamera(cam config.CameraSource) error {
	mm.mu.Lock()
	mgr, exists := mm.streams[cam.ID]
	if !exists {
		mm.mu.Unlock()
		return fmt.Errorf("camera %q not found", cam.ID)
	}

	// Update config
	for i, c := range mm.cfg.Cameras {
		if c.ID == cam.ID {
			mm.cfg.Cameras[i] = cam
			break
		}
	}
	_ = config.Save(*mm.cfg, mm.cfgPath)
	mm.mu.Unlock()

	// Restart the stream with new settings
	mgr.Stop()
	mgr.UpdateCamera(cam)
	if cam.Enabled {
		return mgr.Start()
	}
	return nil
}

// Config returns the current config.
func (mm *MultiManager) Config() config.Config {
	mm.mu.Lock()
	defer mm.mu.Unlock()
	return *mm.cfg
}

// UpdateConfig updates the global config.
func (mm *MultiManager) UpdateConfig(cfg config.Config) {
	mm.mu.Lock()
	*mm.cfg = cfg
	for _, mgr := range mm.streams {
		mgr.UpdateConfig(cfg)
	}
	mm.mu.Unlock()
}

// SaveConfig persists the current config to disk.
func (mm *MultiManager) SaveConfig() error {
	mm.mu.Lock()
	defer mm.mu.Unlock()
	return config.Save(*mm.cfg, mm.cfgPath)
}
