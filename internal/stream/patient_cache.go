package stream

import (
	"sync"
	"time"
)

// PatientCache stores the last patient metadata to avoid re-typing in rapid succession.
type PatientCache struct {
	mu          sync.Mutex
	name        string
	tc          string
	history     string
	patientDir  string
	recordCount int
	cachedAt    time.Time
	ttl         time.Duration
}

// NewPatientCache creates a new PatientCache with a specified TTL.
func NewPatientCache(ttl time.Duration) *PatientCache {
	return &PatientCache{
		ttl: ttl,
	}
}

// Store saves patient info and sets/resets the cache timer.
func (pc *PatientCache) Store(name, tc, history, patientDir string) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	pc.name = name
	pc.tc = tc
	pc.history = history
	pc.patientDir = patientDir
	pc.recordCount = 1
	pc.cachedAt = time.Now()
}

// Get returns the cached patient info if it has not expired.
func (pc *PatientCache) Get() (name, tc, history, patientDir string, recordCount int, valid bool) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if pc.cachedAt.IsZero() || time.Since(pc.cachedAt) > pc.ttl {
		return "", "", "", "", 0, false
	}
	return pc.name, pc.tc, pc.history, pc.patientDir, pc.recordCount, true
}

// IncrementRecordCount increments the recording session count and resets the TTL expiration timer.
func (pc *PatientCache) IncrementRecordCount() int {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	pc.recordCount++
	pc.cachedAt = time.Now() // Extend cache duration on new activity
	return pc.recordCount
}

// Invalidate clears the cache.
func (pc *PatientCache) Invalidate() {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	pc.name = ""
	pc.tc = ""
	pc.history = ""
	pc.patientDir = ""
	pc.recordCount = 0
	pc.cachedAt = time.Time{}
}
