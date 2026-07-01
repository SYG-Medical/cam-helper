package stream

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// RecordingSegment is one uninterrupted camera recording file.
type RecordingSegment struct {
	Path      string    `json:"path"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
}

// CameraRecording describes the native recording timeline of one camera.
type CameraRecording struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	Width      int                `json:"width"`
	Height     int                `json:"height"`
	FPS        int                `json:"fps"`
	Order      int                `json:"order"`
	WasRunning bool               `json:"was_running"`
	Segments   []RecordingSegment `json:"segments"`

	// Camera role metadata — passed through from config so post-processing
	// can use them for file naming and VideoFile metadata.
	CameraRole string `json:"camera_role,omitempty"` // "environment" or "glasses"
	EyeSide    string `json:"eye_side,omitempty"`    // "right", "left", or "both"
}

// RecordingSession contains all temporary camera recordings for one user session.
// Its methods are safe to call from camera supervisor goroutines.
type RecordingSession struct {
	ID        string
	TempDir   string
	StartedAt time.Time
	EndedAt   time.Time
	Cols      int
	Rows      int
	RecordTag string

	mu      sync.Mutex
	Cameras map[string]*CameraRecording
}

// RecordingSessionSnapshot is an immutable copy used by post-processing.
type RecordingSessionSnapshot struct {
	ID        string                       `json:"id"`
	TempDir   string                       `json:"temp_dir"`
	StartedAt time.Time                    `json:"started_at"`
	EndedAt   time.Time                    `json:"ended_at"`
	Cols      int                          `json:"cols"`
	Rows      int                          `json:"rows"`
	Cameras   map[string]*CameraRecording  `json:"cameras"`
	RecordTag string                       `json:"record_tag,omitempty"`
}

// NewRecordingSession creates a session that stores raw segments under
// patientDir/raw/. When patientDir is empty the legacy Temp path is used.
func NewRecordingSession(patientDir string, cameras []CameraRecording, cols, rows int, recordTag string) (*RecordingSession, error) {
	now := time.Now()
	id := now.Format("20060102_150405.000")
	var tempDir string
	if patientDir != "" {
		tempDir = filepath.Join(patientDir, "raw")
	} else {
		tempDir = filepath.Join(os.TempDir(), "nystavision", "session_"+id)
	}
	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		return nil, fmt.Errorf("create recording session directory: %w", err)
	}

	s := &RecordingSession{
		ID:        id,
		TempDir:   tempDir,
		StartedAt: now,
		Cols:      cols,
		Rows:      rows,
		RecordTag: recordTag,
		Cameras:   make(map[string]*CameraRecording, len(cameras)),
	}
	for i := range cameras {
		camera := cameras[i]
		camera.Segments = nil
		s.Cameras[camera.ID] = &camera
	}
	return s, nil
}

func (s *RecordingSession) BeginSegment(cameraID string, startedAt time.Time) (int, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	camera := s.Cameras[cameraID]
	if camera == nil {
		return 0, "", fmt.Errorf("camera %q is not part of recording session", cameraID)
	}
	index := len(camera.Segments)
	path := filepath.Join(s.TempDir, fmt.Sprintf("%s_%03d.mkv", sanitizeFilename(cameraID), index+1))
	camera.Segments = append(camera.Segments, RecordingSegment{
		Path:      path,
		StartedAt: startedAt,
	})
	return index, path, nil
}

func (s *RecordingSession) EndSegment(cameraID string, index int, endedAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	camera := s.Cameras[cameraID]
	if camera == nil || index < 0 || index >= len(camera.Segments) {
		return
	}
	camera.Segments[index].EndedAt = endedAt
}

func (s *RecordingSession) Finish(endedAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.EndedAt = endedAt
	for _, camera := range s.Cameras {
		for i := range camera.Segments {
			if camera.Segments[i].EndedAt.IsZero() {
				camera.Segments[i].EndedAt = endedAt
			}
		}
	}
}

func (s *RecordingSession) UpdateStartTime(startedAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.StartedAt = startedAt
}

func (s *RecordingSession) Snapshot() RecordingSessionSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	copySession := RecordingSessionSnapshot{
		ID:        s.ID,
		TempDir:   s.TempDir,
		StartedAt: s.StartedAt,
		EndedAt:   s.EndedAt,
		Cols:      s.Cols,
		Rows:      s.Rows,
		Cameras:   make(map[string]*CameraRecording, len(s.Cameras)),
		RecordTag: s.RecordTag,
	}
	for id, camera := range s.Cameras {
		copyCamera := *camera
		copyCamera.Segments = append([]RecordingSegment(nil), camera.Segments...)
		copySession.Cameras[id] = &copyCamera
	}
	return copySession
}

func (s *RecordingSession) CameraList() []CameraRecording {
	snapshot := s.Snapshot()
	cameras := make([]CameraRecording, 0, len(snapshot.Cameras))
	for _, camera := range snapshot.Cameras {
		cameras = append(cameras, *camera)
	}
	sortCameraRecordings(cameras)
	return cameras
}

func sortCameraRecordings(cameras []CameraRecording) {
	for i := 1; i < len(cameras); i++ {
		for j := i; j > 0 && cameras[j].Order < cameras[j-1].Order; j-- {
			cameras[j], cameras[j-1] = cameras[j-1], cameras[j]
		}
	}
}

// DirectorySize returns the current compressed bytes stored by this session.
func (s *RecordingSession) DirectorySize() uint64 {
	var total uint64
	_ = filepath.Walk(s.TempDir, func(_ string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() && info.Size() > 0 {
			total += uint64(info.Size())
		}
		return nil
	})
	return total
}

// MarshalSnapshot serializes a snapshot to JSON for persistent job storage.
func MarshalSnapshot(snap RecordingSessionSnapshot) ([]byte, error) {
	return json.Marshal(snap)
}

// UnmarshalSnapshot deserializes a snapshot from JSON.
func UnmarshalSnapshot(data []byte) (RecordingSessionSnapshot, error) {
	var snap RecordingSessionSnapshot
	err := json.Unmarshal(data, &snap)
	return snap, err
}

