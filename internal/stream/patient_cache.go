package stream

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type PatientCacheData struct {
	Name        string    `json:"name"`
	PatientID   string    `json:"patient_id"`
	PatientDir  string    `json:"patient_dir"`
	RecordCount int       `json:"record_count"`
	CachedAt    time.Time `json:"cached_at"`
}

// PatientCache stores the last patient metadata to avoid re-typing in rapid succession.
type PatientCache struct {
	mu        sync.Mutex
	data      PatientCacheData
	ttl       time.Duration
	cachePath string
}

// NewPatientCache creates a new PatientCache with a specified TTL.
func NewPatientCache(ttl time.Duration) *PatientCache {
	return &PatientCache{
		ttl: ttl,
	}
}

// SetFilePath sets the path where the cache should be persisted and loads existing data if valid.
func (pc *PatientCache) SetFilePath(path string) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	pc.cachePath = path
	
	b, err := os.ReadFile(path)
	if err == nil {
		var d PatientCacheData
		if err := json.Unmarshal(b, &d); err == nil {
			// Check TTL on load
			if !d.CachedAt.IsZero() && time.Since(d.CachedAt) <= pc.ttl {
				pc.data = d
			}
		}
	}
}

func (pc *PatientCache) saveToFile() {
	if pc.cachePath == "" {
		return
	}
	// Create dir if not exists
	os.MkdirAll(filepath.Dir(pc.cachePath), 0755)
	b, err := json.MarshalIndent(pc.data, "", "  ")
	if err == nil {
		_ = os.WriteFile(pc.cachePath, append(b, '\n'), 0644)
	}
}

// Store saves patient info and sets/resets the cache timer.
func (pc *PatientCache) Store(name, patientID, patientDir string) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	pc.data.Name = name
	pc.data.PatientID = patientID
	pc.data.PatientDir = patientDir
	pc.data.RecordCount = 1
	pc.data.CachedAt = time.Now()
	pc.saveToFile()
}

// Get returns the cached patient info if it has not expired.
func (pc *PatientCache) Get() (name, patientID, patientDir string, recordCount int, valid bool) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if pc.data.CachedAt.IsZero() || time.Since(pc.data.CachedAt) > pc.ttl {
		return "", "", "", 0, false
	}
	return pc.data.Name, pc.data.PatientID, pc.data.PatientDir, pc.data.RecordCount, true
}

// IncrementRecordCount increments the recording session count and resets the TTL expiration timer.
func (pc *PatientCache) IncrementRecordCount() int {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	pc.data.RecordCount++
	pc.data.CachedAt = time.Now() // Extend cache duration on new activity
	pc.saveToFile()
	return pc.data.RecordCount
}

// Invalidate clears the cache.
func (pc *PatientCache) Invalidate() {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	pc.data = PatientCacheData{}
	pc.saveToFile()
}
