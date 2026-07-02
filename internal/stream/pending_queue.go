package stream

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// PendingRecording represents a finalized raw recording waiting for patient details
type PendingRecording struct {
	SessionSnapshot RecordingSessionSnapshot `json:"session_snapshot"`
	CompositeFile   string                   `json:"composite_file"`
	Timestamp       time.Time                `json:"timestamp"`
}

// LoadPendingQueue reads the pending recordings from disk if it exists.
func LoadPendingQueue(dir string) ([]PendingRecording, error) {
	path := filepath.Join(dir, "pending_recordings.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var queue []PendingRecording
	err = json.Unmarshal(data, &queue)
	return queue, err
}

// SavePendingQueue writes the pending recordings to disk.
func SavePendingQueue(dir string, queue []PendingRecording) error {
	path := filepath.Join(dir, "pending_recordings.json")
	if len(queue) == 0 {
		_ = os.Remove(path) // ignore error if already removed
		return nil
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(queue, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
